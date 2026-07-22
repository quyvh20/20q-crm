package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The TikTok Lead Generation provider (L7.5), the second adapter on the L5.1
// connector framework — and the one that proved the framework was still
// Facebook-shaped in four places.
//
// It differs from Meta in three ways that are capability differences rather than
// dialect, each verified against TikTok's own documentation:
//
//  1. THE WEBHOOK CARRIES THE LEAD. Meta sends ids and we read the lead back with
//     GET /{leadgen_id}. TikTok sends the answers inline, and its /lead/get/ endpoint
//     has NO lead id parameter at all — there is no by-id read to make. So FetchLead
//     here is a pure function over the delivery we already stored, and the adapter
//     declares CarriesLeadData so the framework knows that is deliberate.
//  2. THE SIGNATURE IS NOT OVER THE RAW BYTES. TikTok's docs are explicit: the HMAC
//     is computed over an ASCII-ESCAPED form of the payload, and "if you just
//     calculate against the decoded bytes, you will end up with a different
//     signature". See verifyOpenSignature.
//  3. FAILURE IS AN HTTP 200. Every response carries a `code` field, and the docs say
//     it "takes precedence over HTTP status codes" — a 200 with code 40105 is a dead
//     token. The shared HTTPClient's status-based taxonomy cannot see that, so every
//     call goes through decodeTikTok.
//
// Everything talks to the Business API through the shared HTTPClient, so the adapter
// is exercisable against an httptest stand-in. LIVE verification is gated on a real
// TikTok app AND an ad account the connecting user administers: unlike Meta there is
// no sandbox path, because TikTok's sandbox does not serve the lead endpoints at all.
const (
	// tiktokAPIVersion is pinned so a retirement is a reviewed bump, not a silent
	// break. v1.3 has been current since Aug 2022; v1.2 was retired a year after v1.3
	// shipped, which is the notice period to expect.
	tiktokAPIVersion = "v1.3"
	tiktokBase       = "https://business-api.tiktok.com/open_api/" + tiktokAPIVersion

	// Every Business API path ends in a slash. Omitting it returns a bare
	// "404 page not found" rather than the usual error envelope — documented, and
	// easy to reintroduce, so the paths below are written with it and left alone.
	tiktokMaxPages = 50
)

// ProviderKeyTikTok is the registry key / URL identifier. It also names the config
// namespace a tiktok_form source stores its form id under.
const ProviderKeyTikTok = "tiktok"

// TikTokProvider implements Provider for TikTok Lead Generation.
type TikTokProvider struct {
	UnimplementedProvider
	appID     string
	appSecret string
	// authURL is the advertiser authorization URL, copied from the app's own page in
	// the TikTok developer portal.
	//
	// Operator-supplied rather than constructed, because TikTok does not document the
	// query parameters: both official auth pages say to copy the URL out of the portal
	// and send it to the advertiser. Building it from guessed parameter names would be
	// a URL that looks right and silently fails at consent time, so the one thing we
	// cannot verify is the one thing we do not invent.
	authURL string
	// callbackURL is where TikTok POSTs lead events. Unlike Meta's, the subscription
	// is created by an API call that carries this URL, so the adapter needs it.
	callbackURL string
	http        *HTTPClient
	baseURL     string // test seam; production uses tiktokBase
}

// NewTikTokProvider builds the adapter.
func NewTikTokProvider(appID, appSecret, authURL, callbackURL string, httpClient *HTTPClient) *TikTokProvider {
	if httpClient == nil {
		httpClient = NewHTTPClient(nil)
	}
	return &TikTokProvider{
		appID: appID, appSecret: appSecret,
		authURL: authURL, callbackURL: callbackURL,
		http: httpClient, baseURL: tiktokBase,
	}
}

// Info describes the provider.
func (p *TikTokProvider) Info() ProviderInfo {
	return ProviderInfo{
		Key:              ProviderKeyTikTok,
		Label:            "TikTok Lead Generation",
		SourceKind:       KindTikTokForm,
		SupportsWebhooks: true,
		// TikTok's advertiser flow authenticates the exchange with the app secret; no
		// code_challenge appears anywhere in its auth documentation.
		UsesPKCE: false,
		// The distinguishing capability — see the file comment.
		CarriesLeadData: true,
	}
}

