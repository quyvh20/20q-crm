package marketing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crm-backend/internal/ai"

	"github.com/google/uuid"
)

// fakeContentAI replays a scripted sequence of gateway responses so the copilot's
// loop, normalization and repair logic are testable without a model.
type fakeContentAI struct {
	responses []ai.AIResponse
	err       error
	calls     int
	lastMsgs  []ai.Message
}

func (f *fakeContentAI) CompleteWithTools(_ context.Context, _, _ uuid.UUID, _ ai.AITask, msgs []ai.Message, _ []ai.Tool) (ai.AIResponse, error) {
	f.calls++
	f.lastMsgs = msgs
	if f.err != nil {
		return ai.AIResponse{}, f.err
	}
	if f.calls > len(f.responses) {
		return ai.AIResponse{}, nil
	}
	return f.responses[f.calls-1], nil
}

// emit builds a gateway response carrying one emit_email_blocks tool call.
func emit(payload string) ai.AIResponse {
	return ai.AIResponse{ToolCalls: []ai.ToolCall{{ID: "t1", Name: "emit_email_blocks", Params: json.RawMessage(payload)}}}
}

func draftWith(t *testing.T, fake *fakeContentAI, req aiDraftRequest) (*AIDraftResult, error) {
	t.Helper()
	h := &ContentHandler{contentAI: fake}
	return h.generateAIDraft(context.Background(), uuid.New(), uuid.New(), req)
}

