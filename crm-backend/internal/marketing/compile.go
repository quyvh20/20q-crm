package marketing

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	mjml "github.com/Boostport/mjml-go"
	"github.com/microcosm-cc/bluemonday"
)

// mergeTokenRe matches a {{...}} merge token (no braces inside). Used to protect
// tokens from compile-time escaping/sanitization so they reach send verbatim and
// are escaped/resolved exactly once, at send.
var mergeTokenRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// blockHTMLPolicy is the allowlist for author block HTML. It permits basic inline
// + block formatting and safe links only — NO mj-* tags, no <script>/<style>, no
// structural tags — so author content can never restructure the MJML document
// (e.g. inject </mj-body> to strip the compliance footer).
var blockHTMLPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "b", "em", "i", "u", "s", "span", "ul", "ol", "li", "h1", "h2", "h3", "blockquote")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	return p
}()

// rawHTMLPolicy is the WIDE allowlist for author HTML blocks (Klaviyo-style
// custom snippets): layout tables, inline styles and presentational attributes
// survive, but scripts, event handlers, forms/iframes and — crucially — any
// mj-* tag are stripped, so a raw block still cannot restructure the MJML doc
// or escape its mj-raw wrapper.
var rawHTMLPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements(
		"table", "thead", "tbody", "tfoot", "tr", "td", "th",
		"div", "span", "p", "br", "hr", "center", "font",
		"strong", "b", "em", "i", "u", "s", "small", "sup", "sub",
		"h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li", "blockquote",
	)
	p.AllowAttrs("style", "width", "height", "align", "valign", "border",
		"cellpadding", "cellspacing", "bgcolor", "color", "role").Globally()
	p.AllowAttrs("href").OnElements("a")
	p.AllowAttrs("src", "alt").OnElements("img")
	p.AllowElements("a", "img")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireParseableURLs(true)
	return p
}()

// hexColorRe gates author-supplied background colors; anything else is dropped
// rather than risking an MJML attribute error at compile.
var hexColorRe = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// socialNetworks is the subset of MJML's built-in mj-social icon names we
// expose. Names outside this set are silently dropped (no icon asset exists).
var socialNetworks = map[string]bool{
	"facebook": true, "twitter": true, "instagram": true, "linkedin": true,
	"youtube": true, "pinterest": true, "github": true, "web": true,
}

// fontStacks maps the Styles panel's font keys to email-safe stacks. Keys are
// allowlisted — the value is injected into an MJML attribute, so free-form
// author input is never allowed here.
var fontStacks = map[string]string{
	"arial":     "Arial,Helvetica,sans-serif",
	"helvetica": "Helvetica,Arial,sans-serif",
	"georgia":   "Georgia,'Times New Roman',serif",
	"times":     "'Times New Roman',Times,serif",
	"verdana":   "Verdana,Geneva,sans-serif",
	"tahoma":    "Tahoma,Geneva,sans-serif",
	"trebuchet": "'Trebuchet MS',Helvetica,sans-serif",
	"courier":   "'Courier New',Courier,monospace",
}

// bgAttr renders a background-color attribute when the value is a valid hex color.
func bgAttr(bg string) string {
	if !hexColorRe.MatchString(bg) {
		return ""
	}
	return fmt.Sprintf(` background-color=%q`, bg)
}