// AuthURL returns the operator-supplied authorization URL with our state appended.
//
// `state` is the one parameter TikTok documents ("it will be echoed back to your
// application"), and it is the only thing the framework needs to carry: the org and
// user are resolved from the server-side state row, never from the URL.
func (p *TikTokProvider) AuthURL(state, _, _ string) string {
	if p.authURL == "" {
		return ""
	}
	sep := "?"
	if strings.Contains(p.authURL, "?") {
		sep = "&"
	}
	return p.authURL + sep + "state=" + url.QueryEscape(state)
}

// ExchangeCallback trades the auth code for a long-lived token and lists the
// advertiser accounts it covers.
//
// The token does NOT expire and there is no refresh token — TikTok's docs say so
// outright, and using a refresh token against this flow is a documented error
// (40107). So there is no renewal machinery here on purpose; a connection ends
// because it was revoked, not because it aged out.
func (p *TikTokProvider) ExchangeCallback(ctx context.Context, code, _, _ string) ([]Account, error) {
	body, err := json.Marshal(map[string]string{
		"app_id": p.appID, "secret": p.appSecret, "auth_code": code,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := p.call(ctx, http.MethodPost, p.baseURL+"/oauth2/access_token/", "", body, &out); err != nil {
		return nil, err
	}
	if out.AccessToken == "" {
		return nil, errors.New("tiktok: the token exchange returned no access token")
	}

	advertisers, err := p.listAdvertisers(ctx, out.AccessToken)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(advertisers))
	for _, a := range advertisers {
		if a.ID == "" {
			continue
		}
		label := a.Name
		if label == "" {
			label = "Advertiser " + a.ID
		}
		accounts = append(accounts, Account{
			ID:    a.ID,
			Label: label,
			// One token covers every advertiser in the grant, so each connection stores
			// the same one. That is TikTok's model, not a shortcut: /oauth2/access_token/
			// returns a single token plus the advertiser_ids it spans.
			Credentials: Credentials{AccessToken: out.AccessToken, TokenType: "bearer"},
		})
	}
	if len(accounts) == 0 {
		return nil, errors.New("tiktok: the grant covered no advertiser accounts")
	}
	return accounts, nil
}

type tiktokAdvertiser struct {
	ID   string `json:"advertiser_id"`
	Name string `json:"advertiser_name"`
}

func (p *TikTokProvider) listAdvertisers(ctx context.Context, token string) ([]tiktokAdvertiser, error) {
	q := url.Values{}
	q.Set("app_id", p.appID)
	q.Set("secret", p.appSecret)
	var out struct {
		List []tiktokAdvertiser `json:"list"`
	}
	if err := p.get(ctx, token, "/oauth2/advertiser/get/?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out.List, nil
}

// Subscribe registers our callback for this advertiser's lead events.
//
// Two things differ from Meta and both matter operationally. The subscription is
// created by an API call rather than console configuration, so it is ours to
// re-create; and TikTok documents that revoking the access token silently kills the
// subscription — no error, no notification, deliveries simply stop. That is why
// reconnecting must re-subscribe, and why CheckSubscription below is not optional
// decoration on this provider.
func (p *TikTokProvider) Subscribe(ctx context.Context, conn *IntegrationConnection, creds Credentials) error {
	if p.callbackURL == "" {
		return errors.New("tiktok: no callback URL configured, cannot subscribe")
	}
	body, err := json.Marshal(map[string]any{
		"app_id": p.appID, "secret": p.appSecret,
		"subscribe_entity": "LEAD",
		"callback_url":     p.callbackURL,
		"subscription_detail": map[string]any{
			"access_token":  creds.AccessToken,
			"advertiser_id": conn.ExternalAccountID,
			// INSTANT_FORM, not DIRECT_MESSAGE: this platform ingests forms.
			"lead_source": "INSTANT_FORM",
		},
	})
	if err != nil {
		return err
	}
	var out struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := p.call(ctx, http.MethodPost, p.baseURL+"/subscription/subscribe/", "", body, &out); err != nil {
		return err
	}
	if out.SubscriptionID == "" {
		return errors.New("tiktok: the subscription was not confirmed")
	}
	return nil
}

// CheckSubscription reports whether a live LEAD subscription still points at us for
// this advertiser.
//
// It matches on OUR callback URL as well as the advertiser, because a subscription
// belonging to a different app or pointing somewhere else delivers nothing here — the
// same reason the Facebook probe matches on our app id.
func (p *TikTokProvider) CheckSubscription(ctx context.Context, conn *IntegrationConnection, creds Credentials) (bool, error) {
	q := url.Values{}
	q.Set("app_id", p.appID)
	q.Set("secret", p.appSecret)
	q.Set("subscribe_entity", "LEAD")
	q.Set("page_size", "100")
	var out struct {
		Subscriptions []struct {
			SubscriptionID string `json:"subscription_id"`
			CallbackURL    string `json:"callback_url"`
			Detail         struct {
				AdvertiserID json.RawMessage `json:"advertiser_id"`
			} `json:"subscription_detail"`
		} `json:"subscriptions"`
	}
	if err := p.get(ctx, creds.AccessToken, "/subscription/get/?"+q.Encode(), &out); err != nil {
		return false, err
	}
	for _, s := range out.Subscriptions {
		if tiktokID(s.Detail.AdvertiserID) != conn.ExternalAccountID {
			continue
		}
		if p.callbackURL != "" && s.CallbackURL != p.callbackURL {
			// Subscribed, but delivering to something that is not us.
			continue
		}
		return true, nil
	}
	return false, nil
}

// Disconnect is intentionally a no-op beyond the framework's own teardown.
//
// TikTok's unsubscribe is keyed by subscription_id, which we do not persist, and the
// docs state that revoking the token kills the subscription anyway. Guessing at a
// subscription id — or unsubscribing by advertiser, which the API does not offer —
// risks tearing down a subscription another workspace owns, the exact cross-tenant
// mistake the L6.4b teardown was corrected for.
func (p *TikTokProvider) Disconnect(context.Context, *IntegrationConnection, Credentials) error {
	return nil
}

// VerifyWebhook authenticates a lead callback.
//
// The signature is HMAC-SHA256 over an ASCII-ESCAPED form of the payload, not over
// the bytes on the wire — TikTok documents this explicitly, with the example that
// "äöå should be escaped to äöå". Hashing the raw body works for
// every ASCII-only payload and then fails the first time a lead contains an accented
// name, which is the worst possible failure shape: it would look like an attack.
func (p *TikTokProvider) VerifyWebhook(r *http.Request, body []byte) error {
	got := strings.TrimSpace(r.Header.Get("X-Open-Signature"))
	if got == "" {
		return errors.New("tiktok: missing X-Open-Signature")
	}
	raw, err := hex.DecodeString(got)
	if err != nil {
		return errors.New("tiktok: signature is not valid hex")
	}
	mac := hmac.New(sha256.New, []byte(p.appSecret))
	mac.Write(escapeNonASCII(body))
	if !hmac.Equal(raw, mac.Sum(nil)) {
		return errors.New("tiktok: webhook signature mismatch")
	}
	return nil
}

// escapeNonASCII rewrites every non-ASCII rune as a lowercase \uXXXX escape and
// leaves everything else — including whitespace and key order — exactly as received.
//
// A byte scan rather than a re-marshal: unmarshalling and re-encoding would reorder
// keys and renormalise whitespace, so the bytes hashed would no longer be the bytes
// TikTok hashed. Runes outside the BMP become a surrogate pair, which is what Go's
// own encoding/json produces and therefore what the escaping convention expects.
func escapeNonASCII(body []byte) []byte {
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); {
		c := body[i]
		if c < utf8.RuneSelf {
			out = append(out, c)
			i++
			continue
		}
		r, size := utf8.DecodeRune(body[i:])
		if r == utf8.RuneError && size <= 1 {
			// Not valid UTF-8; pass the byte through rather than inventing an escape.
			out = append(out, c)
			i++
			continue
		}
		if r > 0xFFFF {
			r -= 0x10000
			out = append(out, []byte(fmt.Sprintf(`\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF)))...)
		} else {
			out = append(out, []byte(fmt.Sprintf(`\u%04x`, r))...)
		}
		i += size
	}
	return out
}

// tiktokWebhook is the lead callback body.
type tiktokWebhook struct {
	// Object discriminates the event type. 1 is a lead; a single callback URL also
	// receives ad-review, creative-fatigue and service-status events, so this is
	// checked FIRST — treating an ad-review notice as a lead would quarantine noise
	// into the customer's delivery log forever.
	Object int             `json:"object"`
	Entry  []tiktokEntry   `json:"entry"`
	Time   json.RawMessage `json:"time"`
}

type tiktokEntry struct {
	// ID is the LEAD id, and it arrives as a JSON NUMBER here while every REST
	// endpoint returns the same ids as strings.
	ID           json.RawMessage `json:"id"`
	PageID       json.RawMessage `json:"page_id"`
	AdvertiserID json.RawMessage `json:"advertiser_id"`
	CampaignID   json.RawMessage `json:"campaign_id"`
	AdgroupID    json.RawMessage `json:"adgroup_id"`
	AdID         json.RawMessage `json:"ad_id"`
	CreateTime   json.RawMessage `json:"create_time"`
	LeadSource   string          `json:"lead_source"`
	Changes      []struct {
		Field string          `json:"field"`
		Value json.RawMessage `json:"value"`
	} `json:"changes"`
}

// ParseWebhook turns a verified callback into one InboundEvent per lead, carrying
// the answers with it.
func (p *TikTokProvider) ParseWebhook(body []byte) ([]InboundEvent, error) {
	var env tiktokWebhook
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("tiktok: could not parse webhook body: %w", err)
	}
	// object 1 is the lead event. Anything else on this URL is a different product
	// notification and is not ours to interpret.
	if env.Object != 1 {
		return nil, nil
	}
	var out []InboundEvent
	for _, e := range env.Entry {
		leadID := tiktokID(e.ID)
		advertiserID := tiktokID(e.AdvertiserID)
		if leadID == "" || advertiserID == "" {
			// Without a stable delivery id there is nothing to dedupe on, and without an
			// advertiser there is no connection to route to.
			continue
		}
		fields := make(map[string]any, len(e.Changes))
		for _, ch := range e.Changes {
			name := strings.TrimSpace(ch.Field)
			if name == "" {
				continue
			}
			fields[name] = tiktokValue(ch.Value)
		}
		leadCtx := map[string]any{}
		for k, v := range map[string]string{
			"form_id":       tiktokID(e.PageID),
			"advertiser_id": advertiserID,
			"campaign_id":   tiktokID(e.CampaignID),
			"adgroup_id":    tiktokID(e.AdgroupID),
			"ad_id":         tiktokID(e.AdID),
			"created_time":  tiktokID(e.CreateTime),
			"lead_source":   e.LeadSource,
		} {
			if v != "" {
				leadCtx[k] = v
			}
		}
		out = append(out, InboundEvent{
			ExternalAccountID: advertiserID,
			ProviderEventID:   leadID,
			FormID:            tiktokID(e.PageID),
			// The answers travel with the delivery. The worker stores this verbatim as
			// raw_payload and hands it back to FetchLead, so the lead survives a restart
			// between receipt and processing exactly like a fetched one.
			Raw: map[string]any{"fields": fields, "context": leadCtx},
		})
	}
	return out, nil
}

// FetchLead reads the lead out of the delivery instead of off the network.
//
// TikTok's GET /lead/get/ takes a form and returns ONE lead, with no lead id
// parameter and no documented rule about which lead that is — so there is no by-id
// read to make, and inventing one would be a lottery over the advertiser's data. The
// webhook already carried the answers; the worker rehydrates them into ev.Raw.
func (p *TikTokProvider) FetchLead(_ context.Context, _ *IntegrationConnection, _ Credentials, ev InboundEvent) (RawLead, error) {
	if ev.ProviderEventID == "" {
		return RawLead{}, errors.New("tiktok: delivery carries no lead id")
	}
	fields, leadCtx := tiktokLeadFrom(ev.Raw)
	if len(fields) == 0 {
		// No answers anywhere in the stored delivery. Wrapped in ErrDeliveryUnusable
		// because the worker's fetch-failure path otherwise reads every non-retryable
		// error as a dead credential: it would flip the whole advertiser connection to
		// `error` and tell the admin to reconnect, over one content-free callback that
		// says nothing about the token. The delivery fails; the connection is untouched.
		return RawLead{}, fmt.Errorf("%w: tiktok delivery carried no lead fields", ErrDeliveryUnusable)
	}
	return RawLead{Fields: fields, Context: leadCtx, ProviderEventID: ev.ProviderEventID}, nil
}

// tiktokLeadFrom reads the answers out of a stored delivery, accepting BOTH shapes
// the row can legitimately hold.
//
// ParseWebhook stores the envelope {fields, context}. But the pipeline REWRITES
// raw_payload as it goes: a quarantined delivery and a re-ingested one both store the
// flat field map instead. For a provider whose lead is read back from that row rather
// than from the network, an envelope-only reader would find nothing on exactly the
// second attempt — so a retried lead would be destroyed AND misreported as a dead
// credential. Both shapes are therefore first-class here.
func tiktokLeadFrom(raw map[string]any) (map[string]any, map[string]any) {
	if len(raw) == 0 {
		return nil, map[string]any{}
	}
	if inner, ok := raw["fields"].(map[string]any); ok {
		leadCtx, _ := raw["context"].(map[string]any)
		if leadCtx == nil {
			leadCtx = map[string]any{}
		}
		return inner, leadCtx
	}
	// A flat map: the pipeline already unwrapped this delivery once. The routing
	// context is gone from the payload by then, but the worker reads form_id from the
	// event's own context column, so nothing that matters is lost.
	return raw, map[string]any{}
}

// ListForms enumerates an advertiser's Instant Forms.
//
// business_type=LEAD_GEN selects lead forms; the same endpoint also returns pop-up
// message pages and storefronts, which are not lead sources. `page_id` is the form
// id and `title` is its name — TikTok calls the same thing page_id here, form_id in
// its lead export, and `id` in the webhook.
func (p *TikTokProvider) ListForms(ctx context.Context, conn *IntegrationConnection, creds Credentials) ([]ProviderForm, error) {
	var forms []ProviderForm
	for page := 1; page <= tiktokMaxPages; page++ {
		q := url.Values{}
		q.Set("advertiser_id", conn.ExternalAccountID)
		q.Set("business_type", "LEAD_GEN")
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", "100")
		var out struct {
			List []struct {
				PageID json.RawMessage `json:"page_id"`
				Title  string          `json:"title"`
				Status string          `json:"status"`
			} `json:"list"`
			PageInfo struct {
				TotalPage int `json:"total_page"`
			} `json:"page_info"`
		}
		if err := p.get(ctx, creds.AccessToken, "/page/get/?"+q.Encode(), &out); err != nil {
			return nil, err
		}
		for _, f := range out.List {
			id := tiktokID(f.PageID)
			if id == "" {
				continue
			}
			forms = append(forms, ProviderForm{ID: id, Name: f.Title, Status: f.Status})
		}
		if out.PageInfo.TotalPage <= page || len(out.List) == 0 {
			break
		}
	}
	return forms, nil
}

// SeedFieldMap maps TikTok's standard Instant Form question names onto contact
// fields. Custom questions arrive under their own names and are mapped by the admin.
func (p *TikTokProvider) SeedFieldMap() FieldMap {
	return FieldMap{
		"email":        {TargetKey: "email"},
		"phone_number": {TargetKey: "phone"},
		"name":         {TargetKey: "first_name", Transform: TransformSplitName},
		"first_name":   {TargetKey: "first_name"},
		"last_name":    {TargetKey: "last_name"},
	}
}

// HealthCheck probes whether the stored token still works, by asking for the
// advertisers it covers.
func (p *TikTokProvider) HealthCheck(ctx context.Context, _ *IntegrationConnection, creds Credentials) error {
	_, err := p.listAdvertisers(ctx, creds.AccessToken)
	return err
}

// ── Business API plumbing ──────────────────────────────────────────────────

// tiktokEnvelope is the shape EVERY Business API response has, success or failure.
type tiktokEnvelope struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// tiktokRetryableCodes are the documented throttles. They must stay RETRYABLE or a
// busy afternoon flips a healthy connection to `error` and tells the admin to redo
// OAuth over a rate limit — the same reclassification wrapGraphError exists for.
var tiktokRetryableCodes = map[int]bool{
	40016: true, // requests made too frequently (app level)
	40100: true, // requests made too frequently (app level)
	40132: true, // per-field-value QPS
	40133: true, // advertiser-level QPS
}

// decodeTikTok converts a Business API envelope into an error or the data blob.
//
// The load-bearing part: TikTok returns HTTP 200 on failure and puts the verdict in
// `code`, and its docs say that code "takes precedence over HTTP status codes". The
// shared HTTPClient classifies on status, so without this every dead token would look
// like a success and every lead behind it would be written from an empty response.
func decodeTikTok(raw []byte, out any) error {
	var env tiktokEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("tiktok: unreadable response: %w", err)
	}
	code := 0
	if len(env.Code) > 0 {
		_ = json.Unmarshal(env.Code, &code)
	}
	// 0 is success; 20001 is "partially successful" and is documented as a SUCCESS
	// code — treating it as a failure would discard a response that did contain data.
	if code != 0 && code != 20001 {
		msg := env.Message
		if msg == "" {
			msg = "request failed"
		}
		return &HTTPError{
			StatusCode: http.StatusOK,
			Retryable:  tiktokRetryableCodes[code],
			Err:        fmt.Errorf("tiktok: %s (code %d)", msg, code),
		}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("tiktok: unreadable data: %w", err)
		}
	}
	return nil
}

func (p *TikTokProvider) get(ctx context.Context, token, path string, out any) error {
	return p.call(ctx, http.MethodGet, p.baseURL+path, token, nil, out)
}

// call issues one Business API request and decodes its envelope.
//
// token may be empty: the token exchange and the subscription endpoints authenticate
// with app_id + secret in the body instead, so sending an empty Access-Token header
// on those is wrong rather than merely useless.
func (p *TikTokProvider) call(ctx context.Context, method, endpoint, token string, body []byte, out any) error {
	req := OutboundRequest{Method: method, URL: endpoint, Header: http.Header{}}
	if token != "" {
		// Access-Token, NOT `Authorization: Bearer`. The token response says
		// token_type "Bearer" and the API does not accept it that way.
		req.Header.Set("Access-Token", token)
	}
	if body != nil {
		req.Body = body
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(ctx, req)
	if err != nil {
		return err
	}
	return decodeTikTok(resp.Body, out)
}

// tiktokID coerces an id that may arrive as a JSON number (webhook) or a string
// (REST) into a plain string. TikTok is inconsistent about this BY VERSION — v1.2
// returned numbers where v1.3 returns strings — and its webhook still sends numbers,
// so every id read goes through here.
func tiktokID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// A JSON NUMBER's textual form IS the id — take the raw bytes rather than
	// decoding. TikTok lead ids are 19 digits, which float64 cannot hold: decoding
	// 7012345678901234567 through a float returns 7012345678901235000, a DIFFERENT
	// id. That would break dedupe (every redelivery looks new) and make the ledger
	// point at a lead that does not exist. Caught by the parse test.
	txt := strings.TrimSpace(string(raw))
	if txt == "null" {
		return ""
	}
	if _, err := strconv.ParseFloat(txt, 64); err == nil {
		return txt
	}
	return ""
}

// tiktokValue renders an answer. The docs type `value` as an object while every
// example is a string, so both are accepted and anything structured is preserved as
// JSON rather than dropped.
func tiktokValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		return v
	}
	return string(raw)
}
