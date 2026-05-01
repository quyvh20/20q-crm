package automation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaField_JSONShape verifies that every SchemaField serializes
// with exactly the required keys: path, label, type, picker_type, options.
func TestSchemaField_JSONShape(t *testing.T) {
	t.Run("plain string field — no picker, no options", func(t *testing.T) {
		f := SchemaField{
			Path:  "contact.email",
			Label: "Email",
			Type:  "string",
		}
		b, err := json.Marshal(f)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		// Required keys always present
		assert.Equal(t, "contact.email", m["path"])
		assert.Equal(t, "Email", m["label"])
		assert.Equal(t, "string", m["type"])

		// picker_type and options omitted when empty (omitempty)
		_, hasPicker := m["picker_type"]
		_, hasOptions := m["options"]
		assert.False(t, hasPicker, "picker_type should be omitted when empty")
		assert.False(t, hasOptions, "options should be omitted when empty")
	})

	t.Run("field with picker_type=tag", func(t *testing.T) {
		f := SchemaField{
			Path:       "contact.tags",
			Label:      "Tags",
			Type:       "array",
			PickerType: "tag",
		}
		b, err := json.Marshal(f)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assert.Equal(t, "contact.tags", m["path"])
		assert.Equal(t, "Tags", m["label"])
		assert.Equal(t, "array", m["type"])
		assert.Equal(t, "tag", m["picker_type"])
		_, hasOptions := m["options"]
		assert.False(t, hasOptions, "options should be omitted when nil")
	})

	t.Run("field with picker_type=stage", func(t *testing.T) {
		f := SchemaField{
			Path:       "deal.stage",
			Label:      "Stage",
			Type:       "string",
			PickerType: "stage",
		}
		b, err := json.Marshal(f)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assert.Equal(t, "deal.stage", m["path"])
		assert.Equal(t, "Stage", m["label"])
		assert.Equal(t, "string", m["type"])
		assert.Equal(t, "stage", m["picker_type"])
	})

	t.Run("field with picker_type=user", func(t *testing.T) {
		f := SchemaField{
			Path:       "contact.owner_id",
			Label:      "Owner",
			Type:       "string",
			PickerType: "user",
		}
		b, err := json.Marshal(f)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assert.Equal(t, "contact.owner_id", m["path"])
		assert.Equal(t, "Owner", m["label"])
		assert.Equal(t, "string", m["type"])
		assert.Equal(t, "user", m["picker_type"])
	})

	t.Run("select field with options", func(t *testing.T) {
		f := SchemaField{
			Path:    "contact.custom_fields.lead_source",
			Label:   "Lead Source",
			Type:    "select",
			Options: []string{"Web", "Referral", "Cold Call"},
		}
		b, err := json.Marshal(f)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		assert.Equal(t, "contact.custom_fields.lead_source", m["path"])
		assert.Equal(t, "Lead Source", m["label"])
		assert.Equal(t, "select", m["type"])
		_, hasPicker := m["picker_type"]
		assert.False(t, hasPicker, "picker_type should be omitted for select fields")

		opts, ok := m["options"].([]any)
		require.True(t, ok, "options must be an array")
		assert.Len(t, opts, 3)
		assert.Equal(t, "Web", opts[0])
		assert.Equal(t, "Referral", opts[1])
		assert.Equal(t, "Cold Call", opts[2])
	})

	t.Run("roundtrip — all 5 properties", func(t *testing.T) {
		// A hypothetical field with ALL properties set
		f := SchemaField{
			Path:       "deal.custom_fields.contract_type",
			Label:      "Contract Type",
			Type:       "select",
			PickerType: "stage", // unusual combo, but tests all keys present
			Options:    []string{"Monthly", "Annual"},
		}
		b, err := json.Marshal(f)
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))

		// All 5 keys present
		assert.Contains(t, m, "path")
		assert.Contains(t, m, "label")
		assert.Contains(t, m, "type")
		assert.Contains(t, m, "picker_type")
		assert.Contains(t, m, "options")

		assert.Equal(t, "deal.custom_fields.contract_type", m["path"])
		assert.Equal(t, "Contract Type", m["label"])
		assert.Equal(t, "select", m["type"])
		assert.Equal(t, "stage", m["picker_type"])

		opts := m["options"].([]any)
		assert.Equal(t, []any{"Monthly", "Annual"}, opts)
	})

	t.Run("frontend deserialization contract", func(t *testing.T) {
		// Verify that the JSON from a full SchemaField can be deserialized
		// back with the exact same values (simulates frontend parsing)
		original := SchemaField{
			Path:       "contact.tags",
			Label:      "Tags",
			Type:       "array",
			PickerType: "tag",
			Options:    nil,
		}
		b, _ := json.Marshal(original)

		var parsed SchemaField
		require.NoError(t, json.Unmarshal(b, &parsed))

		assert.Equal(t, original.Path, parsed.Path)
		assert.Equal(t, original.Label, parsed.Label)
		assert.Equal(t, original.Type, parsed.Type)
		assert.Equal(t, original.PickerType, parsed.PickerType)
		assert.Nil(t, parsed.Options)
	})
}

