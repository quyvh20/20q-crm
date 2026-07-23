package marketing

import (
	"testing"

	"github.com/google/uuid"
)

func TestDefaultScopeForReason(t *testing.T) {
	all := []string{ReasonHardBounce, ReasonSoftBounce, ReasonComplaint, ReasonGDPRErasure}
	for _, r := range all {
		if got := DefaultScopeForReason(r); got != ScopeAll {
			t.Errorf("reason %q: got scope %q, want %q", r, got, ScopeAll)
		}
	}
	mkt := []string{ReasonUnsubscribe, ReasonManual}
	for _, r := range mkt {
		if got := DefaultScopeForReason(r); got != ScopeMarketing {
			t.Errorf("reason %q: got scope %q, want %q", r, got, ScopeMarketing)
		}
	}
}

func TestValidators(t *testing.T) {
	if !IsValidReason(ReasonComplaint) || IsValidReason("bogus") {
		t.Error("IsValidReason wrong")
	}
	if !IsValidScope(ScopeAll) || IsValidScope("bogus") {
		t.Error("IsValidScope wrong")
	}
	if !IsValidStatus(StatusSubscribed) || IsValidStatus("bogus") {
		t.Error("IsValidStatus wrong")
	}
	if !IsValidConsentBasis(BasisLegitimateInterest) || IsValidConsentBasis("bogus") {
		t.Error("IsValidConsentBasis wrong")
	}
}

func TestSuppresses_ChannelAndScope(t *testing.T) {
	topic := uuid.New()
	other := uuid.New()

	tests := []struct {
		name    string
		s       Suppression
		channel Channel
		topic   *uuid.UUID
		want    bool
	}{
		{"scope=all blocks transactional", Suppression{Reason: ReasonHardBounce, Scope: ScopeAll}, ChannelTransactional, nil, true},
		{"scope=all blocks marketing", Suppression{Reason: ReasonHardBounce, Scope: ScopeAll}, ChannelMarketing, nil, true},
		{"scope=marketing does not block transactional", Suppression{Reason: ReasonUnsubscribe, Scope: ScopeMarketing}, ChannelTransactional, nil, false},
		{"scope=marketing global blocks marketing", Suppression{Reason: ReasonUnsubscribe, Scope: ScopeMarketing}, ChannelMarketing, &topic, true},
		{"topic-scoped blocks matching topic", Suppression{Reason: ReasonUnsubscribe, Scope: ScopeMarketing, TopicID: &topic}, ChannelMarketing, &topic, true},
		{"topic-scoped ignores other topic", Suppression{Reason: ReasonUnsubscribe, Scope: ScopeMarketing, TopicID: &topic}, ChannelMarketing, &other, false},
		{"topic-scoped ignores nil topic send", Suppression{Reason: ReasonUnsubscribe, Scope: ScopeMarketing, TopicID: &topic}, ChannelMarketing, nil, false},
		{"soft bounce below threshold never blocks", Suppression{Reason: ReasonSoftBounce, Scope: ScopeAll, SoftBounceCount: 1}, ChannelTransactional, nil, false},
		{"soft bounce at threshold blocks", Suppression{Reason: ReasonSoftBounce, Scope: ScopeAll, SoftBounceCount: SoftBounceSuppressThreshold}, ChannelTransactional, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Suppresses(tc.channel, tc.topic); got != tc.want {
				t.Fatalf("Suppresses() = %v, want %v", got, tc.want)
			}
		})
	}
}
