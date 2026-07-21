package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeGraph is an httptest stand-in for the Graph API, so the whole Facebook
// connect/subscribe path is exercised without a live Meta app.
type fakeGraph struct {
	server    *httptest.Server
	appSecret string

	// captured for assertions
	sawProofs     []string // appsecret_proof on each request that carried one
	subscribed    []string // page ids POSTed to /subscribed_apps
	unsubscribed  []string // page ids DELETEd from /subscribed_apps
	pagesPerBatch int      // how many pages to return per /me/accounts page (to test paging)

	// leadgen fetch (GET /{leadgen_id}?fields=field_data,...)
	leadStatus    int              // 0 → 200; set >=400 to simulate a failed fetch
	leadErrorCode int              // Graph error code on a failed fetch (0 → 190, a dead token)
	leadFormID    string           // form_id returned on the lead
	leadFieldData []map[string]any // field_data array

	// L7.1 placement + subscription
	sawLeadFields       []string         // every `fields` param a lead read asked for, in order
	rejectPlacement     bool             // 400 any lead read whose fields include `platform`
	leadPlatform        string           // `platform` on the returned lead node ("" ⇒ omitted)
	leadIsOrganic       *bool            // `is_organic` on the returned lead node (nil ⇒ omitted)
	subscribedApps      []map[string]any // GET /{page}/subscribed_apps response data
	sawSubscribedGET    int              // how many times the subscription was READ
	sawSubscribedFields string           // the `fields` projection the subscription read asked for
}

// rejectsPlacement answers a lead read that asked for a field this fake refuses to
// recognise, the way Graph refuses an unknown field: a PERMANENT 400 that fails the
// whole node request rather than dropping the field.
func (g *fakeGraph) rejectsPlacement(w http.ResponseWriter, fields string) bool {
	if !g.rejectPlacement || !strings.Contains(fields, "platform") {
		return false
	}
	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, map[string]any{"error": map[string]any{
		"message": "(#100) Tried accessing nonexisting field (platform)", "code": 100,
	}})
	return true
}

