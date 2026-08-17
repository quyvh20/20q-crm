package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Filter SQL compiler — the one filter language
// ============================================================
//
// Moved here from repository/report_sql.go (filtering overhaul F1): the
// compiler is pure — no DB handle, standard library only — and it serves three
// consumers: reports (P9), marketing segments (M5, field leaves), and record
// lists. It lives in domain so usecase validation, repository SQL assembly and
// the schema endpoint's operator matrix all read ONE source instead of three
// hand-mirrored copies. The repository keeps its old function names as thin
// delegating wrappers, so its internal callers and tests are unchanged.
//
// Injection safety rests on three rules, enforced here even when a usecase
// validates first:
//
//   1. every field key must resolve in the caller-supplied catalog, and the SQL
//      address (column / JSONB key) comes from the catalog entry — never from
//      the request;
//   2. operators are mapped through the fixed per-type whitelist below — an
//      unknown token is an error, not a pass-through;
//   3. every value is a bind argument.
//
// gorm trap, load-bearing: this SQL is executed through db.Raw / .Where, which
// treat EVERY literal `?` in the SQL TEXT — even inside a quoted string — as a
// positional bind placeholder. No emitted fragment may contain a `?` that is
// not a bind site; the numeric-validity regex uses {0,1} instead of `?`
// quantifiers for exactly this reason.

// FilterTableRef is the physical target a filter compiles against.
type FilterTableRef struct {
	Table      string // "contacts" | "deals" | "companies" | "custom_object_records" | "tasks" | "activities"
	JSONColumn string // the row's JSONB blob: "custom_fields" (system) or "data" (custom); "" = none
}

// CompiledFilter is one validated, compiled filter fragment: a parenthesised
// SQL predicate with `?` placeholders plus its bind args, ready to AND onto a
// repository's existing WHERE chain. It never contains row-scope or org
// predicates — the repositories already apply those.
type CompiledFilter struct {
	SQL  string
	Args []any
}

const (
	MaxFilterDepth = 5
	MaxFilterRules = 50
)

// FilterOperatorsOrdered gates which operators each field type accepts, in UI
// display order. This is THE operator matrix: the leaf compiler validates
// against it, and the schema endpoint serves it so the frontend renders
// operator menus from the server instead of hand-mirroring the list (the
// report FilterEditor and the workflow builder had already drifted that way).
var FilterOperatorsOrdered = map[string][]string{
	"text":     {"eq", "neq", "contains", "not_contains", "starts_with", "ends_with", "in", "not_in", "is_empty", "is_not_empty"},
	"url":      {"eq", "neq", "contains", "not_contains", "starts_with", "ends_with", "in", "not_in", "is_empty", "is_not_empty"},
	"select":   {"eq", "neq", "in", "not_in", "is_empty", "is_not_empty"},
	"number":   {"eq", "neq", "gt", "gte", "lt", "lte", "between", "in", "not_in", "is_empty", "is_not_empty"},
	"date":     {"on", "gt", "gte", "lt", "lte", "between", "last_n_days", "next_n_days", "eq", "neq", "is_empty", "is_not_empty"},
	"boolean":  {"eq", "neq", "is_empty", "is_not_empty"},
	"relation": {"eq", "neq", "in", "not_in", "is_empty", "is_not_empty"},
}

// filterOperatorSet is the set form of FilterOperatorsOrdered, derived once.
var filterOperatorSet = func() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(FilterOperatorsOrdered))
	for t, ops := range FilterOperatorsOrdered {
		set := make(map[string]bool, len(ops))
		for _, op := range ops {
			set[op] = true
		}
		out[t] = set
	}
	return out
}()

// FilterOperators returns the per-type operator whitelist in set form (the
// shape the repository's leaf compiler historically used).
func FilterOperators() map[string]map[string]bool { return filterOperatorSet }

// FilterOperatorAllowed reports whether a field type accepts an operator.
func FilterOperatorAllowed(fieldType, operator string) bool {
	return filterOperatorSet[fieldType][operator]
}

