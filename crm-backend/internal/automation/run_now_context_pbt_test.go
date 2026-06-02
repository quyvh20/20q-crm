package automation

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"pgregory.net/rapid"
)

// Feature: run-now-workflow, Property 3: Trigger_Context construction invariants
//
// For any loaded contact or deal and its workflow's trigger type, the
// Trigger_Context produced by buildRunNowTriggerContext satisfies all of:
//   - entity_id equals the entity's id (entity["id"]);
//   - the entity is present under the matching contact/deal key, and that nested
//     entity map carries its own id under an "id" key;
//   - the trigger object's type equals the workflow's trigger type and its source
//     equals "run_now";
//   - the context contains no "_internal_update" marker anywhere;
//   - for a deal_stage_changed context, new_stage_id equals the deal's current
//     stage id (entity["stage_id"]).
//
// Validates: Requirements 5.1, 5.2, 5.3, 5.4, 5.5, 5.6

// ctxPbtFieldKeys is the pool of optional, non-reserved field keys the generators
// draw from. It deliberately mixes ASCII and unicode keys and excludes the keys the
// builder manages ("id", "stage_id") and the marker the invariant forbids
// ("_internal_update"), so a generated map can never spuriously inject that marker.
var ctxPbtFieldKeys = []string{
	"first_name", "last_name", "email", "phone", "company",
	"title", "value", "notes", "owner_id",
	"名前", "日本語フィールド", "emoji_😀", "ünïcödé", "поле",
}

// ctxPbtDrawValue draws an arbitrary entity field value as an `any`, covering
// unicode strings, integers, floats, booleans, and nil (a missing/empty optional
// field's value).
func ctxPbtDrawValue(t *rapid.T, label string) any {
	switch rapid.IntRange(0, 4).Draw(t, label+"_kind") {
	case 0:
		return rapid.String().Draw(t, label+"_str")
	case 1:
		return rapid.Int().Draw(t, label+"_int")
	case 2:
		return rapid.Float64().Draw(t, label+"_float")
	case 3:
		return rapid.Bool().Draw(t, label+"_bool")
	default:
		return nil
	}
}

// ctxPbtGenEntity builds a random entity map. It always includes an "id" key
// (Req 5.3) and a random subset of the optional unicode/ASCII fields, modelling
// "missing optional fields". For deals (kind=="deal") it includes a "stage_id" key
// only sometimes, exercising both the present and absent stage paths (Req 5.5).
func ctxPbtGenEntity(t *rapid.T, kind string) map[string]any {
	entity := map[string]any{
		// id is a UUID string in production; the builder only copies it, so any
		// value works. Use a real UUID string for realism.
		"id": uuid.NewString(),
	}

	// Draw a random subset of optional fields (possibly none -> missing fields).
	n := rapid.IntRange(0, len(ctxPbtFieldKeys)).Draw(t, "field_count")
	keys := rapid.SampledFrom(ctxPbtFieldKeys)
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		k := keys.Draw(t, "field_key")
		if seen[k] {
			continue
		}
		seen[k] = true
		entity[k] = ctxPbtDrawValue(t, "field_val_"+k)
	}

	if kind == "deal" {
		// stage_id sometimes present, sometimes absent.
		if rapid.Bool().Draw(t, "has_stage_id") {
			// Mostly a UUID string, but allow an empty string too.
			if rapid.Bool().Draw(t, "stage_id_empty") {
				entity["stage_id"] = ""
			} else {
				entity["stage_id"] = uuid.NewString()
			}
		}
	}

	return entity
}

// ctxPbtContainsInternalUpdate recursively reports whether the "_internal_update"
// key appears anywhere in the value (maps or nested slices/maps).
func ctxPbtContainsInternalUpdate(v any) bool {
	switch tv := v.(type) {
	case map[string]any:
		if _, ok := tv["_internal_update"]; ok {
			return true
		}
		for _, child := range tv {
			if ctxPbtContainsInternalUpdate(child) {
				return true
			}
		}
	case []any:
		for _, child := range tv {
			if ctxPbtContainsInternalUpdate(child) {
				return true
			}
		}
	}
	return false
}

func TestRunNowTriggerContextInvariants_Property(t *testing.T) {
	// rapid.Check runs a minimum of 100 iterations by default (the -rapid.checks
	// flag's default), satisfying the >=100-iteration requirement for this property.
	rapid.Check(t, func(t *rapid.T) {
		// Pick a kind, then a trigger type compatible with that kind.
		kind := rapid.SampledFrom([]string{"contact", "deal"}).Draw(t, "kind")

		var triggerType string
		switch kind {
		case "contact":
			triggerType = rapid.SampledFrom([]string{
				TriggerContactCreated,
				TriggerContactUpdated,
				TriggerWebhookInbound,
			}).Draw(t, "contact_trigger")
		default: // deal
			triggerType = TriggerDealStageChanged
		}

		entity := ctxPbtGenEntity(t, kind)

		ctx := buildRunNowTriggerContext(kind, triggerType, entity)

		// Invariant 1 (Req 5.1/5.2): entity_id equals the entity's own id.
		if !reflect.DeepEqual(ctx["entity_id"], entity["id"]) {
			t.Fatalf("entity_id %#v != entity id %#v", ctx["entity_id"], entity["id"])
		}

		// Invariant 2 (Req 5.1/5.2/5.3): the entity is present under the matching
		// kind key and that nested map carries its own "id".
		nested, ok := ctx[kind].(map[string]any)
		if !ok {
			t.Fatalf("expected entity under key %q, got %#v", kind, ctx[kind])
		}
		if !reflect.DeepEqual(nested, entity) {
			t.Fatalf("nested entity under %q != source entity", kind)
		}
		if _, hasID := nested["id"]; !hasID {
			t.Fatalf("nested entity under %q is missing its own id key", kind)
		}

		// The other entity kind key must NOT be present.
		otherKey := "deal"
		if kind == "deal" {
			otherKey = "contact"
		}
		if _, present := ctx[otherKey]; present {
			t.Fatalf("unexpected %q key present for %q-targeted context", otherKey, kind)
		}

		// Invariant 3 (Req 5.4): trigger.type == workflow trigger type, trigger.source == "run_now".
		trigger, ok := ctx["trigger"].(map[string]any)
		if !ok {
			t.Fatalf("expected trigger map, got %#v", ctx["trigger"])
		}
		if trigger["type"] != triggerType {
			t.Fatalf("trigger.type = %#v, want %q", trigger["type"], triggerType)
		}
		if trigger["source"] != "run_now" {
			t.Fatalf("trigger.source = %#v, want %q", trigger["source"], "run_now")
		}

		// Invariant 4 (Req 5.6): no _internal_update marker anywhere in the context.
		if ctxPbtContainsInternalUpdate(ctx) {
			t.Fatalf("context contains forbidden _internal_update marker: %#v", ctx)
		}

		// Invariant 5 (Req 5.5): for deal_stage_changed, new_stage_id == the deal's
		// current stage id (entity["stage_id"]), defaulting to "" when absent.
		if triggerType == TriggerDealStageChanged {
			wantStage := any("")
			if v, hasStage := entity["stage_id"]; hasStage {
				wantStage = v
			}
			if !reflect.DeepEqual(ctx["new_stage_id"], wantStage) {
				t.Fatalf("new_stage_id = %#v, want %#v", ctx["new_stage_id"], wantStage)
			}
		}
	})
}
