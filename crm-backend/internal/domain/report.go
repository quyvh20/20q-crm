package domain

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ============================================================
// Reports (P9)
// ============================================================
//
// A report is a saved query definition over ONE object: an object slug plus a
// config (filters / group_by / aggregate / chart). The definition is data; the
// engine is stateless and re-runs the query for every viewer, so OLS, FLS and
// the caller's data scope always apply to whoever is LOOKING at the report,
// never to whoever authored it. visibility only gates the definition:
// 'private' = creator only, 'org' = every workspace member (data still
// per-viewer).

// Report visibility values (reports.visibility).
const (
	ReportVisibilityPrivate = "private"
	ReportVisibilityOrg     = "org"
)

// Report chart kinds (config.chart). "table" is the only kind that returns raw
// rows; "kpi" returns a single scalar; the rest return grouped aggregates.
const (
	ReportChartBar   = "bar"
	ReportChartLine  = "line"
	ReportChartPie   = "pie"
	ReportChartDonut = "donut"
	ReportChartKPI   = "kpi"
	ReportChartTable = "table"
)

// ReportResult kinds — which of the result's payload fields is populated.
const (
	ReportResultGroups = "groups"
	ReportResultRows   = "rows"
	ReportResultScalar = "scalar"
)

type Report struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	Name        string         `gorm:"size:255;not null" json:"name"`
	Description string         `gorm:"not null;default:''" json:"description"`
	ObjectSlug  string         `gorm:"size:100;not null" json:"object_slug"`
	Config      JSON           `gorm:"type:jsonb;default:'{}'" json:"config"`
	Visibility  string         `gorm:"size:10;not null;default:'private'" json:"visibility"`
	CreatedBy   *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
	// AccessLevel is the caller's effective level on this report (view/comment/
	// edit/manage), computed on read — never stored. Drives the frontend UI
	// (show Share/Edit only at the right level). Empty in list responses.
	AccessLevel string `gorm:"-" json:"access_level,omitempty"`
}

func (Report) TableName() string { return "reports" }

// ============================================================
// Report config (the JSONB stored in reports.config)
// ============================================================

// ReportConfig is the parsed reports.config. Every field key it references
// (filters, group_by, aggregate, columns, sort) must resolve in the object's
// report field catalog or the run is rejected — that resolution, together with
// the operator/bucket/function whitelists, is the SQL-injection boundary.
type ReportConfig struct {
	Version   int                `json:"version,omitempty"`
	Chart     string             `json:"chart"`
	Filters   *ReportFilterGroup `json:"filters,omitempty"`
	GroupBy   *ReportGroupBy     `json:"group_by,omitempty"`
	Aggregate *ReportAggregate   `json:"aggregate,omitempty"`
	// Columns selects the fields returned in table mode; ignored otherwise.
	Columns []string    `json:"columns,omitempty"`
	Sort    *ReportSort `json:"sort,omitempty"`
	// Limit is clamped server-side: grouped results to MaxReportGroups, table
	// rows to MaxReportRows. 0 means "use the default".
	Limit int `json:"limit,omitempty"`
}

// ReportFilterGroup / ReportFilterRule deliberately mirror the automation
// package's ConditionGroup/ConditionRule JSON shape (op/rules or
// field/operator/value), so the frontend can reuse its condition-builder UI and
// a workflow-literate admin sees one filter language everywhere. They are
// declared here rather than imported because domain must stay import-free of
// automation, and reports translate conditions to SQL instead of evaluating
// them in memory.
type ReportFilterGroup struct {
	Op    string             `json:"op,omitempty"` // "AND" | "OR"
	Rules []ReportFilterRule `json:"rules,omitempty"`
	// Leaf fields (mutually exclusive with Op/Rules)
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    any    `json:"value,omitempty"`
}

type ReportFilterRule struct {
	// Leaf fields
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    any    `json:"value,omitempty"`
	// Nested group fields
	Op    string             `json:"op,omitempty"`
	Rules []ReportFilterRule `json:"rules,omitempty"`
}

// IsGroup reports whether the rule is a nested group rather than a leaf.
func (r ReportFilterRule) IsGroup() bool { return r.Op != "" || len(r.Rules) > 0 }

// ReportGroupBy buckets rows by one field. Bucket applies to date fields only
// (day|week|month|quarter|year) and is whitelisted by the SQL builder.
type ReportGroupBy struct {
	Field  string `json:"field"`
	Bucket string `json:"bucket,omitempty"`
}

// ReportAggregate is the measure: count|count_distinct|sum|avg|min|max. Field
// is empty for count; sum/avg require a number field, min/max number or date.
type ReportAggregate struct {
	Fn    string `json:"fn"`
	Field string `json:"field,omitempty"`
}

