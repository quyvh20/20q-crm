package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// reportRunnerRepository executes validated report configs: it derives the
// physical table from the object def, reads the caller's data scope from ctx
// (same channel as every typed repository — see scopes.go), hands both to the
// pure SQL builder, and normalizes the driver's raw rows into a ReportResult.
type reportRunnerRepository struct {
	db *gorm.DB
}

func NewReportRunnerRepository(db *gorm.DB) domain.ReportRunner {
	return &reportRunnerRepository{db: db}
}

// reportRefForDef maps an object def to its physical storage: system objects
// live in their typed table with a custom_fields blob; custom objects share
// custom_object_records, discriminated by object_def_id.
func reportRefForDef(def *domain.ObjectDef) (reportTableRef, error) {
	if def.Storage == "table" && def.RecordTable != nil && *def.RecordTable != "" {
		table := *def.RecordTable
		if !reportIdentRe.MatchString(table) {
			return reportTableRef{}, fmt.Errorf("report: invalid record table %q", table)
		}
		return reportTableRef{Table: table, JSONColumn: "custom_fields", Slug: def.Slug}, nil
	}
	defID := def.ID
	return reportTableRef{Table: "custom_object_records", JSONColumn: "data", ObjectDefID: &defID, Slug: def.Slug}, nil
}

func (r *reportRunnerRepository) Run(ctx context.Context, orgID uuid.UUID, def *domain.ObjectDef, catalog []domain.ReportField, cfg domain.ReportConfig) (*domain.ReportResult, error) {
	ref, err := reportRefForDef(def)
	if err != nil {
		return nil, err
	}

	scope, scopeUserID, scopeRoleID, ok := extractCallerScope(ctx)
	if !ok {
		// No caller on the context — a trusted in-process run (no HTTP request).
		scope = domain.DataScopeAll
	}

	query, args, err := buildReportSQL(ref, catalog, cfg, orgID, reportScope{Scope: scope, UserID: scopeUserID, RoleID: scopeRoleID})
	if err != nil {
		return nil, err
	}

	var rows []map[string]any
	if err := r.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := &domain.ReportResult{Kind: cfg.ResultKind()}
	switch result.Kind {
	case domain.ReportResultScalar:
		if len(rows) > 0 {
			result.Value = reportToFloat(rows[0]["agg_value"])
			result.RowCount = int(reportToFloat(rows[0]["row_count"]))
		}
	case domain.ReportResultGroups:
		result.Groups = make([]domain.ReportGroup, 0, len(rows))
		for _, row := range rows {
			g := domain.ReportGroup{
				Key:   reportNormalizeValue(row["group_key"]),
				Value: reportToFloat(row["agg_value"]),
				Count: int(reportToFloat(row["row_count"])),
			}
			result.Groups = append(result.Groups, g)
			result.RowCount += g.Count
		}
	case domain.ReportResultRows:
		result.Columns = append([]string{}, cfg.Columns...)
		result.Rows = make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			clean := make(map[string]any, len(row))
			for k, v := range row {
				clean[k] = reportNormalizeValue(v)
			}
			result.Rows = append(result.Rows, clean)
		}
		result.RowCount = len(result.Rows)
	}
	return result, nil
}

// reportNormalizeValue flattens driver-specific scan types ([]byte strings,
// numeric byte slices) into JSON-friendly values. time.Time passes through so
// the usecase can label date buckets from the typed value.
func reportNormalizeValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x
	default:
		return v
	}
}

// reportToFloat coerces the driver's representation of an aggregate (NUMERIC
// often scans as string/[]byte, COUNT as int64) into a float64.
func reportToFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case []byte:
		f, _ := strconv.ParseFloat(string(x), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
