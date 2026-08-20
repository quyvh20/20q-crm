package marketing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"crm-backend/internal/ai"
	"crm-backend/internal/automation"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
)

// content_ai.go is the email builder's copilot: POST /api/marketing/content/ai/draft
// turns a natural-language brief into BLOCKS — the same wire shape a manual edit
// produces — so everything the AI writes stays editable block by block. It NEVER
// saves: the client inserts the blocks into the canvas as one undoable step and the
// user reviews them there (the canvas is the preview), mirroring the automation
// copilot in internal/automation/ai_draft.go.
//
// The differentiator over a stock ESP copilot is the merge-tag layer: the model is
// told the campaign's DECLARED merge scope and the real field catalog, so it writes
// {{contact.first_name|there}} against this CRM's data — and whatever it returns is
// repaired to satisfy the same mandatory-fallback rule a human author faces, so an
// AI draft can never be the thing that makes a save fail or renders blank at scale.

// contentAICaller is the narrow slice of the AI gateway the copilot needs.
// *ai.AIGateway satisfies it; a fake drives the loop in unit tests.
type contentAICaller interface {
	CompleteWithTools(ctx context.Context, orgID, userID uuid.UUID, task ai.AITask, messages []ai.Message, tools []ai.Tool) (ai.AIResponse, error)
}

// SetContentAI wires the AI gateway for the builder copilot. Called once at
// startup; until set, AIDraft returns 503.
func (h *ContentHandler) SetContentAI(g contentAICaller) { h.contentAI = g }

// aiDraftTimeout hard-bounds the whole draft. Kept in step with the client's abort
// (contentApi.ts) and well under the ~100s proxy cap, for the same reason the
// automation copilot documents: a slow model must fail as clean JSON, never as an
// HTML gateway-timeout page the client can't parse.
const aiDraftTimeout = 60 * time.Second

// maxAIDraftIterations bounds the tool loop: one emit, plus slack for a corrective
// re-emit when the model writes an unparseable tool call (the qwen3 bracket-miscount
// failure the automation copilot hit live).
const maxAIDraftIterations = 3

// The three jobs the copilot does, mirroring what the best builders expose: write a
// whole email, add one section, or rewrite the selected block.
const (
	aiModeEmail   = "email"
	aiModeSection = "section"
	aiModeRewrite = "rewrite"
)

type aiDraftRequest struct {
	Prompt string `json:"prompt"`
	Mode   string `json:"mode"`
	// MergeScope is the campaign's declared scope — it gates which merge fields the
	// copilot may use, exactly as it gates a human author's picker.
	MergeScope []string `json:"merge_scope"`
	// CurrentDoc is the working copy's blocks. Context for section/rewrite modes so
	// new content matches the email already on the canvas.
	CurrentDoc json.RawMessage `json:"current_doc,omitempty"`
	// Block is the block being rewritten (rewrite mode).
	Block json.RawMessage `json:"block,omitempty"`
	// Subject is the current subject line — context for suggesting a better one.
	Subject string `json:"subject,omitempty"`
}

// AIDraftResult is what the client applies. Blocks are always present (exactly one
// for a rewrite); Subject/Preheader only for a whole-email draft. Repairs lists what
// the server had to fix in the model's output — surfaced in the UI so the copilot's
// limits are visible rather than silent.
type AIDraftResult struct {
	Blocks    []Block  `json:"blocks"`
	Subject   string   `json:"subject,omitempty"`
	Preheader string   `json:"preheader,omitempty"`
	Repairs   []string `json:"repairs,omitempty"`
}

