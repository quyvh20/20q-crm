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
)

// The Facebook Lead Ads provider (L5.2), the first adapter on the L5.1 connector
// framework. It turns a Facebook Login grant into one connection per page (each
// with its own page access token) and subscribes each page's leadgen field so
// the app-level webhook (L5.3) receives its leads.
//
// Everything here talks to the Graph API through the shared HTTPClient, so the
// whole adapter is testable against an httptest server standing in for Graph — no
// live Meta app is needed to exercise the connect/subscribe logic. The live path
// is gated on Meta's business-process track M (Business Verification + App
// Review); the code and its fixtures run against pages we own in dev mode.

const (
	// graphVersion is pinned so a Graph deprecation is a one-line, reviewed bump
	// rather than a silent behavior change. v25.0 is current; v26.0 lands ~Sep 2026.
	graphVersion = "v25.0"
	graphBase    = "https://graph.facebook.com/" + graphVersion
	fbDialogBase = "https://www.facebook.com/" + graphVersion + "/dialog/oauth"

	// facebookScopes is the App Review bundle (submitted as ONE request in track M).
	// Used only in the CLASSIC flow; Login for Business bakes scopes into its
	// config_id, so the scope param is omitted there.
	facebookScopes = "pages_show_list,pages_read_engagement,pages_manage_metadata,pages_manage_ads,leads_retrieval,ads_management"

	// maxAccountPages caps how many pages of /me/accounts results we will follow.
	// A single admin over ~5000 pages is not a real shape; the cap stops a runaway
	// cursor from turning connect into an unbounded fetch.
	maxAccountPages = 50
)

// FacebookProvider implements Provider for Facebook Lead Ads.
//
// It embeds UnimplementedProvider so the webhook/fetch/forms/backfill half of the
// interface (L5.3/L5.4) returns "unsupported" until those phases fill it in —
// L5.2 implements only the connect + subscribe lifecycle.
type FacebookProvider struct {
	UnimplementedProvider
	appID         string
	appSecret     string
	loginConfigID string // Facebook Login for Business config; empty ⇒ classic scope flow
	http          *HTTPClient
	// graphBaseURL is the Graph API origin+version. A field (defaulting to the
	// pinned graphBase const) only so a test can point the adapter at an httptest
	// server standing in for Graph; production never sets it.
	graphBaseURL string
}

// NewFacebookProvider builds the adapter. The HTTPClient is injected so tests can
// point it at a fake Graph.
func NewFacebookProvider(appID, appSecret, loginConfigID string, httpClient *HTTPClient) *FacebookProvider {
	if httpClient == nil {
		httpClient = NewHTTPClient(nil)
	}
	return &FacebookProvider{appID: appID, appSecret: appSecret, loginConfigID: loginConfigID, http: httpClient, graphBaseURL: graphBase}
}

// ProviderKeyFacebook is the registry key / URL identifier.
const ProviderKeyFacebook = "facebook"

// Info describes the provider.
func (p *FacebookProvider) Info() ProviderInfo {
	return ProviderInfo{
		Key: ProviderKeyFacebook,
		// One connection, two placements: an Instagram lead ad runs through the same
		// Facebook Page, the same app subscription and the same leadgen webhook, so a
		// page connected here already receives both (L7.1). The label says so because
		// an admin looking for Instagram would otherwise conclude we do not support it.
		Label:            "Facebook & Instagram Lead Ads",
		SourceKind:       KindFacebookForm,
		SupportsWebhooks: true,
		// Facebook's server-side flow does not use PKCE (it authenticates the token
		// exchange with the app secret), so the framework skips the verifier dance.
		UsesPKCE: false,
	}
}

