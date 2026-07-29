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

func TestCompile_ImageHrefHonoredAtRootAndInColumns(t *testing.T) {
	// An author can place a linked image anywhere; the compiled href must not
	// depend on whether the image sits at the root or inside a column.
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "root", Type: BlockImage, Src: "https://x.example.com/a.png", Alt: "Root", Href: "https://x.example.com/root-target"},
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{
			{{ID: "nested", Type: BlockImage, Src: "https://x.example.com/b.png", Alt: "Nested", Href: "https://x.example.com/nested-target"}},
			{{ID: "plain", Type: BlockImage, Src: "https://x.example.com/c.png", Alt: "Plain"}},
		}},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	for _, want := range []string{"https://x.example.com/root-target", "https://x.example.com/nested-target"} {
		if !strings.Contains(res.HTML, want) {
			t.Fatalf("image link %q missing from compiled output", want)
		}
	}
	// An unlinked image must stay unlinked (no phantom anchor around c.png).
	if strings.Contains(res.HTML, `href="https://x.example.com/c.png"`) {
		t.Fatalf("unlinked image should not gain an href")
	}
}

func TestCompile_ImageHrefTokenNotDoubleEscaped(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{{ID: "cols", Type: BlockColumns, Columns: [][]Block{
		{{ID: "n", Type: BlockImage, Src: "https://x.example.com/a.png", Alt: "A", Href: "https://x.example.com/?u={{contact.email}}&r=1"}},
	}}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// The token passes through verbatim (resolved+escaped once at send); the
	// literal & around it is attribute-escaped as usual.
	if !strings.Contains(res.HTML, "{{contact.email}}") {
		t.Fatalf("merge token in a nested image href must survive compile verbatim")
	}
}

func TestCompile_HTMLBlockRendersSafely(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{{ID: "h", Type: BlockHTML, Text: `
		<table role="presentation" width="100%" style="background:#0f172a;color:#fff"><tr>
			<td style="padding:16px" align="center"><h2 style="margin:0">Custom section</h2>
			<a href="https://x.example.com/deal?u={{contact.email}}">Claim {{contact.first_name|friend}}</a></td>
		</tr></table>
		<script>alert(1)</script>
		<img src="https://x.example.com/pix.png" alt="px" onerror="alert(2)">
		</mj-raw><mj-section><mj-column><mj-text>escape attempt</mj-text></mj-column></mj-section>`}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile html block: %v", err)
	}
	// Layout table with inline styles survives.
	if !strings.Contains(res.HTML, "Custom section") || !strings.Contains(res.HTML, "background:#0f172a") {
		t.Fatalf("author table/styles should survive the wide policy")
	}
	// Script and event handlers stripped; the escape attempt's mj-* tags stripped.
	for _, banned := range []string{"<script", "alert(1)", "onerror", "escape attempt</mj-text>"} {
		if strings.Contains(res.HTML, banned) {
			t.Fatalf("%q must not survive sanitize", banned)
		}
	}
	// Merge tokens survive — including inside an href.
	for _, want := range []string{"{{contact.email}}", "{{contact.first_name|friend}}"} {
		if !strings.Contains(res.HTML, want) {
			t.Fatalf("token %q lost in html block", want)
		}
	}
	// The compliance footer still survives an html block.
	if !strings.Contains(res.HTML, "Unsubscribe") {
		t.Fatalf("footer missing with html block present")
	}
}

func TestCompile_HTMLBlockInsideColumn(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{{ID: "cols", Type: BlockColumns, Columns: [][]Block{
		{{ID: "raw", Type: BlockHTML, Text: `<div style="border:1px solid #eee">nested raw</div>`}},
		{{ID: "t", Type: BlockText, Text: "plain"}},
	}}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile html-in-column: %v", err)
	}
	if !strings.Contains(res.HTML, "nested raw") {
		t.Fatalf("html sub-block content missing from a column")
	}
}

func TestCompile_BackgroundColors(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{
		BodyBg: "#f1f5f9",
		Blocks: []Block{
			{ID: "t", Type: BlockText, Text: "hi", Bg: "#ffffff"},
			{ID: "cols", Type: BlockColumns, Bg: "#e2e8f0", Columns: [][]Block{{{ID: "x", Type: BlockText, Text: "col"}}}},
			{ID: "bad", Type: BlockText, Text: "bad bg", Bg: "url(javascript:alert(1))"},
		},
	}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile bg: %v", err)
	}
	for _, want := range []string{"#f1f5f9", "#ffffff", "#e2e8f0"} {
		if !strings.Contains(res.HTML, want) {
			t.Fatalf("background %q missing from compiled output", want)
		}
	}
	if strings.Contains(res.HTML, "javascript:alert") {
		t.Fatalf("non-hex background value must be dropped")
	}
}

