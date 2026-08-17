package repository

import (
	"fmt"
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
// The builder applies the SAME row predicate as the list pages by calling the one
// builder in access_predicate.go — so a report can never show a row-scoped role a
// different set of records than its list pages do. It used to carry its own copy
// of the rule, which is precisely how the two drift.

// reportTableRef is the physical target of a report query.
type reportTableRef struct {
	Table      string // "contacts" | "deals" | "companies" | "custom_object_records"
	JSONColumn string // the row's JSONB blob: "custom_fields" (system) or "data" (custom)
	// ObjectDefID is set for custom_object_records, which multiplexes every
	// custom object into one table.
	ObjectDefID *uuid.UUID
	// Slug is the object slug — the record_shares.record_type discriminator, needed
	// to apply the row predicate to custom objects.
	Slug string
}

// reportScope is the caller's row identity as the runner extracts it from ctx.
type reportScope struct {
	Scope  string
	UserID uuid.UUID
	RoleID uuid.UUID
}

// rowPredicateFor returns the row filter for this report's table. An empty
// string means "this table genuinely has no row-level rule" — today only
// companies, which are org-wide and carry no owner column.
//
// It FAILS CLOSED. A table with no arm here is an ERROR, not an unfiltered
// report. The previous shape returned ("", nil) for anything it did not
// recognize, which meant the day a new table became reportable it would have
// shipped with org_id + deleted_at as its ONLY predicate — an org-wide read for
// every row-scoped rep, with no exception, no log line and no failing test.
// R9.5 made tasks and activities reportable and would have walked straight into
// it. A broken report is recoverable; a silent leak is not.
func rowPredicateFor(ref reportTableRef, orgID uuid.UUID, sc reportScope) (string, []any, error) {
	// Custom objects are checked FIRST, by def id rather than by name. They all
	// share one physical table, and nothing stops an org from slugging a custom
	// object "task" — dispatching on the slug before this would run those rows
	// through TaskAccessPredicate and emit `tasks.assigned_to` against
	// custom_object_records.
	if ref.ObjectDefID != nil {
		if ref.Slug == "" {
			return "", nil, fmt.Errorf("report: custom object has no slug for row scoping")
		}
		sql, args := RecordAccessPredicate(RecordAccessArgs{
			Table: ref.Table, RecordType: ref.Slug, OrgID: orgID,
			Scope: sc.Scope, UserID: sc.UserID, RoleID: sc.RoleID,
		})
		return sql, args, nil
	}

	switch ref.Table {
	case "contacts", "deals":
		sql, args := RecordAccessPredicate(RecordAccessArgs{
			Table: ref.Table, RecordType: recordTypeForTable(ref.Table), OrgID: orgID,
			Scope: sc.Scope, UserID: sc.UserID, RoleID: sc.RoleID,
		})
		return sql, args, nil
	case "companies":
		// Genuinely ownerless: a company is org-wide, has no owner_user_id, and
		// is never a record_shares target. Org scoping is the whole rule.
		return "", nil, nil
	case "tasks":
		// R9.5. The SAME predicate GET /api/tasks applies — see
		// TaskAccessPredicate's comment for why it is shared rather than copied.
		sql, args := TaskAccessPredicate(orgID, sc.Scope, sc.UserID, sc.RoleID)
		return sql, args, nil
	case "activities":
		sql, args := ActivityAccessPredicate(orgID, sc.Scope, sc.UserID, sc.RoleID)
		return sql, args, nil
	}
	return "", nil, fmt.Errorf("report: no row-access rule defined for table %q", ref.Table)
}

// The generic filter compiler (operator whitelist, leaf/group compilation,
// value coercion) moved to domain/filter_sql.go so record lists, reports and
// segments share one filter language. The names below are kept as thin
// delegates so this file's callers — and lead_score_sql.go, segment_sql.go,
// report_runner_repository.go and the tests — are unchanged.
const (
	maxReportFilterDepth = domain.MaxFilterDepth
	maxReportFilterRules = domain.MaxFilterRules
)

var reportBuckets = map[string]bool{
	"day": true, "week": true, "month": true, "quarter": true, "year": true,
}

// reportIdentRe validates every identifier the builder splices into SQL text
// (column names from the registry, JSONB keys, catalog field keys used as
// aliases) as belt-and-braces even though none of them originate from the
// request. Covers every column name and registry field key while excluding
// quotes and whitespace.
var reportIdentRe = domain.FilterIdentRe

// filterRef projects the report table ref onto the domain compiler's shape
// (ObjectDefID/Slug are row-scope concerns the pure compiler never sees).
func (ref reportTableRef) filterRef() domain.FilterTableRef {
	return domain.FilterTableRef{Table: ref.Table, JSONColumn: ref.JSONColumn}
}

// buildReportSQL translates one validated config into a parameterized query.
// sc mirrors the caller's row scope as the runner extracts it from ctx.
func buildReportSQL(ref reportTableRef, catalog []domain.ReportField, cfg domain.ReportConfig, orgID uuid.UUID, sc reportScope) (string, []any, error) {
	fields := make(map[string]domain.ReportField, len(catalog))
	for _, f := range catalog {
		fields[f.Key] = f
	}

	where, args, err := buildReportWhere(ref, fields, cfg, orgID, sc)
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
func buildReportWhere(ref reportTableRef, fields map[string]domain.ReportField, cfg domain.ReportConfig, orgID uuid.UUID, sc reportScope) (string, []any, error) {
	parts := []string{ref.Table + ".org_id = ?", ref.Table + ".deleted_at IS NULL"}
	args := []any{orgID}

	if ref.ObjectDefID != nil {
		parts = append(parts, ref.Table+".object_def_id = ?")
		args = append(args, *ref.ObjectDefID)
	}

	// The row predicate is the SAME one the list pages use (access_predicate.go):
	// a row-scoped caller sees what they own, what their team owns, and what is
	// shared to them. The error is not swallowed — see rowPredicateFor.
	rowSQL, rowArgs, err := rowPredicateFor(ref, orgID, sc)
	if err != nil {
		return "", nil, err
	}
	if rowSQL != "" {
		parts = append(parts, rowSQL)
		args = append(args, rowArgs...)
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
// emptiness checks, DISTINCT counts). Delegates to the shared compiler.
func reportFieldExpr(ref reportTableRef, f domain.ReportField) (string, error) {
	return domain.FilterFieldExpr(ref.filterRef(), f)
}

// reportTypedExpr is the field in its comparable type. Native columns are
// already typed; JSONB extractions get guarded casts so one row of dirty data
// (a non-numeric "sqft") NULLs out instead of killing the whole query.
func reportTypedExpr(ref reportTableRef, f domain.ReportField) (string, error) {
	return domain.FilterTypedExpr(ref.filterRef(), f)
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

// buildReportFilterGroup / buildReportFilterLeaf delegate to the shared
// compiler in domain/filter_sql.go — one grammar, one operator whitelist, one
// injection discipline for reports, segments and record lists alike.
func buildReportFilterGroup(ref reportTableRef, fields map[string]domain.ReportField, g domain.ReportFilterGroup, depth int, leafCount *int) (string, []any, error) {
	return domain.BuildFilterGroup(ref.filterRef(), fields, g, depth, leafCount)
}

// reportOperatorsByType gates which operators each field type accepts. The
// matrix now lives in domain (FilterOperatorsOrdered) so the schema endpoint
// serves the same list the compiler enforces.
var reportOperatorsByType = domain.FilterOperators()

func buildReportFilterLeaf(ref reportTableRef, fields map[string]domain.ReportField, fieldKey, operator string, value any, leafCount *int) (string, []any, error) {
	return domain.BuildFilterLeaf(ref.filterRef(), fields, fieldKey, operator, value, leafCount)
}

func isReportTextual(f domain.ReportField) bool { return domain.IsTextualFilterField(f) }

// ============================================================
// Value coercion (JSON → bind args)
// ============================================================

func reportValueArg(f domain.ReportField, v any) (any, error) { return domain.FilterValueArg(f, v) }

func reportListArg(f domain.ReportField, v any) ([]any, error) { return domain.FilterListArg(f, v) }

func reportFloatArg(v any) (float64, error) { return domain.FilterFloatArg(v) }

func reportStringArg(v any) (string, error) { return domain.FilterStringArg(v) }

// escapeReportLike escapes LIKE wildcards in a user value so "50%" matches a
// literal percent sign instead of everything starting with 50.
func escapeReportLike(s string) string { return domain.EscapeFilterLike(s) }
