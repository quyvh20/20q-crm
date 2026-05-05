package automation

import (
	"testing"
)

func TestPayloadContainsChangedField(t *testing.T) {
	tests := []struct {
		name       string
		payload    map[string]any
		watchField string
		want       bool
	}{
		{
			name:       "field present in []string",
			payload:    map[string]any{"changed_fields": []string{"contact.email", "contact.owner_user_id"}},
			watchField: "contact.owner_user_id",
			want:       true,
		},
		{
			name:       "field absent in []string",
			payload:    map[string]any{"changed_fields": []string{"contact.email"}},
			watchField: "contact.owner_user_id",
			want:       false,
		},
		{
			name:       "field present in []any (JSON unmarshal shape)",
			payload:    map[string]any{"changed_fields": []any{"contact.email", "contact.owner_user_id"}},
			watchField: "contact.owner_user_id",
			want:       true,
		},
		{
			name:       "field absent in []any",
			payload:    map[string]any{"changed_fields": []any{"contact.first_name"}},
			watchField: "contact.owner_user_id",
			want:       false,
		},
		{
			name:       "no changed_fields key",
			payload:    map[string]any{"entity_id": "123"},
			watchField: "contact.email",
			want:       false,
		},
		{
			name:       "empty changed_fields",
			payload:    map[string]any{"changed_fields": []string{}},
			watchField: "contact.email",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := payloadContainsChangedField(tt.payload, tt.watchField)
			if got != tt.want {
				t.Errorf("payloadContainsChangedField() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetNestedValue(t *testing.T) {
	payload := map[string]any{
		"contact": map[string]any{
			"email":         "jane@example.com",
			"owner_user_id": "uuid-123",
			"custom_fields": map[string]any{
				"tier": "gold",
			},
		},
		"entity_id": "abc",
	}

	tests := []struct {
		name string
		path string
		want any
	}{
		{"top-level key", "entity_id", "abc"},
		{"nested key", "contact.email", "jane@example.com"},
		{"deeply nested", "contact.custom_fields.tier", "gold"},
		{"missing key", "contact.phone", nil},
		{"invalid path", "nonexistent.field", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getNestedValue(payload, tt.path)
			if got != tt.want {
				t.Errorf("getNestedValue(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestValuesMatch(t *testing.T) {
	tests := []struct {
		name     string
		actual   any
		expected any
		want     bool
	}{
		{"string match", "uuid-123", "uuid-123", true},
		{"string mismatch", "uuid-123", "uuid-456", false},
		{"nil vs nil", nil, nil, true},
		{"nil vs value", nil, "hello", false},
		{"value vs nil", "hello", nil, false},
		{"int vs string of int", 42, "42", true},
		{"float vs string", 3.14, "3.14", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := valuesMatch(tt.actual, tt.expected)
			if got != tt.want {
				t.Errorf("valuesMatch(%v, %v) = %v, want %v", tt.actual, tt.expected, got, tt.want)
			}
		})
	}
}

func TestSplitDotPath(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"contact.email", []string{"contact", "email"}},
		{"contact.custom_fields.tier", []string{"contact", "custom_fields", "tier"}},
		{"entity_id", []string{"entity_id"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := splitDotPath(tt.path)
			if len(got) != len(tt.want) {
				t.Fatalf("splitDotPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitDotPath(%q)[%d] = %q, want %q", tt.path, i, got[i], tt.want[i])
				}
			}
		})
	}
}
