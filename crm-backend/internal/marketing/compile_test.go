package marketing

import (
	"context"
	"strings"
	"testing"
)

func sampleDoc() BlockDocument {
	return BlockDocument{Blocks: []Block{
		{ID: "h1", Type: BlockHeading, Level: 1, Text: "Welcome {{contact.first_name|there}}"},
		{ID: "t1", Type: BlockText, Text: `<p>Thanks for joining. Visit our <a href="https://x.example.com">site</a>.</p>`},
		{ID: "b1", Type: BlockButton, Label: "Shop now", Href: "https://x.example.com/shop"},
		{ID: "img1", Type: BlockImage, Src: "https://x.example.com/logo.png", Alt: "Logo"},
		{ID: "d1", Type: BlockDivider},
		{ID: "c1", Type: BlockColumns, Columns: [][]Block{
			{{ID: "c1a", Type: BlockText, Text: "Left"}},
			{{ID: "c1b", Type: BlockText, Text: "Right"}},
		}},
	}}
}

func TestCompile_ProducesEmailSafeHTML(t *testing.T) {
	c := NewCompiler()
	res, err := c.Compile(context.Background(), sampleDoc(), "Your weekly update")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	html := res.HTML
	if !strings.Contains(html, "<table") {
		t.Fatalf("expected nested-table HTML")
	}
	// Merge tokens survive compilation, resolved per recipient at send.
	if !strings.Contains(html, "{{contact.first_name|there}}") {
		t.Fatalf("merge token did not survive compile")
	}
	// Preheader rendered (mj-preview) — the text is present in the output.
	if !strings.Contains(html, "Your weekly update") {
		t.Fatalf("preheader not rendered")
	}
	// Dark-mode hint emitted.
	if !strings.Contains(html, "color-scheme") {
		t.Fatalf("color-scheme meta missing")
	}
	// Compliance footer always present.
	if !strings.Contains(html, "Unsubscribe") {
		t.Fatalf("footer/unsubscribe missing")
	}
	// Button + image rendered.
	if !strings.Contains(html, "Shop now") || !strings.Contains(html, "logo.png") {
		t.Fatalf("button/image not rendered")
	}
	if res.SizeBytes == 0 || res.TooLarge {
		t.Fatalf("unexpected size: %d too_large=%v", res.SizeBytes, res.TooLarge)
	}
	// Plain-text alternative derived, tags stripped, merge token kept.
	if !strings.Contains(res.PlainText, "Welcome {{contact.first_name|there}}") {
		t.Fatalf("plaintext heading missing: %q", res.PlainText)
	}
	if strings.Contains(res.PlainText, "<a href") {
		t.Fatalf("plaintext still has HTML tags: %q", res.PlainText)
	}
}

func TestCompile_EmptyDocStillHasFooter(t *testing.T) {
	c := NewCompiler()
	res, err := c.Compile(context.Background(), BlockDocument{}, "")
	if err != nil {
		t.Fatalf("compile empty: %v", err)
	}
	if !strings.Contains(res.HTML, "Unsubscribe") {
		t.Fatalf("footer must be present even for an empty document")
	}
}

func TestCompile_MergedValuesEscapedAtRenderNotCompile(t *testing.T) {
	// The compiler must not resolve tokens; escaping of resolved values happens at
	// send via InterpolateTemplateHTML. So a token stays literal after compile.
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{{ID: "t", Type: BlockText, Text: "Hi {{contact.first_name|friend}}"}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !strings.Contains(res.HTML, "{{contact.first_name|friend}}") {
		t.Fatalf("token should be literal post-compile")
	}
}

// ── review-fix regressions ───────────────────────────────────────────────────

func TestCompile_SanitizesStructuralInjection(t *testing.T) {
	c := NewCompiler()
	// A privileged author bypasses the UI and injects structural MJML to strip the footer.
	doc := BlockDocument{Blocks: []Block{{ID: "x", Type: BlockText,
		Text: `Hello</mj-body><mj-section><mj-column><mj-text>evil</mj-text></mj-column></mj-section>`}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile should succeed after sanitize, got %v", err)
	}
	if !strings.Contains(res.HTML, "Unsubscribe") {
		t.Fatalf("compliance footer must survive a structural-injection attempt")
	}
	if !strings.Contains(res.HTML, "Hello") {
		t.Fatalf("benign text content should be preserved")
	}
}

func TestCompile_ButtonLabelTokenEscaping(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{{ID: "b", Type: BlockButton, Label: "Hi {{contact.first_name|A & B}}", Href: "https://x.example.com"}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// The & inside the token's fallback must stay RAW at compile so send escapes it
	// exactly once (no double-escape). The literal text around it is escaped normally.
	if !strings.Contains(res.HTML, "{{contact.first_name|A & B}}") {
		t.Fatalf("token fallback must be verbatim at compile; got %s", between(res.HTML, "Hi ", "}}"))
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return "(not found: " + a + ")"
	}
	j := strings.Index(s[i:], b)
	if j < 0 {
		return "(no close)"
	}
	return s[i : i+j+len(b)]
}