// AuthURL builds the consent dialog URL. state and redirectURI come from the
// framework; the challenge is unused (no PKCE).
func (p *FacebookProvider) AuthURL(state, redirectURI, _ string) string {
	q := url.Values{}
	q.Set("client_id", p.appID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("response_type", "code")
	if p.loginConfigID != "" {
		// Login for Business: scopes are defined by the configuration, and passing a
		// scope alongside config_id is rejected. This is the path that issues Business
		// Integration System User tokens, which survive the connecting employee's
		// departure — the whole reason a lead pipe must not die with a person.
		q.Set("config_id", p.loginConfigID)
	} else {
		q.Set("scope", facebookScopes)
	}
	return fbDialogBase + "?" + q.Encode()
}

// ExchangeCallback trades the code for one Account per page the grant exposes.
func (p *FacebookProvider) ExchangeCallback(ctx context.Context, code, redirectURI, _ string) ([]Account, error) {
	userToken, err := p.exchangeCode(ctx, code, redirectURI)
	if err != nil {
		return nil, err
	}
	// Upgrade to a long-lived token. Page tokens derived from a long-lived user
	// token are themselves long-lived, which is what lets a connection keep working
	// for months without a re-auth. Best-effort: if the exchange fails (some BISU
	// tokens are already long-lived and the grant type is a no-op or errors), fall
	// back to the token we have rather than failing the whole connect.
	if longToken, lerr := p.exchangeLongLived(ctx, userToken); lerr == nil && longToken != "" {
		userToken = longToken
	}

	pages, err := p.listPages(ctx, userToken)
	if err != nil {
		return nil, err
	}
	accounts := make([]Account, 0, len(pages))
	for _, pg := range pages {
		if pg.ID == "" || pg.AccessToken == "" {
			// A page the admin manages but has no token for (insufficient role) cannot
			// be connected — skip it rather than store a tokenless connection that can
			// never fetch a lead.
			continue
		}
		acc := Account{
			ID:          pg.ID,
			Label:       pg.Name,
			Credentials: Credentials{AccessToken: pg.AccessToken, TokenType: "page"},
		}
		if pg.Category != "" {
			acc.Meta = map[string]any{"category": pg.Category}
		}
		accounts = append(accounts, acc)
	}
	return accounts, nil
}

// Subscribe activates leadgen delivery for the connected page.
func (p *FacebookProvider) Subscribe(ctx context.Context, conn *IntegrationConnection, creds Credentials) error {
	q := url.Values{}
	q.Set("subscribed_fields", "leadgen")
	q.Set("access_token", creds.AccessToken)
	q.Set("appsecret_proof", p.proof(creds.AccessToken))
	endpoint := p.graphBaseURL + "/" + url.PathEscape(conn.ExternalAccountID) + "/subscribed_apps?" + q.Encode()

	var out struct {
		Success bool `json:"success"`
	}
	if err := p.call(ctx, http.MethodPost, endpoint, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("facebook: page %s did not confirm the leadgen subscription", conn.ExternalAccountID)
	}
	return nil
}

// Disconnect unsubscribes the page's app subscription — the counterpart to
// Subscribe, called when a connection is removed. Best-effort at the framework
// level (ConnectionService logs and still soft-deletes).
func (p *FacebookProvider) Disconnect(ctx context.Context, conn *IntegrationConnection, creds Credentials) error {
	q := url.Values{}
	q.Set("access_token", creds.AccessToken)
	q.Set("appsecret_proof", p.proof(creds.AccessToken))
	endpoint := p.graphBaseURL + "/" + url.PathEscape(conn.ExternalAccountID) + "/subscribed_apps?" + q.Encode()
	return p.call(ctx, http.MethodDelete, endpoint, nil)
}

// VerifyWebhook authenticates an inbound leadgen webhook against the raw body
// bytes, per Meta's X-Hub-Signature-256 contract: HMAC-SHA256(rawBody, appSecret),
// hex, compared with a constant-time equal.
//
// The comparison is over the RAW bytes the request arrived with — the caller must
// pass the exact body it hashed, never a re-marshalled struct (re-serialization
// reorders keys and rewrites whitespace, so the signature would never match).
func (p *FacebookProvider) VerifyWebhook(r *http.Request, body []byte) error {
	header := r.Header.Get("X-Hub-Signature-256")
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return errors.New("facebook: missing or malformed X-Hub-Signature-256")
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return errors.New("facebook: signature is not valid hex")
	}
	mac := hmac.New(sha256.New, []byte(p.appSecret))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return errors.New("facebook: webhook signature mismatch")
	}
	return nil
}