// ReportSort orders grouped results by "value" (the aggregate) or "label" (the
// group key); in table mode By is a field key.
type ReportSort struct {
	By  string `json:"by"`
	Dir string `json:"dir"` // "asc" | "desc"
}

// Server-side clamps (config.Limit is a request, these are the law).
const (
	MaxReportGroups = 100
	MaxReportRows   = 1000
)

// ResultKind derives which result payload the config produces: "rows" for
// table, "scalar" for kpi, "groups" for every other chart.
func (c ReportConfig) ResultKind() string {
	switch c.Chart {
	case ReportChartTable:
		return ReportResultRows
	case ReportChartKPI:
		return ReportResultScalar
	default:
		return ReportResultGroups
	}
}

// ============================================================
// Report results
// ============================================================

// ReportGroup is one grouped bucket: the raw group key (date bucket, UUID,
// option value, bool…), its display label, the aggregate value, and how many
// rows the bucket holds.
type ReportGroup struct {
	Key   any     `json:"key"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Count int     `json:"count"`
}

// ReportResult is one report run. Kind says which payload is populated:
// "groups" (bar/line/pie/donut), "scalar" (kpi), or "rows" (table).
type ReportResult struct {
	Kind     string           `json:"kind"`
	Groups   []ReportGroup    `json:"groups,omitempty"`
	Columns  []string         `json:"columns,omitempty"`
	Rows     []map[string]any `json:"rows,omitempty"`
	Value    float64          `json:"value"`
	RowCount int              `json:"row_count"`
}

// ============================================================
// Field catalog
// ============================================================

// ReportField is one queryable field in an object's report catalog: either a
// registry field or a code-defined virtual field (created_at, owner_user_id,
// is_won… — native columns the registry deliberately doesn't describe). The
// catalog is assembled server-side per request; SQL addressing comes from
// Column/JSONKey, which are NEVER taken from user input.
type ReportField struct {
	Key   string
	Label string
	Type  string // text|number|date|select|boolean|url|relation
	// Exactly one of Column / JSONKey is set: a native column name, or a key
	// inside the row's JSONB blob (custom_fields for system tables, data for
	// custom_object_records).
	Column  string
	JSONKey string
	// LabelKind drives group-label resolution for UUID-valued groups: "stage"
	// (pipeline_stages.name), "user" (users), or a target object slug for
	// relations. Empty for self-labeling fields (text/select/bool/date).
	LabelKind string
	// Options are the select field's values, passed through so the builder UI
	// can offer them in filters.
	Options []string
}

// ReportFieldDescriptor is the catalog entry rendered for the builder UI's
// field pickers (FLS-hidden fields are already excluded for the caller).
type ReportFieldDescriptor struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

// ============================================================
// Ports
// ============================================================

var (
	ErrReportNotFound      = NewAppError(http.StatusNotFound, "report not found")
	ErrReportInvalidConfig = NewAppError(http.StatusBadRequest, "invalid report config")
)

// ReportRepository persists report definitions and resolves group labels.
type ReportRepository interface {
	Create(ctx context.Context, r *Report) error
	// GetByID returns the report or nil when absent/deleted (org-scoped).
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*Report, error)
	// GetByIDs batch-loads reports by id (absent/deleted ids are simply
	// missing from the map) — the dashboard's one-query summary join.
	GetByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*Report, error)
	// ListVisible returns every report the caller may see: their own, org-wide
	// ones, and reports shared with them directly, via their role, or via a
	// group they belong to (ident carries user/role/group handles). Newest first.
	ListVisible(ctx context.Context, orgID uuid.UUID, ident ShareIdentity) ([]Report, error)
	Update(ctx context.Context, r *Report) error
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) error
	// ResolveGroupLabels maps UUID group keys to display labels for one kind:
	// "stage" → pipeline_stages.name, "user" → the user's name, any object slug
	// → that object's display value. Unknown ids are simply absent.
	ResolveGroupLabels(ctx context.Context, orgID uuid.UUID, kind string, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

// ReportRunner executes one validated report config against an object's
// storage. Implemented in the repository layer (it builds SQL); reads the
// caller's data scope from ctx exactly like the typed repositories, so an
// own-scoped role sees its own numbers.
type ReportRunner interface {
	Run(ctx context.Context, orgID uuid.UUID, def *ObjectDef, catalog []ReportField, cfg ReportConfig) (*ReportResult, error)
}

// ReportInput creates or fully replaces a report definition.
type ReportInput struct {
	Name        string       `json:"name" binding:"required,min=1,max=255"`
	Description string       `json:"description"`
	ObjectSlug  string       `json:"object_slug" binding:"required"`
	Visibility  string       `json:"visibility"`
	Config      ReportConfig `json:"config"`
}

// ReportUseCase is the report surface: definition CRUD (visibility-gated) and
// execution (OLS/FLS/scope-gated per viewer).
type ReportUseCase interface {
	List(ctx context.Context, orgID, userID uuid.UUID) ([]Report, error)
	Create(ctx context.Context, orgID, userID uuid.UUID, in ReportInput) (*Report, error)
	Get(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*Report, error)
	Update(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID, in ReportInput) (*Report, error)
	Delete(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) error
	// Run executes a saved report for the current caller.
	Run(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*ReportResult, error)
	// Preview executes an unsaved config — the builder's live preview.
	Preview(ctx context.Context, orgID uuid.UUID, slug string, cfg ReportConfig) (*ReportResult, error)
	// ListFields returns the object's queryable catalog (registry + virtual
	// fields, minus the caller's FLS-hidden ones) for the builder UI.
	ListFields(ctx context.Context, orgID uuid.UUID, slug string) ([]ReportFieldDescriptor, error)
	// ResolveAccess loads a report plus the caller's effective level
	// (view/comment/edit/manage), 404ing when the caller has no grant. Sibling
	// usecases (share, comments) gate on the returned level.
	ResolveAccess(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*Report, string, error)
}

// ============================================================
// Dashboard widgets (P9, Phase B)
// ============================================================

// DashboardWidget is one report pinned to one user's dashboard. Widgets store
// only layout (position, size); the data comes from running the report per
// viewer like anywhere else.
type DashboardWidget struct {
	ID        uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	UserID    uuid.UUID `gorm:"type:uuid;not null" json:"user_id"`
	ReportID  uuid.UUID `gorm:"type:uuid;not null" json:"report_id"`
	Position  int       `gorm:"not null;default:0" json:"position"`
	Size      string    `gorm:"size:10;not null;default:'half'" json:"size"` // 'half' | 'full'
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (DashboardWidget) TableName() string { return "dashboard_widgets" }

// DashboardWidgetView is a widget with its report definition attached, so the
// dashboard renders titles/chart kinds without N follow-up fetches. Widgets
// whose report is gone or no longer visible to the caller are dropped from
// the listing entirely.
type DashboardWidgetView struct {
	DashboardWidget
	Report *Report `json:"report"`
}

type AddWidgetInput struct {
	ReportID uuid.UUID `json:"report_id" binding:"required"`
	Size     string    `json:"size"`
}

type UpdateWidgetInput struct {
	Size string `json:"size" binding:"required"`
}

type ReorderWidgetsInput struct {
	WidgetIDs []uuid.UUID `json:"widget_ids" binding:"required"`
}

// DashboardWidgetRepository persists the per-user widget rows.
type DashboardWidgetRepository interface {
	ListForUser(ctx context.Context, orgID, userID uuid.UUID) ([]DashboardWidget, error)
	// FindByReport returns the user's widget for a report, or nil — pinning is
	// idempotent.
	FindByReport(ctx context.Context, orgID, userID, reportID uuid.UUID) (*DashboardWidget, error)
	Create(ctx context.Context, w *DashboardWidget) error
	// UpdateSize updates one widget owned by the user. Returns rows affected.
	UpdateSize(ctx context.Context, orgID, userID, id uuid.UUID, size string) (int64, error)
	// Delete removes one widget owned by the user. Returns rows affected.
	Delete(ctx context.Context, orgID, userID, id uuid.UUID) (int64, error)
	// Reorder sets position = index of each id in the slice (ids not owned by
	// the user are ignored).
	Reorder(ctx context.Context, orgID, userID uuid.UUID, ids []uuid.UUID) error
	// NextPosition returns max(position)+1 for the user's dashboard.
	NextPosition(ctx context.Context, orgID, userID uuid.UUID) (int, error)
}

// DashboardUseCase manages the caller's own dashboard. There is no
// cross-user surface at all — every method is scoped to (org, caller).
type DashboardUseCase interface {
	ListWidgets(ctx context.Context, orgID, userID uuid.UUID) ([]DashboardWidgetView, error)
	AddWidget(ctx context.Context, orgID, userID uuid.UUID, in AddWidgetInput) (*DashboardWidget, error)
	UpdateWidget(ctx context.Context, orgID, userID, id uuid.UUID, in UpdateWidgetInput) error
	RemoveWidget(ctx context.Context, orgID, userID, id uuid.UUID) error
	Reorder(ctx context.Context, orgID, userID uuid.UUID, in ReorderWidgetsInput) error
}