var attrReplacer = strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;", `"`, "&quot;")
var textReplacer = strings.NewReplacer(`&`, "&amp;", `<`, "&lt;", `>`, "&gt;")

// escapeOutsideTokens applies esc to the literal spans of s but leaves {{merge}}
// tokens verbatim — so a token's fallback isn't escaped at compile and then again
// at send (which would double-escape & < >).
func escapeOutsideTokens(s string, esc func(string) string) string {
	locs := mergeTokenRe.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return esc(s)
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		b.WriteString(esc(s[last:loc[0]]))
		b.WriteString(s[loc[0]:loc[1]])
		last = loc[1]
	}
	b.WriteString(esc(s[last:]))
	return b.String()
}

// sanitizeBlockHTML strips author block HTML to the safe allowlist while preserving
// {{merge}} tokens (protected across the sanitize so their fallbacks aren't escaped
// here — send escapes resolved values once). This is the control that stops a
// privileged author from restructuring the MJML doc via raw body_json.
func sanitizeBlockHTML(s string) string {
	if s == "" {
		return ""
	}
	var toks []string
	protected := mergeTokenRe.ReplaceAllStringFunc(s, func(m string) string {
		i := len(toks)
		toks = append(toks, m)
		return fmt.Sprintf("@@MTOK%d@@", i)
	})
	clean := blockHTMLPolicy.Sanitize(protected)
	for i, m := range toks {
		clean = strings.ReplaceAll(clean, fmt.Sprintf("@@MTOK%d@@", i), m)
	}
	return clean
}

// sanitizeRawHTML is sanitizeBlockHTML's wide-policy sibling for html blocks.
// Token placeholders are URL-SHAPED (with a terminating slash so index 1 is
// never a prefix of index 10) because a {{token}} used as an href/src must
// survive the policy's RequireParseableURLs.
func sanitizeRawHTML(s string) string {
	if s == "" {
		return ""
	}
	var toks []string
	protected := mergeTokenRe.ReplaceAllStringFunc(s, func(m string) string {
		i := len(toks)
		toks = append(toks, m)
		return fmt.Sprintf("https://mtok.invalid/%d/", i)
	})
	clean := rawHTMLPolicy.Sanitize(protected)
	for i, m := range toks {
		clean = strings.ReplaceAll(clean, fmt.Sprintf("https://mtok.invalid/%d/", i), m)
	}
	return clean
}

// maxCompiledBytes guards against Gmail clipping (>102KB). We flag at 100KB.
const maxCompiledBytes = 100 * 1024

// Compiler turns a block document into email-safe, nested-table, inlined-CSS HTML
// via mjml-go (pure-Go WASM MJML — B2). Compile is once-per-save, not per recipient,
// so its ~tens-of-ms cost is off the hot path.
type Compiler struct{}

// NewCompiler builds a compiler.
func NewCompiler() *Compiler { return &Compiler{} }

// CompileResult is the output of a compile.
type CompileResult struct {
	HTML      string `json:"html"`
	PlainText string `json:"plain_text"`
	SizeBytes int    `json:"size_bytes"`
	TooLarge  bool   `json:"too_large"`
}

// Warmup pays the wazero MJML instantiation cost off the request path (call at boot).
func (c *Compiler) Warmup(ctx context.Context) error {
	_, err := mjml.ToHTML(ctx, "<mjml><mj-body></mj-body></mjml>")
	return err
}

// Compile builds the MJML for the document + preheader and renders it to HTML. The
// compiled HTML still carries {{merge}} tokens — they are resolved per recipient at
// send. A compile error is returned so the caller can BLOCK the save (never ship
// raw contenteditable HTML).
func (c *Compiler) Compile(ctx context.Context, doc BlockDocument, preheader string) (CompileResult, error) {
	src := buildMJML(doc, preheader)
	out, err := mjml.ToHTML(ctx, src, mjml.WithMinify(true))
	if err != nil {
		return CompileResult{}, fmt.Errorf("marketing: mjml compile failed: %w", err)
	}
	// Defense in depth: the compliance footer is always appended, so its unsubscribe
	// slot MUST survive to the output. If it didn't, author content restructured the
	// document — block the save rather than ship a footer-less email.
	if !strings.Contains(out, "{{unsubscribe_url") {
		return CompileResult{}, fmt.Errorf("marketing: compiled email is missing its required footer — content markup is invalid")
	}
	return CompileResult{
		HTML:      out,
		PlainText: blocksToPlainText(doc),
		SizeBytes: len(out),
		TooLarge:  len(out) > maxCompiledBytes,
	}, nil
}

// buildMJML assembles the MJML document. mj-preview emits the hidden, non-scraped
// preheader span; mj-raw carries the dark-mode color-scheme metas; a compliance
// footer (M3 render-time slots as {{tokens}}) is ALWAYS appended so a CAN-SPAM
// footer can never be omitted.
func buildMJML(doc BlockDocument, preheader string) string {
	var b strings.Builder
	b.WriteString("<mjml><mj-head>")
	if strings.TrimSpace(preheader) != "" {
		b.WriteString("<mj-preview>" + mjmlText(preheader) + "</mj-preview>")
	}
	// Dark-mode hints + a sane default font. mj-raw content lands in <head> verbatim.
	b.WriteString(`<mj-raw><meta name="color-scheme" content="light dark" /><meta name="supported-color-schemes" content="light dark" /></mj-raw>`)

	// Global styles (all allowlist/hex-gated). Headings and text inherit the
	// mj-text color so the Styles panel restyles the whole email at once.
	font := fontStacks["arial"]
	if f, ok := fontStacks[doc.FontFamily]; ok {
		font = f
	}
	textColor := "#111827"
	if hexColorRe.MatchString(doc.TextColor) {
		textColor = doc.TextColor
	}
	b.WriteString(fmt.Sprintf(`<mj-attributes><mj-all font-family=%q /><mj-text color=%q font-size="15px" line-height="1.5" /></mj-attributes>`, font, textColor))
	// Device-visibility classes (Brevo-style). 480px is MJML's own mobile
	// breakpoint, so hide/show flips exactly where the layout stacks.
	b.WriteString(`<mj-style>@media only screen and (max-width:479px){ .hide-mobile { display:none !important; } } @media only screen and (min-width:480px){ .hide-desktop { display:none !important; } }</mj-style>`)
	if hexColorRe.MatchString(doc.LinkColor) {
		// inline="inline" bakes the color onto each <a> so it survives clients
		// that strip <style>.
		b.WriteString(`<mj-style inline="inline">a { color: ` + doc.LinkColor + `; }</mj-style>`)
	}

	width := 600
	if doc.Width >= 480 && doc.Width <= 800 {
		width = doc.Width
	}
	// ALWAYS emit an explicit body background. Without one, clients (and the
	// editor preview) that honor the color-scheme metas paint their own dark
	// canvas behind our fixed #111827 text — unreadable. Explicit color keeps
	// the design's ground truth in both schemes.
	bodyBg := "#ffffff"
	if hexColorRe.MatchString(doc.BodyBg) {
		bodyBg = doc.BodyBg
	}
	b.WriteString(fmt.Sprintf(`</mj-head><mj-body background-color=%q width="%dpx">`, bodyBg, width))
	for _, blk := range doc.Blocks {
		sec := compileBlock(blk, 0)
		if sec != "" && ValidCondition(blk.Cond) {
			sec = condStartMJML(blk.Cond) + sec + condEndMJML()
		}
		b.WriteString(sec)
	}
	b.WriteString(footerMJML(doc.Footer))
	b.WriteString("</mj-body></mjml>")
	return b.String()
}

// compileBlock renders one block. depth caps column nesting at one level.
func compileBlock(blk Block, depth int) string {
	switch blk.Type {
	case BlockText, BlockHeading:
		return section(textMJML(blk, false), blk)
	case BlockButton:
		return section(buttonMJML(blk), blk)
	case BlockImage:
		return section(imageMJML(blk), blk)
	case BlockDivider:
		return section(dividerMJML(blk), blk)
	case BlockSpacer:
		h := blk.Height
		if h <= 0 {
			h = 20
		}
		return section(fmt.Sprintf(`<mj-spacer height="%dpx" />`, h), blk)
	case BlockHTML:
		// An empty html block (the builder seeds them blank) renders NOTHING —
		// not an empty padded section.
		if strings.TrimSpace(blk.Text) == "" {
			return ""
		}
		return section("<mj-raw>"+sanitizeRawHTML(blk.Text)+"</mj-raw>", blk)
	case BlockSocial:
		s := socialMJML(blk)
		if s == "" {
			return ""
		}
		return section(s, blk)
	case BlockVideo:
		return section(videoMJML(blk), blk)
	case BlockQuote:
		return section(quoteMJML(blk), blk)
	case BlockMenu:
		m := menuMJML(blk)
		if m == "" {
			return ""
		}
		return section(m, blk)
	case BlockProduct:
		return section(productMJML(blk), blk)
	case BlockColumns:
		if depth > 0 || len(blk.Columns) == 0 {
			return "" // no nested columns; drop an empty/oversized columns block
		}
		var cols strings.Builder
		cols.WriteString("<mj-section" + bgAttr(blk.Bg) + padAttr(blk.PadY) + visAttr(blk) + ">")
		for _, col := range blk.Columns {
			cols.WriteString("<mj-column>")
			for _, sub := range col {
				cols.WriteString(columnInner(sub))
			}
			cols.WriteString("</mj-column>")
		}
		cols.WriteString("</mj-section>")
		return cols.String()
	default:
		return "" // unknown block types are dropped rather than breaking the compile
	}
}

// textMJML renders text/heading with optional size/color overrides. Headings
// deliberately have NO hardcoded color: they inherit the global mj-text color
// (Styles panel) unless the block sets its own.
func textMJML(blk Block, nested bool) string {
	a := fmt.Sprintf(` align=%q`, alignOf(blk.Align))
	if blk.Type == BlockHeading {
		size := "24px"
		switch blk.Level {
		case 2:
			size = "20px"
		case 3:
			size = "17px"
		}
		if nested {
			size = "18px"
		}
		if blk.Size >= 10 && blk.Size <= 60 {
			size = fmt.Sprintf("%dpx", blk.Size)
		}
		a += fmt.Sprintf(` font-size=%q font-weight="bold"`, size)
	} else if blk.Size >= 10 && blk.Size <= 60 {
		a += fmt.Sprintf(` font-size="%dpx"`, blk.Size)
	}
	if hexColorRe.MatchString(blk.Color) {
		a += fmt.Sprintf(` color=%q`, blk.Color)
	}
	return "<mj-text" + a + ">" + sanitizeBlockHTML(blk.Text) + "</mj-text>"
}

// buttonMJML renders a styled button. Align defaults to CENTER (mj-button's
// own default) rather than alignOf's left.
func buttonMJML(blk Block) string {
	href := blk.Href
	if href == "" {
		href = "#"
	}
	align := blk.Align
	if align != "left" && align != "right" {
		align = "center"
	}
	a := fmt.Sprintf(` href=%q align=%q`, attr(href), align)
	if hexColorRe.MatchString(blk.BtnBg) {
		a += fmt.Sprintf(` background-color=%q`, blk.BtnBg)
	}
	if hexColorRe.MatchString(blk.BtnColor) {
		a += fmt.Sprintf(` color=%q`, blk.BtnColor)
	}
	if blk.Radius > 0 && blk.Radius <= 40 {
		a += fmt.Sprintf(` border-radius="%dpx"`, blk.Radius)
	}
	if blk.Size >= 10 && blk.Size <= 40 {
		a += fmt.Sprintf(` font-size="%dpx"`, blk.Size)
	}
	return "<mj-button" + a + ">" + mjmlText(blk.Label) + "</mj-button>"
}

// imageMJML renders an image, optionally wrapped in a link. Shared by the root
// and in-column paths so a linked image can't silently lose its href depending
// on where the author placed it. Align is only emitted when explicitly set —
// mj-image's own default is center.
func imageMJML(blk Block) string {
	img := fmt.Sprintf(`<mj-image src=%q alt=%q`, attr(blk.Src), attr(blk.Alt))
	if blk.Href != "" {
		img += fmt.Sprintf(` href=%q`, attr(blk.Href))
	}
	if blk.WidthPx >= 40 && blk.WidthPx <= 800 {
		img += fmt.Sprintf(` width="%dpx"`, blk.WidthPx)
	}
	if blk.Radius > 0 && blk.Radius <= 100 {
		img += fmt.Sprintf(` border-radius="%dpx"`, blk.Radius)
	}
	if blk.Align == "left" || blk.Align == "center" || blk.Align == "right" {
		img += fmt.Sprintf(` align=%q`, blk.Align)
	}
	return img + ` />`
}

func dividerMJML(blk Block) string {
	color := "#e5e7eb"
	if hexColorRe.MatchString(blk.Color) {
		color = blk.Color
	}
	w := 1
	if blk.Thickness >= 1 && blk.Thickness <= 10 {
		w = blk.Thickness
	}
	return fmt.Sprintf(`<mj-divider border-width="%dpx" border-color=%q />`, w, color)
}

// socialMJML renders mj-social with MJML's built-in icons. Unknown networks and
// empty hrefs are skipped; an all-empty block renders nothing.
func socialMJML(blk Block) string {
	var els strings.Builder
	for _, s := range blk.Social {
		if !socialNetworks[s.Network] || strings.TrimSpace(s.Href) == "" {
			continue
		}
		els.WriteString(fmt.Sprintf(`<mj-social-element name=%q href=%q />`, s.Network, attr(s.Href)))
	}
	if els.Len() == 0 {
		return ""
	}
	size := 24
	if blk.Size >= 12 && blk.Size <= 48 {
		size = blk.Size
	}
	align := blk.Align
	if align != "left" && align != "right" {
		align = "center"
	}
	return fmt.Sprintf(`<mj-social align=%q icon-size="%dpx" mode="horizontal">%s</mj-social>`, align, size, els.String())
}

// videoMJML is the email-safe video pattern: linked thumbnail + watch button
// (real <video> is dead in almost every client).
func videoMJML(blk Block) string {
	return imageMJML(blk) + videoButtonMJML(blk)
}

func videoButtonMJML(blk Block) string {
	label := strings.TrimSpace(blk.Label)
	if label == "" {
		label = "▶ Watch video"
	}
	href := blk.Href
	if href == "" {
		href = "#"
	}
	return fmt.Sprintf(`<mj-button href=%q>%s</mj-button>`, attr(href), mjmlText(label))
}

// quoteMJML renders a testimonial: accent-bordered italic body + attribution.
// The wrapper styles are compiler-authored (added after sanitize).
func quoteMJML(blk Block) string {
	accent := "#d1d5db"
	if hexColorRe.MatchString(blk.Color) {
		accent = blk.Color
	}
	inner := `<blockquote style="border-left:3px solid ` + accent + `;margin:0;padding:4px 0 4px 16px;font-style:italic">` +
		sanitizeBlockHTML(blk.Text) + `</blockquote>`
	if strings.TrimSpace(blk.Label) != "" {
		inner += `<p style="margin:8px 0 0;color:#6b7280;font-size:13px">— ` + mjmlText(blk.Label) + `</p>`
	}
	return fmt.Sprintf(`<mj-text align=%q>%s</mj-text>`, alignOf(blk.Align), inner)
}

// menuMJML renders a horizontal nav of links. Link color comes from the global
// Styles a{} rule (mj-style inline).
func menuMJML(blk Block) string {
	var parts []string
	for _, it := range blk.Items {
		if strings.TrimSpace(it.Label) == "" || strings.TrimSpace(it.Href) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf(`<a href=%q style="text-decoration:none;font-weight:500">%s</a>`, attr(it.Href), mjmlText(it.Label)))
	}
	if len(parts) == 0 {
		return ""
	}
	return `<mj-text align="center">` + strings.Join(parts, `&nbsp;&nbsp;&#8226;&nbsp;&nbsp;`) + `</mj-text>`
}

