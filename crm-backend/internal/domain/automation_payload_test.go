package domain

import "testing"

// The bug this guards: three call sites wrote custom fields as a FLATTENED key
// ("custom_fields.tier"), while every reader resolves dotted paths by splitting on
// "." and walking nested maps. The flattened key is therefore unreachable — merge
// tags render empty and conditions fail closed, with no error and no failed run.
//
// walkDotPath below is a faithful stand-in for automation.getNestedValue, which
// this package cannot import (automation imports domain, not the reverse). If the
// engine's resolution strategy ever changes, this test is the tripwire.
func walkDotPath(payload map[string]any, parts ...string) any {
	var current any = payload
	for _, p := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[p]
	}
	return current
}

func TestCustomFieldsAreReachableByDottedPath(t *testing.T) {
	m := map[string]any{"id": "abc"}
	SetAutomationCustomFields(m, []byte(`{"tier":"gold","seats":25}`))

	if got := walkDotPath(m, "custom_fields", "tier"); got != "gold" {
		t.Errorf(`custom_fields.tier did not resolve: got %v (payload %+v)`, got, m)
	}
	// A flattened key would leave this nil — the exact silent failure in production.
	if got := walkDotPath(m, "custom_fields", "seats"); got == nil {
		t.Error("custom_fields.seats did not resolve")
	}
}

func TestCustomFieldsRejectsTheFlattenedShape(t *testing.T) {
	m := map[string]any{}
	SetAutomationCustomFields(m, []byte(`{"tier":"gold"}`))

	if _, flat := m["custom_fields.tier"]; flat {
		t.Error("custom fields must NOT be written as a flattened dotted key")
	}
	if _, nested := m["custom_fields"]; !nested {
		t.Error("custom fields must be written nested under custom_fields")
	}
}

// Empty payloads must not plant an empty map: `{{contact.custom_fields.x}}` on a
// record with no custom fields should resolve to nil either way, but leaving the
// key absent keeps the payload honest about what the record actually carries.
func TestCustomFieldsSkipsEmptyPayloads(t *testing.T) {
	for name, raw := range map[string]string{
		"nil":          "",
		"sql null":     "null",
		"empty object": "{}",
		"malformed":    "{not json",
		"empty map":    `{}`,
	} {
		m := map[string]any{}
		SetAutomationCustomFields(m, []byte(raw))
		if _, present := m["custom_fields"]; present {
			t.Errorf("%s: should not have set custom_fields, got %+v", name, m)
		}
	}
}

func TestCustomFieldsPreservesValueTypes(t *testing.T) {
	m := map[string]any{}
	SetAutomationCustomFields(m, []byte(`{"n":42,"b":true,"s":"x","null":null}`))
	cf, ok := m["custom_fields"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", m["custom_fields"])
	}
	// Numbers arrive as float64 through encoding/json; conditions compare
	// numerically, so this is the shape the engine expects.
	if cf["n"] != float64(42) {
		t.Errorf("n = %v (%T), want float64(42)", cf["n"], cf["n"])
	}
	if cf["b"] != true {
		t.Errorf("b = %v, want true", cf["b"])
	}
	if cf["s"] != "x" {
		t.Errorf("s = %v, want x", cf["s"])
	}
	if _, present := cf["null"]; !present {
		t.Error("an explicit null should still be present as a key")
	}
}