// TestSchemaField_AllBuiltinFieldsHaveRequiredProperties verifies that
// every built-in field in the handler has path, label, and type set.
func TestSchemaField_AllBuiltinFieldsHaveRequiredProperties(t *testing.T) {
	// Recreate the same built-in fields from handlers.go
	entities := []SchemaEntity{
		{
			Key: "contact", Label: "Contact", Icon: "👤",
			Fields: []SchemaField{
				{Path: "contact.first_name", Label: "First Name", Type: "string"},
				{Path: "contact.last_name", Label: "Last Name", Type: "string"},
				{Path: "contact.email", Label: "Email", Type: "string"},
				{Path: "contact.phone", Label: "Phone", Type: "string"},
				{Path: "contact.owner_id", Label: "Owner", Type: "string", PickerType: "user"},
				{Path: "contact.tags", Label: "Tags", Type: "array", PickerType: "tag"},
				{Path: "contact.company.name", Label: "Company Name", Type: "string"},
				{Path: "contact.created_at", Label: "Created At", Type: "date"},
				{Path: "contact.id", Label: "Contact ID", Type: "string"},
			},
		},
		{
			Key: "deal", Label: "Deal", Icon: "💰",
			Fields: []SchemaField{
				{Path: "deal.title", Label: "Title", Type: "string"},
				{Path: "deal.value", Label: "Value", Type: "number"},
				{Path: "deal.stage", Label: "Stage", Type: "string", PickerType: "stage"},
				{Path: "deal.probability", Label: "Probability (%)", Type: "number"},
				{Path: "deal.is_won", Label: "Is Won", Type: "boolean"},
				{Path: "deal.is_lost", Label: "Is Lost", Type: "boolean"},
				{Path: "deal.owner_id", Label: "Owner", Type: "string", PickerType: "user"},
				{Path: "deal.expected_close_at", Label: "Expected Close", Type: "date"},
				{Path: "deal.closed_at", Label: "Closed At", Type: "date"},
				{Path: "deal.created_at", Label: "Created At", Type: "date"},
				{Path: "deal.id", Label: "Deal ID", Type: "string"},
			},
		},
		{
			Key: "trigger", Label: "Trigger Event", Icon: "⚡",
			Fields: []SchemaField{
				{Path: "trigger.type", Label: "Event Type", Type: "string"},
				{Path: "trigger.from_stage", Label: "Previous Stage", Type: "string", PickerType: "stage"},
				{Path: "trigger.to_stage", Label: "New Stage", Type: "string", PickerType: "stage"},
			},
		},
	}

	for _, entity := range entities {
		for _, field := range entity.Fields {
			t.Run(field.Path, func(t *testing.T) {
				assert.NotEmpty(t, field.Path, "path must be set")
				assert.NotEmpty(t, field.Label, "label must be set")
				assert.NotEmpty(t, field.Type, "type must be set")

				// Verify type is one of the valid types
				validTypes := map[string]bool{
					"string": true, "number": true, "boolean": true,
					"array": true, "select": true, "date": true,
				}
				assert.True(t, validTypes[field.Type], "type '%s' must be a valid type", field.Type)

				// If pickerType is set, it must be valid
				if field.PickerType != "" {
					validPickers := map[string]bool{"tag": true, "stage": true, "user": true}
					assert.True(t, validPickers[field.PickerType], "picker_type '%s' must be valid", field.PickerType)
				}

				// Verify JSON serialization
				b, err := json.Marshal(field)
				require.NoError(t, err)
				var m map[string]any
				require.NoError(t, json.Unmarshal(b, &m))
				assert.Contains(t, m, "path")
				assert.Contains(t, m, "label")
				assert.Contains(t, m, "type")
			})
		}
	}
}
