package integrations

import (
	"context"
	"errors"
	"net/url"
	"testing"
)

// fakeProvider is the in-package test adapter used by the provider, service and
// connection integration tests. It embeds UnimplementedProvider (so the compile
// itself proves the embed satisfies the optional half of the interface) and
// implements only the mandatory connect triad.
type fakeProvider struct {
	UnimplementedProvider
	info        ProviderInfo
	accounts    []Account
	exchangeErr error

	// captured on the last ExchangeCallback, so tests can assert the framework
	// passed the right code / redirect / PKCE verifier through.
	lastCode     string
	lastRedirect string
	lastVerifier string

	// Subscribe bookkeeping: activateDelivery calls this for a webhook-capable
	// provider after a connect.
	subscribeCalls int
	subscribeErr   error
}

func (f *fakeProvider) Info() ProviderInfo { return f.info }

func (f *fakeProvider) AuthURL(state, redirectURI, challenge string) string {
	return "https://provider.example/auth?state=" + url.QueryEscape(state) +
		"&redirect_uri=" + url.QueryEscape(redirectURI) +
		"&code_challenge=" + url.QueryEscape(challenge)
}

func (f *fakeProvider) ExchangeCallback(_ context.Context, code, redirectURI, verifier string) ([]Account, error) {
	f.lastCode = code
	f.lastRedirect = redirectURI
	f.lastVerifier = verifier
	if f.exchangeErr != nil {
		return nil, f.exchangeErr
	}
	return f.accounts, nil
}

// Subscribe overrides the UnimplementedProvider no-op so tests can observe (and
// fail) the post-connect activation.
func (f *fakeProvider) Subscribe(_ context.Context, _ *IntegrationConnection, _ Credentials) error {
	f.subscribeCalls++
	return f.subscribeErr
}

func TestRegistry_RegisterGetKeys(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("fake"); ok {
		t.Fatal("empty registry should not resolve any provider")
	}
	r.Register(&fakeProvider{info: ProviderInfo{Key: "fake", Label: "Fake"}})
	r.Register(&fakeProvider{info: ProviderInfo{Key: "another", Label: "Another"}})

	if p, ok := r.Get("fake"); !ok || p.Info().Label != "Fake" {
		t.Fatalf("expected to resolve the fake provider, got ok=%v", ok)
	}
	if _, ok := r.Get("nope"); ok {
		t.Fatal("unknown provider must not resolve")
	}
	keys := r.Keys()
	if len(keys) != 2 || keys[0] != "another" || keys[1] != "fake" {
		t.Fatalf("Keys must be sorted, got %v", keys)
	}
}

func TestUnimplementedProvider_Defaults(t *testing.T) {
	var u UnimplementedProvider
	ctx := context.Background()

	if err := u.VerifyWebhook(nil, nil); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Errorf("VerifyWebhook = %v, want unsupported", err)
	}
	if _, err := u.ParseWebhook(nil); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Errorf("ParseWebhook = %v, want unsupported", err)
	}
	if _, err := u.FetchLead(ctx, nil, Credentials{}, InboundEvent{}); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Errorf("FetchLead = %v, want unsupported", err)
	}
	if _, err := u.ListForms(ctx, nil, Credentials{}); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Errorf("ListForms = %v, want unsupported", err)
	}
	if _, _, err := u.Backfill(ctx, nil, Credentials{}, "", ""); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Errorf("Backfill = %v, want unsupported", err)
	}
	if err := u.HealthCheck(ctx, nil, Credentials{}); !errors.Is(err, ErrProviderCapabilityUnsupported) {
		t.Errorf("HealthCheck = %v, want unsupported", err)
	}
	// Disconnect is the one that defaults to nil — removing a connection whose
	// provider has nothing to tear down is a success, not an unsupported op.
	if err := u.Disconnect(ctx, nil, Credentials{}); err != nil {
		t.Errorf("Disconnect default = %v, want nil", err)
	}
}

func TestChoicesOf_StripsCredentials(t *testing.T) {
	accounts := []Account{
		{ID: "1", Label: "One", Credentials: Credentials{AccessToken: "secret-1"}, Meta: map[string]any{"k": "v"}},
		{ID: "2", Label: "Two", Credentials: Credentials{AccessToken: "secret-2"}},
	}
	choices := choicesOf(accounts)
	if len(choices) != 2 {
		t.Fatalf("want 2 choices, got %d", len(choices))
	}
	// AccountChoice has no credential field at all — this is a type-level guarantee,
	// so the assertion here is really that ids/labels/meta survive intact.
	if choices[0].ID != "1" || choices[0].Label != "One" || choices[0].Meta["k"] != "v" {
		t.Errorf("choice[0] = %+v", choices[0])
	}
	if choices[1].ID != "2" || choices[1].Label != "Two" {
		t.Errorf("choice[1] = %+v", choices[1])
	}
}
