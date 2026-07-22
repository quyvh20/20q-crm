package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTikTok is an httptest stand-in for the Business API, so the whole adapter is
// exercisable without a live TikTok app. Unlike Meta there is no sandbox to fall back
// on — TikTok's does not serve the lead endpoints — so this is the only pre-live
// coverage that exists.
type fakeTikTok struct {
	server *httptest.Server

	sawTokenHeaders []string // Access-Token on each request that carried one
	sawPaths        []string

	advertisers   []map[string]any
	forms         []map[string]any
	formTotalPage int
	subscriptions []map[string]any

	// failCode makes the NEXT response an envelope failure (HTTP 200 + a code).
	failCode int
	failMsg  string
}

func newFakeTikTok(t *testing.T) *fakeTikTok {
	t.Helper()
	f := &fakeTikTok{
		advertisers:   []map[string]any{{"advertiser_id": "adv1", "advertiser_name": "Acme Ads"}},
		forms:         []map[string]any{{"page_id": "form1", "title": "Spring Form", "status": "PUBLISHED"}},
		formTotalPage: 1,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		f.sawPaths = append(f.sawPaths, r.URL.Path)
		if tok := r.Header.Get("Access-Token"); tok != "" {
			f.sawTokenHeaders = append(f.sawTokenHeaders, tok)
		}
		w.Header().Set("Content-Type", "application/json")

		// Every Business API response is an HTTP 200; the verdict is in `code`.
		if f.failCode != 0 {
			msg := f.failMsg
			if msg == "" {
				msg = "failed"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": f.failCode, "message": msg})
			return
		}
		var data any
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth2/access_token/"):
			data = map[string]any{"access_token": "tt-token", "advertiser_ids": []string{"adv1"}}
		case strings.HasSuffix(r.URL.Path, "/oauth2/advertiser/get/"):
			data = map[string]any{"list": f.advertisers}
		case strings.HasSuffix(r.URL.Path, "/subscription/subscribe/"):
			data = map[string]any{"subscription_id": "sub1"}
		case strings.HasSuffix(r.URL.Path, "/subscription/get/"):
			data = map[string]any{"subscriptions": f.subscriptions}
		case strings.HasSuffix(r.URL.Path, "/page/get/"):
			data = map[string]any{"list": f.forms, "page_info": map[string]any{"total_page": f.formTotalPage}}
		default:
			data = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "OK", "data": data})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeTikTok) provider(appID, appSecret string) *TikTokProvider {
	p := NewTikTokProvider(appID, appSecret, "https://business-api.tiktok.com/portal/auth?app_id="+appID,
		"https://crm.example/api/integrations/tiktok/webhook", NewHTTPClient(nil))
	p.baseURL = f.server.URL + "/open_api/v1.3"
	return p
}

// The whole reason this adapter needed a framework change: TikTok's failures arrive
// as HTTP 200 with a code in the body, and its docs say that code "takes precedence
// over HTTP status codes". The shared client classifies on status, so without the
// envelope decode a dead token reads as success and the lead behind it is written
// from an empty response.
func TestTikTok_EnvelopeFailureIsAnErrorDespiteHTTP200(t *testing.T) {
	f := newFakeTikTok(t)
	f.failCode = 40105 // invalid access token
	f.failMsg = "Access token is invalid"
	p := f.provider("app1", "secret")

	err := p.HealthCheck(context.Background(), &IntegrationConnection{ExternalAccountID: "adv1"}, Credentials{AccessToken: "dead"})
	if err == nil {
		t.Fatal("an HTTP 200 carrying a failure code must be an error")
	}
	if IsRetryableHTTP(err) {
		t.Errorf("a dead token must be PERMANENT so the connection flips rather than retrying forever: %v", err)
	}
}