// AIDraft handles POST /api/marketing/content/ai/draft.
func (h *ContentHandler) AIDraft(c *gin.Context) {
	orgID, userID, ok := actorFromCtx(c)
	if !ok {
		return
	}
	if h.contentAI == nil {
		abortErr(c, http.StatusServiceUnavailable, "The email copilot is not configured on this server.")
		return
	}
	var req aiDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		abortErr(c, http.StatusBadRequest, "prompt is required")
		return
	}
	switch req.Mode {
	case aiModeEmail, aiModeSection, aiModeRewrite:
	default:
		req.Mode = aiModeSection
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), aiDraftTimeout)
	defer cancel()

	res, err := h.generateAIDraft(ctx, orgID, userID, req)
	if err != nil {
		abortErr(c, http.StatusUnprocessableEntity, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": res})
}

// generateAIDraft runs the tool loop and returns blocks that are already normalized
// and merge-tag-repaired — i.e. blocks that would pass ValidateContent and compile.
func (h *ContentHandler) generateAIDraft(ctx context.Context, orgID, userID uuid.UUID, req aiDraftRequest) (*AIDraftResult, error) {
	scope := NormalizeMergeScope(req.MergeScope)
	messages := []ai.Message{
		{Role: "system", Content: buildAISystemPrompt(req.Mode, scope)},
		{Role: "user", Content: buildAIUserPrompt(req)},
	}
	tools := aiDraftTools(req.Mode)

	for i := 0; i < maxAIDraftIterations; i++ {
		resp, err := h.contentAI.CompleteWithTools(ctx, orgID, userID, ai.TaskMarketingEmailDraft, messages, tools)
		if err != nil {
			return nil, fmt.Errorf("the email copilot is unavailable right now — please try again")
		}
		if len(resp.ToolCalls) == 0 {
			msg := strings.TrimSpace(resp.Content)
			// The model tried to tool-call but wrote JSON the gateway couldn't parse.
			// Tell it what broke and let it re-emit inside the iteration budget.
			if strings.Contains(msg, "<tool_call>") {
				messages = append(messages,
					ai.Message{Role: "assistant", Content: resp.Content},
					ai.Message{Role: "user", Content: "Your emit_email_blocks call was not parseable (the JSON had unbalanced brackets). Call emit_email_blocks again with the same content as strictly valid JSON."})
				continue
			}
			if msg == "" {
				msg = "the copilot didn't produce any content — try describing what the email should say"
			}
			return nil, fmt.Errorf("%s", msg)
		}
		messages = append(messages, ai.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			if tc.Name == "emit_email_blocks" {
				return finalizeAIDraft(tc.Params, req, scope)
			}
			messages = append(messages, ai.Message{Role: "tool", ToolUseID: tc.ID, Content: `{"error":"unknown tool — call emit_email_blocks"}`})
		}
	}
	return nil, fmt.Errorf("the copilot couldn't produce a usable draft — try describing the email in a sentence or two")
}

// aiEmitPayload is the model's tool-call shape.
type aiEmitPayload struct {
	Blocks    []Block `json:"blocks"`
	Subject   string  `json:"subject"`
	Preheader string  `json:"preheader"`
}

// finalizeAIDraft parses, normalizes and repairs the model's output. Anything that
// cannot be made safe is DROPPED rather than returned — a draft the user cannot
// save is worse than a shorter one.
func finalizeAIDraft(params json.RawMessage, req aiDraftRequest, scope []string) (*AIDraftResult, error) {
	var payload aiEmitPayload
	if err := json.Unmarshal(params, &payload); err != nil {
		return nil, fmt.Errorf("the copilot returned malformed content — please try again")
	}
	allowed := map[string]bool{}
	for _, r := range scope {
		allowed[r] = true
	}

	repairs := []string{}
	blocks := normalizeAIBlocks(payload.Blocks, allowed, &repairs, 0)
	if len(blocks) == 0 {
		return nil, fmt.Errorf("the copilot didn't produce any usable blocks — try describing the email differently")
	}
	// A rewrite replaces ONE block: keep the first and let the client preserve the
	// original's styling (the server only owns the content). The model is asked
	// for the same type but does not always comply — force it, or the client
	// would patch a heading's fields onto a button.
	if req.Mode == aiModeRewrite {
		blocks = blocks[:1]
		if orig := originalBlockType(req.Block); orig != "" && blocks[0].Type != orig {
			addRepair(&repairs, "kept the original block type")
			blocks[0].Type = orig
		}
	}
	res := &AIDraftResult{Blocks: blocks}
	if req.Mode == aiModeEmail {
		// Clamp to the stored column widths — an over-long line would turn the
		// user's next save into an opaque database error.
		res.Subject = clampRunes(repairMergeTags(strings.TrimSpace(payload.Subject), allowed, &repairs), 998)
		res.Preheader = clampRunes(repairMergeTags(strings.TrimSpace(payload.Preheader), allowed, &repairs), 255)
	}
	res.Repairs = repairs
	return res, nil
}

// originalBlockType reads the type of the block being rewritten, so the result
// can be coerced back to it.
func originalBlockType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var b Block
	if err := json.Unmarshal(raw, &b); err != nil {
		return ""
	}
	t := strings.ToLower(strings.TrimSpace(b.Type))
	if !aiBlockTypes[t] {
		return "" // not a type the copilot can express; leave what it produced
	}
	return t
}