func newFakeGraph(t *testing.T, appSecret string) *fakeGraph {
	t.Helper()
	g := &fakeGraph{
		appSecret:     appSecret,
		pagesPerBatch: 2,
		leadFormID:    "form1",
		leadFieldData: []map[string]any{
			{"name": "email", "values": []string{"lead@example.com"}},
			{"name": "full_name", "values": []string{"Ada Lovelace"}},
		},
	}
	mux := http.NewServeMux()

	// Token exchange (both the code→short and fb_exchange_token→long calls hit here).
	mux.HandleFunc("/v25.0/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		tok := "user-short-token"
		if q.Get("grant_type") == "fb_exchange_token" {
			tok = "user-long-token"
		}
		writeJSON(w, map[string]any{"access_token": tok, "token_type": "bearer", "expires_in": 5184000})
	})

	// /me/accounts with cursor paging: batch 1 returns 2 pages + an `after`, batch 2
	// returns 1 page and no cursor.
	mux.HandleFunc("/v25.0/me/accounts", func(w http.ResponseWriter, r *http.Request) {
		g.sawProofs = append(g.sawProofs, r.URL.Query().Get("appsecret_proof"))
		after := r.URL.Query().Get("after")
		if after == "" {
			writeJSON(w, map[string]any{
				"data": []map[string]any{
					{"id": "page1", "name": "Page One", "access_token": "tok-page1", "category": "Business"},
					{"id": "page2", "name": "Page Two", "access_token": "tok-page2"},
					// A page the admin manages but has no token for (insufficient role) —
					// must be skipped, never stored as a tokenless connection.
					{"id": "page0", "name": "No Access", "category": "X"},
				},
				"paging": map[string]any{"cursors": map[string]any{"after": "CURSOR2"}, "next": g.server.URL + "/v25.0/me/accounts?after=CURSOR2"},
			})
			return
		}
		writeJSON(w, map[string]any{
			"data":   []map[string]any{{"id": "page3", "name": "Page Three", "access_token": "tok-page3"}},
			"paging": map[string]any{},
		})
	})

	// subscribed_apps: POST subscribes, DELETE unsubscribes. Path is /v25.0/{page}/subscribed_apps.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/subscribed_apps") {
			page := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v25.0/"), "/subscribed_apps")
			switch r.Method {
			case http.MethodPost:
				g.subscribed = append(g.subscribed, page)
				writeJSON(w, map[string]any{"success": true})
			case http.MethodDelete:
				g.unsubscribed = append(g.unsubscribed, page)
				writeJSON(w, map[string]any{"success": true})
			case http.MethodGet:
				g.sawSubscribedGET++
				g.sawSubscribedFields = r.URL.Query().Get("fields")
				writeJSON(w, map[string]any{"data": g.subscribedApps})
			}
			return
		}
		// Form discovery: GET /v25.0/{page-id}/leadgen_forms
		if strings.HasSuffix(r.URL.Path, "/leadgen_forms") {
			writeJSON(w, map[string]any{
				"data": []map[string]any{
					{"id": "form1", "name": "Contact Form", "status": "ACTIVE"},
					{"id": "form2", "name": "Newsletter", "status": "ACTIVE"},
				},
				"paging": map[string]any{},
			})
			return
		}
		// Backfill: GET /v25.0/{form-id}/leads (paged). One lead then empty. Checked
		// BEFORE the single-lead fetch branch because /leads also requests field_data.
		if strings.HasSuffix(r.URL.Path, "/leads") {
			g.sawLeadFields = append(g.sawLeadFields, r.URL.Query().Get("fields"))
			if g.rejectsPlacement(w, r.URL.Query().Get("fields")) {
				return
			}
			if r.URL.Query().Get("after") == "" {
				writeJSON(w, map[string]any{
					"data": []map[string]any{{
						"id": "BL1", "created_time": "2026-06-01T00:00:00+0000", "form_id": "form1",
						"platform":   "ig",
						"is_organic": true,
						"field_data": []map[string]any{{"name": "email", "values": []string{"past@example.com"}}},
					}},
					"paging": map[string]any{"cursors": map[string]any{"after": "BC2"}, "next": g.server.URL + "/v25.0/form1/leads?after=BC2"},
				})
				return
			}
			writeJSON(w, map[string]any{"data": []map[string]any{}, "paging": map[string]any{}})
			return
		}
		// A leadgen fetch: GET /v25.0/{leadgen_id}?fields=field_data,...
		if strings.Contains(r.URL.Query().Get("fields"), "field_data") {
			g.sawLeadFields = append(g.sawLeadFields, r.URL.Query().Get("fields"))
			if g.leadStatus >= 400 {
				code := g.leadErrorCode
				if code == 0 {
					code = 190 // default: a dead/invalid token (permanent)
				}
				w.WriteHeader(g.leadStatus)
				writeJSON(w, map[string]any{"error": map[string]any{"message": "fetch failed", "code": code}})
				return
			}
			if g.rejectsPlacement(w, r.URL.Query().Get("fields")) {
				return
			}
			node := map[string]any{
				"id":           strings.TrimPrefix(r.URL.Path, "/v25.0/"),
				"form_id":      g.leadFormID,
				"created_time": "2026-07-20T00:00:00+0000",
				"field_data":   g.leadFieldData,
			}
			if g.leadPlatform != "" {
				node["platform"] = g.leadPlatform
			}
			if g.leadIsOrganic != nil {
				node["is_organic"] = *g.leadIsOrganic
			}
			writeJSON(w, node)
			return
		}
		// A page node GET (HealthCheck): /v25.0/{page}
		writeJSON(w, map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/v25.0/"), "name": "A Page"})
	})

	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGraph) provider(appID string) *FacebookProvider {
	p := NewFacebookProvider(appID, g.appSecret, "", NewHTTPClient(nil))
	p.graphBaseURL = g.server.URL + "/v25.0"
	return p
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestFacebook_AuthURL_ClassicScopes(t *testing.T) {
	p := NewFacebookProvider("app123", "secret", "", nil)
	got := p.AuthURL("st4te", "https://api.example/api/integrations/providers/facebook/callback", "")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	if q.Get("client_id") != "app123" || q.Get("state") != "st4te" || q.Get("response_type") != "code" {
		t.Errorf("auth url query = %v", q)
	}
	if q.Get("scope") == "" || !strings.Contains(q.Get("scope"), "leads_retrieval") {
		t.Errorf("classic flow must request leads_retrieval scope, got %q", q.Get("scope"))
	}
	if q.Get("config_id") != "" {
		t.Errorf("classic flow must not send config_id")
	}
}

func TestFacebook_AuthURL_LoginForBusiness(t *testing.T) {
	p := NewFacebookProvider("app123", "secret", "cfg999", nil)
	u, _ := url.Parse(p.AuthURL("s", "https://api.example/cb", ""))
	q := u.Query()
	if q.Get("config_id") != "cfg999" {
		t.Errorf("login-for-business must send config_id, got %q", q.Get("config_id"))
	}
	if q.Get("scope") != "" {
		t.Errorf("login-for-business must NOT send scope (rejected alongside config_id), got %q", q.Get("scope"))
	}
}

func TestFacebook_ExchangeCallback_ReturnsPageAccounts(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app123")

	accounts, err := p.ExchangeCallback(context.Background(), "auth-code", "https://api.example/cb", "")
	if err != nil {
		t.Fatalf("ExchangeCallback: %v", err)
	}
	// All three pages across the two cursor batches, each with its own page token.
	if len(accounts) != 3 {
		t.Fatalf("want 3 accounts across paging, got %d", len(accounts))
	}
	byID := map[string]Account{}
	for _, a := range accounts {
		byID[a.ID] = a
	}
	if byID["page1"].Credentials.AccessToken != "tok-page1" || byID["page1"].Label != "Page One" {
		t.Errorf("page1 = %+v", byID["page1"])
	}
	if byID["page3"].Credentials.AccessToken != "tok-page3" {
		t.Errorf("cursor paging dropped page3: %+v", byID)
	}
	if byID["page1"].Meta["category"] != "Business" {
		t.Errorf("page1 category meta missing: %+v", byID["page1"].Meta)
	}
	// The tokenless page must have been skipped, not returned as a dead connection.
	if _, ok := byID["page0"]; ok {
		t.Errorf("a tokenless page was returned as a connectable account")
	}

	// appsecret_proof must be present on the /me/accounts calls and be HMAC of the
	// LONG-lived user token (the one listPages uses), keyed by the app secret.
	wantProof := hmacHex("app-secret", "user-long-token")
	if len(g.sawProofs) == 0 {
		t.Fatal("no appsecret_proof was sent on /me/accounts")
	}
	for i, got := range g.sawProofs {
		if got != wantProof {
			t.Errorf("me/accounts call %d proof = %q, want %q", i, got, wantProof)
		}
	}
}

func TestFacebook_Subscribe(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app")
	conn := &IntegrationConnection{ExternalAccountID: "page1"}
	if err := p.Subscribe(context.Background(), conn, Credentials{AccessToken: "tok-page1"}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if len(g.subscribed) != 1 || g.subscribed[0] != "page1" {
		t.Errorf("expected page1 subscribed, got %v", g.subscribed)
	}
}

func TestFacebook_Disconnect_Unsubscribes(t *testing.T) {
	g := newFakeGraph(t, "s")
	p := g.provider("app")
	conn := &IntegrationConnection{ExternalAccountID: "page7"}
	if err := p.Disconnect(context.Background(), conn, Credentials{AccessToken: "tok"}); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if len(g.unsubscribed) != 1 || g.unsubscribed[0] != "page7" {
		t.Errorf("expected page7 unsubscribed, got %v", g.unsubscribed)
	}
}

func TestFacebook_GraphErrorSurfacesReason(t *testing.T) {
	// A Graph 400 with an error envelope must surface Facebook's message and stay
	// NON-retryable (a bad token is permanent).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		writeJSON(w, map[string]any{"error": map[string]any{"message": "Invalid OAuth access token.", "type": "OAuthException", "code": 190}})
	}))
	defer srv.Close()
	p := NewFacebookProvider("app", "s", "", NewHTTPClient(nil))
	p.graphBaseURL = srv.URL + "/v25.0"

	err := p.Subscribe(context.Background(), &IntegrationConnection{ExternalAccountID: "p"}, Credentials{AccessToken: "bad"})
	if err == nil {
		t.Fatal("expected an error for a 400")
	}
	if !strings.Contains(err.Error(), "Invalid OAuth access token") {
		t.Errorf("error should carry Facebook's reason, got %v", err)
	}
	if IsRetryableHTTP(err) {
		t.Error("a 400 OAuth error must be permanent, not retryable")
	}
}

