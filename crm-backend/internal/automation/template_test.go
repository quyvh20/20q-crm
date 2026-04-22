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
