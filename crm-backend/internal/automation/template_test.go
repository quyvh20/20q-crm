package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func testContext() EvalContext {
	return EvalContext{
		Contact: map[string]any{
			"email":      "john@example.com",
			"first_name": "John",
			"last_name":  "Doe",
			"phone":      "+1234567890",
			"tags":       []any{"vip", "enterprise"},
			"owner_id":   "usr_123",
			"custom_fields": map[string]any{
				"industry": "tech",
				"score":    float64(85),
			},
		},
		Deal: map[string]any{
			"title":  "Big Deal",
			"stage":  "qualified",
			"amount": int64(50000),
			"value":  float64(49999.99),
		},
		Trigger: map[string]any{
			"from_stage": "prospecting",
			"to_stage":   "qualified",
		},
		Org: map[string]any{
			"name": "Acme Corp",
		},
		User: map[string]any{
			"email": "admin@acme.com",
		},
		Actions: map[string]any{
			"a1": map[string]any{
				"message_id": "msg_456",
				"status":     "sent",
			},
		},
	}
}

func TestInterpolateTemplate_Simple(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Hello {{contact.first_name}}", ctx)
	assert.Equal(t, "Hello John", result)
}

func TestInterpolateTemplate_MultipleTokens(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Hi {{contact.first_name}} {{contact.last_name}}, welcome!", ctx)
	assert.Equal(t, "Hi John Doe, welcome!", result)
}

func TestInterpolateTemplate_NestedPath(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Industry: {{contact.custom_fields.industry}}", ctx)
	assert.Equal(t, "Industry: tech", result)
}

func TestInterpolateTemplate_DeepNestedPath(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Score: {{contact.custom_fields.score}}", ctx)
	assert.Equal(t, "Score: 85", result)
}

func TestInterpolateTemplate_MissingPath(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Addr: {{contact.address}}", ctx)
	assert.Equal(t, "Addr: ", result)
}

func TestInterpolateTemplate_MissingRoot(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Val: {{unknown.field}}", ctx)
	assert.Equal(t, "Val: ", result)
}

func TestInterpolateTemplate_ArrayIndex(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Tag: {{contact.tags.0}}", ctx)
	assert.Equal(t, "Tag: vip", result)
}

func TestInterpolateTemplate_ArrayIndexOutOfBounds(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Tag: {{contact.tags.5}}", ctx)
	assert.Equal(t, "Tag: ", result)
}

func TestInterpolateTemplate_EscapedBraces(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate(`Show \{\{literal\}\} here`, ctx)
	assert.Equal(t, "Show {{literal}} here", result)
}

func TestInterpolateTemplate_DealAmount(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Amount: {{deal.amount}}", ctx)
	assert.Equal(t, "Amount: 50000", result)
}

func TestInterpolateTemplate_DealFloat(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Value: {{deal.value}}", ctx)
	assert.Equal(t, "Value: 49999.99", result)
}

func TestInterpolateTemplate_TriggerFields(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Moved from {{trigger.from_stage}} to {{trigger.to_stage}}", ctx)
	assert.Equal(t, "Moved from prospecting to qualified", result)
}

func TestInterpolateTemplate_ActionOutput(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Email ID: {{actions.a1.message_id}}", ctx)
	assert.Equal(t, "Email ID: msg_456", result)
}

func TestInterpolateTemplate_WhitespaceInBraces(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Hi {{ contact.first_name }}", ctx)
	assert.Equal(t, "Hi John", result)
}

func TestInterpolateTemplate_EmptyTemplate(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("", ctx)
	assert.Equal(t, "", result)
}

func TestInterpolateTemplate_NoTemplates(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Just plain text", ctx)
	assert.Equal(t, "Just plain text", result)
}

func TestInterpolateTemplate_NilContext(t *testing.T) {
	ctx := EvalContext{}
	result := InterpolateTemplate("{{contact.email}}", ctx)
	assert.Equal(t, "", result)
}

func TestInterpolateTemplate_OrgField(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate("Org: {{org.name}}", ctx)
	assert.Equal(t, "Org: Acme Corp", result)
}

func TestInterpolateTemplate_BoolValue(t *testing.T) {
	ctx := EvalContext{
		Contact: map[string]any{"active": true},
	}
	result := InterpolateTemplate("Active: {{contact.active}}", ctx)
	assert.Equal(t, "Active: true", result)
}

func TestInterpolateTemplate_MixedEscapedAndReal(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplate(`\{\{raw\}\} and {{contact.first_name}}`, ctx)
	assert.Equal(t, "{{raw}} and John", result)
}

func TestInterpolateTemplateHTML_EscapesMergedValues(t *testing.T) {
	ctx := EvalContext{
		Contact: map[string]any{"first_name": `<img src=x onerror="alert(1)">`},
	}
	result := InterpolateTemplateHTML("<p>Hi {{contact.first_name}}</p>", ctx)
	assert.Equal(t, "<p>Hi &lt;img src=x onerror=&#34;alert(1)&#34;&gt;</p>", result)
}