// The copilot's core promise: whatever the model emits, the blocks it hands back
// must save. This drives adversarial output through the SAME validator and compiler
// a manual save uses — if this test can fail, the copilot can hand a user a document
// that refuses to save.
func TestAIDraft_OutputAlwaysValidatesAndCompiles(t *testing.T) {
	// Every trap at once: an out-of-scope root, an unknown leaf, a custom field, a
	// fallback-less known tag, an invented URL, a bogus color, a junk block type,
	// columns nested inside columns, and out-of-range numbers.
	payload := `{
	  "subject": "Hi {{contact.first_name}}",
	  "preheader": "News from {{org.name}}",
	  "blocks": [
	    {"type":"heading","level":1,"text":"<p>Hello {{contact.first_name}}</p>","color":"red","size":900},
	    {"type":"text","text":"<p>Your deal {{deal.value|lots}} is ready, {{contact.nickname|pal}}, tier {{contact.custom_fields.tier|gold}}.</p>"},
	    {"type":"button","label":"Shop {{company.name}}","href":"https://invented.example.com/deal"},
	    {"type":"carousel","text":"<p>not a real block</p>"},
	    {"type":"image","alt":"a product photo","src":"https://invented.example.com/x.png"},
	    {"type":"columns","columns":[
	      [{"type":"text","text":"<p>left</p>"},{"type":"columns","columns":[[{"type":"text","text":"<p>deep</p>"}]]}],
	      [{"type":"text","text":"<p>right</p>"}]
	    ]}
	  ]
	}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "write it", Mode: aiModeEmail, MergeScope: []string{"contact", "org", "campaign", "company"}})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}

	doc := BlockDocument{Blocks: res.Blocks}
	if errs := ValidateContent(res.Subject, res.Preheader, doc, []string{"contact", "org", "campaign", "company"}); len(errs) != 0 {
		t.Fatalf("AI output must pass the same validation a manual save faces; got %+v", errs)
	}
	if _, err := NewCompiler().Compile(context.Background(), doc, res.Preheader); err != nil {
		t.Fatalf("AI output must compile: %v", err)
	}

	// Scan the CONTENT only — the repairs list legitimately names what it removed.
	flat, _ := json.Marshal(struct {
		Blocks    []Block `json:"blocks"`
		Subject   string  `json:"subject"`
		Preheader string  `json:"preheader"`
	}{res.Blocks, res.Subject, res.Preheader})
	body := string(flat)
	// Out-of-scope / unknowable fields are removed, not passed through.
	for _, banned := range []string{"deal.value", "contact.nickname", "custom_fields", "carousel", "invented.example.com", "red"} {
		if strings.Contains(body, banned) {
			t.Fatalf("%q must not survive normalization: %s", banned, body)
		}
	}
	// A fallback-less known tag is repaired rather than dropped.
	if !strings.Contains(body, "{{contact.first_name|there}}") {
		t.Fatalf("expected a repaired first_name tag, got %s", body)
	}
	// org.name is guaranteed, so it needs no fallback and must survive as-is.
	if !strings.Contains(res.Preheader, "{{org.name}}") {
		t.Fatalf("guaranteed tag should survive unchanged: %q", res.Preheader)
	}
	if len(res.Repairs) == 0 {
		t.Fatalf("repairs must be reported, never silent")
	}
}

func TestAIDraft_NormalizesStructure(t *testing.T) {
	payload := `{"blocks":[
	  {"type":"columns","columns":[
	    [{"type":"text","text":"<p>a</p>"},{"type":"columns","columns":[[{"type":"text","text":"<p>nested</p>"}]]}],
	    [{"type":"text","text":"<p>b</p>"}]
	  ]},
	  {"type":"spacer","height":99999},
	  {"type":"heading","level":9,"text":"<p>h</p>"}
	]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	cols := res.Blocks[0]
	if cols.Type != BlockColumns || len(cols.Columns) != 2 {
		t.Fatalf("expected a 2-column block, got %+v", cols)
	}
	for _, col := range cols.Columns {
		for _, sub := range col {
			if sub.Type == BlockColumns {
				t.Fatalf("columns must never survive inside a column")
			}
		}
	}
	// Ids are assigned and unique — React keys depend on it.
	seen := map[string]bool{}
	for _, b := range res.Blocks {
		if b.ID == "" || seen[b.ID] {
			t.Fatalf("block ids must be present and unique, got %q", b.ID)
		}
		seen[b.ID] = true
		for _, col := range b.Columns {
			for _, sub := range col {
				if sub.ID == "" || seen[sub.ID] {
					t.Fatalf("nested ids must be present and unique, got %q", sub.ID)
				}
				seen[sub.ID] = true
			}
		}
	}
	if h := res.Blocks[1].Height; h < 4 || h > 200 {
		t.Fatalf("spacer height must be clamped, got %d", h)
	}
	if lvl := res.Blocks[2].Level; lvl != 0 {
		t.Fatalf("an out-of-range heading level must be cleared, got %d", lvl)
	}
}

func TestAIDraft_ScopeGatesFields(t *testing.T) {
	// company.* is NOT in the declared scope here, so it must be stripped even
	// though it is a real field elsewhere.
	payload := `{"blocks":[{"type":"text","text":"<p>Hi from {{company.name|Acme}} to {{contact.first_name|there}}</p>"}]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: []string{"contact", "org", "campaign"}})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	got := res.Blocks[0].Text
	if strings.Contains(got, "company.name") {
		t.Fatalf("out-of-scope field must be stripped: %q", got)
	}
	if !strings.Contains(got, "{{contact.first_name|there}}") {
		t.Fatalf("in-scope field must survive: %q", got)
	}
	// The prompt must advertise only the scoped fields, or the model is being
	// invited to use something the repair layer will then delete.
	sys := fake.lastMsgs[0].Content
	if strings.Contains(sys, "company.industry") {
		t.Fatalf("system prompt must not advertise out-of-scope fields")
	}
	if !strings.Contains(sys, "contact.first_name") {
		t.Fatalf("system prompt must advertise the in-scope catalog")
	}
}

func TestAIDraft_RewriteReturnsExactlyOneBlock(t *testing.T) {
	payload := `{"blocks":[{"type":"text","text":"<p>first</p>"},{"type":"text","text":"<p>second</p>"}]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "shorter", Mode: aiModeRewrite, MergeScope: DefaultMergeScope(),
		Block: json.RawMessage(`{"type":"text","text":"<p>long original</p>"}`)})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("a rewrite must replace exactly one block, got %d", len(res.Blocks))
	}
	if res.Subject != "" {
		t.Fatalf("a rewrite must not invent a subject line")
	}
	if !strings.Contains(fake.lastMsgs[1].Content, "long original") {
		t.Fatalf("the block being rewritten must reach the model")
	}
}