func TestFacebook_TransportErrorDoesNotLeakSecrets(t *testing.T) {
	// A transport failure (nothing listening) must not carry the token, app secret,
	// or appsecret_proof — which live in the query string — into the error text,
	// because that text reaches server logs and (for subscribe) the FE-visible note.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed := srv.URL
	srv.Close() // connection refused for every call below

	p := NewFacebookProvider("app123", "super-secret-value", "", NewHTTPClient(nil))
	p.graphBaseURL = closed + "/v25.0"
	// Make the retry backoff a no-op so the test does not actually sleep.
	p.http.sleep = func(context.Context, time.Duration) error { return nil }

	secrets := []string{"super-secret-value"} // app secret

	// Token exchange leaks the app secret if unredacted.
	_, err := p.ExchangeCallback(context.Background(), "the-code", "https://api.example/cb", "")
	if err == nil {
		t.Fatal("expected a transport error")
	}
	assertNoSecrets(t, "ExchangeCallback error", err.Error(), append(secrets, "the-code")...)

	// Subscribe leaks the page token + appsecret_proof if unredacted.
	pageToken := "EAA-live-page-token"
	serr := p.Subscribe(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: pageToken})
	if serr == nil {
		t.Fatal("expected a transport error")
	}
	assertNoSecrets(t, "Subscribe error", serr.Error(), pageToken, hmacHex("super-secret-value", pageToken))
}

