package marketing

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type fakePreflightStore struct {
	profile  *OrgMarketingProfile
	mailable int64
	known    int64
	hasUniq  bool
}

func (f *fakePreflightStore) GetProfile(_ context.Context, _ uuid.UUID) (*OrgMarketingProfile, error) {
	return f.profile, nil
}
func (f *fakePreflightStore) CountMailableContacts(_ context.Context, _ uuid.UUID) (int64, error) {
	return f.mailable, nil
}
func (f *fakePreflightStore) CountKnownMarketingContacts(_ context.Context, _ uuid.UUID) (int64, error) {
	return f.known, nil
}
func (f *fakePreflightStore) HasConsentUniqueConstraint(_ context.Context) (bool, error) {
	return f.hasUniq, nil
}

// fakeDomainGate and fakeTokenGate already exist in campaign_usecase_test.go with
// exactly the shapes needed here — reused rather than shadowed.

func healthyEnv() PreflightEnv {
	return PreflightEnv{
		ResendAPIKeySet:        true,
		ResendWebhookSecretSet: true,
		UnsubKeyVersions:       []int{1},
		UnsubKeyPrimary:        1,
		FrontendURL:            "https://app.example.com",
		PublicAPIBaseURL:       "https://api.example.com",
		SendWorkerStarted:      true,
	}
}

func healthyStore() *fakePreflightStore {
	return &fakePreflightStore{
		profile:  &OrgMarketingProfile{FromName: "Acme", PhysicalPostalAddress: "1 Main St"},
		mailable: 40,
		known:    100,
		hasUniq:  true,
	}
}

