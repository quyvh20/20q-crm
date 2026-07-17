package domain

import (
	"context"
	"testing"
)

// TestSilenceImpliesSuppression is the whole composition guarantee, and it is the
// reason IsAutomationSuppressed derives from silence rather than the two flags being
// independent siblings.
//
// The two failures are not equally expensive. Forgetting to honor silence costs one
// stray timer; forgetting to honor suppression enrolls every synthetic record into
// every workflow. Deriving makes the expensive mistake unrepresentable — but only
// while this holds, so any refactor that splits the derivation must fail here rather
// than ship an enrollment storm quietly.
func TestSilenceImpliesSuppression(t *testing.T) {
	ctx := WithAutomationSilenced(context.Background())

	if !IsAutomationSilenced(ctx) {
		t.Fatal("WithAutomationSilenced must mark the context")
	}
	if !IsAutomationSuppressed(ctx) {
		t.Fatal("silence must imply suppression, or a silenced write still enrolls")
	}
}

// TestSuppressionDoesNotImplySilence pins the other direction. Backfill depends on
// it: a backfilled lead is a real person, so their close-date reminder SHOULD still
// arm — only the enrollment storm is being prevented.
func TestSuppressionDoesNotImplySilence(t *testing.T) {
	ctx := WithAutomationSuppressed(context.Background())

	if !IsAutomationSuppressed(ctx) {
		t.Fatal("WithAutomationSuppressed must mark the context")
	}
	if IsAutomationSilenced(ctx) {
		t.Fatal("suppression must NOT silence: a backfilled record's own future schedule should still arm")
	}
}

// TestOrdinaryWriteIsNeitherSuppressedNorSilenced is the control. A positive-only
// suite cannot catch an always-on flag — the failure where every write in the app
// silently stops enrolling.
func TestOrdinaryWriteIsNeitherSuppressedNorSilenced(t *testing.T) {
	ctx := context.Background()

	if IsAutomationSuppressed(ctx) {
		t.Error("a plain context must not read as suppressed")
	}
	if IsAutomationSilenced(ctx) {
		t.Error("a plain context must not read as silenced")
	}
}

// TestAutomationFlagsUseDistinctKeys guards against the two flags being stored under
// one context key. The suppression reader discards the comma-ok, so a non-bool
// sharing that key would read false and silently un-suppress the write.
func TestAutomationFlagsUseDistinctKeys(t *testing.T) {
	if AutomationSuppressedPayloadKey == AutomationSilencedPayloadKey {
		t.Fatal("the payload keys must differ, or the engine cannot tell arming from enrollment")
	}

	silenced := WithAutomationSilenced(context.Background())
	if !IsAutomationSuppressed(WithAutomationSuppressed(silenced)) {
		t.Error("the flags must compose without clobbering each other")
	}
	if !IsAutomationSilenced(WithAutomationSuppressed(silenced)) {
		t.Error("suppressing an already-silenced context must not erase the silence")
	}
}
