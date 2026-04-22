package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func condTestContext() EvalContext {
	return EvalContext{
		Contact: map[string]any{
			"email":      "john@example.com",
			"first_name": "John",
			"last_name":  "Doe",
			"tags":       []any{"vip", "enterprise", "tech"},
			"age":        float64(30),
			"company":    "Acme",
			"phone":      "",
			"custom_fields": map[string]any{
				"industry": "technology",
			},
		},
		Deal: map[string]any{
			"stage":  "qualified",
			"amount": float64(50000),
			"title":  "Enterprise Deal",
		},
		Trigger: map[string]any{
			"from_stage": "prospecting",
			"to_stage":   "qualified",
		},
	}
}

// --- Operator tests ---

func TestCondition_Eq(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.first_name", "eq", "John", ctx))
	assert.False(t, evaluateLeaf("contact.first_name", "eq", "Jane", ctx))
}

func TestCondition_Neq(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.first_name", "neq", "Jane", ctx))
	assert.False(t, evaluateLeaf("contact.first_name", "neq", "John", ctx))
}

func TestCondition_Gt(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("deal.amount", "gt", float64(10000), ctx))
	assert.False(t, evaluateLeaf("deal.amount", "gt", float64(50000), ctx))
	assert.False(t, evaluateLeaf("deal.amount", "gt", float64(100000), ctx))
}

func TestCondition_Gte(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("deal.amount", "gte", float64(50000), ctx))
	assert.True(t, evaluateLeaf("deal.amount", "gte", float64(10000), ctx))
	assert.False(t, evaluateLeaf("deal.amount", "gte", float64(100000), ctx))
}

func TestCondition_Lt(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("deal.amount", "lt", float64(100000), ctx))
	assert.False(t, evaluateLeaf("deal.amount", "lt", float64(50000), ctx))
}

func TestCondition_Lte(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("deal.amount", "lte", float64(50000), ctx))
	assert.True(t, evaluateLeaf("deal.amount", "lte", float64(100000), ctx))
	assert.False(t, evaluateLeaf("deal.amount", "lte", float64(10000), ctx))
}

func TestCondition_Contains_String(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.email", "contains", "example", ctx))
	assert.False(t, evaluateLeaf("contact.email", "contains", "gmail", ctx))
}

func TestCondition_Contains_Array(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.tags", "contains", "vip", ctx))
	assert.False(t, evaluateLeaf("contact.tags", "contains", "basic", ctx))
}

func TestCondition_NotContains(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.tags", "not_contains", "basic", ctx))
	assert.False(t, evaluateLeaf("contact.tags", "not_contains", "vip", ctx))
}

func TestCondition_In(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("deal.stage", "in", []any{"qualified", "won"}, ctx))
	assert.False(t, evaluateLeaf("deal.stage", "in", []any{"lost", "new"}, ctx))
}

func TestCondition_NotIn(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("deal.stage", "not_in", []any{"lost", "new"}, ctx))
	assert.False(t, evaluateLeaf("deal.stage", "not_in", []any{"qualified", "won"}, ctx))
}

func TestCondition_IsEmpty(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.phone", "is_empty", nil, ctx))
	assert.False(t, evaluateLeaf("contact.email", "is_empty", nil, ctx))
}

func TestCondition_IsEmpty_NilField(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.nonexistent", "is_empty", nil, ctx))
}

func TestCondition_IsNotEmpty(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.email", "is_not_empty", nil, ctx))
	assert.False(t, evaluateLeaf("contact.phone", "is_not_empty", nil, ctx))
}

func TestCondition_StartsWith(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.email", "starts_with", "john", ctx))
	assert.False(t, evaluateLeaf("contact.email", "starts_with", "jane", ctx))
}

func TestCondition_EndsWith(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.email", "ends_with", "example.com", ctx))
	assert.False(t, evaluateLeaf("contact.email", "ends_with", "gmail.com", ctx))
}

// --- Numeric string coercion ---

func TestCondition_NumericStringCoercion(t *testing.T) {
	ctx := condTestContext()
	// String "50000" should coerce to float64 50000
	assert.True(t, evaluateLeaf("deal.amount", "eq", "50000", ctx))
	assert.True(t, evaluateLeaf("deal.amount", "gte", "10000", ctx))
}