// The throttles must stay retryable, or a busy afternoon flips a healthy connection
// to `error` and tells the admin to redo OAuth over a rate limit.
func TestTikTok_ThrottleCodesStayRetryable(t *testing.T) {
	for _, code := range []int{40016, 40100, 40133} {
		f := newFakeTikTok(t)
		f.failCode = code
		p := f.provider("app1", "secret")
		err := p.HealthCheck(context.Background(), &IntegrationConnection{ExternalAccountID: "adv1"}, Credentials{AccessToken: "t"})
		if err == nil {
			t.Fatalf("code %d should surface as an error", code)
		}
		if !IsRetryableHTTP(err) {
			t.Errorf("code %d is a documented throttle and must be retryable: %v", code, err)
		}
	}
}

// 20001 is documented as a SUCCESS code ("partially successful"). Treating it as a
// failure would discard a response that did contain data.
func TestTikTok_PartialSuccessIsNotAFailure(t *testing.T) {
	var out struct {
		List []tiktokAdvertiser `json:"list"`
	}
	body := []byte(`{"code":20001,"message":"Partially successful","data":{"list":[{"advertiser_id":"a1"}]}}`)
	if err := decodeTikTok(body, &out); err != nil {
		t.Fatalf("20001 must not be treated as a failure: %v", err)
	}
	if len(out.List) != 1 {
		t.Errorf("the data must still be decoded, got %v", out.List)
	}
}

func TestTikTok_ExchangeCallbackReturnsAdvertiserAccounts(t *testing.T) {
	f := newFakeTikTok(t)
	f.advertisers = []map[string]any{
		{"advertiser_id": "adv1", "advertiser_name": "Acme Ads"},
		{"advertiser_id": "adv2", "advertiser_name": ""},
	}
	p := f.provider("app1", "secret")

	accounts, err := p.ExchangeCallback(context.Background(), "auth-code", "", "")
	if err != nil {
		t.Fatalf("ExchangeCallback: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("expected one account per advertiser, got %d", len(accounts))
	}
	if accounts[0].ID != "adv1" || accounts[0].Label != "Acme Ads" {
		t.Errorf("unexpected first account: %+v", accounts[0])
	}
	if accounts[1].Label == "" {
		t.Errorf("an unnamed advertiser still needs a label to pick from, got %+v", accounts[1])
	}
	for _, a := range accounts {
		if a.Credentials.AccessToken != "tt-token" {
			t.Errorf("every advertiser in one grant shares its token, got %q", a.Credentials.AccessToken)
		}
	}
}

// The signature is over an ASCII-ESCAPED form of the payload, not the bytes on the
// wire. Hashing the raw body works for every ASCII payload and then fails the first
// time a lead carries an accented name — which would look like an attack rather than
// a bug, and would reject a real person's lead.
func TestTikTok_VerifyWebhookHashesTheEscapedPayload(t *testing.T) {
	p := NewTikTokProvider("app1", "the-secret", "", "", nil)
	body := []byte(`{"name":"Ada Lovelåce"}`)

	sign := func(msg []byte) string {
		m := hmac.New(sha256.New, []byte("the-secret"))
		m.Write(msg)
		return hex.EncodeToString(m.Sum(nil))
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Open-Signature", sign(escapeNonASCII(body)))
	if err := p.VerifyWebhook(req, body); err != nil {
		t.Fatalf("a correctly escaped signature must verify: %v", err)
	}

	// The control, and the whole point: signing the RAW bytes must NOT verify, or the
	// escaping is decorative and the first non-ASCII lead is rejected in production.
	raw := httptest.NewRequest(http.MethodPost, "/", nil)
	raw.Header.Set("X-Open-Signature", sign(body))
	if err := p.VerifyWebhook(raw, body); err == nil {
		t.Error("a raw-byte signature must be refused — it is not what TikTok computes")
	}
}

func TestTikTok_EscapeNonASCIILeavesASCIIAlone(t *testing.T) {
	in := []byte(`{"a":"plain ascii","b":1}`)
	if got := string(escapeNonASCII(in)); got != string(in) {
		t.Errorf("ASCII must pass through byte-identically:\n got %s\nwant %s", got, in)
	}
	if got := string(escapeNonASCII([]byte("\u00e5"))); got != "\\u00e5" {
		t.Errorf("non-ASCII must become a lowercase escape, got %s", got)
	}
	// Outside the BMP: a surrogate pair, matching what encoding/json emits.
	if got := string(escapeNonASCII([]byte("\U0001F600"))); got != "\\ud83d\\ude00" {
		t.Errorf("an astral rune must become a surrogate pair, got %s", got)
	}
}

const tiktokLeadBody = `{
  "object": 1,
  "time": 1614334356,
  "entry": [{
    "id": 7012345678901234567,
    "page_id": 1700000000000001,
    "advertiser_id": 6900000000000001,
    "campaign_id": 1234, "adgroup_id": 5678, "ad_id": 9012,
    "create_time": 1614239152,
    "lead_source": "INSTANT_FORM",
    "changes": [
      {"field": "email", "value": "lead@example.com"},
      {"field": "phone_number", "value": "15088888888"},
      {"field": "name", "value": "Jane Doe"}
    ]
  }]
}`

// The webhook carries the answers, so ParseWebhook must bring them along — this is
// the difference the whole framework change exists for.
func TestTikTok_ParseWebhookCarriesTheAnswers(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "", "", nil)
	events, err := p.ParseWebhook([]byte(tiktokLeadBody))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one lead, got %d", len(events))
	}
	ev := events[0]
	// Ids arrive as JSON NUMBERS on the webhook while every REST endpoint returns
	// them as strings — coercing is not optional.
	if ev.ProviderEventID != "7012345678901234567" {
		t.Errorf("lead id must be coerced from a number, got %q", ev.ProviderEventID)
	}
	if ev.ExternalAccountID != "6900000000000001" {
		t.Errorf("advertiser id must route the delivery, got %q", ev.ExternalAccountID)
	}
	if ev.FormID != "1700000000000001" {
		t.Errorf("page_id is the form id, got %q", ev.FormID)
	}
	fields, _ := ev.Raw["fields"].(map[string]any)
	if fields["email"] != "lead@example.com" || fields["name"] != "Jane Doe" {
		t.Errorf("the answers must ride with the delivery, got %v", fields)
	}
}

