package automation

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// EvaluateConditions evaluates a ConditionGroup against an EvalContext.
// Returns true if the conditions are satisfied.
func EvaluateConditions(group ConditionGroup, ctx EvalContext) bool {
	if group.Op == "" && group.Field == "" {
		// Empty group — treat as true (no conditions).
		return true
	}

	// If it's a leaf (has Field set directly on the group)
	if group.Field != "" {
		return evaluateLeaf(group.Field, group.Operator, group.Value, ctx)
	}

	if len(group.Rules) == 0 {
		return true
	}

	switch strings.ToUpper(group.Op) {
	case "AND":
		for _, rule := range group.Rules {
			if !evaluateRule(rule, ctx) {
				return false
			}
		}
		return true
	case "OR":
		for _, rule := range group.Rules {
			if evaluateRule(rule, ctx) {
				return true
			}
		}
		return false
	default:
		slog.Warn("conditions: unknown operator, treating as AND", "op", group.Op)
		for _, rule := range group.Rules {
			if !evaluateRule(rule, ctx) {
				return false
			}
		}
		return true
	}
}

// evaluateRule evaluates a single rule — either a leaf or a nested group.
func evaluateRule(rule ConditionRule, ctx EvalContext) bool {
	if rule.IsGroup() {
		// Nested group — recurse
		nestedGroup := ConditionGroup{
			Op:    rule.Op,
			Rules: rule.Rules,
		}
		return EvaluateConditions(nestedGroup, ctx)
	}
	// Leaf rule
	return evaluateLeaf(rule.Field, rule.Operator, rule.Value, ctx)
}

// evaluateLeaf evaluates a single field comparison.
func evaluateLeaf(field, operator string, value any, ctx EvalContext) bool {
	resolved := resolvePath(field, ctx)
	// Unknown field → false (fail-closed, per spec §6)
	if resolved == nil && operator != "is_empty" {
		if operator == "is_not_empty" {
			return false
		}
		return false
	}

	switch operator {
	case "eq":
		return compareEqual(resolved, value)
	case "neq":
		return !compareEqual(resolved, value)
	case "gt":
		return compareNumeric(resolved, value) > 0
	case "gte":
		return compareNumeric(resolved, value) >= 0
	case "lt":
		return compareNumeric(resolved, value) < 0
	case "lte":
		return compareNumeric(resolved, value) <= 0
	case "contains":
		return evalContains(resolved, value)
	case "not_contains":
		return !evalContains(resolved, value)
	case "in":
		return evalIn(resolved, value)
	case "not_in":
		return !evalIn(resolved, value)
	case "is_empty":
		return evalIsEmpty(resolved)
	case "is_not_empty":
		return !evalIsEmpty(resolved)
	case "starts_with":
		return evalStartsWith(resolved, value)
	case "ends_with":
		return evalEndsWith(resolved, value)
	default:
		slog.Warn("conditions: unknown operator", "operator", operator)
		return false
	}
}

// compareEqual compares two values for equality, with type coercion.
func compareEqual(a, b any) bool {
	// Try string comparison
	aStr := toString(a)
	bStr := toString(b)
	if aStr == bStr {
		return true
	}

	// Try numeric comparison
	aNum, aOk := toFloat64(a)
	bNum, bOk := toFloat64(b)
	if aOk && bOk {
		return aNum == bNum
	}

	return false
}

// compareNumeric compares two values as numbers.
// Returns -1, 0, or 1 like strcmp. Returns 0 if comparison is not possible.
func compareNumeric(a, b any) int {
	aNum, aOk := toFloat64(a)
	bNum, bOk := toFloat64(b)
	if !aOk || !bOk {
		// Fall back to string comparison
		aStr := toString(a)
		bStr := toString(b)
		if aStr < bStr {
			return -1
		}
		if aStr > bStr {
			return 1
		}
		return 0
	}

	if aNum < bNum {
		return -1
	}
	if aNum > bNum {
		return 1
	}
	return 0
}

// evalContains checks if a string contains a substring, or an array contains a value.
func evalContains(resolved, value any) bool {
	// Array contains
	if arr, ok := toSlice(resolved); ok {
		valStr := toString(value)
		for _, item := range arr {
			if toString(item) == valStr {
				return true
			}
		}
		return false
	}

	// String contains
	resolvedStr := toString(resolved)
	valueStr := toString(value)
	return strings.Contains(resolvedStr, valueStr)
}

// evalIn checks if the resolved value is in the rhs array.
func evalIn(resolved, value any) bool {
	arr, ok := toSlice(value)
	if !ok {
		return false
	}
	resolvedStr := toString(resolved)
	for _, item := range arr {
		if toString(item) == resolvedStr {
			return true
		}
	}
	return false
}

// evalIsEmpty checks if a value is empty/nil/zero.
func evalIsEmpty(resolved any) bool {
	if resolved == nil {
		return true
	}
	switch v := resolved.(type) {
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	case float64:
		return v == 0
	case int:
		return v == 0
	case int64:
		return v == 0
	case bool:
		return !v
	default:
		return false
	}
}

// evalStartsWith checks if the resolved string starts with the value.
func evalStartsWith(resolved, value any) bool {
	return strings.HasPrefix(toString(resolved), toString(value))
}

// evalEndsWith checks if the resolved string ends with the value.
func evalEndsWith(resolved, value any) bool {
	return strings.HasSuffix(toString(resolved), toString(value))
}

// --- Type coercion helpers ---

// toFloat64 converts a value to float64, with string coercion.
func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	case string:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// toString converts a value to string.
func toString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case bool:
		return strconv.FormatBool(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// toSlice attempts to convert a value to []any.
func toSlice(v any) ([]any, bool) {
	switch val := v.(type) {
	case []any:
		return val, true
	case []string:
		result := make([]any, len(val))
		for i, s := range val {
			result[i] = s
		}
		return result, true
	default:
		return nil, false
	}
}
