package integrations

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"
)

// A Provider is one third-party lead platform behind the connector framework
// (L5): Facebook now, TikTok/Instagram as later adapters. The framework owns the
// OAuth custody, encryption, event ledger and async pipeline; a Provider supplies
// only the platform-specific pieces, so a new platform is an adapter rather than
// a rebuild.
//
// The interface is deliberately WIDER than L5.1 exercises. L5.1 drives only the
// connect triad (Info/AuthURL/ExchangeCallback); the webhook/fetch/forms/backfill
// methods are what L5.3 and L5.4 fill in. Declaring them now — with
// UnimplementedProvider supplying "unsupported" defaults — is what lets those
// phases add behaviour without touching the framework's call sites.
type Provider interface {
	// Info describes the provider and its capabilities. Called at registration
	// and to decide, e.g., whether to run the PKCE dance.
	Info() ProviderInfo

	// AuthURL builds the provider's consent URL. state is the opaque server-stored
	// nonce; redirectURI is config-derived (never c.Request.Host); pkceChallenge is
	// the S256 challenge and is empty for providers whose Info().UsesPKCE is false.
	AuthURL(state, redirectURI, pkceChallenge string) string

	// ExchangeCallback trades the authorization code for the accounts the user may
	// connect, each carrying its own credentials (a Facebook user grants access to
	// several pages, each with its own page token). codeVerifier is the PKCE
	// verifier, empty when PKCE is not used.
	//
	// Returning the per-account credentials here — rather than a single user token
	// plus a later fetch — is what lets the account picker stay token-free: the
	// framework seals the whole list into pending custody and the browser sees only
	// {id, label}.
	ExchangeCallback(ctx context.Context, code, redirectURI, codeVerifier string) ([]Account, error)

	// Subscribe activates delivery for a freshly-connected account — for Facebook,
	// subscribing the page's leadgen field so the app-level webhook (L5.3) receives
	// its leads. Called once after a connection is stored, only for providers whose
	// Info().SupportsWebhooks is true. A failure does not undo the connection; it is
	// surfaced so the admin can retry, because a connected-but-unsubscribed page
	// looks healthy while silently receiving nothing.
	Subscribe(ctx context.Context, conn *IntegrationConnection, creds Credentials) error

	// VerifyWebhook authenticates an inbound provider webhook against the raw body
	// bytes (HMAC over the app secret for Facebook). L5.3.
	VerifyWebhook(r *http.Request, body []byte) error

	// ParseWebhook turns a verified webhook body into the deliveries it announces.
	// A provider webhook carries IDs only; FetchLead resolves each. L5.3.
	ParseWebhook(body []byte) ([]InboundEvent, error)

	// FetchLead resolves one announced delivery into a RawLead. L5.3.
	FetchLead(ctx context.Context, conn *IntegrationConnection, creds Credentials, ev InboundEvent) (RawLead, error)

	// ListForms enumerates the lead forms available on a connected account, for the
	// per-form config UI. L5.4.
	ListForms(ctx context.Context, conn *IntegrationConnection, creds Credentials) ([]ProviderForm, error)

	// Backfill pages historical leads for one form, returning the FULL leads (the
	// provider's /leads response carries field_data, so no per-lead FetchLead is
	// needed) and the next cursor (empty when exhausted). Each RawLead carries its
	// ProviderEventID (leadgen id) for connection-scoped dedupe against leads that
	// already arrived by webhook. L5.4.
	Backfill(ctx context.Context, conn *IntegrationConnection, creds Credentials, formID, cursor string) ([]RawLead, string, error)

	// CheckSubscription reports whether the provider is currently configured to PUSH
	// deliveries to us for this account — the read counterpart to Subscribe, which
	// only ever writes. Without it "connected" can only mean "the token works", and a
	// page whose subscription silently lapsed looks identical to a healthy one.
	CheckSubscription(ctx context.Context, conn *IntegrationConnection, creds Credentials) (bool, error)

	// SeedFieldMap is the mapping a newly-enabled form starts with: the provider's
	// standard question names onto contact fields. Custom questions arrive under
	// their own names, quarantine, surface as observed keys, and get one-click mapped
	// by the admin — the L2 flow.
	SeedFieldMap() FieldMap

	// HealthCheck probes whether the stored credential still works, for the L6
	// diagnose action.
	HealthCheck(ctx context.Context, conn *IntegrationConnection, creds Credentials) error

	// Disconnect best-effort tears down the provider-side subscription when a
	// connection is removed (unsubscribe the page's leadgen webhook). L6.
	Disconnect(ctx context.Context, conn *IntegrationConnection, creds Credentials) error
}