func assertNoSecrets(t *testing.T, label, got string, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if s != "" && strings.Contains(got, s) {
			t.Errorf("%s leaked a secret (%q) in: %s", label, s, got)
		}
	}
	// The redacted URL should have no query string at all.
	if strings.Contains(got, "access_token=") || strings.Contains(got, "client_secret=") || strings.Contains(got, "appsecret_proof=") {
		t.Errorf("%s still contains a secret query param: %s", label, got)
	}
}

func hmacHex(secret, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

// ── L7.1: placement signal + the subscription probe ────────────────────────

// An Instagram lead ad rides the same page, the same webhook and the same form as a
// Facebook one, so `platform` is the ONLY thing in the ledger that can distinguish
// them — which is what makes "are the Instagram leads arriving?" answerable at all.
func TestFacebook_FetchLead_CarriesPlacement(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.leadPlatform = "ig"
	organic := false
	g.leadIsOrganic = &organic
	p := g.provider("app123")

	lead, err := p.FetchLead(context.Background(), nil, Credentials{AccessToken: "tok"},
		InboundEvent{ExternalAccountID: "page1", ProviderEventID: "LG1", FormID: "form1"})
	if err != nil {
		t.Fatalf("FetchLead: %v", err)
	}
	if lead.Context["platform"] != "ig" {
		t.Errorf("platform must reach the delivery context, got %v", lead.Context["platform"])
	}
	if lead.Context["is_organic"] != false {
		t.Errorf("is_organic must reach the delivery context, got %v", lead.Context["is_organic"])
	}
}

// Absent is not false. A defaulted `is_organic:false` would assert the lead came from
// a paid ad on every delivery Graph declined to tell us about.
func TestFacebook_FetchLead_OmitsPlacementItNeverReceived(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app123")

	lead, err := p.FetchLead(context.Background(), nil, Credentials{AccessToken: "tok"},
		InboundEvent{ExternalAccountID: "page1", ProviderEventID: "LG1"})
	if err != nil {
		t.Fatalf("FetchLead: %v", err)
	}
	if _, ok := lead.Context["platform"]; ok {
		t.Errorf("platform must be absent when Graph sent none, got %v", lead.Context["platform"])
	}
	if _, ok := lead.Context["is_organic"]; ok {
		t.Errorf("is_organic must be absent when Graph sent none, got %v", lead.Context["is_organic"])
	}
}

// The load-bearing half of the placement change: a `fields` list Graph does not
// recognise fails the WHOLE node request, so without the fallback rung adding a
// reporting field would have put every Facebook AND Instagram lead behind it.
func TestFacebook_FetchLead_SurvivesAnUnknownPlacementField(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.rejectPlacement = true
	p := g.provider("app123")

	lead, err := p.FetchLead(context.Background(), nil, Credentials{AccessToken: "tok"},
		InboundEvent{ExternalAccountID: "page1", ProviderEventID: "LG1", FormID: "form1"})
	if err != nil {
		t.Fatalf("a rejected placement field must cost the LABEL, never the LEAD: %v", err)
	}
	if lead.Fields["email"] != "lead@example.com" {
		t.Errorf("the lead itself must still arrive, got %v", lead.Fields)
	}
	if len(g.sawLeadFields) != 2 {
		t.Fatalf("expected exactly two reads (wide, then narrow), got %d: %v", len(g.sawLeadFields), g.sawLeadFields)
	}
	if !strings.Contains(g.sawLeadFields[0], "platform") {
		t.Errorf("the FIRST read must ask for the placement fields, got %q", g.sawLeadFields[0])
	}
	if strings.Contains(g.sawLeadFields[1], "platform") {
		t.Errorf("the fallback read must drop them, got %q", g.sawLeadFields[1])
	}
}

// The control for the test above, and the reason the ladder keys on RETRYABILITY: a
// throttle or an outage says nothing about the field list, so retrying narrower would
// burn a second request against the same failure and learn nothing — and, worse, could
// turn a transient fault into a permanent silent downgrade of every lead's placement.
func TestFacebook_FetchLead_DoesNotNarrowOnATransientFailure(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.leadStatus = http.StatusTooManyRequests
	g.leadErrorCode = 4 // a Graph throttle code — retryable
	p := g.provider("app123")

	_, err := p.FetchLead(context.Background(), nil, Credentials{AccessToken: "tok"},
		InboundEvent{ExternalAccountID: "page1", ProviderEventID: "LG1"})
	if err == nil {
		t.Fatal("expected the throttle to surface")
	}
	if !IsRetryableHTTP(err) {
		t.Errorf("a throttle must stay retryable so the worker repends: %v", err)
	}
	for _, f := range g.sawLeadFields {
		if !strings.Contains(f, "platform") {
			t.Fatalf("a transient failure must not trigger the narrow retry, saw %v", g.sawLeadFields)
		}
	}
}

// Backfill shares the ladder for a different reason: a rejected field list there is
// not lead loss, but it would leave historical import permanently broken while the
// webhook path kept working.
func TestFacebook_Backfill_SurvivesAnUnknownPlacementField(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.rejectPlacement = true
	p := g.provider("app123")

	leads, next, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"},
		Credentials{AccessToken: "tok"}, "form1", "")
	if err != nil {
		t.Fatalf("backfill must fall back rather than fail: %v", err)
	}
	if len(leads) != 1 {
		t.Fatalf("expected the page of historical leads, got %d", len(leads))
	}
	// The cursor must come from the response the LEADS came from. Computing it from
	// the failed wide attempt's zero value would return "", runBackfill would break
	// after page one, and every historical lead past the first 100 would be silently
	// dropped by an import that reported success.
	if next != "BC2" {
		t.Errorf("the fallback must carry the cursor of the page it actually read, got %q", next)
	}
	// Without these two, reverting Backfill to the narrow field list leaves the whole
	// package green — proven by mutation during review. The ladder was pinned; the
	// request WIDTH was not, so a future narrowing would ship placement-less historical
	// imports with CI silent.
	if len(g.sawLeadFields) != 2 {
		t.Fatalf("expected exactly two reads (wide, then narrow), got %d: %v", len(g.sawLeadFields), g.sawLeadFields)
	}
	if !strings.Contains(g.sawLeadFields[0], "platform") || strings.Contains(g.sawLeadFields[1], "platform") {
		t.Errorf("backfill must ask wide first and narrow second, got %v", g.sawLeadFields)
	}
}

