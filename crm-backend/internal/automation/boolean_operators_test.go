package automation

import (
	"bytes"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

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

// ── between / last_n_days ───────────────────────────────────────────────────

func evalLeaf(t *testing.T, field, op string, value any, ctx EvalContext) bool {
	t.Helper()
	return EvaluateConditions(ConditionGroup{Field: field, Operator: op, Value: value}, ctx)
}

func numCtx(v any) EvalContext {
	return EvalContext{Deal: map[string]any{"amount": v}}
}

func TestEvaluateConditions_Between_Numbers(t *testing.T) {
	run := func(v any, bounds any) bool { return evalLeaf(t, "deal.amount", "between", bounds, numCtx(v)) }

	// Inclusive on BOTH bounds, matching domain/filter_sql.go so "between" means
	// one thing across the product.
	assert.True(t, run(1000, []any{1000, 5000}), "the low bound is inside the range")
	assert.True(t, run(5000, []any{1000, 5000}), "the high bound is inside the range")
	assert.True(t, run(2500, []any{1000, 5000}))
	assert.False(t, run(999, []any{1000, 5000}))
	assert.False(t, run(5001, []any{1000, 5000}))

	// The builder sends strings from a text input; they must compare as numbers.
	assert.True(t, run("2500", []any{"1000", "5000"}))

	// Reversed bounds are a user slip, not a reason to silently never match.
	assert.True(t, run(2500, []any{5000, 1000}))

	// Wrong arity fails closed rather than guessing which bound is missing.
	assert.False(t, run(2500, []any{1000}))
	assert.False(t, run(2500, []any{1000, 5000, 9000}))
	assert.False(t, run(2500, "1000"))
	assert.False(t, run(2500, nil))
}

func TestEvaluateConditions_Between_DatesCompareAsInstants(t *testing.T) {
	ctx := func(v string) EvalContext { return EvalContext{Contact: map[string]any{"created_at": v}} }
	run := func(v string, bounds any) bool { return evalLeaf(t, "contact.created_at", "between", bounds, ctx(v)) }

	assert.True(t, run("2026-06-15", []any{"2026-01-01", "2026-12-31"}))
	assert.False(t, run("2027-01-01", []any{"2026-01-01", "2026-12-31"}))
	assert.True(t, run("2026-01-01", []any{"2026-01-01", "2026-12-31"}), "inclusive")

	// The reason dates get their own branch: compareNumeric's string fallback
	// orders these lexicographically, which puts "2026-2-01" AFTER "2026-12-31".
	assert.True(t, run("2026-02-01", []any{"2026-01-01", "2026-12-31"}))

	// RFC3339 on one side, plain date on the other.
	assert.True(t, run("2026-06-15T10:30:00Z", []any{"2026-06-01", "2026-06-30"}))

	// An unparseable bound fails closed rather than falling back to string order.
	assert.False(t, run("2026-06-15", []any{"not-a-date", "2026-12-31"}))
}

func TestEvaluateConditions_LastNDays(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	run := func(v string, n any) bool {
		return evalLastNDays(v, n, now)
	}

	assert.True(t, run("2026-08-17", 7), "yesterday is in the last 7 days")
	assert.True(t, run("2026-08-11T12:00:00Z", 7), "exactly N days ago is inside the window")
	assert.False(t, run("2026-08-10", 7), "older than the window")
	assert.False(t, run("2026-08-19", 7), "the window is closed at now — a future date is not 'in the last N days'")

	// Bounds, including the NaN case the OR form would wave through.
	assert.False(t, run("2026-08-17", 0))
	assert.False(t, run("2026-08-17", -1))
	assert.False(t, run("2026-08-17", 3651))
	assert.False(t, run("2026-08-17", math.NaN()))
	assert.False(t, run("2026-08-17", "not a number"))
	assert.False(t, run("2026-08-17", nil))

	// An unparseable date fails closed.
	assert.False(t, run("whenever", 7))
}

// ── the parity that makes drift structural, not a convention ────────────────

// evaluateLeaf's default arm logs "conditions: unknown operator" and returns
// FALSE. It does not panic — which is exactly why an unimplemented operator is
// dangerous: the save succeeds, then every condition using it takes the No branch
// forever with nothing surfaced to the user.
//
// So the detector has to read that log line. An earlier version of this test
// asserted NotPanics and therefore could not fail at all, while its comment
// claimed it enforced the opposite; TestTheGateItselfCanFail below exists so that
// cannot happen again silently.
func unknownOperatorLogged(t *testing.T, fn func()) bool {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)
	fn()
	return strings.Contains(buf.String(), "unknown operator")
}

// operatorProbeContext resolves every field the probe below reads, because
// evaluateLeaf short-circuits on an unresolvable field BEFORE reaching the
// switch — a probe against a missing field would pass vacuously.
func operatorProbeContext() EvalContext {
	return EvalContext{
		Contact: map[string]any{"email": "a@b.com", "score": 5, "created_at": "2026-08-17", "vip": true},
	}
}

func operatorProbeOperand(op string) any {
	switch op {
	case "between":
		return []any{1, 10}
	case "last_n_days":
		return 7
	case "in", "not_in":
		return []any{"a@b.com"}
	default:
		return "a@b.com"
	}
}

func TestEveryValidOperatorIsImplemented(t *testing.T) {
	ctx := operatorProbeContext()
	require.NotNil(t, resolvePath("contact.email", ctx), "the probe field must resolve, or the switch is never reached")

	for _, op := range ConditionOperators {
		t.Run(op.Value, func(t *testing.T) {
			logged := unknownOperatorLogged(t, func() {
				_ = EvaluateConditions(ConditionGroup{
					Field: "contact.email", Operator: op.Value, Value: operatorProbeOperand(op.Value),
				}, ctx)
			})
			assert.False(t, logged,
				"%s is declared in ConditionOperators but evaluateLeaf has no case for it — it would save fine and then silently take the No branch forever", op.Value)
		})
	}
}

func TestTheGateItselfCanFail(t *testing.T) {
	// Without this, the test above is indistinguishable from one that asserts
	// nothing. An operator that is NOT declared must trip the detector.
	logged := unknownOperatorLogged(t, func() {
		_ = EvaluateConditions(ConditionGroup{
			Field: "contact.email", Operator: "not_a_real_operator", Value: "x",
		}, operatorProbeContext())
	})
	assert.True(t, logged, "the detector cannot see an unimplemented operator, so TestEveryValidOperatorIsImplemented proves nothing")
}

func TestValidateOperandShape(t *testing.T) {
	check := func(op string, v any) *ValidationResult {
		res := &ValidationResult{Valid: true}
		validateConditionRules([]ConditionRule{{Field: "deal.amount", Operator: op, Value: v}}, "steps[0].condition", res)
		return res
	}

	assert.True(t, check("between", []any{1, 10}).Valid)
	assert.True(t, check("last_n_days", 30).Valid)
	assert.True(t, check("eq", "anything").Valid)
	assert.True(t, check("is_empty", nil).Valid, "a no-value operator needs no operand")

	// The builder seeds a dual value as ['','']; an untouched range must not save.
	assert.False(t, check("between", []any{"", ""}).Valid, "a half-filled range would save as a condition that can never match")
	assert.False(t, check("between", []any{5}).Valid)
	assert.False(t, check("between", []any{1, 2, 3}).Valid)
	assert.False(t, check("between", "5").Valid)

	assert.False(t, check("last_n_days", 0).Valid)
	assert.False(t, check("last_n_days", 3651).Valid)
	assert.False(t, check("last_n_days", "soon").Valid)
}