// The copilot must not write links or images: an invented URL is a dead link in a
// real send, and the builder's own placeholder checks are what catch a missing one.
func TestAIDraft_NeverWritesURLs(t *testing.T) {
	payload := `{"blocks":[
	  {"type":"button","label":"Buy","href":"https://totally.real.example.com/buy"},
	  {"type":"image","alt":"hero","src":"https://cdn.example.com/hero.png","bg_url":"https://cdn.example.com/bg.png"}
	]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	for _, b := range res.Blocks {
		if b.Href != "" || b.Src != "" || b.BgURL != "" {
			t.Fatalf("copilot output must carry no URLs, got %+v", b)
		}
	}
	if res.Blocks[1].Alt != "hero" {
		t.Fatalf("alt text should survive — it is the useful part of an AI image block")
	}
}

// The scalar-field check above is not enough: blockHTMLPolicy passes <a href> to
// the sent email, so a link INSIDE the model's HTML would ship an invented URL
// past the same contract. This is the case the first version of the test missed.
func TestAIDraft_StripsLinksInsideModelHTML(t *testing.T) {
	payload := `{"blocks":[
	  {"type":"text","text":"<p>Claim it <a href=\"https://attacker.example/steal?u=x\">here</a> now.</p>"},
	  {"type":"columns","columns":[
	    [{"type":"text","text":"<p>Left <a href=\"https://invented.example/a\">link</a></p>"}],
	    [{"type":"text","text":"<p>Right</p>"}]
	  ]}
	]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	compiled, err := NewCompiler().Compile(context.Background(), BlockDocument{Blocks: res.Blocks}, "")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	// The URL must not reach the SENT email — that is the contract that matters.
	// (The compliance footer's own unsubscribe anchor is compiler-owned and
	// always present, so this checks the URLs, not the existence of any <a>.)
	for _, banned := range []string{"attacker.example", "invented.example"} {
		if strings.Contains(compiled.HTML, banned) {
			t.Fatalf("%q must not survive into the compiled email", banned)
		}
	}
	for _, b := range res.Blocks {
		if strings.Contains(strings.ToLower(b.Text), "<a ") {
			t.Fatalf("an anchor survived in a block: %q", b.Text)
		}
		for _, col := range b.Columns {
			for _, sub := range col {
				if strings.Contains(strings.ToLower(sub.Text), "<a ") {
					t.Fatalf("an anchor survived in a nested block: %q", sub.Text)
				}
			}
		}
	}
	// The sentence survives — only the link is removed.
	if !strings.Contains(compiled.HTML, "Claim it here now.") {
		t.Fatalf("the anchor's text must be kept, got: %s", compiled.HTML)
	}
	if len(res.Repairs) == 0 {
		t.Fatalf("removing a link must be reported to the user, not silent")
	}
}

// Unwrapping the anchor is not enough: Gmail and Outlook auto-linkify a bare URL,
// so a model-invented address left as plain text is still a clickable link in the
// inbox — exactly what the contract forbids.
func TestAIDraft_StripsBareURLText(t *testing.T) {
	payload := `{"blocks":[{"type":"text","text":"<p>Enjoy 50% off at https://invented.example.com/sale — hurry!</p>"}]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	got := res.Blocks[0].Text
	if strings.Contains(got, "invented.example.com") {
		t.Fatalf("a bare URL must not survive as text (clients auto-link it): %q", got)
	}
	// The sentence still reads properly — no double space, no orphaned comma.
	if strings.Contains(got, "  ") {
		t.Fatalf("removing the URL left doubled spaces: %q", got)
	}
	if !strings.Contains(got, "Enjoy 50% off at") {
		t.Fatalf("the surrounding copy must survive: %q", got)
	}
}

// The repair layer and the validator must agree about what a tag IS. These are
// the shapes where a lookalike regex disagreed with automation.ExtractMergeTags.
func TestAIDraft_MalformedTagsStillValidate(t *testing.T) {
	cases := map[string]string{
		"nested":          `<p>Hi {{contact.first_name|{{contact.nickname|pal}}}}!</p>`,
		"braced fallback": `<p>Hi {{contact.first_name|{oops}}}</p>`,
		"empty fallback":  `<p>Hi {{contact.first_name|}}</p>`,
		"piped fallback":  `<p>Hi {{contact.first_name|a|b}}</p>`,
		"unknown nested":  `<p>{{deal.value|{{contact.nickname|x}}}}</p>`,
	}
	for name, text := range cases {
		fake := &fakeContentAI{responses: []ai.AIResponse{emit(`{"blocks":[{"type":"text","text":` + jsonString(text) + `}]}`)}}
		res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
		if err != nil {
			t.Fatalf("%s: draft: %v", name, err)
		}
		doc := BlockDocument{Blocks: res.Blocks}
		if errs := ValidateContent("", "", doc, DefaultMergeScope()); len(errs) != 0 {
			t.Fatalf("%s: must still validate, got %+v (text=%q)", name, errs, res.Blocks[0].Text)
		}
		// Nothing may render raw braces to a recipient.
		if strings.Contains(res.Blocks[0].Text, "{{") != strings.Contains(res.Blocks[0].Text, "}}") {
			t.Fatalf("%s: unbalanced brace debris left behind: %q", name, res.Blocks[0].Text)
		}
		if strings.Contains(res.Blocks[0].Text, "{oops}") {
			t.Fatalf("%s: corrupt fallback text leaked: %q", name, res.Blocks[0].Text)
		}
	}
}

// jsonString quotes a Go string as a JSON string literal for the fixtures above.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// A model that echoes a ref from the document it was shown must not produce a
// block that claims to be a synced library instance — that instance would offer
// "update everywhere" and push AI copy into every email using the real block.
func TestAIDraft_NeverClaimsASyncedLink(t *testing.T) {
	payload := `{"blocks":[
	  {"type":"text","text":"<p>a</p>","ref":"9f2c0000-0000-0000-0000-000000000001"},
	  {"type":"columns","columns":[
	    [{"type":"text","text":"<p>b</p>","ref":"9f2c0000-0000-0000-0000-000000000001"}],
	    [{"type":"text","text":"<p>c</p>"}]
	  ]}
	]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	for _, b := range res.Blocks {
		if b.Ref != "" {
			t.Fatalf("AI output must never carry a synced ref, got %q", b.Ref)
		}
		for _, col := range b.Columns {
			for _, sub := range col {
				if sub.Ref != "" {
					t.Fatalf("nested AI output must never carry a synced ref, got %q", sub.Ref)
				}
			}
		}
	}
}

func TestAIDraft_ClampsSubjectAndPreheader(t *testing.T) {
	long := strings.Repeat("very long subject ", 120) // ~2160 chars
	payload := `{"subject":` + jsonString(long) + `,"preheader":` + jsonString(long) + `,"blocks":[{"type":"text","text":"<p>x</p>"}]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeEmail, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if len([]rune(res.Subject)) > 998 {
		t.Fatalf("subject must be clamped to the stored column width, got %d", len([]rune(res.Subject)))
	}
	if len([]rune(res.Preheader)) > 255 {
		t.Fatalf("preheader must be clamped, got %d", len([]rune(res.Preheader)))
	}
}

func TestAIDraft_RewriteKeepsTheOriginalBlockType(t *testing.T) {
	// The model ignores "same type" and returns a heading for a button.
	payload := `{"blocks":[{"type":"heading","level":1,"text":"<p>Shop the sale</p>"}]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "punchier", Mode: aiModeRewrite, MergeScope: DefaultMergeScope(),
		Block: json.RawMessage(`{"id":"b1","type":"button","label":"Buy now"}`)})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if res.Blocks[0].Type != BlockButton {
		t.Fatalf("a rewrite must stay the original type, got %q", res.Blocks[0].Type)
	}
}

func TestAIDraft_DropsEmptyColumns(t *testing.T) {
	// Both columns held only unsupported blocks, so the row is padding, not content.
	payload := `{"blocks":[{"type":"columns","columns":[[{"type":"video"}],[{"type":"social"}]]}]}`
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(payload)}}
	if _, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()}); err == nil {
		t.Fatalf("an all-empty columns block must not count as a usable draft")
	}
}