func intp(v int) *int { return &v }

func TestCompile_NewBlockTypes(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "s", Type: BlockSocial, Social: []SocialLink{
			{Network: "facebook", Href: "https://facebook.com/acme"},
			{Network: "instagram", Href: "https://instagram.com/acme"},
			{Network: "myspace", Href: "https://myspace.com/acme"}, // unknown → dropped
			{Network: "twitter", Href: ""},                          // empty href → dropped
		}},
		{ID: "v", Type: BlockVideo, Src: "https://x.example.com/thumb.png", Alt: "Demo", Href: "https://youtube.com/watch?v=1"},
		{ID: "q", Type: BlockQuote, Text: "<p>Best CRM ever.</p>", Label: "A. Customer", Color: "#2563eb"},
		{ID: "m", Type: BlockMenu, Items: []LinkItem{
			{Label: "Shop", Href: "https://x.example.com/shop"},
			{Label: "About", Href: "https://x.example.com/about"},
			{Label: "", Href: "https://x.example.com/skipme"}, // empty label → dropped
		}},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile new blocks: %v", err)
	}
	html := res.HTML
	for _, want := range []string{
		"facebook.com/acme", "instagram.com/acme", // social hrefs
		"thumb.png", "Watch video", "youtube.com/watch", // video image + button
		"Best CRM ever.", "A. Customer", "#2563eb", // quote body + attribution + accent
		"Shop", "About", // menu labels
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("%q missing from compiled new-block output", want)
		}
	}
	if strings.Contains(html, "myspace") || strings.Contains(html, "skipme") {
		t.Fatalf("invalid social/menu entries must be dropped")
	}
	// Plaintext covers the new blocks too.
	for _, want := range []string{"Best CRM ever. — A. Customer", "Watch video", "Shop · About"} {
		if !strings.Contains(res.PlainText, want) {
			t.Fatalf("plaintext missing %q: %q", want, res.PlainText)
		}
	}
}

func TestCompile_NewBlocksInsideColumns(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{{ID: "cols", Type: BlockColumns, Columns: [][]Block{
		{{ID: "s", Type: BlockSocial, Social: []SocialLink{{Network: "youtube", Href: "https://youtube.com/@acme"}}}},
		{{ID: "q", Type: BlockQuote, Text: "nested quote"}},
	}}}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile new blocks in columns: %v", err)
	}
	if !strings.Contains(res.HTML, "youtube.com/@acme") || !strings.Contains(res.HTML, "nested quote") {
		t.Fatalf("social/quote content missing when nested in columns")
	}
}

func TestCompile_PerBlockStyling(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "b", Type: BlockButton, Label: "Go", Href: "https://x.example.com", BtnBg: "#16a34a", BtnColor: "#f0fdf4", Radius: 24, Align: "left"},
		{ID: "i", Type: BlockImage, Src: "https://x.example.com/a.png", Alt: "A", WidthPx: 240, Radius: 12},
		{ID: "d", Type: BlockDivider, Color: "#a855f7", Thickness: 3},
		{ID: "t", Type: BlockText, Text: "sized", Size: 18, Color: "#334155"},
		{ID: "p", Type: BlockSpacer, Height: 10, PadY: intp(0)},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile styled: %v", err)
	}
	for _, want := range []string{"#16a34a", "#f0fdf4", "24px", "240px", "12px", "#a855f7", "3px", "18px", "#334155"} {
		if !strings.Contains(res.HTML, want) {
			t.Fatalf("styling value %q missing from compiled output", want)
		}
	}
}

func TestCompile_GlobalStylesAndStyledFooter(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{
		Width:      680,
		FontFamily: "georgia",
		TextColor:  "#1e293b",
		LinkColor:  "#dc2626",
		Footer:     &FooterStyle{Bg: "#0f172a", Color: "#94a3b8", Text: "<p>Follow us for more.</p>"},
		Blocks:     []Block{{ID: "t", Type: BlockText, Text: `<p>Hello <a href="https://x.example.com">link</a></p>`}},
	}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile global styles: %v", err)
	}
	html := res.HTML
	for _, want := range []string{"Georgia", "#1e293b", "#dc2626", "#0f172a", "#94a3b8", "Follow us for more."} {
		if !strings.Contains(html, want) {
			t.Fatalf("global style %q missing", want)
		}
	}
	// Footer compliance slots survive styling — the sentinel stays satisfiable.
	if !strings.Contains(html, "{{unsubscribe_url") || !strings.Contains(html, "Unsubscribe") {
		t.Fatalf("styled footer lost its compliance slots")
	}
	// Bogus font keys fall back to the default stack rather than injecting.
	doc.FontFamily = `x" onload="evil`
	res2, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile fallback font: %v", err)
	}
	if !strings.Contains(res2.HTML, "Arial") || strings.Contains(res2.HTML, "evil") {
		t.Fatalf("unknown font key must fall back to Arial, never inject")
	}
}