// The claim the shared-const comment makes: both paths produce identical shapes. A
// backfilled Instagram lead must carry the same placement keys the webhook path does,
// or "import past leads" quietly rewrites history as Facebook-only.
func TestFacebook_Backfill_CarriesPlacementLikeTheWebhookPath(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app123")

	leads, _, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"},
		Credentials{AccessToken: "tok"}, "form1", "")
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(leads) != 1 {
		t.Fatalf("expected one historical lead, got %d", len(leads))
	}
	if leads[0].Context["platform"] != "ig" {
		t.Errorf("a backfilled lead must carry its placement, got %v", leads[0].Context["platform"])
	}
	if leads[0].Context["is_organic"] != true {
		t.Errorf("a backfilled lead must carry is_organic, got %v", leads[0].Context["is_organic"])
	}
}

// The third state: our app IS attached, and Graph said nothing about what it is
// subscribed to. Folding that into `false` would tell an admin to re-subscribe a page
// on evidence we never obtained.
func TestFacebook_CheckSubscription_UnknownWhenTheFieldListIsAbsent(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.subscribedApps = []map[string]any{{"id": "app123", "name": "Our App"}} // no subscribed_fields
	p := g.provider("app123")

	if _, err := p.CheckSubscription(context.Background(),
		&IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"}); err == nil {
		t.Error("an absent field list must report unknown, not `not subscribed`")
	}
}