func TestInterpolateTemplateHTML_TemplateMarkupPreserved(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplateHTML("<h1>Hello {{contact.first_name}}</h1><br/>", ctx)
	assert.Equal(t, "<h1>Hello John</h1><br/>", result, "the template's own markup is authored content and must pass through verbatim")
}

func TestInterpolateTemplateHTML_EscapesAllSpecialChars(t *testing.T) {
	ctx := EvalContext{
		Deal: map[string]any{"title": `Tom & Jerry's <"Big"> Deal`},
	}
	result := InterpolateTemplateHTML("Deal: {{deal.title}}", ctx)
	assert.Equal(t, "Deal: Tom &amp; Jerry&#39;s &lt;&#34;Big&#34;&gt; Deal", result)
}

func TestInterpolateTemplateHTML_EscapedBracesStayLiteral(t *testing.T) {
	ctx := EvalContext{
		Contact: map[string]any{"first_name": "<b>Eve</b>"},
	}
	result := InterpolateTemplateHTML(`Use \{\{merge_tag\}\} like {{contact.first_name}} does`, ctx)
	assert.Equal(t, "Use {{merge_tag}} like &lt;b&gt;Eve&lt;/b&gt; does", result)
}

func TestInterpolateTemplateHTML_MissingPathStillEmpty(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplateHTML("<p>{{contact.nonexistent}}</p>", ctx)
	assert.Equal(t, "<p></p>", result)
}

func TestInterpolateTemplateHTML_NonStringValues(t *testing.T) {
	ctx := testContext()
	result := InterpolateTemplateHTML("Amount: {{deal.amount}}, Score: {{contact.custom_fields.score}}", ctx)
	assert.Equal(t, "Amount: 50000, Score: 85", result, "numeric values have nothing to escape and format as before")
}

func TestInterpolateTemplate_PlainVariantDoesNotEscape(t *testing.T) {
	ctx := EvalContext{
		Contact: map[string]any{"first_name": "<b>Eve</b> & co"},
	}
	result := InterpolateTemplate("Hi {{contact.first_name}}", ctx)
	assert.Equal(t, "Hi <b>Eve</b> & co", result, "non-HTML consumers (webhooks, conditions, addresses, subjects) must receive raw values")
}

// ── M6: {{path|fallback}} grammar ────────────────────────────────────────────

func TestInterpolateTemplate_Fallback(t *testing.T) {
	ctx := testContext()
	// Present value wins over the fallback.
	assert.Equal(t, "John", InterpolateTemplate("{{contact.first_name|there}}", ctx))
	// Unresolved path → fallback.
	assert.Equal(t, "there", InterpolateTemplate("Hi {{contact.nope|there}}", ctx)[3:])
	// Present-but-empty value → fallback.
	empty := testContext()
	empty.Contact["first_name"] = ""
	assert.Equal(t, "friend", InterpolateTemplate("{{contact.first_name|friend}}", empty))
	// No fallback + unresolved → empty (pre-M6 behavior, byte-for-byte).
	assert.Equal(t, "", InterpolateTemplate("{{contact.nope}}", ctx))
	// Whitespace around the pipe is trimmed.
	assert.Equal(t, "there", InterpolateTemplate("{{ contact.nope | there }}", ctx))
}

func TestInterpolateTemplateHTML_FallbackEscaped(t *testing.T) {
	ctx := testContext()
	// A fallback with HTML-special chars is escaped (same treatment as a merged value).
	assert.Equal(t, "Jane &amp; Co", InterpolateTemplateHTML("{{contact.nope|Jane & Co}}", ctx))
	// A resolved value is still escaped.
	ctx.Contact["first_name"] = "<b>x</b>"
	assert.Equal(t, "&lt;b&gt;x&lt;/b&gt;", InterpolateTemplateHTML("{{contact.first_name|f}}", ctx))
}

func TestExtractMergeTags(t *testing.T) {
	got := ExtractMergeTags("Hi {{contact.first_name|there}}, {{contact.email}} and {{a|}}")
	if len(got) != 3 {
		t.Fatalf("expected 3 tags, got %d: %+v", len(got), got)
	}
	if got[0].Path != "contact.first_name" || got[0].Fallback != "there" || !got[0].HasFallback {
		t.Errorf("tag0 wrong: %+v", got[0])
	}
	if got[1].Path != "contact.email" || got[1].HasFallback {
		t.Errorf("tag1 should have no fallback: %+v", got[1])
	}
	if got[2].Path != "a" || got[2].HasFallback {
		t.Errorf("tag2 empty fallback must be HasFallback=false: %+v", got[2])
	}
}