// A single callback URL also receives ad-review and service-status events. Treating
// one as a lead would quarantine noise into the customer's delivery log forever.
func TestTikTok_ParseWebhookIgnoresNonLeadObjects(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "", "", nil)
	events, err := p.ParseWebhook([]byte(`{"object":2,"entry":[{"id":1}]}`))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("only object 1 is a lead; got %d events", len(events))
	}
}

// FetchLead makes no network call, because there is no by-id read to make: TikTok's
// /lead/get/ takes a form and returns one unspecified lead.
func TestTikTok_FetchLeadReadsTheDeliveryRatherThanTheNetwork(t *testing.T) {
	f := newFakeTikTok(t)
	p := f.provider("app1", "secret")
	events, _ := p.ParseWebhook([]byte(tiktokLeadBody))

	lead, err := p.FetchLead(context.Background(), nil, Credentials{}, events[0])
	if err != nil {
		t.Fatalf("FetchLead: %v", err)
	}
	if lead.Fields["email"] != "lead@example.com" {
		t.Errorf("the lead must come from the delivery, got %v", lead.Fields)
	}
	if lead.Context["form_id"] != "1700000000000001" {
		t.Errorf("the routing context must survive, got %v", lead.Context)
	}
	if len(f.sawPaths) != 0 {
		t.Errorf("FetchLead must not call the API at all, saw %v", f.sawPaths)
	}
}

// A delivery that arrives without answers is permanently unusable — a redelivery of
// the same bytes is equally empty — so it must fail rather than look like a lead.
func TestTikTok_FetchLeadRefusesAnEmptyDelivery(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "", "", nil)
	_, err := p.FetchLead(context.Background(), nil, Credentials{},
		InboundEvent{ProviderEventID: "1", Raw: map[string]any{}})
	if err == nil {
		t.Error("a delivery with no fields must not be written as a lead")
	}
}