// clampRunes truncates on a rune boundary so a clamp can never split a multi-byte
// character into invalid UTF-8.
func clampRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max]))
}

// aiBlockTypes is the set of block types the copilot may emit. Deliberately a
// subset of the palette: the copilot writes CONTENT, so blocks that only make sense
// with author-supplied assets or code (product cards, raw HTML, social, menu) are
// excluded rather than hallucinated into the document.
var aiBlockTypes = map[string]bool{
	BlockText: true, BlockHeading: true, BlockButton: true, BlockDivider: true,
	BlockSpacer: true, BlockColumns: true, BlockQuote: true, BlockImage: true,
}

// normalizeAIBlocks makes model output indistinguishable from hand-authored blocks:
// unknown types dropped, ids assigned, nested columns flattened away, numeric fields
// clamped to the compiler's ranges, colors gated to hex, merge tags repaired.
func normalizeAIBlocks(in []Block, allowed map[string]bool, repairs *[]string, depth int) []Block {
	out := make([]Block, 0, len(in))
	for _, b := range in {
		b.Type = strings.ToLower(strings.TrimSpace(b.Type))
		if !aiBlockTypes[b.Type] {
			if b.Type != "" {
				addRepair(repairs, fmt.Sprintf("dropped an unsupported %q block", b.Type))
			}
			continue
		}
		if b.Type == BlockColumns {
			// Columns nest exactly one level (compileBlock drops deeper ones), so a
			// nested columns block becomes its contents in reading order.
			if depth > 0 {
				addRepair(repairs, "flattened columns nested inside columns")
				out = append(out, normalizeAIBlocks(flattenColumns(b), allowed, repairs, depth)...)
				continue
			}
			cols := make([][]Block, 0, len(b.Columns))
			for _, col := range b.Columns {
				inner := normalizeAIBlocks(col, allowed, repairs, depth+1)
				kept := make([]Block, 0, len(inner))
				for _, sub := range inner {
					if sub.Type == BlockColumns || sub.Type == BlockSpacer {
						continue // spacers are legal but pointless inside AI columns
					}
					kept = append(kept, sub)
				}
				cols = append(cols, kept)
			}
			if len(cols) < 2 || len(cols) > 4 {
				addRepair(repairs, "dropped a columns block with an unusable column count")
				continue
			}
			// Every sub-block was dropped: an empty columns block is padding, not
			// content, and would count toward "the copilot produced something".
			empty := true
			for _, col := range cols {
				if len(col) > 0 {
					empty = false
					break
				}
			}
			if empty {
				addRepair(repairs, "dropped an empty columns block")
				continue
			}
			b.Columns = cols
			b.ColWidths = nil // the model is not trusted to keep ratios coherent
			b.KeepColumns = false
			out = append(out, normalizeAIBlock(b, allowed, repairs))
			continue
		}
		b.Columns = nil
		out = append(out, normalizeAIBlock(b, allowed, repairs))
	}
	// Assign ids AFTER filtering so numbering has no gaps. The client regenerates
	// ids on insert anyway; these keep React keys unique in between.
	for i := range out {
		out[i].ID = fmt.Sprintf("ai%d", i+1)
		for c := range out[i].Columns {
			for j := range out[i].Columns[c] {
				out[i].Columns[c][j].ID = fmt.Sprintf("ai%d_%d_%d", i+1, c+1, j+1)
			}
		}
	}
	return out
}

