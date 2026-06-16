package fieldvalidate

import (
	"strings"
	"testing"

	"crm-backend/internal/domain"
)

func def(key, typ string, opts ...string) domain.CustomFieldDef {
	return domain.CustomFieldDef{Key: key, Label: key, Type: typ, Options: opts}
}

func TestValidateValue_Types(t *testing.T) {
	tests := []struct {
		name    string
		def     domain.CustomFieldDef
		val     interface{}
		wantErr bool
	}{
		// nil always passes (presence handled separately)
		{"nil passes", def("f", "text"), nil, false},

		// text / url
		{"text string", def("f", "text"), "hello", false},
		{"text non-string", def("f", "text"), 42.0, true},
		{"url string", def("f", "url"), "https://x.io", false},
		{"url non-string", def("f", "url"), true, true},

		// number
		{"number float", def("f", "number"), 12.5, false},
		{"number numeric string", def("f", "number"), "12.5", false},
		{"number non-numeric string", def("f", "number"), "abc", true},
		{"number bool", def("f", "number"), true, true},

		// boolean
		{"boolean true", def("f", "boolean"), true, false},
		{"boolean non-bool", def("f", "boolean"), "true", true},

		// date
		{"date ymd", def("f", "date"), "2026-06-16", false},
		{"date rfc3339", def("f", "date"), "2026-06-16T10:00:00Z", false},
		{"date garbage", def("f", "date"), "not-a-date", true},
		{"date non-string", def("f", "date"), 20260616.0, true},

		// select (case-insensitive)
		{"select valid", def("f", "select", "Low", "High"), "low", false},
		{"select exact", def("f", "select", "Low", "High"), "High", false},
		{"select invalid", def("f", "select", "Low", "High"), "medium", true},
		{"select non-string", def("f", "select", "Low"), 1.0, true},

		// unknown type → no constraints, passes
		{"unknown type passes", def("f", "mystery"), "anything", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateValue(tt.def, tt.val)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidateFields_UnknownKeyPassthrough(t *testing.T) {
	defs := []domain.CustomFieldDef{def("name", "text")}
	data := map[string]interface{}{"name": "Acme", "not_defined": 12345.0}
	if err := ValidateFields(defs, data, "data"); err != nil {
		t.Fatalf("unknown key should pass through, got %v", err)
	}
}

func TestValidateFields_EmptyDefs(t *testing.T) {
	data := map[string]interface{}{"anything": "goes"}
	if err := ValidateFields(nil, data, "data"); err != nil {
		t.Fatalf("no defs means no validation, got %v", err)
	}
}

func TestValidateFields_TypeErrorIsAppError400WithPrefix(t *testing.T) {
	defs := []domain.CustomFieldDef{def("score", "number")}
	data := map[string]interface{}{"score": "not-a-number"}

	err := ValidateFields(defs, data, "custom_fields")
	if err == nil {
		t.Fatal("expected error for bad number")
	}
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected *domain.AppError, got %T", err)
	}
	if appErr.Code != 400 {
		t.Fatalf("expected code 400, got %d", appErr.Code)
	}
	if !strings.HasPrefix(appErr.Message, "custom_fields.score:") {
		t.Fatalf("expected message prefixed with field path, got %q", appErr.Message)
	}
}

func TestValidateFields_Required(t *testing.T) {
	required := domain.CustomFieldDef{Key: "email", Label: "Email", Type: "text", Required: true}
	defs := []domain.CustomFieldDef{required}

	cases := []struct {
		name    string
		data    map[string]interface{}
		wantErr bool
	}{
		{"present", map[string]interface{}{"email": "a@b.com"}, false},
		{"missing", map[string]interface{}{}, true},
		{"nil", map[string]interface{}{"email": nil}, true},
		{"empty string", map[string]interface{}{"email": ""}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateFields(defs, c.data, "data")
			if c.wantErr && err == nil {
				t.Fatalf("expected required error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if c.wantErr {
				appErr, ok := err.(*domain.AppError)
				if !ok || appErr.Code != 400 {
					t.Fatalf("expected *domain.AppError 400, got %v", err)
				}
				if !strings.Contains(appErr.Message, "is required") {
					t.Fatalf("expected 'is required' message, got %q", appErr.Message)
				}
			}
		})
	}
}

func TestValidateFields_RequiredNotTriggeredForFalse(t *testing.T) {
	// A non-required field that is absent must not error.
	defs := []domain.CustomFieldDef{def("nickname", "text")}
	if err := ValidateFields(defs, map[string]interface{}{}, "data"); err != nil {
		t.Fatalf("absent optional field should pass, got %v", err)
	}
}