func TestTikTok_ListFormsFollowsPaging(t *testing.T) {
	f := newFakeTikTok(t)
	f.forms = []map[string]any{
		{"page_id": "form1", "title": "Spring", "status": "PUBLISHED"},
		{"page_id": 220000, "title": "Numeric id", "status": "PUBLISHED"},
	}
	p := f.provider("app1", "secret")

	forms, err := p.ListForms(context.Background(), &IntegrationConnection{ExternalAccountID: "adv1"}, Credentials{AccessToken: "t"})
	if err != nil {
		t.Fatalf("ListForms: %v", err)
	}
	if len(forms) != 2 {
		t.Fatalf("expected both forms, got %d", len(forms))
	}
	if forms[1].ID != "220000" {
		t.Errorf("a numeric page_id must coerce to a string, got %q", forms[1].ID)
	}
}

// The subscription is ours to create and TikTok documents that revoking the token
// silently kills it — no error, deliveries just stop. So the probe has to be real.
func TestTikTok_CheckSubscriptionMatchesOurCallbackAndAdvertiser(t *testing.T) {
	cases := []struct {
		name string
		subs []map[string]any
		want bool
	}{
		{"ours, for this advertiser", []map[string]any{
			{"subscription_id": "s1", "callback_url": "https://crm.example/api/integrations/tiktok/webhook",
				"subscription_detail": map[string]any{"advertiser_id": "adv1"}},
		}, true},
		// Subscribed, but delivering somewhere that is not us.
		{"someone else's callback", []map[string]any{
			{"subscription_id": "s2", "callback_url": "https://other.example/hook",
				"subscription_detail": map[string]any{"advertiser_id": "adv1"}},
		}, false},
		{"our callback, a different advertiser", []map[string]any{
			{"subscription_id": "s3", "callback_url": "https://crm.example/api/integrations/tiktok/webhook",
				"subscription_detail": map[string]any{"advertiser_id": "adv999"}},
		}, false},
		{"none at all", []map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeTikTok(t)
			f.subscriptions = tc.subs
			p := f.provider("app1", "secret")
			got, err := p.CheckSubscription(context.Background(),
				&IntegrationConnection{ExternalAccountID: "adv1"}, Credentials{AccessToken: "t"})
			if err != nil {
				t.Fatalf("CheckSubscription: %v", err)
			}
			if got != tc.want {
				t.Errorf("subscribed = %v, want %v", got, tc.want)
			}
		})
	}
}

// The token goes in Access-Token. The exchange response says token_type "Bearer" and
// the API does not accept it that way — an easy and silent thing to get wrong.
func TestTikTok_SendsAccessTokenHeaderNotBearer(t *testing.T) {
	f := newFakeTikTok(t)
	p := f.provider("app1", "secret")
	if _, err := p.ListForms(context.Background(),
		&IntegrationConnection{ExternalAccountID: "adv1"}, Credentials{AccessToken: "tok-123"}); err != nil {
		t.Fatalf("ListForms: %v", err)
	}
	if len(f.sawTokenHeaders) == 0 || f.sawTokenHeaders[0] != "tok-123" {
		t.Errorf("the token must ride in Access-Token, saw %v", f.sawTokenHeaders)
	}
}

// The authorization URL is operator-supplied because TikTok does not document its
// parameters. What the framework DOES need is that state survives.
func TestTikTok_AuthURLAppendsStateToTheConfiguredURL(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "https://business-api.tiktok.com/portal/auth?app_id=app1", "", nil)
	got := p.AuthURL("st4te", "", "")
	if !strings.Contains(got, "state=st4te") || !strings.HasPrefix(got, "https://business-api.tiktok.com/portal/auth?app_id=app1") {
		t.Errorf("state must be appended to the configured URL, got %s", got)
	}
	// Unconfigured is empty rather than a guessed URL: StartConnect refuses an empty
	// auth URL, which is a visible failure instead of a broken consent page.
	if bare := NewTikTokProvider("app1", "s", "", "", nil).AuthURL("x", "", ""); bare != "" {
		t.Errorf("with no configured URL there is nothing honest to return, got %s", bare)
	}
}