// flattenColumns returns a nested columns block's sub-blocks in reading order.
func flattenColumns(b Block) []Block {
	var out []Block
	for _, col := range b.Columns {
		out = append(out, col...)
	}
	return out
}

// normalizeAIBlock sanitizes one block's scalar fields.
func normalizeAIBlock(b Block, allowed map[string]bool, repairs *[]string) Block {
	// Links must be stripped from the HTML too, not just the scalar href fields:
	// blockHTMLPolicy allows <a href> through to the sent email, so a model that
	// writes a link inside a paragraph would otherwise ship an invented (at best
	// dead, at worst attacker-suggested) URL past the "no urls" contract.
	b.Text = stripAILinks(b.Text, repairs)
	b.Text = repairMergeTags(b.Text, allowed, repairs)
	b.Label = repairMergeTags(b.Label, allowed, repairs)
	b.Title = repairMergeTags(b.Title, allowed, repairs)
	b.Price = repairMergeTags(b.Price, allowed, repairs)
	b.Alt = repairMergeTags(b.Alt, allowed, repairs)

	// The copilot never writes links: a model-invented URL is a dead link, and a
	// merge tag in an href is a save-blocking trap even for human authors. The
	// author fills these in, and the builder's placeholder checks flag them.
	b.Href = sanitizeAIURL(b.Href)
	b.Src = sanitizeAIURL(b.Src)
	b.BgURL = sanitizeAIURL(b.BgURL)

	if b.Align != "left" && b.Align != "center" && b.Align != "right" {
		b.Align = ""
	}
	if b.Level < 1 || b.Level > 3 {
		b.Level = 0
	}
	b.Color = hexOrEmpty(b.Color)
	b.Bg = hexOrEmpty(b.Bg)
	b.BtnBg = hexOrEmpty(b.BtnBg)
	b.BtnColor = hexOrEmpty(b.BtnColor)
	if _, ok := fontStacks[b.Font]; !ok {
		b.Font = ""
	}
	b.Size = clampOrZero(b.Size, 10, 60)
	b.Thickness = clampOrZero(b.Thickness, 1, 10)
	b.Radius = clampOrZero(b.Radius, 0, 40)
	b.WidthPx = clampOrZero(b.WidthPx, 40, 800)
	if b.Type == BlockSpacer {
		b.Height = clampOrZero(b.Height, 4, 200)
		if b.Height == 0 {
			b.Height = 24
		}
	} else {
		b.Height = 0
	}
	// The copilot doesn't get to target or hide content: device visibility and
	// per-recipient conditions are author decisions with send-time consequences.
	b.HideMobile, b.HideDesktop, b.Cond = false, false, nil
	// Nor does it get to claim a LINK to a synced library block. The model is
	// shown the working copy (refs included) and small models mimic the input
	// shape — an echoed ref would let AI copy masquerade as a synced instance and
	// push itself into every email using that block.
	b.Ref = ""
	b.PadX, b.PadY = nil, nil
	b.Social, b.Items = nil, nil
	b.FullWidth = false
	return b
}

func clampOrZero(v, lo, hi int) int {
	if v < lo || v > hi {
		return 0
	}
	return v
}

func hexOrEmpty(s string) string {
	if hexColorRe.MatchString(s) {
		return s
	}
	return ""
}

// aiTextPolicy is compile.go's blockHTMLPolicy MINUS links. bluemonday keeps the
// text of a disallowed element, so an anchor unwraps to its words rather than
// vanishing — the sentence survives, the invented URL does not.
var aiTextPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "b", "em", "i", "u", "s", "span", "ul", "ol", "li", "h1", "h2", "h3", "blockquote")
	return p
}()

// bareURLRe matches a plain-text URL. Removing the anchor is not enough: Gmail
// and Outlook auto-linkify a bare URL, so a model-invented address left as text
// still becomes a clickable link in the inbox.
var bareURLRe = regexp.MustCompile(`https?://[^\s<>"']+`)

