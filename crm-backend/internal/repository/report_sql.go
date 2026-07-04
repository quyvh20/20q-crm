package repository

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Report SQL builder (P9)
// ============================================================
//
// Pure functions that translate a validated ReportConfig into one parameterized
// SQL statement, so they are testable without a database. Injection safety rests
// on three rules, enforced here even though the usecase validates first:
//
//   1. every field key must resolve in the caller-supplied catalog, and the SQL
//      address (column / JSONB key) comes from the catalog entry — never from
//      the config;
//   2. operators, date buckets, aggregate functions, and sort directions are
//      mapped through fixed whitelists — an unknown token is an error, not a
//      pass-through;
//   3. every value is a bind argument.
//
// The builder also replicates applyScopeFromCtx's 'own'-scope predicate
// (owned OR shared-to-me) for exactly the tables it applies to today —
// contacts and deals — so a report shows an own-scoped role the same records
// its list pages do.

// reportTableRef is the physical target of a report query.
type reportTableRef struct {
	Table      string // "contacts" | "deals" | "companies" | "custom_object_records"
	JSONColumn string // the row's JSONB blob: "custom_fields" (system) or "data" (custom)
	// ObjectDefID is set for custom_object_records, which multiplexes every
	// custom object into one table.
	ObjectDefID *uuid.UUID
}

const (
	maxReportFilterDepth = 5
	maxReportFilterRules = 50
)

var reportBuckets = map[string]bool{
	"day": true, "week": true, "month": true, "quarter": true, "year": true,
}