// The projection is explicit precisely so the verdict does not depend on Graph's
// default field set staying as documented.
func TestFacebook_CheckSubscription_AsksForTheFieldItJudgesOn(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.subscribedApps = []map[string]any{{"id": "app123", "subscribed_fields": []string{"leadgen"}}}
	p := g.provider("app123")

	if _, err := p.CheckSubscription(context.Background(),
		&IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"}); err != nil {
		t.Fatalf("CheckSubscription: %v", err)
	}
	if got := g.sawSubscribedFields; got != "subscribed_fields" {
		t.Errorf("the probe must request subscribed_fields explicitly, asked for %q", got)
	}
}

// CheckSubscription is the layer L6.3's diagnose panel was built for and shipped
// without: `subscription` answered "unknown" for the only provider in the product.
func TestFacebook_CheckSubscription_TrueOnlyForOurAppWithLeadgen(t *testing.T) {
	cases := []struct {
		name string
		data []map[string]any
		want bool
	}{
		{"our app, subscribed to leadgen", []map[string]any{
			{"id": "app123", "subscribed_fields": []string{"leadgen", "messages"}},
		}, true},
		// The whole reason the app id is matched: another agency's CRM subscribed to
		// leadgen on the same page delivers nothing to US.
		{"someone else's app, subscribed to leadgen", []map[string]any{
			{"id": "otherapp", "subscribed_fields": []string{"leadgen"}},
		}, false},
		{"our app, attached but not for leads", []map[string]any{
			{"id": "app123", "subscribed_fields": []string{"messages"}},
		}, false},
		{"no app attached at all", []map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGraph(t, "app-secret")
			g.subscribedApps = tc.data
			p := g.provider("app123")

			got, err := p.CheckSubscription(context.Background(),
				&IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"})
			if err != nil {
				t.Fatalf("CheckSubscription: %v", err)
			}
			if got != tc.want {
				t.Errorf("subscribed = %v, want %v", got, tc.want)
			}
			if g.sawSubscribedGET != 1 {
				t.Errorf("the probe must READ the subscription exactly once, saw %d reads", g.sawSubscribedGET)
			}
		})
	}
}

// "We could not ask" must never collapse into "the answer is no": diagnose renders
// `false` as a definite failure and sends the admin to re-do OAuth, which is the wrong
// action when the truth is that Graph was unreachable.
func TestFacebook_CheckSubscription_ErrorsRatherThanAnsweringNo(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	// No app id configured: nothing in the response could ever match, so a bare `false`
	// would be an answer we did not earn.
	p := g.provider("")
	if _, err := p.CheckSubscription(context.Background(),
		&IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"}); err == nil {
		t.Error("a provider with no app id must report unknown, not `not subscribed`")
	}
	if g.sawSubscribedGET != 0 {
		t.Error("it must not even ask when it could not interpret the answer")
	}
}

// When the narrow read fails too, the field list was never the problem — so the
// FIRST error must propagate. The classification is load-bearing downstream: the
// async worker repends a retryable failure and flips the connection to `error` on a
// permanent one, so letting a transient blip on the second call stand in for a dead
// token means the connection never flips and the reconnect banner never appears.
func TestFacebook_FetchLead_ReportsTheFirstErrorWhenBothReadsFail(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	g.leadStatus = http.StatusBadRequest
	g.leadErrorCode = 190 // a dead token — permanent, and the truth about this call
	p := g.provider("app123")

	_, err := p.FetchLead(context.Background(), nil, Credentials{AccessToken: "tok"},
		InboundEvent{ExternalAccountID: "page1", ProviderEventID: "LG1"})
	if err == nil {
		t.Fatal("expected the failure to surface")
	}
	if IsRetryableHTTP(err) {
		t.Errorf("a dead token must stay PERMANENT so the worker flips the connection: %v", err)
	}
}
