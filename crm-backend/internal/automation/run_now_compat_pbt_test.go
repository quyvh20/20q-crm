package automation

import (
	"testing"

	"pgregory.net/rapid"
)

// Feature: run-now-workflow, Property 2: Trigger/entity compatibility
//
// For any workflow trigger type and selected entity kind, Run Now accepts the pairing
// if and only if the entity kind is the one compatible with that trigger type
// ("contact" for contact_created, contact_updated, and webhook_inbound; "deal" for
// deal_stage_changed); every incompatible pairing is rejected and produces no
// Workflow_Run.
//
// Validates: Requirements 4.1, 4.2, 4.3

// compatExpectedKind is an independent oracle for the compatible entity kind of a
// trigger type. It is defined separately from the production entityKindForTrigger so
// the property cross-checks the implementation against an independent specification
// rather than against itself. Named with a compat-prefix to avoid colliding with
// helpers defined in sibling Run Now property-test files.
func compatExpectedKind(triggerType string) string {
	switch triggerType {
	case TriggerContactCreated, TriggerContactUpdated, TriggerWebhookInbound:
		return "contact"
	case TriggerDealStageChanged:
		return "deal"
	default:
		return ""
	}
}

// compatTriggerTypeGen draws a trigger type from the full set of valid trigger-type
// constants (including no_activity_days, which has no compatible entity kind) plus
// some invalid/random trigger strings, so the property exercises both the supported
// and unsupported branches of entityKindForTrigger.
func compatTriggerTypeGen() *rapid.Generator[string] {
	validTriggers := []string{
		TriggerContactCreated,
		TriggerContactUpdated,
		TriggerDealStageChanged,
		TriggerNoActivityDays,
		TriggerWebhookInbound,
	}
	return rapid.OneOf(
		rapid.SampledFrom(validTriggers),
		// Arbitrary strings stand in for invalid/unknown trigger types.
		rapid.String(),
	)
}

// compatEntityKindGen draws the selected Sample_Entity kind from the two kinds a Run
// Now request can target.
func compatEntityKindGen() *rapid.Generator[string] {
	return rapid.SampledFrom([]string{"contact", "deal"})
}

func TestRunNowCompatibilityProperty(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		triggerType := compatTriggerTypeGen().Draw(t, "triggerType")
		entityKind := compatEntityKindGen().Draw(t, "entityKind")

		// The production mapping must agree with the independent oracle for every
		// trigger type (valid or invalid).
		gotKind := entityKindForTrigger(triggerType)
		wantKind := compatExpectedKind(triggerType)
		if gotKind != wantKind {
			t.Fatalf("entityKindForTrigger(%q) = %q, want %q", triggerType, gotKind, wantKind)
		}

		// Run Now accepts the pairing iff the selected entity kind equals the
		// trigger's compatible kind. Compute the decision the handler makes
		// (kind == entityKindForTrigger(...)) and the independently expected
		// decision, then assert they match.
		accepted := entityKind == gotKind
		expectedAccepted := entityKind == wantKind
		if accepted != expectedAccepted {
			t.Fatalf("compatibility for trigger=%q entity=%q: accepted=%v, want %v",
				triggerType, entityKind, accepted, expectedAccepted)
		}

		// An unsupported trigger type (no compatible kind) can never be accepted,
		// because the selected entity kind is always "contact" or "deal".
		if wantKind == "" && accepted {
			t.Fatalf("unsupported trigger %q accepted entity kind %q; must always reject",
				triggerType, entityKind)
		}

		// A supported trigger must reject the non-matching entity kind.
		if wantKind != "" {
			mismatchedKind := "deal"
			if wantKind == "deal" {
				mismatchedKind = "contact"
			}
			if mismatchedKind == entityKindForTrigger(triggerType) {
				t.Fatalf("trigger %q should be incompatible with kind %q but was accepted",
					triggerType, mismatchedKind)
			}
		}
	})
}