// stripAILinks removes anchors AND bare URLs from model HTML, protecting merge
// tokens across the sanitize exactly as sanitizeBlockHTML does (the policy would
// otherwise escape a token's fallback).
func stripAILinks(s string, repairs *[]string) string {
	if s == "" {
		return s
	}
	if !strings.Contains(strings.ToLower(s), "<a") && !bareURLRe.MatchString(s) {
		return s
	}
	var toks []string
	protected := mergeTokenRe.ReplaceAllStringFunc(s, func(m string) string {
		i := len(toks)
		toks = append(toks, m)
		return fmt.Sprintf("@@MTOK%d@@", i)
	})
	clean := aiTextPolicy.Sanitize(protected)
	// Tidy the punctuation and doubled spaces a removed URL leaves behind
	// ("Shop at  — today!" reads as a typo to the recipient).
	deLinked := bareURLRe.ReplaceAllString(clean, "")
	deLinked = strings.TrimSpace(spaceCollapseRe.ReplaceAllString(deLinked, " "))
	deLinked = danglingPunctRe.ReplaceAllString(deLinked, "$1")
	changed := deLinked != protected
	for i, m := range toks {
		deLinked = strings.ReplaceAll(deLinked, fmt.Sprintf("@@MTOK%d@@", i), m)
	}
	if changed {
		addRepair(repairs, "removed a link the copilot invented — add your own with a button")
	}
	return deLinked
}

// spaceCollapseRe/danglingPunctRe clean up after a removed URL: collapse the
// double space it leaves and drop an orphaned separator before punctuation.
var spaceCollapseRe = regexp.MustCompile(`[ \t]{2,}`)
var danglingPunctRe = regexp.MustCompile(`\s+([,.;:!?])`)

// sanitizeAIURL drops every model-supplied URL. Kept as a function (rather than
// assigning "") so the intent is greppable and a future "the author pasted this
// URL into the prompt" path has one place to relax.
func sanitizeAIURL(string) string { return "" }

// aiTagFallbacks are the default fallbacks the copilot's tags get when it omits
// one. Every value is a real word: an EMPTY fallback satisfies the validator but
// renders blank at scale, which is the exact failure the mandatory-fallback rule
// exists to prevent.
var aiTagFallbacks = map[string]string{
	"contact.first_name": "there",
	"contact.last_name":  "there",
	"contact.name":       "there",
	"company.name":       "your company",
	"company.industry":   "your industry",
	"company.website":    "our site",
	"campaign.name":      "this campaign",
}

// repairMergeTags rewrites every tag so it is in scope, is a known field, and
// carries a usable fallback — dropping any tag that cannot be made safe. It scans
// with the VALIDATOR'S OWN extractor rather than a lookalike regex: the two
// disagree on braces-in-a-fallback and on nested tags, and a tag the repair layer
// cannot see is exactly the one that blocks the save this function promises can
// never fail.
func repairMergeTags(s string, allowed map[string]bool, repairs *[]string) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	// A few passes: repairing one tag can reveal another (a nested tag degenerates
	// into a malformed fallback that the next pass then fixes).
	for pass := 0; pass < 4; pass++ {
		changed := false
		for _, ref := range automation.ExtractMergeTags(s) {
			replacement, ok := repairedToken(ref, allowed, repairs)
			if ok {
				continue
			}
			s = strings.ReplaceAll(s, ref.Raw, replacement)
			changed = true
		}
		if !changed {
			break
		}
	}
	return stripBraceDebris(s)
}

// repairedToken decides what one extracted tag becomes. ok=true means "already
// fine, leave it alone".
func repairedToken(ref automation.MergeRef, allowed map[string]bool, repairs *[]string) (string, bool) {
	path := strings.TrimSpace(ref.Path)
	if !mergeTagUsable(path, allowed) {
		addRepair(repairs, fmt.Sprintf("removed the unavailable merge field %q", path))
		return "", false
	}
	if guaranteedTags[path] {
		want := "{{" + path + "}}"
		return want, ref.Raw == want
	}
	fb := strings.TrimSpace(ref.Fallback)
	// A fallback carrying grammar characters is corrupt (it is what a nested
	// {{a|{{b|c}}}} collapses into) and would render braces to the recipient.
	if ref.HasFallback && fb != "" && !strings.ContainsAny(fb, "{}|") {
		return ref.Raw, true
	}
	def, ok := aiTagFallbacks[path]
	if !ok {
		// No sensible default and none supplied: no personalization beats a
		// blank render.
		addRepair(repairs, fmt.Sprintf("removed %q — it had no fallback text", path))
		return "", false
	}
	addRepair(repairs, fmt.Sprintf("added the fallback %q to %s", def, path))
	return "{{" + path + "|" + def + "}}", false
}