// ProviderInfo is a provider's self-description. JSON-tagged because the
// management API returns it verbatim to the frontend (GET /api/integrations/
// providers) — without tags Go would emit the Go field names (Key, Label…) and
// the FE's snake_case contract would silently break.
type ProviderInfo struct {
	// Key is the URL/registry identifier ("facebook"). Lowercase, stable — it
	// appears in the callback path and in stored connection rows.
	Key string `json:"key"`
	// Label is the human name shown in the UI ("Facebook Lead Ads").
	Label string `json:"label"`
	// SupportsWebhooks reports whether the provider pushes deliveries (Facebook)
	// versus poll-only. L6 health leans on this to know whether "not subscribed" is
	// even a diagnosable state.
	SupportsWebhooks bool `json:"supports_webhooks"`
	// UsesPKCE reports whether the connect flow should generate a code verifier and
	// S256 challenge. Facebook's server-side flow does not; a provider that does
	// sets this and reads the verifier back in ExchangeCallback.
	UsesPKCE bool `json:"uses_pkce"`
	// SourceKind is the lead_sources.kind a form enabled on this provider produces
	// ("facebook_form", "tiktok_form"). It is also the key of the config namespace
	// the form id is stored under, so `config -> <provider key> ->> 'form_id'` is
	// what resolves a delivery back to its source. Before L7.5 both were hardcoded to
	// Facebook in four places.
	SourceKind string `json:"source_kind"`
	// CarriesLeadData reports that the provider's WEBHOOK already contains the lead's
	// answers, so no per-lead fetch is needed or possible.
	//
	// This is not an optimisation, it is a capability difference. Meta's leadgen
	// webhook carries ids only and the lead is read back with GET /{leadgen_id};
	// TikTok's carries the answers inline and its /lead/get/ endpoint has NO lead id
	// parameter at all, so there is no by-id read to make. A provider that sets this
	// implements FetchLead as a pure function over InboundEvent.Raw, which the worker
	// rehydrates from the stored delivery for exactly this purpose.
	CarriesLeadData bool `json:"carries_lead_data"`
}

// Credentials is a provider access-token blob, sealed at rest by the envelope
// codec. Providers populate whichever fields they hold; Extra carries anything
// provider-specific that must survive a round trip (a Facebook page id, say).
type Credentials struct {
	AccessToken  string         `json:"access_token,omitempty"`
	RefreshToken string         `json:"refresh_token,omitempty"`
	TokenType    string         `json:"token_type,omitempty"`
	ExpiresAt    *time.Time     `json:"expires_at,omitempty"`
	Scopes       []string       `json:"scopes,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

// Account is one connectable account a grant exposes (a Facebook page). Its
// Credentials are SECRET and never reach the browser — only ID/Label/Meta do,
// via AccountChoice.
type Account struct {
	ID          string         `json:"id"`
	Label       string         `json:"label"`
	Credentials Credentials    `json:"credentials"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// AccountChoice is the token-free projection of an Account for the picker. It is
// what CandidateAccounts stores and what the select-account UI renders — a
// separate type so an Account's Credentials cannot leak into the browser by
// someone forgetting a json:"-".
type AccountChoice struct {
	ID    string         `json:"id"`
	Label string         `json:"label"`
	Meta  map[string]any `json:"meta,omitempty"`
}

// choicesOf strips the credentials off a candidate list for the picker.
func choicesOf(accounts []Account) []AccountChoice {
	out := make([]AccountChoice, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, AccountChoice{ID: a.ID, Label: a.Label, Meta: a.Meta})
	}
	return out
}

// InboundEvent is one delivery a provider webhook or backfill announces. It
// carries IDs, not lead data — FetchLead resolves the payload. L5.3+.
type InboundEvent struct {
	// ExternalAccountID routes the delivery to its connection (a Facebook page id).
	ExternalAccountID string
	// ProviderEventID is the stable delivery id used for connection-scoped dedupe
	// (a Meta leadgen_id).
	ProviderEventID string
	// FormID names the form the lead came from, for per-form mapping.
	FormID string
	// Raw is the verbatim announcement, stored on the pending event row so a
	// mapping drift can never lose it.
	Raw map[string]any
}

// ProviderForm is one lead form on a connected account, for the per-form config
// UI. L5.4.
type ProviderForm struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