// fbWebhookEnvelope is the leadgen webhook body Meta POSTs.
type fbWebhookEnvelope struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"` // the page id
		Time    int64  `json:"time"`
		Changes []struct {
			Field string         `json:"field"`
			Value map[string]any `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// ParseWebhook turns a verified webhook body into one InboundEvent per leadgen
// change. Non-leadgen changes (a page can be subscribed to other fields) are
// skipped, and a change missing the leadgen id is dropped — without a stable
// delivery id there is nothing to dedupe or fetch.
func (p *FacebookProvider) ParseWebhook(body []byte) ([]InboundEvent, error) {
	var env fbWebhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("facebook: could not parse webhook body: %w", err)
	}
	var out []InboundEvent
	for _, entry := range env.Entry {
		for _, ch := range entry.Changes {
			if ch.Field != "leadgen" {
				continue
			}
			leadgenID := fbStr(ch.Value["leadgen_id"])
			if leadgenID == "" {
				continue
			}
			// page_id from the change value, falling back to the entry id (they are the
			// same page; the entry id is always present).
			pageID := fbStr(ch.Value["page_id"])
			if pageID == "" {
				pageID = entry.ID
			}
			out = append(out, InboundEvent{
				ExternalAccountID: pageID,
				ProviderEventID:   leadgenID,
				FormID:            fbStr(ch.Value["form_id"]),
				Raw:               ch.Value,
			})
		}
	}
	return out, nil
}

// fbLead is the /{leadgen_id} response.
type fbLead struct {
	ID          string `json:"id"`
	CreatedTime string `json:"created_time"`
	FormID      string `json:"form_id"`
	AdID        string `json:"ad_id"`
	CampaignID  string `json:"campaign_id"`
	AdgroupID   string `json:"adgroup_id"`
	// Platform names the placement the lead was submitted from ("fb", "ig"). IsOrganic
	// is a POINTER so "Graph did not tell us" stays distinguishable from "no, it was
	// a paid ad" — the same rule the diagnose panel applies to unknown-vs-fail.
	Platform  string `json:"platform"`
	IsOrganic *bool  `json:"is_organic"`
	FieldData []struct {
		Name   string   `json:"name"`
		Values []string `json:"values"`
	} `json:"field_data"`
}

// FetchLead resolves a leadgen delivery into a RawLead: GET /{leadgen_id} with the
// page token, then flatten field_data into a { question_name -> value } map the
// mapping engine consumes. The ad/campaign/form ids ride the context for
// attribution. The webhook carries only ids, so this fetch is where the lead data
// actually enters the system — its failure is loud (the worker flips the
// connection to error) precisely because a silent fetch failure is silent lead loss.
func (p *FacebookProvider) FetchLead(ctx context.Context, _ *IntegrationConnection, creds Credentials, ev InboundEvent) (RawLead, error) {
	if ev.ProviderEventID == "" {
		return RawLead{}, errors.New("facebook: cannot fetch a lead with no leadgen id")
	}
	lead, err := withPlacementFallback(func(fields string) (fbLead, error) {
		q := url.Values{}
		q.Set("fields", fields)
		q.Set("access_token", creds.AccessToken)
		q.Set("appsecret_proof", p.proof(creds.AccessToken))
		endpoint := p.graphBaseURL + "/" + url.PathEscape(ev.ProviderEventID) + "?" + q.Encode()

		var lead fbLead
		err := p.call(ctx, http.MethodGet, endpoint, &lead)
		return lead, err
	})
	if err != nil {
		return RawLead{}, err
	}
	return rawLeadFromFB(lead, ev.ExternalAccountID, ev.FormID, ev.ProviderEventID), nil
}

// withPlacementFallback runs a Graph lead read asking for the placement fields and,
// ONLY if that fails permanently, runs it once more without them.
//
// The asymmetry is the whole point. A `fields` list Graph does not recognise — a
// version retirement, a permission the app has not been granted — fails the WHOLE
// node request, so adding a field to satisfy a reporting question would put every
// Facebook and Instagram lead behind it. A placement label is never worth losing the
// lead it labels. A RETRYABLE error is about the call rather than the field list, so
// it propagates untouched: retrying narrower against an outage would burn a second
// request and learn nothing.
//
// When the narrow read ALSO fails, the FIRST error is what propagates. The fallback
// exists to survive a rejected field list and nothing else, so a second failure is
// proof the field list was never the problem — and the retry classification is
// load-bearing downstream: the async worker repends a retryable failure and flips the
// connection to `error` on a permanent one. Returning the narrow attempt's verdict
// would let a transient blip on the second call mask a genuinely dead token (the
// connection never flips, the reconnect banner never appears) or the reverse.
func withPlacementFallback[T any](attempt func(fields string) (T, error)) (T, error) {
	out, err := attempt(facebookLeadFields)
	if err == nil || IsRetryableHTTP(err) {
		return out, err
	}
	narrow, narrowErr := attempt(facebookLeadFieldsBase)
	if narrowErr != nil {
		return out, err
	}
	return narrow, nil
}

// rawLeadFromFB flattens a Graph lead node into a RawLead. Shared by the webhook
// FetchLead and the backfill pager, so both produce identical shapes. formIDFallback
// / leadgenFallback supply values the node itself may omit (the webhook's change
// carries them; a backfill page does not need a fallback).
func rawLeadFromFB(lead fbLead, pageID, formIDFallback, leadgenFallback string) RawLead {
	fields := make(map[string]any, len(lead.FieldData))
	for _, fd := range lead.FieldData {
		name := strings.TrimSpace(fd.Name)
		if name == "" || len(fd.Values) == 0 {
			continue
		}
		// A standard field has one value; a multi-select can have several. Join
		// rather than drop the extras, so nothing the subject entered is silently lost.
		fields[name] = strings.Join(fd.Values, ", ")
	}

	leadCtx := map[string]any{}
	formID := lead.FormID
	if formID == "" {
		formID = formIDFallback
	}
	for k, v := range map[string]string{
		"form_id":      formID,
		"created_time": lead.CreatedTime,
		"ad_id":        lead.AdID,
		"campaign_id":  lead.CampaignID,
		"adgroup_id":   lead.AdgroupID,
		"page_id":      pageID,
		// Facebook and Instagram leads are otherwise byte-identical here: same page,
		// same webhook, same form. This key is the only thing in the ledger that can
		// answer "are the Instagram leads arriving?" (L7.1).
		"platform": lead.Platform,
	} {
		if v != "" {
			leadCtx[k] = v
		}
	}
	// Kept out of the loop above because it is a bool, and out of the ledger entirely
	// when absent: a defaulted `false` would assert the lead came from a paid ad on
	// every delivery Graph declined to tell us about.
	if lead.IsOrganic != nil {
		leadCtx["is_organic"] = *lead.IsOrganic
	}

	leadgenID := lead.ID
	if leadgenID == "" {
		leadgenID = leadgenFallback
	}
	return RawLead{Fields: fields, Context: leadCtx, ProviderEventID: leadgenID}
}

// facebookLeadFields is the field set both the single-lead fetch and the backfill
// pager request, so the two return identical lead shapes. (Before L7.1 the comment
// claimed that and the two lists had silently drifted — FetchLead inlined its own,
// which is why adding a field here alone would have changed backfill and not the
// webhook path. Both now go through withPlacementFallback and share these consts.)
const facebookLeadFields = "id,field_data,form_id,created_time,ad_id,campaign_id,adgroup_id,platform,is_organic"

// facebookLeadFieldsBase is facebookLeadFields without the placement fields — the
// rung withPlacementFallback drops to when Graph rejects the wider list.
const facebookLeadFieldsBase = "id,field_data,form_id,created_time,ad_id,campaign_id,adgroup_id"

// ListForms enumerates a page's lead forms (L5.4), following the cursor. Each is
// projected to id/name/status for the enable-a-form UI.
func (p *FacebookProvider) ListForms(ctx context.Context, conn *IntegrationConnection, creds Credentials) ([]ProviderForm, error) {
	proof := p.proof(creds.AccessToken)
	after := ""
	var forms []ProviderForm
	for i := 0; i < maxAccountPages; i++ {
		q := url.Values{}
		q.Set("fields", "id,name,status,locale")
		q.Set("limit", "100")
		q.Set("access_token", creds.AccessToken)
		q.Set("appsecret_proof", proof)
		if after != "" {
			q.Set("after", after)
		}
		var resp struct {
			Data []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"data"`
			Paging struct {
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
				Next string `json:"next"`
			} `json:"paging"`
		}
		if err := p.call(ctx, http.MethodGet, p.graphBaseURL+"/"+url.PathEscape(conn.ExternalAccountID)+"/leadgen_forms?"+q.Encode(), &resp); err != nil {
			return nil, err
		}
		for _, f := range resp.Data {
			forms = append(forms, ProviderForm{ID: f.ID, Name: f.Name, Status: f.Status})
		}
		if resp.Paging.Cursors.After == "" || resp.Paging.Next == "" {
			break
		}
		after = resp.Paging.Cursors.After
	}
	return forms, nil
}