// stripBraceDebris removes brace pairs left over from a malformed or nested tag.
// Valid tags are protected first (the sanitizeBlockHTML idiom), so only orphans
// are removed — nothing should ever render {{ or }} to a recipient.
func stripBraceDebris(s string) string {
	if !strings.Contains(s, "{{") && !strings.Contains(s, "}}") {
		return s
	}
	var toks []string
	protected := s
	for _, ref := range automation.ExtractMergeTags(s) {
		i := len(toks)
		toks = append(toks, ref.Raw)
		protected = strings.Replace(protected, ref.Raw, fmt.Sprintf("@@MTOK%d@@", i), 1)
	}
	protected = strings.NewReplacer("{{", "", "}}", "").Replace(protected)
	for i, m := range toks {
		protected = strings.ReplaceAll(protected, fmt.Sprintf("@@MTOK%d@@", i), m)
	}
	return protected
}

// mergeTagUsable mirrors validateTag's scope + known-leaf rules (without the
// fallback rule, which repairMergeTags handles itself).
func mergeTagUsable(path string, allowed map[string]bool) bool {
	root, leaf, _ := strings.Cut(path, ".")
	if !allowed[root] || leaf == "" {
		return false
	}
	if strings.HasPrefix(leaf, "custom_fields.") {
		// The copilot cannot know an org's custom fields, and inventing one renders
		// blank for everyone.
		return false
	}
	set := knownLeaves[root]
	return set != nil && set[leaf]
}

// addRepair records one line per DISTINCT repair, however often it happened, and
// caps the list so a pathological draft can't flood the UI.
func addRepair(repairs *[]string, msg string) {
	for _, r := range *repairs {
		if r == msg {
			return
		}
	}
	if len(*repairs) < 8 {
		*repairs = append(*repairs, msg)
	}
}

// aiDraftTools defines the single tool the copilot calls. One tool, not three: a
// small model picks correctly far more often when there is nothing to pick.
func aiDraftTools(mode string) []ai.Tool {
	desc := "Emit the finished email content as blocks. Call this exactly once."
	switch mode {
	case aiModeSection:
		desc = "Emit the new section as blocks. Call this exactly once with ONLY the new blocks — not the whole email."
	case aiModeRewrite:
		desc = "Emit the rewritten block. Call this exactly once with a SINGLE block of the same type as the original."
	}
	props := map[string]any{
		"blocks": map[string]any{
			"type":        "array",
			"description": `ordered blocks; each is an object with a "type" and that type's fields`,
			"items":       map[string]any{"type": "object"},
		},
	}
	required := []string{"blocks"}
	if mode == aiModeEmail {
		props["subject"] = map[string]any{"type": "string", "description": "a short, specific subject line (under 60 characters)"}
		props["preheader"] = map[string]any{"type": "string", "description": "the inbox preview line that follows the subject"}
		required = append(required, "subject")
	}
	return []ai.Tool{{
		Name:   "emit_email_blocks",
		Desc:   desc,
		Params: map[string]any{"type": "object", "properties": props, "required": required},
	}}
}