// productMJML renders the product card: optional image, bold title, sanitized
// description, bold price, and a buy/view button. Every text part is
// token-aware — merge tags in title/price/description resolve at send.
func productMJML(blk Block) string {
	var b strings.Builder
	if strings.TrimSpace(blk.Src) != "" {
		b.WriteString(imageMJML(blk))
	}
	align := alignOf(blk.Align)
	if strings.TrimSpace(blk.Title) != "" {
		titleColor := ""
		if hexColorRe.MatchString(blk.Color) {
			titleColor = fmt.Sprintf(` color=%q`, blk.Color)
		}
		b.WriteString(fmt.Sprintf(`<mj-text align=%q font-size="18px" font-weight="bold"%s padding-bottom="0">%s</mj-text>`, align, titleColor, mjmlText(blk.Title)))
	}
	if strings.TrimSpace(blk.Text) != "" {
		b.WriteString(fmt.Sprintf(`<mj-text align=%q padding-top="6px" padding-bottom="0">%s</mj-text>`, align, sanitizeBlockHTML(blk.Text)))
	}
	if strings.TrimSpace(blk.Price) != "" {
		b.WriteString(fmt.Sprintf(`<mj-text align=%q font-size="16px" font-weight="bold" padding-top="6px">%s</mj-text>`, align, mjmlText(blk.Price)))
	}
	if strings.TrimSpace(blk.Href) != "" || strings.TrimSpace(blk.Label) != "" {
		btn := blk
		if strings.TrimSpace(btn.Label) == "" {
			btn.Label = "View product"
		}
		b.WriteString(buttonMJML(btn))
	}
	return b.String()
}

