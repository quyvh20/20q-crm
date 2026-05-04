package automation

import (
	"testing"
)

func TestGetStringParam(t *testing.T) {
	evalCtx := EvalContext{}
	params := map[string]any{
		"to":    "hello@example.com",
		"count": 42,
	}
	got := getStringParam(params, "to", evalCtx)
	if got != "hello@example.com" {
		t.Fatalf("expected 'hello@example.com', got '%s'", got)
	}

	got = getStringParam(params, "nonexistent", evalCtx)
	if got != "" {
		t.Fatalf("expected empty string for missing key, got '%s'", got)
	}

	got = getStringParam(params, "count", evalCtx)
	if got != "42" {
		t.Fatalf("expected '42' for non-string value, got '%s'", got)
	}
}

func TestGetStringParam_WithTemplate(t *testing.T) {
	evalCtx := EvalContext{
		Contact: map[string]any{"email": "user@test.com"},
	}
	params := map[string]any{
		"to": "{{contact.email}}",
	}
	got := getStringParam(params, "to", evalCtx)
	if got != "user@test.com" {
		t.Fatalf("expected 'user@test.com', got '%s'", got)
	}
}

func TestGetIntParam(t *testing.T) {
	params := map[string]any{
		"count":  float64(42),
		"str":    "notanint",
		"float":  3.14,
		"intval": int(7),
	}
	got := getIntParam(params, "count")
	if got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}

	got = getIntParam(params, "str")
	if got != 0 {
		t.Fatalf("expected 0 for string value, got %d", got)
	}

	got = getIntParam(params, "nonexistent")
	if got != 0 {
		t.Fatalf("expected 0 for missing key, got %d", got)
	}

	got = getIntParam(params, "float")
	if got != 3 {
		t.Fatalf("expected 3 (truncated), got %d", got)
	}

	got = getIntParam(params, "intval")
	if got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

func TestGetMapParam(t *testing.T) {
	evalCtx := EvalContext{}
	inner := map[string]any{"key": "value", "num": 42}
	params := map[string]any{
		"headers": inner,
		"str":     "notamap",
	}

	got := getMapParam(params, "headers", evalCtx)
	if got == nil || got["key"] != "value" {
		t.Fatalf("expected map with key=value, got %+v", got)
	}
	if got["num"] != "42" {
		t.Fatalf("expected num='42' (formatted), got '%s'", got["num"])
	}

	got = getMapParam(params, "str", evalCtx)
	if got != nil {
		t.Fatalf("expected nil for string value, got %+v", got)
	}

	got = getMapParam(params, "nonexistent", evalCtx)
	if got != nil {
		t.Fatalf("expected nil for missing key, got %+v", got)
	}
}

func TestGetStringSliceParam(t *testing.T) {
	evalCtx := EvalContext{}
	params := map[string]any{
		"tags":  []any{"a", "b", "c"},
		"mixed": []any{"a", 42},
		"empty": []any{},
		"str":   "notaslice",
	}

	got := getStringSliceParam(params, "tags", evalCtx)
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("expected [a,b,c], got %+v", got)
	}

	got = getStringSliceParam(params, "empty", evalCtx)
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %+v", got)
	}

	// Plain string → treated as single-element comma-separated list
	got = getStringSliceParam(params, "str", evalCtx)
	if len(got) != 1 || got[0] != "notaslice" {
		t.Fatalf("expected [notaslice] for plain string, got %+v", got)
	}

	got = getStringSliceParam(params, "nonexistent", evalCtx)
	if got != nil {
		t.Fatalf("expected nil for missing key, got %+v", got)
	}

	got = getStringSliceParam(params, "mixed", evalCtx)
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("expected [a] (skipping non-strings), got %+v", got)
	}
}

func TestGetStringSliceParam_CommaSeparated(t *testing.T) {
	evalCtx := EvalContext{}

	// Multiple comma-separated emails
	params := map[string]any{
		"cc": "a@x.com, b@y.com, c@z.com",
	}
	got := getStringSliceParam(params, "cc", evalCtx)
	if len(got) != 3 || got[0] != "a@x.com" || got[1] != "b@y.com" || got[2] != "c@z.com" {
		t.Fatalf("expected 3 trimmed emails, got %+v", got)
	}

	// Empty string → nil
	params = map[string]any{"cc": ""}
	got = getStringSliceParam(params, "cc", evalCtx)
	if got != nil {
		t.Fatalf("expected nil for empty string, got %+v", got)
	}

	// Whitespace-only → nil
	params = map[string]any{"cc": "  ,  , "}
	got = getStringSliceParam(params, "cc", evalCtx)
	if got != nil {
		t.Fatalf("expected nil for whitespace-only parts, got %+v", got)
	}

	// Template inside comma-separated
	evalCtx = EvalContext{
		Contact: map[string]any{"email": "user@test.com"},
	}
	params = map[string]any{"cc": "{{contact.email}}, manager@co.com"}
	got = getStringSliceParam(params, "cc", evalCtx)
	if len(got) != 2 || got[0] != "user@test.com" || got[1] != "manager@co.com" {
		t.Fatalf("expected [user@test.com, manager@co.com], got %+v", got)
	}
}

func TestTokenBucket_Allow(t *testing.T) {
	tb := newTokenBucket()

	for i := 0; i < 100; i++ {
		if !tb.allow("token1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	if tb.allow("token1") {
		t.Fatal("101st request should be blocked")
	}

	if !tb.allow("token2") {
		t.Fatal("different token should be allowed")
	}
}

func TestTokenBucket_DifferentTokensIndependent(t *testing.T) {
	tb := newTokenBucket()

	for i := 0; i < 50; i++ {
		tb.allow("tokenA")
	}

	// tokenB should still have full quota
	for i := 0; i < 100; i++ {
		if !tb.allow("tokenB") {
			t.Fatalf("tokenB request %d should be allowed", i)
		}
	}
}
