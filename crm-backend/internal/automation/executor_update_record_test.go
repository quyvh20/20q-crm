package automation

import (
	"testing"
)

// ============================================================
// VALIDATOR tests for the update_contact / update_record actions.
//
// The filename says "executor" and always lied: every test here is a validator test
// (the executor's own tests live in executor_update_record_exec_test.go). What changed
// in R5 deploy 1 is the ENTRY POINT, not the rules: these used to call validateActions
// with a flat action array, and validateActions is gone. Every assertion below is about
// validateActionParams, which is still live — it is what validateStepsRecursive calls
// for each action step — so the tests were re-expressed through the steps validator
// rather than deleted with the dead entry point.
//
// The only visible difference is the error field path: `actions[0].params.updates[0].op`
// became `steps[0].action.params.updates[0].op`. That equivalence was not assumed; the
// package used to assert it directly with a property test over flat-vs-steps validation
// (see steps_fixtures_test.go).
// ============================================================

func TestValidateStepAction_UpdateContact_Valid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_MultipleUpdates(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
				map[string]any{"field": "contact.tags", "op": "add", "value": []string{"uuid1"}},
				map[string]any{"field": "contact.phone", "op": "clear"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("expected valid for multi-update, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_EmptyUpdates(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid for empty updates array")
	}
}

func TestValidateStepAction_UpdateContact_MissingField(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"op": "set", "value": "Jane"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid when field is missing in update entry")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[0].field" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[0].field, got: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_MissingOp(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "value": "Jane"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid when op is missing")
	}
}

func TestValidateStepAction_UpdateContact_InvalidOp(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "multiply", "value": "Jane"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown op")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[0].op" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[0].op, got: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_ClearNoValueOK(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.email", "op": "clear"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("clear op should not require value, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_SetRequiresValue(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid when value is missing for 'set' op")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[0].value" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[0].value, got: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_AllOpsValid(t *testing.T) {
	// Each operation paired with a compatible field type
	tests := []struct {
		field string
		op    string
		value any
	}{
		{"contact.tags", "set", "test"},
		{"contact.tags", "add", "test"},
		{"contact.tags", "remove", "test"},
		{"custom_fields.score", "increment", 5},
		{"custom_fields.score", "decrement", 3},
	}
	for _, tt := range tests {
		actions := []ActionSpec{
			{Type: "update_contact", ID: "uc1", Params: map[string]any{
				"updates": []any{
					map[string]any{"field": tt.field, "op": tt.op, "value": tt.value},
				},
			}},
		}
		result := &ValidationResult{Valid: true}
		validateSingleActionParams(t, actions, result)
		if !result.Valid {
			t.Errorf("op '%s' on '%s' should be valid, got errors: %+v", tt.op, tt.field, result.Errors)
		}
	}
}

func TestValidateStepAction_UpdateContact_IncrementWithValue(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": 5},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_TagsAddWithArray(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{
					"field": "contact.tags",
					"op":    "add",
					"value": []string{"tag-uuid-1", "tag-uuid-2"},
				},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("expected valid for tags add with array, got errors: %+v", result.Errors)
	}
}

// ============================================================
// Legacy flat format backward compatibility
// ============================================================

func TestValidateStepAction_UpdateContact_LegacyFlatValid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field":     "contact.first_name",
			"operation": "set",
			"value":     "Jane",
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("legacy flat format should still be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_LegacyMissingField(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"operation": "set",
			"value":     "Jane",
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid when legacy field is missing")
	}
}

func TestValidateStepAction_UpdateContact_MixedErrorPaths(t *testing.T) {
	// Two updates: first valid, second missing op → error path should be updates[1].op
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
				map[string]any{"field": "contact.email"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid for second update missing op")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[1].op" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[1].op, got: %+v", result.Errors)
	}
}

// ============================================================
// Schema-aware validation tests
// ============================================================

func TestValidateStepAction_UpdateContact_UnknownFieldRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.nonexistent", "op": "set", "value": "x"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown field 'contact.nonexistent'")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[0].field" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on .field, got: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_IncrementOnStringRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "increment", "value": 5},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid: can't increment a string field")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[0].op" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on .op, got: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_RemoveOnStringRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.email", "op": "remove", "value": "test@x.com"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid: can't remove from a string field")
	}
}

