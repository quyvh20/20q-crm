// Package fieldvalidate holds the single, shared validator for custom field
// values. It is used by every object that stores user-defined fields — system
// objects (Contact/Deal/Company, via org_settings) and custom objects alike —
// so a value is checked the same way no matter where it is stored.
package fieldvalidate

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ValidateValue checks a single value against a field definition's declared
// type. It returns a plain (unprefixed) error describing the mismatch so the
// caller can wrap it with the appropriate field path. A nil value always
// passes — presence/required is handled by ValidateFields, not here.
func ValidateValue(def domain.CustomFieldDef, val interface{}) error {
	if val == nil {
		return nil
	}

	switch def.Type {
	case "text", "url":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expected string, got %T", val)
		}
	case "number":
		switch v := val.(type) {
		case float64: // JSON numbers
			_ = v
		case string:
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return fmt.Errorf("expected number, got %q", v)
			}
		default:
			return fmt.Errorf("expected number, got %T", val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", val)
		}
	case "date":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected date string, got %T", val)
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				return fmt.Errorf("expected date in YYYY-MM-DD or RFC3339 format")
			}
		}
	case "select":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected string for select, got %T", val)
		}
		valid := false
		for _, opt := range def.Options {
			if strings.EqualFold(opt, s) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("value %q is not a valid option (valid: %v)", s, def.Options)
		}
	case "relation":
		// A relation holds the related record's id (a UUID string), matching how
		// system relations (contact_id, company_id) are carried in UniformRecord.
		// Empty string means "no relation" and passes.
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("expected a related record id string, got %T", val)
		}
		if s != "" {
			if _, err := uuid.Parse(s); err != nil {
				return fmt.Errorf("expected a related record id (uuid), got %q", s)
			}
		}
	case "mirror":
		// A mirror stores no value of its own — it is resolved from a linked record
		// at display time — so any incoming value is simply ignored.
		return nil
	}
	return nil
}

// ValidateFields validates a decoded field map against a set of definitions:
//
//   - every provided key that matches a definition is type-checked;
//   - unknown keys pass through (kept flexible by design);
//   - required fields that are missing, nil, or empty-string are rejected.
//
// prefix names the containing JSON object (e.g. "custom_fields" for system
// objects or "data" for custom objects) so error messages read
// "<prefix>.<key>: ...", matching the surrounding API. It returns a
// *domain.AppError with code 400 on the first violation, or nil when valid.
// An empty defs slice means "nothing to validate" and returns nil.
func ValidateFields(defs []domain.CustomFieldDef, data map[string]interface{}, prefix string) error {
	if len(defs) == 0 {
		return nil
	}

	defMap := make(map[string]domain.CustomFieldDef, len(defs))
	for _, d := range defs {
		defMap[d.Key] = d
	}

	// Type-check each provided field that has a definition.
	for key, val := range data {
		def, ok := defMap[key]
		if !ok {
			continue // unknown keys are allowed
		}
		if err := ValidateValue(def, val); err != nil {
			return domain.NewAppError(400, fmt.Sprintf("%s.%s: %s", prefix, key, err.Error()))
		}
	}

	// Enforce required fields.
	for _, def := range defs {
		if def.Required {
			v, exists := data[def.Key]
			if !exists || v == nil || v == "" {
				return domain.NewAppError(400, fmt.Sprintf("%s.%s is required", prefix, def.Key))
			}
		}
	}

	return nil
}
