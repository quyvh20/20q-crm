package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// boolean_operators_test.go covers is_true / is_false. The builder offers ONLY
// these two for a boolean field (useSchema.ts BOOLEAN_BASE) and auto-selects the
// first one, so every boolean condition a user can construct arrives as one of
// them — including the wait-for-event outcome the Wait step tells you to branch
// on. They were absent from ValidOperators, which made the save a 400, and from
// evaluateLeaf, which would have taken the No branch forever.

func TestValidOperators_AcceptsTheBuildersBooleanOperators(t *testing.T) {
	assert.True(t, ValidOperators["is_true"])
	assert.True(t, ValidOperators["is_false"])
}

func TestValidateConditionRules_BooleanRuleSaves(t *testing.T) {
	res := &ValidationResult{Valid: true}
	validateConditionRules([]ConditionRule{
		{Field: "actions.w1.happened", Operator: "is_true"},
		{Field: "contact.is_vip", Operator: "is_false"},
	}, "steps[0].condition", res)

	require.True(t, res.Valid, "a boolean condition built in the UI must survive validation: %v", res.Errors)
}

func TestEvaluateConditions_IsTrueIsFalse(t *testing.T) {
	ctx := func(v any) EvalContext {
		return EvalContext{Actions: map[string]any{"w1": map[string]any{"happened": v}}}
	}
	leaf := func(op string, v any) bool {
		return EvaluateConditions(ConditionGroup{Field: "actions.w1.happened", Operator: op}, ctx(v))
	}

	assert.True(t, leaf("is_true", true))
	assert.False(t, leaf("is_true", false))
	assert.False(t, leaf("is_false", true))
	assert.True(t, leaf("is_false", false))

	// The string form a text-column round trip produces.
	assert.True(t, leaf("is_true", "true"))
	assert.True(t, leaf("is_false", "false"))
	assert.False(t, leaf("is_true", "false"))

	// Numeric truthiness (jsonb numbers, legacy 0/1 columns).
	assert.True(t, leaf("is_true", float64(1)))
	assert.False(t, leaf("is_true", float64(0)))
	assert.True(t, leaf("is_false", float64(0)))

	// Anything unparseable is not true.
	assert.False(t, leaf("is_true", "yes-ish"))
	assert.False(t, leaf("is_true", map[string]any{}))
}

func TestEvaluateConditions_BooleanOperatorsFailClosedOnAMissingValue(t *testing.T) {
	// A wait step that has not run yet publishes nothing. Neither operator may
	// pass on that: "no evidence the event happened" is not "the event did not
	// happen", and taking a branch on an unwritten value is how a silent
	// mis-branch ships.
	empty := EvalContext{Actions: map[string]any{}}
	assert.False(t, EvaluateConditions(ConditionGroup{Field: "actions.w1.happened", Operator: "is_true"}, empty))
	assert.False(t, EvaluateConditions(ConditionGroup{Field: "actions.w1.happened", Operator: "is_false"}, empty))
}

func TestEvaluateConditions_WaitOutcomeResolvesThroughTheActionsMap(t *testing.T) {
	// The exact shape finishWaitStep writes into evalCtx.Actions, read through the
	// exact path waitOutcomes.ts puts in the condition rule.
	out := map[string]any{"wake_at": "2026-08-18T12:00:00Z", "happened": true, "timed_out": false, "wait_event": TriggerEmailClicked}
	ctx := EvalContext{Actions: map[string]any{"w1": out}}

	assert.True(t, EvaluateConditions(ConditionGroup{Field: "actions.w1.happened", Operator: "is_true"}, ctx))
	assert.False(t, EvaluateConditions(ConditionGroup{Field: "actions.w1.timed_out", Operator: "is_true"}, ctx))
}
