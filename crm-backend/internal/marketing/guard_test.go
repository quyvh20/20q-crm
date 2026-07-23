package marketing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeReader is an in-memory ledgerReader so IsSendable is testable without a DB.
type fakeReader struct {
	sups     []Suppression
	state    *ContactMarketingState
	supErr   error
	stateErr error
}

func (f *fakeReader) SuppressionsForEmail(_ context.Context, _ uuid.UUID, _ string) ([]Suppression, error) {
	return f.sups, f.supErr
}
func (f *fakeReader) MarketingStateForEmail(_ context.Context, _ uuid.UUID, _ string) (*ContactMarketingState, error) {
	return f.state, f.stateErr
}

var testNow = time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

func guardWith(r ledgerReader) *SuppressionGuard {
	return &SuppressionGuard{r: r, now: func() time.Time { return testNow }}
}

func strp(s string) *string { return &s }

func TestIsSendable_Transactional(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()

	cases := []struct {
		name     string
		sups     []Suppression
		want     bool
		wantReas string
	}{
		{"clean", nil, true, ""},
		{"unsubscribe does not block transactional",
			[]Suppression{{Reason: ReasonUnsubscribe, Scope: ScopeMarketing}}, true, ""},
		{"hard bounce blocks transactional",
			[]Suppression{{Reason: ReasonHardBounce, Scope: ScopeAll}}, false, "suppressed:hard_bounce"},
		{"complaint blocks transactional",
			[]Suppression{{Reason: ReasonComplaint, Scope: ScopeAll}}, false, "suppressed:complaint"},
		{"soft bounce below threshold does not block",
			[]Suppression{{Reason: ReasonSoftBounce, Scope: ScopeAll, SoftBounceCount: 2}}, true, ""},
		{"soft bounce at threshold blocks",
			[]Suppression{{Reason: ReasonSoftBounce, Scope: ScopeAll, SoftBounceCount: 3}}, false, "suppressed:soft_bounce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := guardWith(&fakeReader{sups: tc.sups})
			got := g.IsSendable(ctx, org, "a@b.com", ChannelTransactional, nil)
			if got.Sendable != tc.want || got.Reason != tc.wantReas {
				t.Fatalf("got (%v,%q), want (%v,%q)", got.Sendable, got.Reason, tc.want, tc.wantReas)
			}
		})
	}
}

func TestIsSendable_Marketing_LawfulBasis(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()
	future := testNow.Add(24 * time.Hour)
	past := testNow.Add(-24 * time.Hour)

	cases := []struct {
		name  string
		state *ContactMarketingState
		want  bool
	}{
		{"no state row is not consent", nil, false},
		{"subscribed", &ContactMarketingState{MarketingStatus: StatusSubscribed}, true},
		{"pending with no basis", &ContactMarketingState{MarketingStatus: StatusPending}, false},
		{"pending + EBR", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisExistingBusinessRelationship)}, true},
		{"pending + legitimate interest", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisLegitimateInterest)}, true},
		{"pending + implied, casl future", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisImpliedTransaction), CASLExpiresAt: &future}, true},
		{"pending + implied, casl expired", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisImpliedTransaction), CASLExpiresAt: &past}, false},
		{"pending + implied, no expiry", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisImpliedInquiry)}, true},
		// Express / double opt-in must promote status to subscribed first; a
		// recorded-but-pending express/double-opt-in is NOT yet mailable.
		{"pending + express is not yet mailable", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisExpress)}, false},
		{"pending + unconfirmed double opt-in is not mailable", &ContactMarketingState{MarketingStatus: StatusPending, ConsentBasis: strp(BasisDoubleOptIn)}, false},
		{"subscribed + express is mailable", &ContactMarketingState{MarketingStatus: StatusSubscribed, ConsentBasis: strp(BasisExpress)}, true},
		{"unsubscribed status blocks even with basis", &ContactMarketingState{MarketingStatus: StatusUnsubscribed, ConsentBasis: strp(BasisExpress)}, false},
		{"cleaned status blocks", &ContactMarketingState{MarketingStatus: StatusCleaned, ConsentBasis: strp(BasisExpress)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := guardWith(&fakeReader{state: tc.state})
			got := g.IsSendable(ctx, org, "a@b.com", ChannelMarketing, nil)
			if got.Sendable != tc.want {
				t.Fatalf("got %v (reason %q), want %v", got.Sendable, got.Reason, tc.want)
			}
			if !tc.want && got.Reason != "no_lawful_basis" {
				t.Fatalf("expected no_lawful_basis, got %q", got.Reason)
			}
		})
	}
}

