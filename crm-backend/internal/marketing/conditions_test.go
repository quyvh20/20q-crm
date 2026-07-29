package marketing

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/automation"
)

// The critical path: markers must survive the REAL mjml compile (incl. minify)
// and then evaluate per recipient through RenderForRecipient.
func TestConditions_CompileThenRenderPerRecipient(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "always", Type: BlockText, Text: "<p>everyone sees this</p>"},
		{ID: "vip", Type: BlockText, Text: "<p>VIP-only offer</p>",
			Cond: &BlockCondition{Field: "contact.first_name", Op: "eq", Value: "Katherine"}},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(res.HTML, "data-cond=") || !strings.Contains(res.HTML, "data-cond-end") {
		t.Fatalf("condition markers must survive mjml compile+minify")
	}

	fc := FooterContext{OrgName: "Acme", PostalAddress: "1 Main St", UnsubURL: "https://x.example.com/u"}

	match := automation.EvalContext{Contact: map[string]any{"first_name": "Katherine"}}
	got := RenderForRecipient(res.HTML, match, fc)
	if !strings.Contains(got, "VIP-only offer") || !strings.Contains(got, "everyone sees this") {
		t.Fatalf("matching recipient must see the conditional block")
	}
	if strings.Contains(got, "data-cond") {
		t.Fatalf("markers must never reach a recipient (match path)")
	}

	miss := automation.EvalContext{Contact: map[string]any{"first_name": "Grace"}}
	got = RenderForRecipient(res.HTML, miss, fc)
	if strings.Contains(got, "VIP-only offer") {
		t.Fatalf("non-matching recipient must NOT see the conditional block")
	}
	if !strings.Contains(got, "everyone sees this") || !strings.Contains(got, "Unsubscribe") {
		t.Fatalf("unconditional content and the footer must survive a dropped block")
	}
	if strings.Contains(got, "data-cond") {
		t.Fatalf("markers must never reach a recipient (drop path)")
	}
}

func TestConditions_Operators(t *testing.T) {
	ec := automation.EvalContext{Contact: map[string]any{"first_name": "Katherine", "phone": ""}}
	cases := []struct {
		cond BlockCondition
		want bool
	}{
		{BlockCondition{Field: "contact.first_name", Op: "exists"}, true},
		{BlockCondition{Field: "contact.phone", Op: "exists"}, false},
		{BlockCondition{Field: "contact.phone", Op: "not_exists"}, true},
		{BlockCondition{Field: "contact.first_name", Op: "eq", Value: "katherine"}, true}, // case-insensitive
		{BlockCondition{Field: "contact.first_name", Op: "neq", Value: "Grace"}, true},
		{BlockCondition{Field: "contact.first_name", Op: "contains", Value: "ather"}, true},
		{BlockCondition{Field: "contact.first_name", Op: "contains", Value: "zzz"}, false},
		{BlockCondition{Field: "contact.missing_field", Op: "exists"}, false},
	}
	for i, tc := range cases {
		if got := evalCondition(&tc.cond, ec); got != tc.want {
			t.Fatalf("case %d (%+v): got %v want %v", i, tc.cond, got, tc.want)
		}
	}
}

func TestConditions_StripKeepsContent(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "vip", Type: BlockText, Text: "<p>maybe hidden</p>",
			Cond: &BlockCondition{Field: "contact.first_name", Op: "exists"}},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := StripConditionMarkers(res.HTML)
	if !strings.Contains(got, "maybe hidden") {
		t.Fatalf("strip must keep the content (sample preview / test-send)")
	}
	if strings.Contains(got, "data-cond") {
		t.Fatalf("strip must remove the markers")
	}
}

func TestConditions_Validation(t *testing.T) {
	doc := BlockDocument{Blocks: []Block{
		{ID: "b1", Type: BlockText, Text: "x", Cond: &BlockCondition{Field: "deal.value", Op: "exists"}}, // root not in scope
		{ID: "b2", Type: BlockText, Text: "x", Cond: &BlockCondition{Field: "contact.first_name", Op: "sounds_like", Value: "K"}}, // bad op
		{ID: "b3", Type: BlockText, Text: "x", Cond: &BlockCondition{Field: "contact.first_name", Op: "eq", Value: "K"}}, // fine
	}}
	errs := ValidateContent("", "", doc, []string{"contact", "org", "campaign"})
	var badRoot, badOp, b3Errs bool
	for _, e := range errs {
		if e.Field == "block:b1" && strings.Contains(e.Reason, "condition:") {
			badRoot = true
		}
		if e.Field == "block:b2" && strings.Contains(e.Reason, "unknown operator") {
			badOp = true
		}
		if e.Field == "block:b3" {
			b3Errs = true
		}
	}
	if !badRoot || !badOp {
		t.Fatalf("expected out-of-scope root and unknown-op errors, got %+v", errs)
	}
	if b3Errs {
		t.Fatalf("a valid condition must not error (no fallback required), got %+v", errs)
	}
}