// columnInner renders a sub-block WITHOUT its own section wrapper (it is already
// inside a column). Spacers and nested columns are refused (dropped). Device
// visibility rides on each component's css-class (mj-raw content gets a div).
func columnInner(blk Block) string {
	switch blk.Type {
	case BlockText, BlockHeading:
		return withVisClass(blk, textMJML(blk, true))
	case BlockButton:
		return withVisClass(blk, buttonMJML(blk))
	case BlockImage:
		return withVisClass(blk, imageMJML(blk))
	case BlockDivider:
		return withVisClass(blk, dividerMJML(blk))
	case BlockHTML:
		if strings.TrimSpace(blk.Text) == "" {
			return ""
		}
		return "<mj-raw>" + visWrapRaw(blk, sanitizeRawHTML(blk.Text)) + "</mj-raw>"
	case BlockSocial:
		return withVisClass(blk, socialMJML(blk))
	case BlockVideo:
		return withVisClass(blk, imageMJML(blk)) + withVisClass(blk, videoButtonMJML(blk))
	case BlockQuote:
		return withVisClass(blk, quoteMJML(blk))
	case BlockMenu:
		return withVisClass(blk, menuMJML(blk))
	case BlockProduct:
		// The card is multiple components; visibility wraps each (same approach
		// as video's image+button pair, generalized via the marker-free path:
		// inject the class into every top-level component tag).
		return visWrapProduct(blk)
	default:
		return ""
	}
}