func TestValidateStepAction_UpdateContact_NonNumericIncrementValueRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": "not-a-number"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid: increment value must be numeric")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "steps[0].action.params.updates[0].value" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on .value, got: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_NumericStringCoercionOK(t *testing.T) {
	// "5" should be accepted as numeric for increment (coercion)
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": "5"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("numeric string '5' should coerce for increment, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_TemplateValueBypassesTypeCheck(t *testing.T) {
	// Template values are resolved at runtime, should not be type-checked
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": "{{trigger.amount}}"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("template value should bypass type check, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateContact_CustomFieldAccepted(t *testing.T) {
	// Any custom_fields.* path should be structurally accepted
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.industry", "op": "set", "value": "Tech"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("custom field should be accepted, got errors: %+v", result.Errors)
	}
}

// ============================================================
// update_record type tests (new action type + deal support)
// ============================================================

func TestValidateStepAction_UpdateRecord_Valid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("update_record should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateRecord_DealFieldValid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.title", "op": "set", "value": "Big Deal"},
				map[string]any{"field": "deal.value", "op": "set", "value": "50000"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("deal fields should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateRecord_DealUnknownFieldRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.nonexistent", "op": "set", "value": "x"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown deal field 'deal.nonexistent'")
	}
}

// Regression (won/lost backdoor): is_won/is_lost must NOT be directly settable via
// update_record. Winning or losing a deal goes through a stage change so the flags
// stay coupled with closed_at + a stage_change activity (see handleDealStageChange).
// A bare boolean write would mark a deal won while it still sits in an open stage
// with no closed_at — a state no other write path in the system can produce.
func TestValidateStepAction_UpdateRecord_DealWonLostFlagsRejected(t *testing.T) {
	for _, field := range []string{"deal.is_won", "deal.is_lost"} {
		t.Run(field, func(t *testing.T) {
			actions := []ActionSpec{
				{Type: "update_record", ID: "ur1", Params: map[string]any{
					"updates": []any{
						map[string]any{"field": field, "op": "set", "value": true},
					},
				}},
			}
			result := &ValidationResult{Valid: true}
			validateSingleActionParams(t, actions, result)
			if result.Valid {
				t.Fatalf("expected invalid: %s must not be directly settable (use a won/lost stage change)", field)
			}
		})
	}
}

func TestValidateStepAction_UpdateRecord_DealCustomFieldAccepted(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.custom_fields.priority", "op": "set", "value": "high"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("deal custom field should be accepted, got errors: %+v", result.Errors)
	}
}

// ── Deal stage (P14): "deal.stage" / "deal.stage_id", set-only ──────

func TestValidateStepAction_UpdateRecord_DealStageValid(t *testing.T) {
	// The builder emits the schema path "deal.stage" (picker_type=stage); the value
	// is the target stage's UUID. This must validate.
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.stage", "op": "set", "value": "11111111-1111-1111-1111-111111111111"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("deal.stage set should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateRecord_DealStageIDLegacyValid(t *testing.T) {
	// Legacy / AI-generated workflows may use the raw column name "deal.stage_id".
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.stage_id", "op": "set", "value": "{{trigger.to_stage}}"},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if !result.Valid {
		t.Errorf("deal.stage_id set should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateStepAction_UpdateRecord_DealValueIncrementValid(t *testing.T) {
	// deal.value / deal.probability are numeric columns: increment/decrement is valid
	// at validation AND now executes (handleGenericColumn supports numeric columns).
	for _, tc := range []struct {
		field string
		op    string
	}{
		{"deal.value", "increment"},
		{"deal.value", "decrement"},
		{"deal.probability", "increment"},
	} {
		actions := []ActionSpec{
			{Type: "update_record", ID: "ur1", Params: map[string]any{
				"updates": []any{
					map[string]any{"field": tc.field, "op": tc.op, "value": 10},
				},
			}},
		}
		result := &ValidationResult{Valid: true}
		validateSingleActionParams(t, actions, result)
		if !result.Valid {
			t.Errorf("%s %s should be valid, got errors: %+v", tc.field, tc.op, result.Errors)
		}
	}
}

func TestValidateStepAction_UpdateRecord_DealStringIncrementRejected(t *testing.T) {
	// increment on a non-numeric column (title is a string) is rejected at validation;
	// handleGenericColumn's numericCols guard is the defense-in-depth backstop.
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.title", "op": "increment", "value": 1},
			},
		}},
	}
	result := &ValidationResult{Valid: true}
	validateSingleActionParams(t, actions, result)
	if result.Valid {
		t.Fatal("expected invalid: cannot increment a string column 'deal.title'")
	}
}

func TestValidateStepAction_UpdateRecord_DealStageNonSetRejected(t *testing.T) {
	// Only "set" (move the deal to a stage) is meaningful — clear/add/etc. must fail
	// at validation rather than surfacing as a runtime executor error.
	for _, op := range []string{"clear", "add", "increment", "remove"} {
		actions := []ActionSpec{
			{Type: "update_record", ID: "ur1", Params: map[string]any{
				"updates": []any{
					map[string]any{"field": "deal.stage", "op": op, "value": "11111111-1111-1111-1111-111111111111"},
				},
			}},
		}
		result := &ValidationResult{Valid: true}
		validateSingleActionParams(t, actions, result)
		if result.Valid {
			t.Errorf("deal.stage op '%s' should be rejected (set-only)", op)
		}
	}
}
