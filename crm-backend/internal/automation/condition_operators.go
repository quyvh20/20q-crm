package automation

import (
	"time"
)

// condition_operators.go is the ONE place a condition operator is declared.
//
// There used to be three hand-maintained copies of this vocabulary — ValidOperators
// here, the switch in evaluateLeaf, and six hardcoded arrays in the frontend's
// useSchema.ts — with nothing enforcing agreement. They drifted: the builder
// offered is_true/is_false, between, in_last_days and four change-detection
// operators that this package had never heard of, so every one of them was a hard
// 400 at save with a raw "unknown operator" the user could not act on.
//
// Two of the three copies are now derived from this list (ValidOperators below,
// and TestEveryValidOperatorIsImplemented drives each one through the evaluator to
// prove the switch handles it). The frontend is pinned to it by a test that reads
// THIS file — see crm-frontend/src/features/workflows/__tests__/operatorParity.test.ts.

// ConditionOperator declares one operator and how many operands it takes.
type ConditionOperator struct {
	Value string
	// NoValue operators compare the resolved value against nothing (is_empty).
	NoValue bool
	// DualValue operators take exactly two operands, carried as a two-element
	// array in ConditionRule.Value — there is no second value field.
	DualValue bool
}

// ConditionOperators is the complete vocabulary. Adding one here without adding a
// case to evaluateLeaf fails TestEveryValidOperatorIsImplemented, which detects
// the miss by reading the "unknown operator" warning evaluateLeaf's default arm
// emits (it does NOT panic — an unimplemented operator saves fine and then takes
// the No branch forever). TestTheGateItselfCanFail proves that detector works.
var ConditionOperators = []ConditionOperator{
	{Value: "eq"},
	{Value: "neq"},
	{Value: "gt"},
	{Value: "gte"},
	{Value: "lt"},
	{Value: "lte"},
	{Value: "contains"},
	{Value: "not_contains"},
	{Value: "starts_with"},
	{Value: "ends_with"},
	{Value: "in"},
	{Value: "not_in"},
	{Value: "between", DualValue: true},
	// Relative date window, resolved against the clock at evaluation time. Named
	// to match the lists/reports/segments compiler (domain.filter_sql) rather than
	// inventing a second name for one concept — the builder said `in_last_days`
	// and was renamed to meet this.
	{Value: "last_n_days"},
	{Value: "is_empty", NoValue: true},
	{Value: "is_not_empty", NoValue: true},
	{Value: "is_true", NoValue: true},
	{Value: "is_false", NoValue: true},
}

// ValidOperators is the save-time gate, derived so it can never fall behind the
// list above.
var ValidOperators = func() map[string]bool {
	m := make(map[string]bool, len(ConditionOperators))
	for _, op := range ConditionOperators {
		m[op.Value] = true
	}
	return m
}()

func conditionOperator(value string) (ConditionOperator, bool) {
	for _, op := range ConditionOperators {
		if op.Value == value {
			return op, true
		}
	}
	return ConditionOperator{}, false
}

// maxRelativeDays bounds last_n_days, matching domain.filter_sql.
const maxRelativeDays = 3650

// evalBetween reports whether the resolved value falls inside an inclusive range.
// Inclusive on BOTH bounds and requiring exactly two operands, matching the
// lists/reports compiler (domain/filter_sql.go) so "between" means one thing
// across the product.
//
// Dates are compared as instants when both sides parse as dates, rather than
// falling through to compareNumeric — whose string fallback would order
// "2026-1-5" after "2026-12-01" lexicographically and quietly return the wrong
// branch.
func evalBetween(resolved, value any) bool {
	bounds, ok := toSlice(value)
	if !ok || len(bounds) != 2 {
		return false
	}
	lo, hi := bounds[0], bounds[1]

	if rt, ok := parseDateValue(resolved); ok {
		loT, loOK := parseDateValue(lo)
		hiT, hiOK := parseDateValue(hi)
		if loOK && hiOK {
			if hiT.Before(loT) {
				loT, hiT = hiT, loT
			}
			return !rt.Before(loT) && !rt.After(hiT)
		}
		return false
	}

	rn, rOK := toFloat64(resolved)
	loN, loOK := toFloat64(lo)
	hiN, hiOK := toFloat64(hi)
	if !rOK || !loOK || !hiOK {
		return false
	}
	if hiN < loN {
		loN, hiN = hiN, loN
	}
	return rn >= loN && rn <= hiN
}

// evalLastNDays reports whether the resolved date falls in the window [now-N, now].
//
// The bound is written !(n >= 1 && n <= maxRelativeDays), never
// (n < 1 || n > maxRelativeDays): every comparison with NaN is false, so the OR
// form waves a NaN through. Same reasoning as domain/filter_sql.go.
func evalLastNDays(resolved, value any, now time.Time) bool {
	n, ok := toFloat64(value)
	if !ok || !(n >= 1 && n <= maxRelativeDays) {
		return false
	}
	t, ok := parseDateValue(resolved)
	if !ok {
		return false
	}
	// A future date is not "in the last N days" — the window is closed at now.
	return !t.After(now) && !t.Before(now.AddDate(0, 0, -int(n)))
}