// visWrapProduct applies the device-visibility class to each component of an
// in-column product card.
func visWrapProduct(blk Block) string {
	if blk.HideMobile == blk.HideDesktop {
		return productMJML(blk)
	}
	var out strings.Builder
	if strings.TrimSpace(blk.Src) != "" {
		out.WriteString(withVisClass(blk, imageMJML(blk)))
	}
	rest := blk
	rest.Src = "" // already emitted
	body := productMJML(rest)
	// productMJML emits sibling mj-* components; tag each one.
	for _, part := range splitTopLevelMJML(body) {
		out.WriteString(withVisClass(blk, part))
	}
	return out.String()
}

// splitTopLevelMJML splits concatenated sibling mj-* components — the shapes
// our builders emit: paired (<mj-text …>…</mj-text>) or self-closing
// (<mj-image … />). Components of the same name never nest at this level.
func splitTopLevelMJML(s string) []string {
	var parts []string
	for strings.HasPrefix(s, "<mj-") {
		gt := strings.Index(s, ">")
		if gt < 0 {
			break
		}
		if s[gt-1] == '/' { // self-closing
			parts = append(parts, s[:gt+1])
			s = s[gt+1:]
			continue
		}
		name := s[1:gt]
		if sp := strings.IndexAny(name, " \t"); sp >= 0 {
			name = name[:sp]
		}
		closeTag := "</" + name + ">"
		i := strings.Index(s, closeTag)
		if i < 0 {
			break
		}
		parts = append(parts, s[:i+len(closeTag)])
		s = s[i+len(closeTag):]
	}
	if s != "" {
		parts = append(parts, s)
	}
	return parts
}