func TestIsSendable_Marketing_Suppression(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()
	subscribed := &ContactMarketingState{MarketingStatus: StatusSubscribed}
	topicA := uuid.New()
	topicB := uuid.New()

	t.Run("global marketing unsubscribe blocks", func(t *testing.T) {
		g := guardWith(&fakeReader{state: subscribed, sups: []Suppression{{Reason: ReasonUnsubscribe, Scope: ScopeMarketing}}})
		if got := g.IsSendable(ctx, org, "a@b.com", ChannelMarketing, &topicA); got.Sendable {
			t.Fatal("expected blocked by global unsubscribe")
		}
	})
	t.Run("topic-scoped unsubscribe blocks only that topic", func(t *testing.T) {
		g := guardWith(&fakeReader{state: subscribed, sups: []Suppression{{Reason: ReasonUnsubscribe, Scope: ScopeMarketing, TopicID: &topicA}}})
		if got := g.IsSendable(ctx, org, "a@b.com", ChannelMarketing, &topicB); !got.Sendable {
			t.Fatalf("topic B should be sendable, got reason %q", got.Reason)
		}
		if got := g.IsSendable(ctx, org, "a@b.com", ChannelMarketing, &topicA); got.Sendable {
			t.Fatal("topic A should be blocked")
		}
	})
	t.Run("suppression is checked before lawful basis", func(t *testing.T) {
		// Even a subscribed contact with a scope=all bounce is blocked, and the reason
		// is the suppression, not the (satisfied) basis.
		g := guardWith(&fakeReader{state: subscribed, sups: []Suppression{{Reason: ReasonHardBounce, Scope: ScopeAll}}})
		got := g.IsSendable(ctx, org, "a@b.com", ChannelMarketing, nil)
		if got.Sendable || got.Reason != "suppressed:hard_bounce" {
			t.Fatalf("got (%v,%q)", got.Sendable, got.Reason)
		}
	})
}

func TestIsSendable_EdgeCases(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()

	t.Run("empty email", func(t *testing.T) {
		g := guardWith(&fakeReader{})
		if got := g.IsSendable(ctx, org, "   ", ChannelMarketing, nil); got.Sendable || got.Reason != "empty_email" {
			t.Fatalf("got (%v,%q)", got.Sendable, got.Reason)
		}
	})
	t.Run("suppression read error fails closed", func(t *testing.T) {
		g := guardWith(&fakeReader{supErr: errors.New("db down")})
		if got := g.IsSendable(ctx, org, "a@b.com", ChannelTransactional, nil); got.Sendable || got.Reason != "error" {
			t.Fatalf("got (%v,%q)", got.Sendable, got.Reason)
		}
	})
	t.Run("state read error fails closed", func(t *testing.T) {
		g := guardWith(&fakeReader{stateErr: errors.New("db down")})
		if got := g.IsSendable(ctx, org, "a@b.com", ChannelMarketing, nil); got.Sendable || got.Reason != "error" {
			t.Fatalf("got (%v,%q)", got.Sendable, got.Reason)
		}
	})
	t.Run("email is normalized before lookup verdict", func(t *testing.T) {
		// A mixed-case, padded address must produce the same verdict as its normal form.
		g := guardWith(&fakeReader{state: &ContactMarketingState{MarketingStatus: StatusSubscribed}})
		if got := g.IsSendable(ctx, org, "  A@B.COM ", ChannelMarketing, nil); !got.Sendable {
			t.Fatalf("expected sendable, got %q", got.Reason)
		}
	})
}