// Backfill pages a form's historical leads (L5.4). Returns ONE page of full leads
// (with field_data) plus the next cursor — the caller loops, so throttling and the
// suppression policy stay in the executor, not the adapter. The /leads endpoint is
// bounded to ~90 days by Graph itself.
func (p *FacebookProvider) Backfill(ctx context.Context, conn *IntegrationConnection, creds Credentials, formID, cursor string) ([]RawLead, string, error) {
	if formID == "" {
		return nil, "", errors.New("facebook: cannot backfill without a form id")
	}
	type leadsPage struct {
		Data   []fbLead `json:"data"`
		Paging struct {
			Cursors struct {
				After string `json:"after"`
			} `json:"cursors"`
			Next string `json:"next"`
		} `json:"paging"`
	}
	// Same ladder as FetchLead, for the same reason one shelf over: a rejected field
	// list here would not lose a live lead, but it would leave historical import
	// permanently broken while the webhook path kept working — a split failure that
	// reads as "backfill is buggy" rather than "this field does not exist".
	resp, err := withPlacementFallback(func(fields string) (leadsPage, error) {
		q := url.Values{}
		q.Set("fields", fields)
		q.Set("limit", "100")
		q.Set("access_token", creds.AccessToken)
		q.Set("appsecret_proof", p.proof(creds.AccessToken))
		if cursor != "" {
			q.Set("after", cursor)
		}
		var page leadsPage
		err := p.call(ctx, http.MethodGet, p.graphBaseURL+"/"+url.PathEscape(formID)+"/leads?"+q.Encode(), &page)
		return page, err
	})
	if err != nil {
		return nil, "", err
	}
	leads := make([]RawLead, 0, len(resp.Data))
	for i := range resp.Data {
		leads = append(leads, rawLeadFromFB(resp.Data[i], conn.ExternalAccountID, formID, ""))
	}
	next := ""
	if resp.Paging.Next != "" {
		next = resp.Paging.Cursors.After
	}
	return leads, next, nil
}