// FilterIdentRe validates every identifier the compiler splices into SQL text
// (column names from the registry, JSONB keys) as belt-and-braces even though
// none of them originate from the request.
var FilterIdentRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,63}$`)

// CompileRecordFilter validates and compiles a whole filter AST against a
// catalog. nil group or an empty compile returns (nil, nil). Every error is a
// user-input problem (unknown field, bad operator, malformed value, too deep)
// — callers surface it as a 400.
func CompileRecordFilter(ref FilterTableRef, catalog []ReportField, g *ReportFilterGroup) (*CompiledFilter, error) {
	if g == nil {
		return nil, nil
	}
	fields := make(map[string]ReportField, len(catalog))
	for _, f := range catalog {
		fields[f.Key] = f
	}
	leafCount := 0
	sql, args, err := BuildFilterGroup(ref, fields, *g, 0, &leafCount)
	if err != nil {
		return nil, err
	}
	if sql == "" {
		return nil, nil
	}
	return &CompiledFilter{SQL: sql, Args: args}, nil
}

// BuildFilterGroup compiles one AND/OR group (or a leaf disguised as a group —
// the automation shape allows it) to a parenthesised predicate.
func BuildFilterGroup(ref FilterTableRef, fields map[string]ReportField, g ReportFilterGroup, depth int, leafCount *int) (string, []any, error) {
	if depth > MaxFilterDepth {
		return "", nil, fmt.Errorf("filter: nested deeper than %d levels", MaxFilterDepth)
	}
	if g.Field != "" {
		return BuildFilterLeaf(ref, fields, g.Field, g.Operator, g.Value, leafCount)
	}
	if len(g.Rules) == 0 {
		return "", nil, nil
	}
	joiner := " AND "
	if strings.EqualFold(g.Op, "OR") {
		joiner = " OR "
	}
	var parts []string
	var args []any
	for _, rule := range g.Rules {
		var sqlPart string
		var ruleArgs []any
		var err error
		if rule.IsGroup() {
			sqlPart, ruleArgs, err = BuildFilterGroup(ref, fields, ReportFilterGroup{Op: rule.Op, Rules: rule.Rules}, depth+1, leafCount)
		} else {
			sqlPart, ruleArgs, err = BuildFilterLeaf(ref, fields, rule.Field, rule.Operator, rule.Value, leafCount)
		}
		if err != nil {
			return "", nil, err
		}
		if sqlPart == "" {
			continue
		}
		parts = append(parts, sqlPart)
		args = append(args, ruleArgs...)
	}
	if len(parts) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(parts, joiner) + ")", args, nil
}

// BuildFilterLeaf compiles one {field, operator, value} condition.
func BuildFilterLeaf(ref FilterTableRef, fields map[string]ReportField, fieldKey, operator string, value any, leafCount *int) (string, []any, error) {
	*leafCount++
	if *leafCount > MaxFilterRules {
		return "", nil, fmt.Errorf("filter: more than %d filter rules", MaxFilterRules)
	}
	f, ok := fields[fieldKey]
	if !ok {
		return "", nil, fmt.Errorf("filter: unknown filter field %q", fieldKey)
	}
	if !FilterOperatorAllowed(f.Type, operator) {
		return "", nil, fmt.Errorf("filter: operator %q not valid for %s field %q", operator, f.Type, fieldKey)
	}

	raw, err := FilterFieldExpr(ref, f)
	if err != nil {
		return "", nil, err
	}
	typed, err := FilterTypedExpr(ref, f)
	if err != nil {
		return "", nil, err
	}

	switch operator {
	case "is_empty":
		if IsTextualFilterField(f) {
			return "(" + raw + " IS NULL OR " + raw + " = '')", nil, nil
		}
		return raw + " IS NULL", nil, nil
	case "is_not_empty":
		if IsTextualFilterField(f) {
			return "(" + raw + " IS NOT NULL AND " + raw + " <> '')", nil, nil
		}
		return raw + " IS NOT NULL", nil, nil
	case "eq":
		arg, err := FilterValueArg(f, value)
		if err != nil {
			return "", nil, err
		}
		return typed + " = ?", []any{arg}, nil
	case "neq":
		arg, err := FilterValueArg(f, value)
		if err != nil {
			return "", nil, err
		}
		return typed + " IS DISTINCT FROM ?", []any{arg}, nil
	case "gt", "gte", "lt", "lte":
		arg, err := FilterValueArg(f, value)
		if err != nil {
			return "", nil, err
		}
		op := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[operator]
		return typed + " " + op + " ?", []any{arg}, nil
	case "on":
		// Day-granular equality — what a date input actually means. Plain eq on a
		// timestamptz matches midnight-exact rows only (the footgun the report UI
		// dodged by omitting eq); this compiles to the whole civil day, in the
		// database's timezone.
		day, err := FilterStringArg(value)
		if err != nil {
			return "", nil, fmt.Errorf("filter: field %q expects a date value: %w", fieldKey, err)
		}
		day = strings.TrimSpace(day)
		if _, perr := time.Parse("2006-01-02", day); perr != nil {
			return "", nil, fmt.Errorf("filter: %q on %q expects a YYYY-MM-DD date", operator, fieldKey)
		}
		return "(" + typed + " >= (?)::date AND " + typed + " < ((?)::date + INTERVAL '1 day'))", []any{day, day}, nil
	case "last_n_days", "next_n_days":
		// Relative dates, resolved by the DATABASE clock at run time — so a saved
		// view or segment using them can never go stale the way absolute bounds do.
		//
		// The bound is written as !(n >= 1 && n <= 3650), NOT (n < 1 || n > 3650):
		// ParseFloat accepts "NaN", every comparison with NaN is false, and the
		// OR form therefore waves NaN through to int(n) — whose result is
		// implementation-defined (MinInt64 on amd64 → an out-of-range interval →
		// a 500 instead of this 400).
		n, err := FilterFloatArg(value)
		if err != nil || !(n >= 1 && n <= 3650) {
			return "", nil, fmt.Errorf("filter: %s on %q expects a number of days between 1 and 3650", operator, fieldKey)
		}
		days := int(n)
		if operator == "last_n_days" {
			return "(" + typed + " >= NOW() - (? * INTERVAL '1 day') AND " + typed + " <= NOW())", []any{days}, nil
		}
		return "(" + typed + " >= NOW() AND " + typed + " <= NOW() + (? * INTERVAL '1 day'))", []any{days}, nil
	case "between":
		items, err := FilterListArg(f, value)
		if err != nil {
			return "", nil, err
		}
		if len(items) != 2 {
			return "", nil, fmt.Errorf("filter: between on %q expects exactly two values", fieldKey)
		}
		// Date bounds are validated HERE, like `on` and unlike the legacy
		// gt/gte/lt/lte (whose looseness predates this operator and is load-bearing
		// for old saved reports): between is new, so nothing depends on a bound
		// Postgres would reject at run time — and a saved view's dry-run compile
		// approving "soon" only for every later open to 500 on the timestamptz
		// cast would defeat the save-gate's whole promise.
		if f.Type == "date" {
			for _, item := range items {
				s, _ := item.(string)
				if !isFilterDateValue(s) {
					return "", nil, fmt.Errorf("filter: between on %q expects YYYY-MM-DD or RFC3339 dates", fieldKey)
				}
			}
		}
		return "(" + typed + " >= ? AND " + typed + " <= ?)", items, nil
	case "contains", "not_contains", "starts_with", "ends_with":
		s, err := FilterStringArg(value)
		if err != nil {
			return "", nil, err
		}
		pattern := EscapeFilterLike(s)
		switch operator {
		case "contains", "not_contains":
			pattern = "%" + pattern + "%"
		case "starts_with":
			pattern = pattern + "%"
		case "ends_with":
			pattern = "%" + pattern
		}
		neg := ""
		if operator == "not_contains" {
			neg = "NOT "
		}
		return raw + " " + neg + "ILIKE ?", []any{pattern}, nil
	case "in", "not_in":
		items, err := FilterListArg(f, value)
		if err != nil {
			return "", nil, err
		}
		if len(items) == 0 {
			// Empty in-list matches nothing; empty not-in excludes nothing.
			if operator == "in" {
				return "FALSE", nil, nil
			}
			return "TRUE", nil, nil
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(items)), ", ")
		neg := ""
		if operator == "not_in" {
			neg = "NOT "
		}
		// NOT IN over a NULL-able expr: keep rows where the expr is NULL too,
		// matching the intuitive "everything except these" reading.
		if operator == "not_in" {
			return "(" + typed + " IS NULL OR " + typed + " " + neg + "IN (" + placeholders + "))", items, nil
		}
		return typed + " IN (" + placeholders + ")", items, nil
	default:
		return "", nil, fmt.Errorf("filter: unknown operator %q", operator)
	}
}

// isFilterDateValue accepts the two formats fieldvalidate allows on writes —
// a bare civil date or a full RFC3339 timestamp.
func isFilterDateValue(s string) bool {
	s = strings.TrimSpace(s)
	if _, err := time.Parse("2006-01-02", s); err == nil {
		return true
	}
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return true
	}
	return false
}

// ============================================================
// Field expressions
// ============================================================

// FilterFieldExpr is the field's raw address: a native column, or the JSONB
// text extraction. Used where text form is wanted (ILIKE, emptiness checks,
// table columns, DISTINCT counts).
func FilterFieldExpr(ref FilterTableRef, f ReportField) (string, error) {
	if f.Column != "" {
		if !FilterIdentRe.MatchString(f.Column) {
			return "", fmt.Errorf("filter: invalid column mapping %q for field %q", f.Column, f.Key)
		}
		// The identifier whitelist above runs BEFORE the cast is wrapped around
		// it, so the cast can never be a splice point.
		if f.CastText {
			return "(" + ref.Table + "." + f.Column + ")::text", nil
		}
		return ref.Table + "." + f.Column, nil
	}
	if f.JSONKey == "" {
		return "", fmt.Errorf("filter: field %q has no storage mapping", f.Key)
	}
	if !FilterIdentRe.MatchString(f.JSONKey) {
		return "", fmt.Errorf("filter: invalid JSON key %q for field %q", f.JSONKey, f.Key)
	}
	if ref.JSONColumn == "" {
		return "", fmt.Errorf("filter: table %q has no JSONB storage for field %q", ref.Table, f.Key)
	}
	return ref.Table + "." + ref.JSONColumn + "->>'" + f.JSONKey + "'", nil
}

// FilterTypedExpr is the field in its comparable type. Native columns are
// already typed; JSONB extractions get guarded casts so one row of dirty data
// (a non-numeric "sqft") NULLs out instead of killing the whole query.
func FilterTypedExpr(ref FilterTableRef, f ReportField) (string, error) {
	raw, err := FilterFieldExpr(ref, f)
	if err != nil {
		return "", err
	}
	if f.Column != "" {
		return raw, nil
	}
	switch f.Type {
	case "number":
		// The numeric-validity regex uses {0,1} rather than `?` quantifiers ON PURPOSE:
		// this SQL is executed through gorm's db.Raw / .Where, which treat EVERY `?` —
		// including ones inside a quoted string literal — as a positional bind
		// placeholder. A `?` here would steal a bind arg and misalign the rest of the
		// query (a jsonb number filter then fails with "syntax error at end of input").
		// {0,1} is the exact equivalent with no `?`.
		return `(CASE WHEN ` + raw + ` ~ '^-{0,1}[0-9]+(\.[0-9]+){0,1}$' THEN (` + raw + `)::numeric END)`, nil
	case "date":
		return "(NULLIF(" + raw + ", ''))::timestamptz", nil
	case "boolean":
		return "(CASE WHEN " + raw + " IN ('true','false') THEN (" + raw + ")::boolean END)", nil
	default:
		return raw, nil
	}
}

// IsTextualFilterField reports whether the field's raw expression is textual,
// which decides the emptiness semantics (NULL OR '' vs plain NULL).
func IsTextualFilterField(f ReportField) bool {
	if f.Column == "" {
		// Every JSONB extraction is text.
		return true
	}
	switch f.Type {
	case "text", "url", "select":
		return true
	}
	return false
}

// ============================================================
// Value coercion (JSON → bind args)
// ============================================================

func FilterValueArg(f ReportField, v any) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("filter: condition on %q needs a value", f.Key)
	}
	switch f.Type {
	case "relation":
		// Relation values are record/user/stage ids. Validated as UUIDs so a
		// junk value is a 400 at compile instead of Postgres 22P02 (→ 500) when
		// the bound text meets a native uuid column; on jsonb storage the same
		// junk used to just match nothing — the silent-empty shape again.
		s, err := FilterStringArg(v)
		if err != nil {
			return nil, fmt.Errorf("filter: field %q expects an id value: %w", f.Key, err)
		}
		s = strings.TrimSpace(s)
		if _, err := uuid.Parse(s); err != nil {
			return nil, fmt.Errorf("filter: field %q expects a record id", f.Key)
		}
		return s, nil
	case "number":
		n, err := FilterFloatArg(v)
		if err != nil {
			return nil, fmt.Errorf("filter: field %q expects a number: %w", f.Key, err)
		}
		return n, nil
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			if b, err := strconv.ParseBool(strings.TrimSpace(x)); err == nil {
				return b, nil
			}
		}
		return nil, fmt.Errorf("filter: field %q expects true/false", f.Key)
	default:
		s, err := FilterStringArg(v)
		if err != nil {
			return nil, fmt.Errorf("filter: field %q expects a string value: %w", f.Key, err)
		}
		return s, nil
	}
}

func FilterListArg(f ReportField, v any) ([]any, error) {
	list, ok := v.([]any)
	if !ok {
		// A single value is treated as a one-element list for convenience.
		single, err := FilterValueArg(f, v)
		if err != nil {
			return nil, fmt.Errorf("filter: a list operator on %q expects a list", f.Key)
		}
		return []any{single}, nil
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		arg, err := FilterValueArg(f, item)
		if err != nil {
			return nil, err
		}
		out = append(out, arg)
	}
	return out, nil
}

func FilterFloatArg(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return x.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(x), 64)
	}
	return 0, fmt.Errorf("not a number: %T", v)
}

func FilterStringArg(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(x), nil
	case json.Number:
		return x.String(), nil
	}
	return "", fmt.Errorf("not a string: %T", v)
}

// EscapeFilterLike escapes LIKE wildcards in a user value so "50%" matches a
// literal percent sign instead of everything starting with 50.
func EscapeFilterLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
