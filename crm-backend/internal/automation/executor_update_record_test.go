package automation

import (
	"encoding/json"
	"testing"
)

// ============================================================
// Validator tests for update_contact action — updates[] format
// ============================================================

func TestValidateActions_UpdateContact_Valid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_MultipleUpdates(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
				map[string]any{"field": "contact.tags", "op": "add", "value": []string{"uuid1"}},
				map[string]any{"field": "contact.phone", "op": "clear"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("expected valid for multi-update, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_EmptyUpdates(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid for empty updates array")
	}
}

func TestValidateActions_UpdateContact_MissingField(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"op": "set", "value": "Jane"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid when field is missing in update entry")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[0].field" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[0].field, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_MissingOp(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "value": "Jane"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid when op is missing")
	}
}

func TestValidateActions_UpdateContact_InvalidOp(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "multiply", "value": "Jane"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown op")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[0].op" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[0].op, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_ClearNoValueOK(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.email", "op": "clear"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("clear op should not require value, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_SetRequiresValue(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid when value is missing for 'set' op")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[0].value" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on updates[0].value, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_AllOpsValid(t *testing.T) {
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
		data, _ := json.Marshal(actions)
		result := &ValidationResult{Valid: true}
		validateActions(data, result)
		if !result.Valid {
			t.Errorf("op '%s' on '%s' should be valid, got errors: %+v", tt.op, tt.field, result.Errors)
		}
	}
}

func TestValidateActions_UpdateContact_IncrementWithValue(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": 5},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_TagsAddWithArray(t *testing.T) {
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
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("expected valid for tags add with array, got errors: %+v", result.Errors)
	}
}

// ============================================================
// Legacy flat format backward compatibility
// ============================================================

func TestValidateActions_UpdateContact_LegacyFlatValid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field":     "contact.first_name",
			"operation": "set",
			"value":     "Jane",
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("legacy flat format should still be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_LegacyMissingField(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"operation": "set",
			"value":     "Jane",
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid when legacy field is missing")
	}
}

func TestValidateActions_UpdateContact_MixedErrorPaths(t *testing.T) {
	// Two updates: first valid, second missing op → error path should be updates[1].op
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
				map[string]any{"field": "contact.email"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid for second update missing op")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[1].op" {
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

func TestValidateActions_UpdateContact_UnknownFieldRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.nonexistent", "op": "set", "value": "x"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown field 'contact.nonexistent'")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[0].field" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on .field, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_IncrementOnStringRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "increment", "value": 5},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid: can't increment a string field")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[0].op" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on .op, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_RemoveOnStringRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.email", "op": "remove", "value": "test@x.com"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid: can't remove from a string field")
	}
}

func TestValidateActions_UpdateContact_NonNumericIncrementValueRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": "not-a-number"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid: increment value must be numeric")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.updates[0].value" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on .value, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_NumericStringCoercionOK(t *testing.T) {
	// "5" should be accepted as numeric for increment (coercion)
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": "5"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("numeric string '5' should coerce for increment, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_TemplateValueBypassesTypeCheck(t *testing.T) {
	// Template values are resolved at runtime, should not be type-checked
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.score", "op": "increment", "value": "{{trigger.amount}}"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("template value should bypass type check, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_CustomFieldAccepted(t *testing.T) {
	// Any custom_fields.* path should be structurally accepted
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "custom_fields.industry", "op": "set", "value": "Tech"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("custom field should be accepted, got errors: %+v", result.Errors)
	}
}

// ============================================================
// update_record type tests (new action type + deal support)
// ============================================================

func TestValidateActions_UpdateRecord_Valid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "contact.first_name", "op": "set", "value": "Jane"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("update_record should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateRecord_DealFieldValid(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.title", "op": "set", "value": "Big Deal"},
				map[string]any{"field": "deal.value", "op": "set", "value": "50000"},
				map[string]any{"field": "deal.is_won", "op": "set", "value": true},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("deal fields should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateRecord_DealUnknownFieldRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.nonexistent", "op": "set", "value": "x"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown deal field 'deal.nonexistent'")
	}
}

func TestValidateActions_UpdateRecord_DealIncrementOnBooleanRejected(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.is_won", "op": "increment", "value": 1},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid: can't increment a boolean field")
	}
}

func TestValidateActions_UpdateRecord_DealCustomFieldAccepted(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.custom_fields.priority", "op": "set", "value": "high"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("deal custom field should be accepted, got errors: %+v", result.Errors)
	}
}

// ── Deal stage (P14): "deal.stage" / "deal.stage_id", set-only ──────

func TestValidateActions_UpdateRecord_DealStageValid(t *testing.T) {
	// The builder emits the schema path "deal.stage" (picker_type=stage); the value
	// is the target stage's UUID. This must validate.
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.stage", "op": "set", "value": "11111111-1111-1111-1111-111111111111"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("deal.stage set should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateRecord_DealStageIDLegacyValid(t *testing.T) {
	// Legacy / AI-generated workflows may use the raw column name "deal.stage_id".
	actions := []ActionSpec{
		{Type: "update_record", ID: "ur1", Params: map[string]any{
			"updates": []any{
				map[string]any{"field": "deal.stage_id", "op": "set", "value": "{{trigger.to_stage}}"},
			},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("deal.stage_id set should be valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateRecord_DealStageNonSetRejected(t *testing.T) {
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
		data, _ := json.Marshal(actions)
		result := &ValidationResult{Valid: true}
		validateActions(data, result)
		if result.Valid {
			t.Errorf("deal.stage op '%s' should be rejected (set-only)", op)
		}
	}
}