// ErrProviderCapabilityUnsupported is what a Provider returns for a capability it
// does not implement. Callers branch on it to skip an optional step rather than
// treat it as a failure.
// ErrDeliveryUnusable marks a delivery that cannot be turned into a lead no matter
// how often it is retried — the payload is empty or malformed — as distinct from a
// credential or transport failure.
//
// The distinction is load-bearing for a provider whose webhook CARRIES the lead.
// There the "fetch" is a read of our own stored row, so its failures say nothing at
// all about the token; without this the worker's fetch-error path would flip the
// connection to `error` and tell the admin to reconnect an account that is working
// perfectly.
var ErrDeliveryUnusable = errors.New("integrations: delivery cannot be turned into a lead")

var ErrProviderCapabilityUnsupported = errors.New("integrations: provider does not support this capability")

// ErrUnknownProvider reports a connect/callback for a provider that is not
// registered — dormant in production until its adapter ships.
var ErrUnknownProvider = errors.New("integrations: unknown provider")

// UnimplementedProvider supplies "unsupported" defaults for every OPTIONAL
// capability, so an adapter embeds it and overrides only what it supports.
//
// It deliberately does NOT implement Info/AuthURL/ExchangeCallback: those three
// are mandatory, and leaving them off the embed turns a provider that forgets one
// into a compile error rather than a silent empty-string auth URL at runtime.
type UnimplementedProvider struct{}

// Subscribe is a no-op for a provider with no delivery to activate.
//
// nil, not ErrProviderCapabilityUnsupported: a poll-only or no-webhook provider
// has nothing to subscribe, and that is a success — surfacing an error would make
// a healthy connect look half-failed. A webhook provider overrides this.
func (UnimplementedProvider) Subscribe(context.Context, *IntegrationConnection, Credentials) error {
	return nil
}

// VerifyWebhook reports the capability is unsupported.
func (UnimplementedProvider) VerifyWebhook(*http.Request, []byte) error {
	return ErrProviderCapabilityUnsupported
}

// ParseWebhook reports the capability is unsupported.
func (UnimplementedProvider) ParseWebhook([]byte) ([]InboundEvent, error) {
	return nil, ErrProviderCapabilityUnsupported
}

// FetchLead reports the capability is unsupported.
func (UnimplementedProvider) FetchLead(context.Context, *IntegrationConnection, Credentials, InboundEvent) (RawLead, error) {
	return RawLead{}, ErrProviderCapabilityUnsupported
}

// ListForms reports the capability is unsupported.
func (UnimplementedProvider) ListForms(context.Context, *IntegrationConnection, Credentials) ([]ProviderForm, error) {
	return nil, ErrProviderCapabilityUnsupported
}

// Backfill reports the capability is unsupported.
func (UnimplementedProvider) Backfill(context.Context, *IntegrationConnection, Credentials, string, string) ([]RawLead, string, error) {
	return nil, "", ErrProviderCapabilityUnsupported
}

// HealthCheck reports the capability is unsupported.
// SeedFieldMap defaults to empty, which ParseFieldMap reads as IDENTITY over
// schema-valid keys — the same thing an unmapped capture-API source does. A provider
// that has standard field names overrides it.
func (UnimplementedProvider) SeedFieldMap() FieldMap { return FieldMap{} }

func (UnimplementedProvider) CheckSubscription(context.Context, *IntegrationConnection, Credentials) (bool, error) {
	return false, ErrProviderCapabilityUnsupported
}

func (UnimplementedProvider) HealthCheck(context.Context, *IntegrationConnection, Credentials) error {
	return ErrProviderCapabilityUnsupported
}

// Disconnect is a no-op for a provider with nothing to tear down.
//
// nil, not ErrProviderCapabilityUnsupported: removing a connection whose provider
// has no server-side subscription to release is a SUCCESS, not an unsupported
// operation — the connection is gone regardless, and surfacing an error here
// would make an ordinary disconnect look like it failed.
func (UnimplementedProvider) Disconnect(context.Context, *IntegrationConnection, Credentials) error {
	return nil
}

// Registry is the set of providers whose adapters have shipped. It is populated
// at wiring time in main.go; an empty registry (the state before any provider
// ships) makes every connect route a clean 404 rather than a panic.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds a provider, keyed by its Info().Key. A second registration under
// the same key replaces the first — deterministic, and the wiring registers each
// provider exactly once.
func (r *Registry) Register(p Provider) {
	r.providers[p.Info().Key] = p
}

// Get resolves a provider by key.
func (r *Registry) Get(key string) (Provider, bool) {
	p, ok := r.providers[key]
	return p, ok
}

// Keys lists the registered provider keys, sorted — for a capabilities endpoint
// and for tests.
func (r *Registry) Keys() []string {
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
