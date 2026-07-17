package integrations

import (
	"testing"
)

// TestParseFieldMap_EmptyMeansIdentity is the compatibility guard that matters
// most on deploy day. Every source created before the mapping engine existed has
// field_map={}. If empty came to mean "strict, map nothing", every live
// integration would stop the moment this shipped — silently, with leads
// quarantining instead of landing.
func TestParseFieldMap_EmptyMeansIdentity(t *testing.T) {
	for _, raw := range []string{"", "{}", "null"} {
		m, err := ParseFieldMap([]byte(raw))
		if err != nil {
			t.Fatalf("ParseFieldMap(%q): %v", raw, err)
		}
		if !m.IsIdentity() {
			t.Errorf("ParseFieldMap(%q) must be identity", raw)
		}
		out, failures := m.Apply(map[string]any{"email": "a@b.com", "first_name": "Ada"})
		if out["email"] != "a@b.com" || out["first_name"] != "Ada" {
			t.Errorf("identity must pass keys through unchanged: %+v", out)
		}
		if len(failures) != 0 {
			t.Errorf("identity cannot fail: %+v", failures)
		}
	}
}

func TestFieldMap_Apply(t *testing.T) {
	t.Run("renames a source's field to ours", func(t *testing.T) {
		// The actual point of the engine: nobody sends {"email": ...}.
		m := FieldMap{"Work Email": {TargetKey: "email"}}
		out, _ := m.Apply(map[string]any{"Work Email": "ada@example.com"})
		if out["email"] != "ada@example.com" {
			t.Errorf("expected the mapped key; got %+v", out)
		}
		if _, still := out["Work Email"]; still {
			t.Error("the source key must not survive the rename")
		}
	})

	t.Run("unmapped keys pass through for the allowlist to judge", func(t *testing.T) {
		// A partial map must not silently swallow everything it does not mention.
		m := FieldMap{"Work Email": {TargetKey: "email"}}
		out, _ := m.Apply(map[string]any{"Work Email": "a@b.com", "phone": "+1555"})
		if out["phone"] != "+1555" {
			t.Error("an unmapped key must pass through unchanged")
		}
	})

	t.Run("split_name splits a single full-name field", func(t *testing.T) {
		// Ad platforms overwhelmingly send one "Full Name".
		m := FieldMap{"Full Name": {TargetKey: "first_name", Transform: TransformSplitName}}
		out, _ := m.Apply(map[string]any{"Full Name": "Ada Lovelace"})
		if out["first_name"] != "Ada" || out["last_name"] != "Lovelace" {
			t.Errorf("split_name should populate both names; got %+v", out)
		}
	})

	t.Run("a mapping that cannot be applied quarantines, never rejects", func(t *testing.T) {
		// A lead half-understood beats a lead refused.
		m := FieldMap{"Full Name": {TargetKey: "first_name", Transform: TransformSplitName}}
		out, failures := m.Apply(map[string]any{"Full Name": "   "})
		if _, ok := out["first_name"]; ok {
			t.Error("an unsplittable name must not be written")
		}
		if failures["Full Name"] == "" {
			t.Error("the failure must be recorded so the integrator can see it")
		}
	})

	t.Run("a map with no target is a recorded failure, not a panic", func(t *testing.T) {
		m := FieldMap{"Mystery": {TargetKey: ""}}
		_, failures := m.Apply(map[string]any{"Mystery": "x"})
		if failures["Mystery"] == "" {
			t.Error("an incomplete mapping must surface as a failure")
		}
	})
}

func TestSplitFullName(t *testing.T) {
	// Last-space split: "Ada Byron King" is far likelier to be first="Ada"
	// last="Byron King" than the reverse.
	for _, tc := range []struct{ in, first, last string }{
		{"Ada Lovelace", "Ada", "Lovelace"},
		{"Ada Byron King", "Ada Byron", "King"},
		{"Cher", "Cher", ""},
		{"  Ada   Lovelace  ", "Ada", "Lovelace"},
		{"", "", ""},
	} {
		f, l := splitFullName(tc.in)
		if f != tc.first || l != tc.last {
			t.Errorf("splitFullName(%q) = (%q,%q), want (%q,%q)", tc.in, f, l, tc.first, tc.last)
		}
	}
}

// TestValidateFieldMap_RejectsAtSaveTime pins that a mapping which can never work
// fails in front of the admin who wrote it, rather than quarantining every lead at
// 3am with nobody watching.
func TestValidateFieldMap_RejectsAtSaveTime(t *testing.T) {
	allow := buildTestAllowlist(t)

	t.Run("a valid map passes", func(t *testing.T) {
		if p := ValidateFieldMap(FieldMap{"Work Email": {TargetKey: "email"}}, allow); len(p) != 0 {
			t.Errorf("expected no problems; got %+v", p)
		}
	})

	t.Run("ownership cannot be a mapping target", func(t *testing.T) {
		// The same reason the allowlist blacklists it: a payload must never choose
		// or strip a record's owner.
		p := ValidateFieldMap(FieldMap{"Rep": {TargetKey: "owner_user_id"}}, allow)
		if p["Rep"] == "" {
			t.Error("owner_user_id must be rejected as a mapping target")
		}
	})

	t.Run("an unknown field is rejected", func(t *testing.T) {
		p := ValidateFieldMap(FieldMap{"X": {TargetKey: "not_a_field"}}, allow)
		if p["X"] == "" {
			t.Error("a target that does not exist must be rejected at save time")
		}
	})

	t.Run("an unknown transform is rejected", func(t *testing.T) {
		p := ValidateFieldMap(FieldMap{"X": {TargetKey: "email", Transform: "sudo"}}, allow)
		if p["X"] == "" {
			t.Error("an unknown transform must be rejected")
		}
	})
}
