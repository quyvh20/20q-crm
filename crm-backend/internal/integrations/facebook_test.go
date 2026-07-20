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
			if r.URL.Query().Get("after") == "" {
				writeJSON(w, map[string]any{
					"data": []map[string]any{{
						"id": "BL1", "created_time": "2026-06-01T00:00:00+0000", "form_id": "form1",
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
			if g.leadStatus >= 400 {
				code := g.leadErrorCode
				if code == 0 {
					code = 190 // default: a dead/invalid token (permanent)
				}
				w.WriteHeader(g.leadStatus)
				writeJSON(w, map[string]any{"error": map[string]any{"message": "fetch failed", "code": code}})
				return
			}
			writeJSON(w, map[string]any{
				"id":           strings.TrimPrefix(r.URL.Path, "/v25.0/"),
				"form_id":      g.leadFormID,
				"created_time": "2026-07-20T00:00:00+0000",
				"field_data":   g.leadFieldData,
			})
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