// facebookSeedFieldMap is the mapping a newly-enabled facebook_form source starts
// with: Facebook's standard leadgen field names onto contact fields. Custom
// questions arrive under their own names, quarantine, surface as observed keys, and
// get one-click mapped by the admin — the L2 flow. FULL_NAME carries a non-empty
// target even though split_name ignores it (Apply rejects an empty target before
// the transform switch).
func (p *FacebookProvider) SeedFieldMap() FieldMap {
	return FieldMap{
		"email":        {TargetKey: "email"},
		"phone_number": {TargetKey: "phone"},
		"full_name":    {TargetKey: "first_name", Transform: TransformSplitName},
		"first_name":   {TargetKey: "first_name"},
		"last_name":    {TargetKey: "last_name"},
	}
}

// fbStr coerces a JSON value (string, or a number Graph sometimes sends for ids)
// to a trimmed string.
func fbStr(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		// Graph ids are large; format without scientific notation or a trailing .0.
		return strings.TrimSpace(strconv.FormatFloat(t, 'f', -1, 64))
	default:
		return ""
	}
}

// CheckSubscription reports whether THIS app is still subscribed to the page's
// leadgen field — the layer L6.3's diagnose panel was built for, and the one that
// shipped without an implementation here, so the only provider in the product has
// been answering "unknown" to it since the day the panel landed.
//
// It is the one broken layer a token probe cannot see. Subscribe only ever WROTE,
// so "subscribed" was a belief recorded at connect time; a subscription can lapse
// on Meta's side alone — an admin removing the app in the page's Business Settings,
// a permission review — while the stored page token keeps passing HealthCheck. That
// page reads perfectly healthy and never delivers another lead.
//
// The app id is matched defensively rather than because the list is crowded: the
// page token is app-scoped, so `data` is realistically zero or one entry, but a row
// belonging to some other agency's CRM would not feed US and must not read as health.
func (p *FacebookProvider) CheckSubscription(ctx context.Context, conn *IntegrationConnection, creds Credentials) (bool, error) {
	if p.appID == "" {
		// "We could not ask" — never `false`, which the panel renders as a definite no
		// and sends the admin to re-subscribe a page that may be perfectly fine.
		return false, errors.New("facebook: no app id configured, cannot check the page subscription")
	}
	q := url.Values{}
	// Asked for explicitly even though Graph documents subscribed_fields as a DEFAULT
	// field of this edge. The whole verdict hangs on that one key being present, and
	// the cost of Meta narrowing its default set later is not a missing label — it is
	// every healthy page reporting a definite "not subscribed" and every admin being
	// told to redo OAuth. A projection we control is cheaper than that bet.
	q.Set("fields", "subscribed_fields")
	q.Set("access_token", creds.AccessToken)
	q.Set("appsecret_proof", p.proof(creds.AccessToken))
	endpoint := p.graphBaseURL + "/" + url.PathEscape(conn.ExternalAccountID) + "/subscribed_apps?" + q.Encode()

	var resp struct {
		Data []struct {
			ID               string   `json:"id"`
			SubscribedFields []string `json:"subscribed_fields"`
		} `json:"data"`
	}
	if err := p.call(ctx, http.MethodGet, endpoint, &resp); err != nil {
		return false, err
	}
	for _, app := range resp.Data {
		if app.ID != p.appID {
			continue
		}
		if app.SubscribedFields == nil {
			// Our app is attached and Graph told us nothing about what it is subscribed
			// TO. That is a third state, and folding it into `false` is the precise
			// mistake this layer exists to avoid: "we could not read the answer" and
			// "the answer is no" send an admin to different places.
			return false, errors.New("facebook: the page did not report which fields this app is subscribed to")
		}
		for _, field := range app.SubscribedFields {
			if field == "leadgen" {
				return true, nil
			}
		}
		// Attached, subscribed to something, but not to leads. Distinct from "not
		// attached" and, unlike the nil case above, an honest definite no.
		return false, nil
	}
	return false, nil
}