// reportIdentRe validates every identifier the builder splices into SQL text
// (column names from the registry, JSONB keys, catalog field keys used as
// aliases) as belt-and-braces even though none of them originate from the
// request. Covers every column name and registry field key while excluding
// quotes and whitespace.
var reportIdentRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,63}$`)

// buildReportSQL translates one validated config into a parameterized query.
// scope/scopeUserID mirror the ctx data scope the runner extracts.
func buildReportSQL(ref reportTableRef, catalog []domain.ReportField, cfg domain.ReportConfig, orgID uuid.UUID, scope string, scopeUserID uuid.UUID) (string, []any, error) {
	fields := make(map[string]domain.ReportField, len(catalog))
	for _, f := range catalog {
		fields[f.Key] = f
	}

	where, args, err := buildReportWhere(ref, fields, cfg, orgID, scope, scopeUserID)
	if err != nil {
		return "", nil, err
	}

	switch cfg.ResultKind() {
	case domain.ReportResultRows:
		return buildReportRowsSQL(ref, fields, cfg, where, args)
	case domain.ReportResultScalar:
		aggExpr, err := reportAggregateExpr(ref, fields, cfg.Aggregate)
		if err != nil {
			return "", nil, err
		}
		q := "SELECT " + aggExpr + " AS agg_value, COUNT(*) AS row_count FROM " + ref.Table + " WHERE " + where
		return q, args, nil
	default:
		return buildReportGroupsSQL(ref, fields, cfg, where, args)
	}
}

// buildReportWhere assembles the mandatory predicates (org scope, soft delete,
// custom-object discriminator, own-scope) plus the config's filter tree.
func buildReportWhere(ref reportTableRef, fields map[string]domain.ReportField, cfg domain.ReportConfig, orgID uuid.UUID, scope string, scopeUserID uuid.UUID) (string, []any, error) {
	parts := []string{ref.Table + ".org_id = ?", ref.Table + ".deleted_at IS NULL"}
	args := []any{orgID}

	if ref.ObjectDefID != nil {
		parts = append(parts, ref.Table+".object_def_id = ?")
		args = append(args, *ref.ObjectDefID)
	}

	// Replicates applyScopeFromCtx (scopes.go): 'own' restricts contacts and
	// deals to owned-or-shared rows; every other table gets org scoping only.
	if scope == domain.DataScopeOwn && (ref.Table == "contacts" || ref.Table == "deals") {
		recordType := "contact"
		if ref.Table == "deals" {
			recordType = "deal"
		}
		parts = append(parts, "("+ref.Table+".owner_user_id = ? OR EXISTS (SELECT 1 FROM record_shares rs WHERE rs.record_id = "+ref.Table+".id AND rs.record_type = ? AND rs.grantee_user_id = ?))")
		args = append(args, scopeUserID, recordType, scopeUserID)
	}

	if cfg.Filters != nil {
		leafCount := 0
		filterSQL, filterArgs, err := buildReportFilterGroup(ref, fields, *cfg.Filters, 1, &leafCount)
		if err != nil {
			return "", nil, err
		}
		if filterSQL != "" {
			parts = append(parts, filterSQL)
			args = append(args, filterArgs...)
		}
	}

	return strings.Join(parts, " AND "), args, nil
}

func buildReportGroupsSQL(ref reportTableRef, fields map[string]domain.ReportField, cfg domain.ReportConfig, where string, args []any) (string, []any, error) {
	if cfg.GroupBy == nil || cfg.GroupBy.Field == "" {
		return "", nil, fmt.Errorf("report: group_by is required for %q charts", cfg.Chart)
	}
	gf, ok := fields[cfg.GroupBy.Field]
	if !ok {
		return "", nil, fmt.Errorf("report: unknown group_by field %q", cfg.GroupBy.Field)
	}
	groupExpr, err := reportGroupExpr(ref, gf, cfg.GroupBy.Bucket)
	if err != nil {
		return "", nil, err
	}
	aggExpr, err := reportAggregateExpr(ref, fields, cfg.Aggregate)
	if err != nil {
		return "", nil, err
	}

	// Default ordering: chronological for date buckets, biggest-first otherwise.
	orderBy := "agg_value DESC NULLS LAST"
	if gf.Type == "date" {
		orderBy = "1 ASC NULLS LAST"
	}
	if cfg.Sort != nil {
		dir := "ASC"
		if strings.EqualFold(cfg.Sort.Dir, "desc") {
			dir = "DESC"
		}
		switch cfg.Sort.By {
		case "value":
			orderBy = "agg_value " + dir + " NULLS LAST"
		case "label", "":
			orderBy = "1 " + dir + " NULLS LAST"
		default:
			return "", nil, fmt.Errorf("report: grouped sort.by must be \"value\" or \"label\", got %q", cfg.Sort.By)
		}
	}

	limit := cfg.Limit
	if limit <= 0 || limit > domain.MaxReportGroups {
		limit = domain.MaxReportGroups
	}

	q := "SELECT " + groupExpr + " AS group_key, " + aggExpr + " AS agg_value, COUNT(*) AS row_count FROM " + ref.Table +
		" WHERE " + where + " GROUP BY 1 ORDER BY " + orderBy + " LIMIT " + strconv.Itoa(limit)
	return q, args, nil
}

func buildReportRowsSQL(ref reportTableRef, fields map[string]domain.ReportField, cfg domain.ReportConfig, where string, args []any) (string, []any, error) {
	if len(cfg.Columns) == 0 {
		return "", nil, fmt.Errorf("report: table charts need at least one column")
	}
	selects := []string{ref.Table + ".id AS \"id\""}
	for _, key := range cfg.Columns {
		f, ok := fields[key]
		if !ok {
			return "", nil, fmt.Errorf("report: unknown column %q", key)
		}
		expr, err := reportFieldExpr(ref, f)
		if err != nil {
			return "", nil, err
		}
		if !reportIdentRe.MatchString(f.Key) {
			return "", nil, fmt.Errorf("report: invalid column key %q", f.Key)
		}
		selects = append(selects, expr+" AS \""+f.Key+"\"")
	}

	// Every reportable table carries created_at, so newest-first is a safe default.
	orderBy := ref.Table + ".created_at DESC"
	if cfg.Sort != nil && cfg.Sort.By != "" {
		f, ok := fields[cfg.Sort.By]
		if !ok {
			return "", nil, fmt.Errorf("report: unknown sort field %q", cfg.Sort.By)
		}
		expr, err := reportTypedExpr(ref, f)
		if err != nil {
			return "", nil, err
		}
		dir := "ASC"
		if strings.EqualFold(cfg.Sort.Dir, "desc") {
			dir = "DESC"
		}
		orderBy = expr + " " + dir + " NULLS LAST"
	}

	limit := cfg.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > domain.MaxReportRows {
		limit = domain.MaxReportRows
	}

	q := "SELECT " + strings.Join(selects, ", ") + " FROM " + ref.Table +
		" WHERE " + where + " ORDER BY " + orderBy + " LIMIT " + strconv.Itoa(limit)
	return q, args, nil
}

// ============================================================
// Field expressions
// ============================================================

// reportFieldExpr is the field's raw address: a native column, or the JSONB
// text extraction. Used where text form is wanted (table columns, ILIKE,
// emptiness checks, DISTINCT counts).
func reportFieldExpr(ref reportTableRef, f domain.ReportField) (string, error) {
	if f.Column != "" {
		if !reportIdentRe.MatchString(f.Column) {
			return "", fmt.Errorf("report: invalid column mapping %q for field %q", f.Column, f.Key)
		}
		return ref.Table + "." + f.Column, nil
	}
	if f.JSONKey == "" {
		return "", fmt.Errorf("report: field %q has no storage mapping", f.Key)
	}
	if !reportIdentRe.MatchString(f.JSONKey) {
		return "", fmt.Errorf("report: invalid JSON key %q for field %q", f.JSONKey, f.Key)
	}
	if ref.JSONColumn == "" {
		return "", fmt.Errorf("report: table %q has no JSONB storage for field %q", ref.Table, f.Key)
	}
	return ref.Table + "." + ref.JSONColumn + "->>'" + f.JSONKey + "'", nil
}

// reportTypedExpr is the field in its comparable type. Native columns are
// already typed; JSONB extractions get guarded casts so one row of dirty data
// (a non-numeric "sqft") NULLs out instead of killing the whole query.
func reportTypedExpr(ref reportTableRef, f domain.ReportField) (string, error) {
	raw, err := reportFieldExpr(ref, f)
	if err != nil {
		return "", err
	}
	if f.Column != "" {
		return raw, nil
	}
	switch f.Type {
	case "number":
		return `(CASE WHEN ` + raw + ` ~ '^-?[0-9]+(\.[0-9]+)?$' THEN (` + raw + `)::numeric END)`, nil
	case "date":
		return "(NULLIF(" + raw + ", ''))::timestamptz", nil
	case "boolean":
		return "(CASE WHEN " + raw + " IN ('true','false') THEN (" + raw + ")::boolean END)", nil
	default:
		return raw, nil
	}
}

func reportGroupExpr(ref reportTableRef, f domain.ReportField, bucket string) (string, error) {
	expr, err := reportTypedExpr(ref, f)
	if err != nil {
		return "", err
	}
	if f.Type == "date" {
		if bucket == "" {
			bucket = "month"
		}
		if !reportBuckets[bucket] {
			return "", fmt.Errorf("report: unknown date bucket %q", bucket)
		}
		return "date_trunc('" + bucket + "', " + expr + ")", nil
	}
	if bucket != "" {
		return "", fmt.Errorf("report: bucket applies to date fields only (field %q is %s)", f.Key, f.Type)
	}
	return expr, nil
}

func reportAggregateExpr(ref reportTableRef, fields map[string]domain.ReportField, agg *domain.ReportAggregate) (string, error) {
	if agg == nil || agg.Fn == "" || agg.Fn == "count" {
		return "COUNT(*)", nil
	}
	f, ok := fields[agg.Field]
	if !ok {
		return "", fmt.Errorf("report: unknown aggregate field %q", agg.Field)
	}
	switch agg.Fn {
	case "count_distinct":
		expr, err := reportFieldExpr(ref, f)
		if err != nil {
			return "", err
		}
		return "COUNT(DISTINCT " + expr + ")", nil
	case "sum", "avg", "min", "max":
		if f.Type != "number" {
			return "", fmt.Errorf("report: %s requires a number field (field %q is %s)", agg.Fn, f.Key, f.Type)
		}
		expr, err := reportTypedExpr(ref, f)
		if err != nil {
			return "", err
		}
		return "COALESCE(" + strings.ToUpper(agg.Fn) + "(" + expr + "), 0)", nil
	default:
		return "", fmt.Errorf("report: unknown aggregate function %q", agg.Fn)
	}
}

// ============================================================
// Filters
// ============================================================

func buildReportFilterGroup(ref reportTableRef, fields map[string]domain.ReportField, g domain.ReportFilterGroup, depth int, leafCount *int) (string, []any, error) {
	if depth > maxReportFilterDepth {
		return "", nil, fmt.Errorf("report: filters nested deeper than %d levels", maxReportFilterDepth)
	}
	// A leaf disguised as a group (the automation shape allows it).
	if g.Field != "" {
		return buildReportFilterLeaf(ref, fields, g.Field, g.Operator, g.Value, leafCount)
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
			sqlPart, ruleArgs, err = buildReportFilterGroup(ref, fields, domain.ReportFilterGroup{Op: rule.Op, Rules: rule.Rules}, depth+1, leafCount)
		} else {
			sqlPart, ruleArgs, err = buildReportFilterLeaf(ref, fields, rule.Field, rule.Operator, rule.Value, leafCount)
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

// reportOperatorsByType gates which operators each field type accepts, mirroring
// the frontend condition builder so a hand-crafted request can't smuggle e.g. an
// ILIKE against a uuid column.
var reportOperatorsByType = map[string]map[string]bool{
	"text":     {"eq": true, "neq": true, "contains": true, "not_contains": true, "starts_with": true, "ends_with": true, "in": true, "not_in": true, "is_empty": true, "is_not_empty": true},
	"url":      {"eq": true, "neq": true, "contains": true, "not_contains": true, "starts_with": true, "ends_with": true, "in": true, "not_in": true, "is_empty": true, "is_not_empty": true},
	"select":   {"eq": true, "neq": true, "in": true, "not_in": true, "is_empty": true, "is_not_empty": true},
	"number":   {"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true, "in": true, "not_in": true, "is_empty": true, "is_not_empty": true},
	"date":     {"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true, "is_empty": true, "is_not_empty": true},
	"boolean":  {"eq": true, "neq": true, "is_empty": true, "is_not_empty": true},
	"relation": {"eq": true, "neq": true, "in": true, "not_in": true, "is_empty": true, "is_not_empty": true},
}

func buildReportFilterLeaf(ref reportTableRef, fields map[string]domain.ReportField, fieldKey, operator string, value any, leafCount *int) (string, []any, error) {
	*leafCount++
	if *leafCount > maxReportFilterRules {
		return "", nil, fmt.Errorf("report: more than %d filter rules", maxReportFilterRules)
	}
	f, ok := fields[fieldKey]
	if !ok {
		return "", nil, fmt.Errorf("report: unknown filter field %q", fieldKey)
	}
	allowed := reportOperatorsByType[f.Type]
	if allowed == nil || !allowed[operator] {
		return "", nil, fmt.Errorf("report: operator %q not valid for %s field %q", operator, f.Type, fieldKey)
	}

	raw, err := reportFieldExpr(ref, f)
	if err != nil {
		return "", nil, err
	}
	typed, err := reportTypedExpr(ref, f)
	if err != nil {
		return "", nil, err
	}

	switch operator {
	case "is_empty":
		if isReportTextual(f) {
			return "(" + raw + " IS NULL OR " + raw + " = '')", nil, nil
		}
		return raw + " IS NULL", nil, nil
	case "is_not_empty":
		if isReportTextual(f) {
			return "(" + raw + " IS NOT NULL AND " + raw + " <> '')", nil, nil
		}
		return raw + " IS NOT NULL", nil, nil
	case "eq":
		arg, err := reportValueArg(f, value)
		if err != nil {
			return "", nil, err
		}
		return typed + " = ?", []any{arg}, nil
	case "neq":
		arg, err := reportValueArg(f, value)
		if err != nil {
			return "", nil, err
		}
		return typed + " IS DISTINCT FROM ?", []any{arg}, nil
	case "gt", "gte", "lt", "lte":
		arg, err := reportValueArg(f, value)
		if err != nil {
			return "", nil, err
		}
		op := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[operator]
		return typed + " " + op + " ?", []any{arg}, nil
	case "contains", "not_contains", "starts_with", "ends_with":
		s, err := reportStringArg(value)
		if err != nil {
			return "", nil, err
		}
		pattern := escapeReportLike(s)
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
		items, err := reportListArg(f, value)
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
		return "", nil, fmt.Errorf("report: unknown operator %q", operator)
	}
}

func isReportTextual(f domain.ReportField) bool {
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

func reportValueArg(f domain.ReportField, v any) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("report: filter on %q needs a value", f.Key)
	}
	switch f.Type {
	case "number":
		n, err := reportFloatArg(v)
		if err != nil {
			return nil, fmt.Errorf("report: field %q expects a number: %w", f.Key, err)
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
		return nil, fmt.Errorf("report: field %q expects true/false", f.Key)
	default:
		s, err := reportStringArg(v)
		if err != nil {
			return nil, fmt.Errorf("report: field %q expects a string value: %w", f.Key, err)
		}
		return s, nil
	}
}

func reportListArg(f domain.ReportField, v any) ([]any, error) {
	list, ok := v.([]any)
	if !ok {
		// A single value is treated as a one-element list for convenience.
		single, err := reportValueArg(f, v)
		if err != nil {
			return nil, fmt.Errorf("report: in/not_in on %q expects a list", f.Key)
		}
		return []any{single}, nil
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		arg, err := reportValueArg(f, item)
		if err != nil {
			return nil, err
		}
		out = append(out, arg)
	}
	return out, nil
}

func reportFloatArg(v any) (float64, error) {
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

func reportStringArg(v any) (string, error) {
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

// escapeReportLike escapes LIKE wildcards in a user value so "50%" matches a
// literal percent sign instead of everything starting with 50.
func escapeReportLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
