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
	ops := []string{"set", "add", "remove", "increment", "decrement"}
	for _, op := range ops {
		actions := []ActionSpec{
			{Type: "update_contact", ID: "uc1", Params: map[string]any{
				"updates": []any{
					map[string]any{"field": "contact.tags", "op": op, "value": "test"},
				},
			}},
		}
		data, _ := json.Marshal(actions)
		result := &ValidationResult{Valid: true}
		validateActions(data, result)
		if !result.Valid {
			t.Errorf("op '%s' should be valid, got errors: %+v", op, result.Errors)
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