// HealthCheck probes whether the stored page token still works (L6 diagnose).
func (p *FacebookProvider) HealthCheck(ctx context.Context, conn *IntegrationConnection, creds Credentials) error {
	q := url.Values{}
	q.Set("fields", "id,name")
	q.Set("access_token", creds.AccessToken)
	q.Set("appsecret_proof", p.proof(creds.AccessToken))
	endpoint := p.graphBaseURL + "/" + url.PathEscape(conn.ExternalAccountID) + "?" + q.Encode()
	return p.call(ctx, http.MethodGet, endpoint, nil)
}

// ── Graph plumbing ─────────────────────────────────────────────────────────

type fbTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type fbPage struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccessToken string `json:"access_token"`
	Category    string `json:"category"`
}

type fbAccountsResponse struct {
	Data   []fbPage `json:"data"`
	Paging struct {
		Cursors struct {
			After string `json:"after"`
		} `json:"cursors"`
		Next string `json:"next"`
	} `json:"paging"`
}

// fbErrorEnvelope is Graph's error shape. Parsed off a non-2xx body so the ledger
// and logs carry Facebook's actual reason (an expired token, a missing
// permission) instead of a bare status code.
type fbErrorEnvelope struct {
	Error struct {
		Message   string `json:"message"`
		Type      string `json:"type"`
		Code      int    `json:"code"`
		Subcode   int    `json:"error_subcode"`
		FBTraceID string `json:"fbtrace_id"`
	} `json:"error"`
}

// proof is the appsecret_proof Graph requires when "Require app secret" is on:
// HMAC-SHA256 of the access token keyed by the app secret, hex-encoded. Computed
// per-token (a page token and the user token each get their own).
func (p *FacebookProvider) proof(token string) string {
	mac := hmac.New(sha256.New, []byte(p.appSecret))
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// exchangeCode swaps the authorization code for a (short-lived) user token.
func (p *FacebookProvider) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	q := url.Values{}
	q.Set("client_id", p.appID)
	q.Set("client_secret", p.appSecret)
	q.Set("redirect_uri", redirectURI)
	q.Set("code", code)
	var tok fbTokenResponse
	if err := p.call(ctx, http.MethodGet, p.graphBaseURL+"/oauth/access_token?"+q.Encode(), &tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("facebook: token exchange returned no access token")
	}
	return tok.AccessToken, nil
}