// The qwen3 failure the automation copilot hit live: the model writes its tool call
// as unparseable text. The loop must coach it and accept the re-emit.
func TestAIDraft_RecoversFromUnparseableToolCall(t *testing.T) {
	fake := &fakeContentAI{responses: []ai.AIResponse{
		{Content: "<tool_call>{\"name\":\"emit_email_blocks\",\"arguments\":{\"blocks\":[}}</tool_call>"},
		emit(`{"blocks":[{"type":"text","text":"<p>recovered</p>"}]}`),
	}}
	res, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()})
	if err != nil {
		t.Fatalf("draft should recover: %v", err)
	}
	if !strings.Contains(res.Blocks[0].Text, "recovered") {
		t.Fatalf("expected the re-emitted content, got %+v", res.Blocks)
	}
	if fake.calls != 2 {
		t.Fatalf("expected exactly one corrective round-trip, got %d calls", fake.calls)
	}
}

func TestAIDraft_EmptyOutputIsAnError(t *testing.T) {
	// Every block unusable ⇒ a clear error, never an empty "success".
	fake := &fakeContentAI{responses: []ai.AIResponse{emit(`{"blocks":[{"type":"video"},{"type":"html","text":"<b>x</b>"}]}`)}}
	if _, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeSection, MergeScope: DefaultMergeScope()}); err == nil {
		t.Fatalf("expected an error when nothing usable was produced")
	}
}

func TestAIDraft_ProseInsteadOfToolCallSurfacesTheMessage(t *testing.T) {
	fake := &fakeContentAI{responses: []ai.AIResponse{{Content: "I need to know what the promotion is about."}}}
	_, err := draftWith(t, fake, aiDraftRequest{Prompt: "x", Mode: aiModeEmail, MergeScope: DefaultMergeScope()})
	if err == nil || !strings.Contains(err.Error(), "promotion") {
		t.Fatalf("the model's own question should reach the user, got %v", err)
	}
}

// The system prompt is sent on every call, so it must be byte-stable — Go map
// iteration is randomized and an unstable prompt defeats gateway caching.
func TestAIDraft_SystemPromptIsStable(t *testing.T) {
	first := buildAISystemPrompt(aiModeEmail, []string{"contact", "org", "campaign", "company"})
	for i := 0; i < 20; i++ {
		if got := buildAISystemPrompt(aiModeEmail, []string{"contact", "org", "campaign", "company"}); got != first {
			t.Fatalf("system prompt is not deterministic across builds")
		}
	}
}
