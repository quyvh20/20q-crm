package automation

import (
	"encoding/json"
	"testing"
)

// ============================================================
// Validator tests for update_contact action
// ============================================================

func TestValidateActions_UpdateContact_Valid(t *testing.T) {
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
		t.Errorf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_MissingField(t *testing.T) {
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
		t.Fatal("expected invalid when field is missing")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.field" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on field param, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_MissingOperation(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field": "contact.first_name",
			"value": "Jane",
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid when operation is missing")
	}
}

func TestValidateActions_UpdateContact_InvalidOperation(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field":     "contact.first_name",
			"operation": "multiply",
			"value":     "Jane",
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid for unknown operation")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.operation" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on operation param, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_ClearNoValueOK(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field":     "contact.email",
			"operation": "clear",
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("clear operation should not require value, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_SetRequiresValue(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field":     "contact.first_name",
			"operation": "set",
			// no value
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if result.Valid {
		t.Fatal("expected invalid when value is missing for 'set' operation")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.value" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error on value param, got: %+v", result.Errors)
	}
}

func TestValidateActions_UpdateContact_AllOperationsValid(t *testing.T) {
	ops := []string{"set", "add", "remove", "increment", "decrement"}
	for _, op := range ops {
		actions := []ActionSpec{
			{Type: "update_contact", ID: "uc1", Params: map[string]any{
				"field":     "contact.tags",
				"operation": op,
				"value":     "test",
			}},
		}
		data, _ := json.Marshal(actions)
		result := &ValidationResult{Valid: true}
		validateActions(data, result)
		if !result.Valid {
			t.Errorf("operation '%s' should be valid, got errors: %+v", op, result.Errors)
		}
	}
}

func TestValidateActions_UpdateContact_IncrementWithValue(t *testing.T) {
	actions := []ActionSpec{
		{Type: "update_contact", ID: "uc1", Params: map[string]any{
			"field":     "custom_fields.score",
			"operation": "increment",
			"value":     5,
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
			"field":     "contact.tags",
			"operation": "add",
			"value":     []string{"tag-uuid-1", "tag-uuid-2"},
		}},
	}
	data, _ := json.Marshal(actions)
	result := &ValidationResult{Valid: true}
	validateActions(data, result)
	if !result.Valid {
		t.Errorf("expected valid for tags add with array, got errors: %+v", result.Errors)
	}
}