// exchangeLongLived upgrades a short-lived user token to a long-lived one.
func (p *FacebookProvider) exchangeLongLived(ctx context.Context, userToken string) (string, error) {
	q := url.Values{}
	q.Set("grant_type", "fb_exchange_token")
	q.Set("client_id", p.appID)
	q.Set("client_secret", p.appSecret)
	q.Set("fb_exchange_token", userToken)
	var tok fbTokenResponse
	if err := p.call(ctx, http.MethodGet, p.graphBaseURL+"/oauth/access_token?"+q.Encode(), &tok); err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

// listPages fetches every page the user administers, following the cursor.
//
// appsecret_proof is recomputed and re-attached on EVERY page of results rather
// than trusting Graph's `paging.next` URL to carry it: with "Require app secret"
// on, a next URL that omits the proof would 400 midway and silently truncate the
// page list — connecting only the first 25 pages and hiding the rest.
func (p *FacebookProvider) listPages(ctx context.Context, userToken string) ([]fbPage, error) {
	proof := p.proof(userToken)
	after := ""
	var pages []fbPage
	for i := 0; i < maxAccountPages; i++ {
		q := url.Values{}
		q.Set("fields", "id,name,access_token,category")
		q.Set("limit", "100")
		q.Set("access_token", userToken)
		q.Set("appsecret_proof", proof)
		if after != "" {
			q.Set("after", after)
		}
		var resp fbAccountsResponse
		if err := p.call(ctx, http.MethodGet, p.graphBaseURL+"/me/accounts?"+q.Encode(), &resp); err != nil {
			return nil, err
		}
		pages = append(pages, resp.Data...)
		if resp.Paging.Cursors.After == "" || resp.Paging.Next == "" {
			break
		}
		after = resp.Paging.Cursors.After
	}
	return pages, nil
}

// call issues a Graph request through the shared client and, on a non-2xx,
// parses Facebook's error envelope for a human-readable reason. out may be nil
// when the caller only needs success/failure.
func (p *FacebookProvider) call(ctx context.Context, method, endpoint string, out any) error {
	resp, err := p.http.Do(ctx, OutboundRequest{Method: method, URL: endpoint})
	if err != nil {
		return p.wrapGraphError(resp, err)
	}
	if out != nil {
		if jerr := json.Unmarshal(resp.Body, out); jerr != nil {
			return fmt.Errorf("facebook: could not parse Graph response: %w", jerr)
		}
	}
	return nil
}

// fbTransientCodes are Graph error CODES that mean "try again", which Graph
// returns with an HTTP 400 (occasionally 403) — NOT 429. The generic HTTPClient
// only sees the status, so it classifies these as permanent; recognizing them
// here, by code, is what keeps a throttled lead on the highest-volume source from
// being lost (marked failed) and its healthy connection wrongly flipped to error.
//
//	4     app-level rate limit
//	17    user-level rate limit
//	32    page-level rate limit ("Page request limit reached")
//	341   application limit reached
//	613   custom-level rate limit
//	80000–80014  Business-Use-Case (BUC) throttles (per-product rate limits)
//	1, 2  transient unknown / service errors ("please retry")
var fbTransientCodes = map[int]bool{
	1: true, 2: true, 4: true, 17: true, 32: true, 341: true, 613: true,
	80000: true, 80001: true, 80002: true, 80003: true, 80004: true,
	80005: true, 80006: true, 80008: true, 80014: true,
}

// wrapGraphError turns an HTTPError plus the response body into a message that
// names Facebook's reason, and — crucially — re-classifies a Graph rate-limit /
// transient error CODE as retryable even though it arrived on an HTTP 400 the
// generic client called permanent. A genuine 4xx (a dead token, bad permission)
// stays non-retryable; a 5xx/429 stays retryable.
func (p *FacebookProvider) wrapGraphError(resp *Response, err error) error {
	var env fbErrorEnvelope
	if resp != nil && len(resp.Body) > 0 {
		_ = json.Unmarshal(resp.Body, &env)
	}
	msg := strings.TrimSpace(env.Error.Message)

	var he *HTTPError
	if errors.As(err, &he) {
		retryable := he.Retryable || fbTransientCodes[env.Error.Code]
		if msg == "" && retryable == he.Retryable {
			return he // nothing to add or change
		}
		wrapped := he.Err
		if msg != "" {
			wrapped = fmt.Errorf("facebook: %s", msg)
		}
		// Wrap into a fresh *HTTPError so IsRetryableHTTP sees the (possibly
		// upgraded) classification.
		return &HTTPError{StatusCode: he.StatusCode, Body: he.Body, Retryable: retryable, Err: wrapped}
	}
	if msg != "" {
		return fmt.Errorf("facebook: %s: %w", msg, err)
	}
	return err
}
