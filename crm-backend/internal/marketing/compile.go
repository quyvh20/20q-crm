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
	b.WriteString(`<mj-attributes><mj-all font-family="Arial,Helvetica,sans-serif" /><mj-text color="#111827" font-size="15px" line-height="1.5" /></mj-attributes>`)
	b.WriteString("</mj-head><mj-body>")
	for _, blk := range doc.Blocks {
		b.WriteString(compileBlock(blk, 0))
	}
	b.WriteString(footerMJML())
	b.WriteString("</mj-body></mjml>")
	return b.String()
}

// compileBlock renders one block. depth caps column nesting at one level.
func compileBlock(blk Block, depth int) string {
	switch blk.Type {
	case BlockText:
		return section(fmt.Sprintf(`<mj-text align=%q>%s</mj-text>`, alignOf(blk.Align), sanitizeBlockHTML(blk.Text)))
	case BlockHeading:
		size := "24px"
		switch blk.Level {
		case 2:
			size = "20px"
		case 3:
			size = "17px"
		}
		return section(fmt.Sprintf(`<mj-text align=%q font-size=%q font-weight="bold" color="#111827">%s</mj-text>`, alignOf(blk.Align), size, sanitizeBlockHTML(blk.Text)))
	case BlockButton:
		href := blk.Href
		if href == "" {
			href = "#"
		}
		return section(fmt.Sprintf(`<mj-button href=%q>%s</mj-button>`, attr(href), mjmlText(blk.Label)))
	case BlockImage:
		img := fmt.Sprintf(`<mj-image src=%q alt=%q`, attr(blk.Src), attr(blk.Alt))
		if blk.Href != "" {
			img += fmt.Sprintf(` href=%q`, attr(blk.Href))
		}
		img += ` />`
		return section(img)
	case BlockDivider:
		return section(`<mj-divider border-width="1px" border-color="#e5e7eb" />`)
	case BlockSpacer:
		h := blk.Height
		if h <= 0 {
			h = 20
		}
		return section(fmt.Sprintf(`<mj-spacer height="%dpx" />`, h))
	case BlockColumns:
		if depth > 0 || len(blk.Columns) == 0 {
			return "" // no nested columns; drop an empty/oversized columns block
		}
		var cols strings.Builder
		cols.WriteString("<mj-section>")
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

// columnInner renders a sub-block WITHOUT its own section wrapper (it is already
// inside a column). Only simple content is allowed inside columns.
func columnInner(blk Block) string {
	switch blk.Type {
	case BlockText:
		return fmt.Sprintf(`<mj-text align=%q>%s</mj-text>`, alignOf(blk.Align), sanitizeBlockHTML(blk.Text))
	case BlockHeading:
		return fmt.Sprintf(`<mj-text align=%q font-size="18px" font-weight="bold">%s</mj-text>`, alignOf(blk.Align), sanitizeBlockHTML(blk.Text))
	case BlockButton:
		href := blk.Href
		if href == "" {
			href = "#"
		}
		return fmt.Sprintf(`<mj-button href=%q>%s</mj-button>`, attr(href), mjmlText(blk.Label))
	case BlockImage:
		return fmt.Sprintf(`<mj-image src=%q alt=%q />`, attr(blk.Src), attr(blk.Alt))
	case BlockDivider:
		return `<mj-divider border-width="1px" border-color="#e5e7eb" />`
	default:
		return ""
	}
}

// footerMJML is the always-present compliance footer. The org name/address and the
// unsubscribe URL are M3 render-time slots ({{tokens}} resolved at send); until M3
// wires them they render their fallbacks (empty / #), never a literal token.
func footerMJML() string {
	return `<mj-section padding-top="24px"><mj-column>` +
		`<mj-divider border-width="1px" border-color="#e5e7eb" padding="0 0 12px 0" />` +
		`<mj-text align="center" font-size="12px" color="#6b7280">` +
		`{{org.name|}}<br />{{org.postal_address|}}<br />` +
		`<a href="{{unsubscribe_url|#}}" style="color:#6b7280;">Unsubscribe</a>` +
		`</mj-text></mj-column></mj-section>`
}

func section(inner string) string {
	return "<mj-section><mj-column>" + inner + "</mj-column></mj-section>"
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
			case BlockText, BlockHeading:
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