// --- Unknown field (fail-closed) ---

func TestCondition_UnknownField(t *testing.T) {
	ctx := condTestContext()
	assert.False(t, evaluateLeaf("unknown.field", "eq", "something", ctx))
	assert.False(t, evaluateLeaf("nonexistent.path", "gt", float64(0), ctx))
}

// --- Group evaluation ---

func TestConditionGroup_AND(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{
		Op: "AND",
		Rules: []ConditionRule{
			{Field: "contact.tags", Operator: "contains", Value: "vip"},
			{Field: "deal.amount", Operator: "gte", Value: float64(10000)},
		},
	}
	assert.True(t, EvaluateConditions(group, ctx))
}

func TestConditionGroup_AND_Fails(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{
		Op: "AND",
		Rules: []ConditionRule{
			{Field: "contact.tags", Operator: "contains", Value: "vip"},
			{Field: "deal.amount", Operator: "gte", Value: float64(100000)}, // fails
		},
	}
	assert.False(t, EvaluateConditions(group, ctx))
}

func TestConditionGroup_OR(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{
		Op: "OR",
		Rules: []ConditionRule{
			{Field: "contact.tags", Operator: "contains", Value: "basic"}, // false
			{Field: "deal.amount", Operator: "gte", Value: float64(10000)}, // true
		},
	}
	assert.True(t, EvaluateConditions(group, ctx))
}

func TestConditionGroup_OR_AllFalse(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{
		Op: "OR",
		Rules: []ConditionRule{
			{Field: "contact.tags", Operator: "contains", Value: "basic"},
			{Field: "deal.amount", Operator: "gte", Value: float64(100000)},
		},
	}
	assert.False(t, EvaluateConditions(group, ctx))
}

// --- Nested groups ---

func TestConditionGroup_Nested(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{
		Op: "AND",
		Rules: []ConditionRule{
			{Field: "contact.tags", Operator: "contains", Value: "vip"},
			{
				Op: "OR",
				Rules: []ConditionRule{
					{Field: "deal.stage", Operator: "eq", Value: "qualified"},
					{Field: "deal.stage", Operator: "eq", Value: "won"},
				},
			},
		},
	}
	assert.True(t, EvaluateConditions(group, ctx))
}

func TestConditionGroup_DeepNested(t *testing.T) {
	ctx := condTestContext()
	// depth 3 nesting
	group := ConditionGroup{
		Op: "AND",
		Rules: []ConditionRule{
			{
				Op: "OR",
				Rules: []ConditionRule{
					{
						Op: "AND",
						Rules: []ConditionRule{
							{Field: "contact.first_name", Operator: "eq", Value: "John"},
							{Field: "contact.last_name", Operator: "eq", Value: "Doe"},
						},
					},
					{Field: "contact.email", Operator: "contains", Value: "admin"},
				},
			},
			{Field: "deal.amount", Operator: "gt", Value: float64(0)},
		},
	}
	assert.True(t, EvaluateConditions(group, ctx))
}

// --- Empty/nil conditions ---

func TestConditionGroup_Empty(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{}
	assert.True(t, EvaluateConditions(group, ctx))
}

func TestConditionGroup_EmptyRules(t *testing.T) {
	ctx := condTestContext()
	group := ConditionGroup{
		Op:    "AND",
		Rules: []ConditionRule{},
	}
	assert.True(t, EvaluateConditions(group, ctx))
}

// --- Custom field path ---

func TestCondition_CustomFieldPath(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("contact.custom_fields.industry", "eq", "technology", ctx))
	assert.False(t, evaluateLeaf("contact.custom_fields.industry", "eq", "finance", ctx))
}

// --- Trigger context ---

func TestCondition_TriggerField(t *testing.T) {
	ctx := condTestContext()
	assert.True(t, evaluateLeaf("trigger.from_stage", "eq", "prospecting", ctx))
	assert.True(t, evaluateLeaf("trigger.to_stage", "eq", "qualified", ctx))
}