// The adapter must declare the capability the framework branches on, and name the
// kind and config namespace the generic form lookup uses.
func TestTikTok_InfoDeclaresItsFrameworkContract(t *testing.T) {
	info := NewTikTokProvider("a", "b", "", "", nil).Info()
	if !info.CarriesLeadData {
		t.Error("TikTok's webhook carries the lead; the worker must not try to fetch one")
	}
	if info.SourceKind != KindTikTokForm {
		t.Errorf("SourceKind = %q, want %q", info.SourceKind, KindTikTokForm)
	}
	if info.Key != ProviderKeyTikTok {
		t.Errorf("Key = %q, want %q", info.Key, ProviderKeyTikTok)
	}
}

// The defect review caught, and the one that would have destroyed real leads: the
// pipeline REWRITES raw_payload as a delivery moves through it — a quarantined or
// re-ingested row holds the flat field map, not the {fields, context} envelope
// ParseWebhook stored. Since a CarriesLeadData provider reads the lead back out of
// that row instead of off the network, an envelope-only reader would find nothing on
// exactly the SECOND attempt: the lead would be lost and the failure would be
// reported as a dead credential, flipping a healthy advertiser connection.
func TestTikTok_FetchLeadSurvivesAPipelineRewrittenPayload(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "", "", nil)

	// The shape a quarantined / re-ingested delivery actually holds.
	flat := InboundEvent{ProviderEventID: "7012345678901234567", Raw: map[string]any{
		"email": "again@example.com", "name": "Jane Doe",
	}}
	lead, err := p.FetchLead(context.Background(), nil, Credentials{}, flat)
	if err != nil {
		t.Fatalf("a retried delivery must still yield its lead: %v", err)
	}
	if lead.Fields["email"] != "again@example.com" {
		t.Errorf("the answers must survive the rewrite, got %v", lead.Fields)
	}

	// And the original envelope must still work, or the FIRST attempt breaks instead.
	events, _ := p.ParseWebhook([]byte(tiktokLeadBody))
	first, err := p.FetchLead(context.Background(), nil, Credentials{}, events[0])
	if err != nil {
		t.Fatalf("the envelope shape must still work: %v", err)
	}
	if first.Fields["email"] != "lead@example.com" {
		t.Errorf("envelope read broke, got %v", first.Fields)
	}
}

// An unusable delivery must not be reported as a credential failure. The worker flips
// the whole connection to `error` — "reconnect this account" — for any non-retryable
// fetch error, and for a provider whose "fetch" is a read of our own stored row that
// would blame the token for a malformed callback.
func TestTikTok_EmptyDeliveryIsUnusableRatherThanACredentialFailure(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "", "", nil)
	_, err := p.FetchLead(context.Background(), nil, Credentials{},
		InboundEvent{ProviderEventID: "1", Raw: map[string]any{}})
	if err == nil {
		t.Fatal("an empty delivery must not be written as a lead")
	}
	if !errors.Is(err, ErrDeliveryUnusable) {
		t.Errorf("it must be distinguishable from a dead token, got %v", err)
	}
	if IsRetryableHTTP(err) {
		t.Errorf("and retrying it cannot help: %v", err)
	}
}

// A 19-digit lead id must survive verbatim. Decoding through float64 returns a
// DIFFERENT id, which breaks dedupe (every redelivery looks new) and points the
// ledger at a lead that does not exist.
func TestTikTok_LeadIDKeepsFullPrecision(t *testing.T) {
	p := NewTikTokProvider("app1", "s", "", "", nil)
	events, err := p.ParseWebhook([]byte(tiktokLeadBody))
	if err != nil || len(events) != 1 {
		t.Fatalf("ParseWebhook: %v (%d events)", err, len(events))
	}
	if events[0].ProviderEventID != "7012345678901234567" {
		t.Errorf("id lost precision: got %q", events[0].ProviderEventID)
	}
}