// withVisClass injects the device-visibility css-class into a component's
// opening tag. No-op when the block has no visibility restriction.
func withVisClass(blk Block, mjml string) string {
	if blk.HideMobile == blk.HideDesktop || mjml == "" {
		return mjml
	}
	class := "hide-desktop"
	if blk.HideMobile {
		class = "hide-mobile"
	}
	i := strings.Index(mjml, " ")
	j := strings.Index(mjml, ">")
	pos := j
	if i >= 0 && i < j {
		pos = i
	}
	if pos < 0 {
		return mjml
	}
	return mjml[:pos] + ` css-class="` + class + `"` + mjml[pos:]
}

// padAttr renders a section vertical-padding override (0 allowed for compact
// layouts; nil = MJML's 20px default).
func padAttr(padY *int) string {
	if padY == nil {
		return ""
	}
	p := *padY
	if p < 0 {
		p = 0
	}
	if p > 80 {
		p = 80
	}
	return fmt.Sprintf(` padding="%dpx 0"`, p)
}

// footerMJML is the always-present compliance footer. The org name/address and the
// unsubscribe URL are M3 render-time slots ({{tokens}} resolved at send); until M3
// wires them they render their fallbacks (empty / #), never a literal token.
// FooterStyle customizes the LOOK (bg/color) and can add author text above the
// compliance lines — the unsubscribe slot itself is never author-removable
// (the compile sentinel would block the save).
func footerMJML(fs *FooterStyle) string {
	bg := ""
	color := "#6b7280"
	custom := ""
	if fs != nil {
		if hexColorRe.MatchString(fs.Bg) {
			bg = fs.Bg
		}
		if hexColorRe.MatchString(fs.Color) {
			color = fs.Color
		}
		if strings.TrimSpace(fs.Text) != "" {
			custom = fmt.Sprintf(`<mj-text align="center" font-size="12px" color=%q>%s</mj-text>`, color, sanitizeBlockHTML(fs.Text))
		}
	}
	return `<mj-section padding-top="24px"` + bgAttr(bg) + `><mj-column>` +
		`<mj-divider border-width="1px" border-color="#e5e7eb" padding="0 0 12px 0" />` +
		custom +
		fmt.Sprintf(`<mj-text align="center" font-size="12px" color=%q>`, color) +
		`{{org.name|}}<br />{{org.postal_address|}}<br />` +
		fmt.Sprintf(`<a href="{{unsubscribe_url|#}}" style="color:%s;">Unsubscribe</a>`, color) +
		`</mj-text></mj-column></mj-section>`
}