func evaluate(t *testing.T, store *fakePreflightStore, env PreflightEnv, dom *fakeDomainGate, tok *fakeTokenGate) *domain.MarketingPreflight {
	t.Helper()
	svc := NewPreflightService(store, dom, tok, env, nil)
	got, err := svc.Evaluate(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return got
}

func check(t *testing.T, p *domain.MarketingPreflight, key string) domain.LaunchCheck {
	t.Helper()
	for _, c := range p.Checks {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", key, p.Checks)
	return domain.LaunchCheck{}
}

func TestPreflight_AllGreen(t *testing.T) {
	got := evaluate(t, healthyStore(), healthyEnv(), &fakeDomainGate{ok: true}, &fakeTokenGate{ok: true})
	if !got.Ready {
		for _, c := range got.Checks {
			if !c.OK {
				t.Logf("failing: %s — %s", c.Key, c.Detail)
			}
		}
		t.Fatal("want ready")
	}
}

// The headline case: this is exactly the state the whole feature was built to make
// visible. Everything is configured, but no contact carries a lawful basis, so a
// campaign would pass its green launch checklist and then suppress every recipient.
func TestPreflight_NoLawfulBasisBlocks(t *testing.T) {
	store := healthyStore()
	store.mailable = 0
	store.known = 500

	got := evaluate(t, store, healthyEnv(), &fakeDomainGate{ok: true}, &fakeTokenGate{ok: true})
	if got.Ready {
		t.Fatal("an org where nobody is mailable must not report ready")
	}
	c := check(t, got, "lawful_basis")
	if c.OK {
		t.Fatal("lawful_basis must be red")
	}
	if !strings.Contains(c.Detail, "no_lawful_basis") {
		t.Fatalf("the detail must name the suppression reason an operator will see: %q", c.Detail)
	}
	if got.MailableContacts != 0 || got.KnownContacts != 500 {
		t.Fatalf("coverage numbers wrong: %+v", got)
	}
}

// A missing webhook secret loses analytics and auto-suppression but blocks nothing,
// so it must warn rather than gate. Getting this backwards would stop a legitimate
// go-live on a non-blocking finding.
func TestPreflight_WebhookSecretWarnsButDoesNotBlock(t *testing.T) {
	env := healthyEnv()
	env.ResendWebhookSecretSet = false

	got := evaluate(t, healthyStore(), env, &fakeDomainGate{ok: true}, &fakeTokenGate{ok: true})
	if !got.Ready {
		t.Fatal("an unset webhook secret must not block readiness")
	}
	c := check(t, got, "delivery_webhook")
	if c.OK {
		t.Fatal("want the check itself red")
	}
	if c.Severity != domain.SeverityWarn {
		t.Fatalf("want severity %q, got %q", domain.SeverityWarn, c.Severity)
	}
}

func TestPreflight_BlockingChecks(t *testing.T) {
	cases := []struct {
		name string
		key  string
		mut  func(*PreflightEnv, *fakePreflightStore, *fakeDomainGate, *fakeTokenGate)
	}{
		{"no api key", "resend_api_key", func(e *PreflightEnv, _ *fakePreflightStore, _ *fakeDomainGate, _ *fakeTokenGate) {
			e.ResendAPIKeySet = false
		}},
		{"no unsub key", "unsubscribe_key", func(e *PreflightEnv, _ *fakePreflightStore, _ *fakeDomainGate, tok *fakeTokenGate) {
			e.UnsubKeyVersions = nil
			tok.ok = false
		}},
		{"localhost frontend url", "frontend_url", func(e *PreflightEnv, _ *fakePreflightStore, _ *fakeDomainGate, _ *fakeTokenGate) {
			e.FrontendURL = "http://localhost:5173"
		}},
		{"unverified domain", "sending_domain", func(_ *PreflightEnv, _ *fakePreflightStore, d *fakeDomainGate, _ *fakeTokenGate) {
			d.ok = false
			d.reason = "no_dmarc"
		}},
		{"no sender profile", "sender_profile", func(_ *PreflightEnv, s *fakePreflightStore, _ *fakeDomainGate, _ *fakeTokenGate) {
			s.profile = nil
		}},
		{"no profile row", "marketing_profile_row", func(_ *PreflightEnv, s *fakePreflightStore, _ *fakeDomainGate, _ *fakeTokenGate) {
			s.profile = nil
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, store := healthyEnv(), healthyStore()
			dom, tok := &fakeDomainGate{ok: true}, &fakeTokenGate{ok: true}
			tc.mut(&env, store, dom, tok)

			got := evaluate(t, store, env, dom, tok)
			if got.Ready {
				t.Fatalf("%s must block readiness", tc.key)
			}
			if c := check(t, got, tc.key); c.OK {
				t.Fatalf("check %s should be red", tc.key)
			}
		})
	}
}

// The report is shown to operators and pasted into support threads. It may say a
// key is configured and which keyring versions parse; it may never say more.
//
// This drives the sentinel through every field that IS echoed verbatim into a
// detail string — the URLs, and the two pass-through default arms of the domain
// and sender reason mappers. Asserting the sentinel is absent from a report that
// was never given it would prove nothing.
func TestPreflight_NeverLeaksSecretValues(t *testing.T) {
	const sentinel = "s3cr3t-do-not-print"

	env := healthyEnv()
	env.PublicAPIBaseURL = "https://api.example.com/" + sentinel
	env.FrontendURL = "https://app.example.com/" + sentinel

	store := healthyStore()
	store.profile = nil // drives senderReasonDetail

	got := evaluate(t, store, env, &fakeDomainGate{ok: false, reason: sentinel}, &fakeTokenGate{ok: true})

	// The URL checks are the deliberate exception: those values are NOT secrets and
	// an operator cannot fix a wrong one without seeing it. Everything else must be
	// clean, and the reason-mapper pass-throughs are where an opaque upstream string
	// would otherwise reach the response.
	for _, c := range got.Checks {
		switch c.Key {
		case "public_api_base_url", "frontend_url":
			continue
		}
		if strings.Contains(c.Key+c.Label+c.Detail, sentinel) {
			t.Fatalf("check %s echoed an opaque upstream value: %q", c.Key, c.Detail)
		}
	}

	// And the keyring detail must still be genuinely useful without being the key.
	c := check(t, got, "unsubscribe_key")
	if !strings.Contains(c.Detail, "versions") {
		t.Fatalf("want keyring versions reported, got %q", c.Detail)
	}
	for _, v := range []string{sentinel, "MARKETING_UNSUB_KEY="} {
		if strings.Contains(c.Detail, v) {
			t.Fatalf("unsubscribe detail leaked %q: %s", v, c.Detail)
		}
	}
}

// The preflight's mailable predicate lives in SQL and cannot be unit-tested, but
// the SHAPE of the coverage report can: a zero numerator must always block.
func TestPreflight_CoverageNumbersAreReported(t *testing.T) {
	store := healthyStore()
	store.mailable = 7
	store.known = 9

	got := evaluate(t, store, healthyEnv(), &fakeDomainGate{ok: true}, &fakeTokenGate{ok: true})
	if got.MailableContacts != 7 || got.KnownContacts != 9 {
		t.Fatalf("coverage not surfaced: %+v", got)
	}
	if c := check(t, got, "lawful_basis"); !c.OK {
		t.Fatal("a non-zero mailable count must pass")
	}
}