// buildAISystemPrompt teaches the model the block schema and — the part a stock ESP
// copilot cannot do — the REAL merge fields this campaign is allowed to use.
func buildAISystemPrompt(mode string, scope []string) string {
	var b strings.Builder
	b.WriteString("You write marketing emails for a CRM's email builder. You return structured BLOCKS, never a raw HTML document.\n\n")

	b.WriteString("BLOCK TYPES (use only these):\n")
	b.WriteString(`- {"type":"heading","text":"<p>Headline</p>","level":1}  level 1-3, 1 is largest` + "\n")
	b.WriteString(`- {"type":"text","text":"<p>A paragraph.</p><p>Another.</p>"}  simple HTML only: p, strong, em, u, ul/ol/li` + "\n")
	b.WriteString(`- {"type":"button","label":"Shop now"}  no link — the author adds the real URL` + "\n")
	b.WriteString(`- {"type":"quote","text":"<p>A customer quote.</p>","label":"Jane Doe, Acme"}` + "\n")
	b.WriteString(`- {"type":"divider"}` + "\n")
	b.WriteString(`- {"type":"spacer","height":24}` + "\n")
	b.WriteString(`- {"type":"image","alt":"describe the picture you would use"}  no src — the author picks the image` + "\n")
	b.WriteString(`- {"type":"columns","columns":[[block,...],[block,...]]}  2-4 columns, one level deep, never columns inside columns` + "\n\n")

	b.WriteString("PERSONALIZATION — this is a real CRM, so write for the actual recipient:\n")
	b.WriteString("A merge tag is {{field|fallback}}. The fallback is REQUIRED and must be real words shown when the value is empty.\n")
	b.WriteString("The only fields available for this email:\n")
	for _, root := range scope {
		leaves := knownLeaves[root]
		if len(leaves) == 0 {
			continue
		}
		names := make([]string, 0, len(leaves))
		for leaf := range leaves {
			names = append(names, root+"."+leaf)
		}
		sortStrings(names)
		b.WriteString("  " + strings.Join(names, ", ") + "\n")
	}
	b.WriteString("Example: <p>Hi {{contact.first_name|there}}, thanks for being with {{org.name}}.</p>\n")
	b.WriteString("Never invent a field that is not listed. Personalize once or twice, not in every line.\n\n")

	b.WriteString("STYLE: short scannable paragraphs, one clear call to action, concrete specifics over marketing filler. ")
	b.WriteString("No emoji unless asked. Never write a subject line into the body, and never write an unsubscribe footer — the system always appends a compliant one.\n\n")

	switch mode {
	case aiModeSection:
		b.WriteString("TASK: produce ONE section (typically 2-4 blocks) to insert into an existing email. Match the tone of the email you are shown.\n")
	case aiModeRewrite:
		b.WriteString("TASK: rewrite the single block you are shown. Return ONE block of the SAME type, keeping its purpose but applying the requested change.\n")
	default:
		b.WriteString("TASK: produce a complete email (typically 5-9 blocks) plus a subject line and preheader.\n")
	}
	b.WriteString("Call emit_email_blocks exactly once with your result.")
	return b.String()
}

// buildAIUserPrompt frames the request with whatever context the mode needs.
func buildAIUserPrompt(req aiDraftRequest) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.Prompt))
	if req.Mode == aiModeRewrite {
		if trimmed := strings.TrimSpace(string(req.Block)); trimmed != "" && trimmed != "null" {
			b.WriteString("\n\nThe block to rewrite:\n" + truncateForPrompt(trimmed, 2000))
		}
		return b.String()
	}
	if trimmed := strings.TrimSpace(string(req.CurrentDoc)); trimmed != "" && trimmed != "null" && trimmed != "[]" {
		label := "\n\nThe email so far (match its tone and style):\n"
		if req.Mode == aiModeEmail {
			label = "\n\nThe current draft, for context — you are replacing it:\n"
		}
		b.WriteString(label + truncateForPrompt(trimmed, 6000))
	}
	if s := strings.TrimSpace(req.Subject); s != "" && req.Mode == aiModeEmail {
		b.WriteString("\n\nThe current subject line is: " + s)
	}
	return b.String()
}

// truncateForPrompt bounds context so a long email can't push the instructions out
// of the model's window.
func truncateForPrompt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}

// sortStrings is a tiny insertion sort — the field lists are a handful of entries
// and this keeps the prompt byte-stable across runs (map iteration is random, and
// an unstable prompt defeats gateway-level caching).
func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