func section(inner string, blk Block) string {
	return "<mj-section" + bgAttr(blk.Bg) + padAttr(blk.PadY) + visAttr(blk) + "><mj-column>" + inner + "</mj-column></mj-section>"
}

// visAttr renders the device-visibility css-class. Both flags together would
// hide the block everywhere — treat that authoring mistake as "no restriction".
func visAttr(blk Block) string {
	if blk.HideMobile == blk.HideDesktop {
		return ""
	}
	if blk.HideMobile {
		return ` css-class="hide-mobile"`
	}
	return ` css-class="hide-desktop"`
}

// visWrap wraps a nested component's HTML-level content for visibility. Used
// for in-column blocks: their components carry the css-class directly where
// supported; mj-raw content gets a wrapping div instead.
func visWrapRaw(blk Block, inner string) string {
	if blk.HideMobile == blk.HideDesktop {
		return inner
	}
	class := "hide-desktop"
	if blk.HideMobile {
		class = "hide-mobile"
	}
	return `<div class="` + class + `">` + inner + `</div>`
}

func alignOf(a string) string {
	switch a {
	case "center", "right", "left":
		return a
	default:
		return "left"
	}
}

// attr escapes a value for an MJML/XML attribute, token-aware: {{merge}} tokens
// pass through verbatim (escaped/resolved once, at send) so they aren't double-escaped.
func attr(s string) string { return escapeOutsideTokens(s, attrReplacer.Replace) }