func TestCompile_DeviceVisibility(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "m", Type: BlockText, Text: "mobile only", HideDesktop: true},
		{ID: "d", Type: BlockText, Text: "desktop only", HideMobile: true},
		{ID: "both", Type: BlockText, Text: "authored wrong", HideMobile: true, HideDesktop: true}, // no restriction
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{
			{{ID: "n", Type: BlockButton, Label: "Nested", Href: "https://x.example.com", HideMobile: true}},
		}},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile visibility: %v", err)
	}
	html := res.HTML
	for _, want := range []string{"hide-desktop", "hide-mobile", "max-width:479px", "min-width:480px"} {
		if !strings.Contains(html, want) {
			t.Fatalf("%q missing from visibility output", want)
		}
	}
	// Both-flags block must NOT be hidden anywhere.
	if strings.Contains(between(html, "authored wrong", "</div>"), "hide-") {
		t.Fatalf("both-flags block must carry no visibility class")
	}
}

func TestSniffImageType(t *testing.T) {
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...)
	if got := sniffImageType(png); got != "image/png" {
		t.Fatalf("png sniff: %q", got)
	}
	if got := sniffImageType([]byte("<script>alert(1)</script> pretending to be an image")); got != "" {
		t.Fatalf("html must not sniff as an image, got %q", got)
	}
	if got := sniffImageType([]byte("%PDF-1.7 not an image")); got != "" {
		t.Fatalf("pdf must not sniff as an image, got %q", got)
	}
}

func TestCompile_ProductBlock(t *testing.T) {
	c := NewCompiler()
	doc := BlockDocument{Blocks: []Block{
		{ID: "p", Type: BlockProduct, Src: "https://x.example.com/widget.png", Alt: "Widget",
			Title: "The Widget {{contact.first_name|}}", Text: "<p>Our <strong>best</strong> widget yet.</p>",
			Price: "$49", Label: "Buy now", Href: "https://x.example.com/buy",
			BtnBg: "#16a34a", Radius: 8},
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{
			{{ID: "np", Type: BlockProduct, Title: "Nested product", Price: "$9", Href: "https://x.example.com/n", HideMobile: true}},
			{{ID: "t", Type: BlockText, Text: "beside it"}},
		}},
	}}
	res, err := c.Compile(context.Background(), doc, "")
	if err != nil {
		t.Fatalf("compile product: %v", err)
	}
	html := res.HTML
	for _, want := range []string{
		"widget.png", "The Widget", "{{contact.first_name|}}", "best</strong> widget",
		"$49", "Buy now", "x.example.com/buy", "#16a34a",
		"Nested product", "$9", "View product", // default button label in the nested card
		"hide-mobile", // visibility class applied to in-column product parts
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("%q missing from product output", want)
		}
	}
	for _, want := range []string{"The Widget", "Our best widget yet.", "$49", "Buy now https://x.example.com/buy"} {
		if !strings.Contains(res.PlainText, want) {
			t.Fatalf("plaintext missing %q: %q", want, res.PlainText)
		}
	}
}

func TestSplitTopLevelMJML(t *testing.T) {
	in := `<mj-text align="left">a</mj-text><mj-image src="x" /><mj-button href="y">b</mj-button>`
	parts := splitTopLevelMJML(in)
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %#v", len(parts), parts)
	}
	if !strings.HasPrefix(parts[1], "<mj-image") || !strings.HasSuffix(parts[1], "/>") {
		t.Fatalf("self-closing part mis-split: %q", parts[1])
	}
}

func TestCompile_EmptyHTMLBlockRendersNothing(t *testing.T) {
	// The builder seeds html blocks EMPTY (the boilerplate is a placeholder,
	// not content) — an untouched block must not ship an empty padded section.
	c := NewCompiler()
	withEmpty, err := c.Compile(context.Background(), BlockDocument{Blocks: []Block{
		{ID: "h", Type: BlockHTML, Text: "   \n  "},
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{{{ID: "n", Type: BlockHTML, Text: ""}}, {{ID: "t", Type: BlockText, Text: "real"}}}},
	}}, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	onlyReal, err := c.Compile(context.Background(), BlockDocument{Blocks: []Block{
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{{}, {{ID: "t", Type: BlockText, Text: "real"}}}},
	}}, "")
	if err != nil {
		t.Fatalf("compile control: %v", err)
	}
	if withEmpty.HTML != onlyReal.HTML {
		t.Fatalf("empty html blocks must compile to nothing (outputs differ)")
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