// mjmlText escapes plain text destined for element content (button label, preview),
// token-aware for the same reason.
func mjmlText(s string) string { return escapeOutsideTokens(s, textReplacer.Replace) }

var tagStripRe = regexp.MustCompile(`<[^>]+>`)
var wsCollapseRe = regexp.MustCompile(`[ \t]+`)
var blankLinesRe = regexp.MustCompile(`\n{3,}`)

// blocksToPlainText derives a text/plain alternative. Basic but multipart-valid:
// strips tags from text/heading HTML, renders buttons/images/links as text, keeps
// {{merge}} tokens (resolved at send).
func blocksToPlainText(doc BlockDocument) string {
	var b strings.Builder
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for _, blk := range blocks {
			switch blk.Type {
			case BlockText, BlockHeading, BlockHTML:
				b.WriteString(stripHTML(blk.Text) + "\n\n")
			case BlockButton:
				if blk.Label != "" || blk.Href != "" {
					b.WriteString(strings.TrimSpace(blk.Label+" "+blk.Href) + "\n\n")
				}
			case BlockImage:
				if blk.Alt != "" {
					b.WriteString("[" + blk.Alt + "]\n\n")
				}
			case BlockDivider:
				b.WriteString("----------\n\n")
			case BlockQuote:
				line := stripHTML(blk.Text)
				if blk.Label != "" {
					line += " — " + blk.Label
				}
				b.WriteString(line + "\n\n")
			case BlockProduct:
				for _, part := range []string{blk.Title, stripHTML(blk.Text), blk.Price, strings.TrimSpace(blk.Label + " " + blk.Href)} {
					if strings.TrimSpace(part) != "" {
						b.WriteString(part + "\n")
					}
				}
				b.WriteString("\n")
			case BlockVideo:
				label := blk.Label
				if strings.TrimSpace(label) == "" {
					label = "Watch video"
				}
				b.WriteString(strings.TrimSpace(label+" "+blk.Href) + "\n\n")
			case BlockMenu:
				var labels []string
				for _, it := range blk.Items {
					if strings.TrimSpace(it.Label) != "" {
						labels = append(labels, it.Label)
					}
				}
				if len(labels) > 0 {
					b.WriteString(strings.Join(labels, " · ") + "\n\n")
				}
			case BlockColumns:
				for _, col := range blk.Columns {
					walk(col)
				}
			}
		}
	}
	walk(doc.Blocks)
	out := blankLinesRe.ReplaceAllString(b.String(), "\n\n")
	return strings.TrimSpace(out)
}

func stripHTML(s string) string {
	s = tagStripRe.ReplaceAllString(s, "")
	r := strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'")
	s = r.Replace(s)
	s = wsCollapseRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
