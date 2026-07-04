package usecase

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes (fakeRegistryRepo and fakeAuthorizer are shared with the other
// usecase tests in this package)
// ============================================================

type fakeReportRepo struct {
	reports map[uuid.UUID]*domain.Report
	labels  map[uuid.UUID]string
	// labelKind records which kind ResolveGroupLabels was asked for.
	labelKind string
}

func newFakeReportRepo() *fakeReportRepo {
	return &fakeReportRepo{reports: map[uuid.UUID]*domain.Report{}}
}

func (f *fakeReportRepo) Create(_ context.Context, r *domain.Report) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	f.reports[r.ID] = r
	return nil
}

func (f *fakeReportRepo) GetByID(_ context.Context, _ uuid.UUID, id uuid.UUID) (*domain.Report, error) {
	return f.reports[id], nil
}

func (f *fakeReportRepo) GetByIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*domain.Report, error) {
	out := map[uuid.UUID]*domain.Report{}
	for _, id := range ids {
		if r, ok := f.reports[id]; ok {
			out[id] = r
		}
	}
	return out, nil
}

func (f *fakeReportRepo) ListVisible(_ context.Context, _ uuid.UUID, userID uuid.UUID) ([]domain.Report, error) {
	var out []domain.Report
	for _, r := range f.reports {
		if r.Visibility == domain.ReportVisibilityOrg || (r.CreatedBy != nil && *r.CreatedBy == userID) {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeReportRepo) Update(_ context.Context, r *domain.Report) error {
	f.reports[r.ID] = r
	return nil
}

func (f *fakeReportRepo) SoftDelete(_ context.Context, _ uuid.UUID, id uuid.UUID) error {
	delete(f.reports, id)
	return nil
}

func (f *fakeReportRepo) ResolveGroupLabels(_ context.Context, _ uuid.UUID, kind string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	f.labelKind = kind
	out := map[uuid.UUID]string{}
	for _, id := range ids {
		if l, ok := f.labels[id]; ok {
			out[id] = l
		}
	}
	return out, nil
}

type fakeReportRunner struct {
	result  *domain.ReportResult
	err     error
	lastCfg domain.ReportConfig
	catalog []domain.ReportField
	called  bool
}

func (f *fakeReportRunner) Run(_ context.Context, _ uuid.UUID, _ *domain.ObjectDef, catalog []domain.ReportField, cfg domain.ReportConfig) (*domain.ReportResult, error) {
	f.called = true
	f.lastCfg = cfg
	f.catalog = catalog
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &domain.ReportResult{Kind: cfg.ResultKind()}, nil
}

// fakeCaps answers HasCapability with a fixed allow/deny.
type fakeCaps struct{ allow bool }

func (f *fakeCaps) HasCapability(context.Context, uuid.UUID, string) error {
	if f.allow {
		return nil
	}
	return domain.ErrForbidden
}
func (f *fakeCaps) CallerCapabilities(context.Context, uuid.UUID) []string { return nil }

// ============================================================
// Test scaffolding
// ============================================================

func reportStrPtr(s string) *string { return &s }

func reportTestRegistry() *fakeRegistryRepo {
	dealDefID := uuid.MustParse("aaaaaaaa-0000-0000-0000-00000000dea1")
	table := "deals"
	return &fakeRegistryRepo{
		defs: []domain.ObjectDef{{
			ID: dealDefID, Slug: "deal", Label: "Deal", LabelPlural: "Deals",
			IsSystem: true, Storage: "table", RecordTable: &table,
		}},
		fields: map[uuid.UUID][]domain.ObjectField{
			dealDefID: {
				{Key: "title", Label: "Title", Type: "text", StorageKind: "column", MapsToColumn: reportStrPtr("title"), IsSystem: true},
				{Key: "value", Label: "Value", Type: "number", StorageKind: "column", MapsToColumn: reportStrPtr("value"), IsSystem: true},
				{Key: "stage", Label: "Stage", Type: "relation", StorageKind: "column", MapsToColumn: reportStrPtr("stage_id"), IsSystem: true},
				{Key: "company", Label: "Company", Type: "relation", StorageKind: "column", MapsToColumn: reportStrPtr("company_id"), TargetSlug: reportStrPtr("company"), IsSystem: true},
				{Key: "priority", Label: "Priority", Type: "select", StorageKind: "jsonb", Options: domain.JSON(`["low","high"]`)},
			},
		},
	}
}

type reportEnv struct {
	uc     domain.ReportUseCase
	repo   *fakeReportRepo
	runner *fakeReportRunner
	authz  *fakeAuthorizer
	caps   *fakeCaps
	orgID  uuid.UUID
}

func newReportEnv() *reportEnv {
	repo := newFakeReportRepo()
	runner := &fakeReportRunner{}
	authz := &fakeAuthorizer{}
	caps := &fakeCaps{}
	return &reportEnv{
		uc:     NewReportUseCase(repo, runner, reportTestRegistry(), authz, caps),
		repo:   repo,
		runner: runner,
		authz:  authz,
		caps:   caps,
		orgID:  uuid.New(),
	}
}

func barByStage() domain.ReportConfig {
	return domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "stage"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
	}
}

func mustCreateReport(t *testing.T, env *reportEnv, creator uuid.UUID, visibility string) *domain.Report {
	t.Helper()
	rep, err := env.uc.Create(context.Background(), env.orgID, creator, domain.ReportInput{
		Name: "Pipeline", ObjectSlug: "deal", Visibility: visibility, Config: barByStage(),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	return rep
}

func appErrCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T: %v", err, err)
	}
	return appErr.Code
}

// ============================================================
// Security ordering
// ============================================================

func TestReport_OLSDeniedPreview(t *testing.T) {
	env := newReportEnv()
	env.authz.deny = map[string]bool{"deal:read": true}
	_, err := env.uc.Preview(context.Background(), env.orgID, "deal", barByStage())
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", code)
	}
	if env.runner.called {
		t.Error("a denied preview must not reach the runner")
	}
}

func TestReport_FLSHiddenGroupByRejected(t *testing.T) {
	env := newReportEnv()
	env.authz.masks = map[string]domain.FieldMask{
		"deal": {Hidden: map[string]bool{"value": true}},
	}
	cfg := domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "stage"},
		Aggregate: &domain.ReportAggregate{Fn: "sum", Field: "value"},
	}
	_, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", code)
	}
	if env.runner.called {
		t.Error("an FLS-rejected preview must not reach the runner")
	}
}

func TestReport_FLSHiddenFilterRejected(t *testing.T) {
	env := newReportEnv()
	env.authz.masks = map[string]domain.FieldMask{
		"deal": {Hidden: map[string]bool{"value": true}},
	}
	cfg := barByStage()
	cfg.Filters = &domain.ReportFilterGroup{Op: "AND", Rules: []domain.ReportFilterRule{
		{Field: "value", Operator: "gt", Value: float64(100000)}, // the aggregation oracle
	}}
	_, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if code := appErrCode(t, err); code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", code)
	}
}

func TestReport_FLSHiddenTableColumnDropped(t *testing.T) {
	env := newReportEnv()
	env.authz.masks = map[string]domain.FieldMask{
		"deal": {Hidden: map[string]bool{"value": true}},
	}
	cfg := domain.ReportConfig{Chart: domain.ReportChartTable, Columns: []string{"title", "value"}}
	_, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env.runner.lastCfg.Columns) != 1 || env.runner.lastCfg.Columns[0] != "title" {
		t.Errorf("hidden column not dropped: %v", env.runner.lastCfg.Columns)
	}
}

func TestReport_UnknownFieldRejected(t *testing.T) {
	env := newReportEnv()
	cfg := barByStage()
	cfg.GroupBy.Field = "password_hash"
	_, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
}

// ============================================================
// Visibility + management gating
// ============================================================

func TestReport_PrivateReportInvisibleToOthers(t *testing.T) {
	env := newReportEnv()
	creator, stranger := uuid.New(), uuid.New()
	rep := mustCreateReport(t, env, creator, domain.ReportVisibilityPrivate)

	if _, err := env.uc.Get(context.Background(), env.orgID, stranger, rep.ID); err != domain.ErrReportNotFound {
		t.Errorf("stranger Get = %v, want ErrReportNotFound", err)
	}
	if _, err := env.uc.Run(context.Background(), env.orgID, stranger, rep.ID); err != domain.ErrReportNotFound {
		t.Errorf("stranger Run = %v, want ErrReportNotFound", err)
	}
	if _, err := env.uc.Get(context.Background(), env.orgID, creator, rep.ID); err != nil {
		t.Errorf("creator Get = %v, want nil", err)
	}
}

func TestReport_OrgReportVisibleButEditGated(t *testing.T) {
	env := newReportEnv()
	creator, stranger := uuid.New(), uuid.New()
	rep := mustCreateReport(t, env, creator, domain.ReportVisibilityOrg)

	if _, err := env.uc.Get(context.Background(), env.orgID, stranger, rep.ID); err != nil {
		t.Fatalf("org report should be visible to members: %v", err)
	}

	in := domain.ReportInput{Name: "Renamed", ObjectSlug: "deal", Visibility: "org", Config: barByStage()}

	// Without reports.manage the stranger cannot edit or delete.
	env.caps.allow = false
	if _, err := env.uc.Update(context.Background(), env.orgID, stranger, rep.ID, in); err == nil {
		t.Error("stranger update should be forbidden")
	}
	if err := env.uc.Delete(context.Background(), env.orgID, stranger, rep.ID); err == nil {
		t.Error("stranger delete should be forbidden")
	}

	// With reports.manage (or the owner role) they can.
	env.caps.allow = true
	if _, err := env.uc.Update(context.Background(), env.orgID, stranger, rep.ID, in); err != nil {
		t.Errorf("manager update failed: %v", err)
	}

	// The creator always can, capability or not.
	env.caps.allow = false
	if _, err := env.uc.Update(context.Background(), env.orgID, creator, rep.ID, in); err != nil {
		t.Errorf("creator update failed: %v", err)
	}
}

func TestReport_InvalidVisibilityRejected(t *testing.T) {
	env := newReportEnv()
	_, err := env.uc.Create(context.Background(), env.orgID, uuid.New(), domain.ReportInput{
		Name: "X", ObjectSlug: "deal", Visibility: "public", Config: barByStage(),
	})
	if code := appErrCode(t, err); code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", code)
	}
}

// ============================================================
// Catalog + labeling
// ============================================================

func TestReport_ListFieldsIncludesVirtualAndAppliesFLS(t *testing.T) {
	env := newReportEnv()
	env.authz.masks = map[string]domain.FieldMask{
		"deal": {Hidden: map[string]bool{"value": true}},
	}
	fields, err := env.uc.ListFields(context.Background(), env.orgID, "deal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byKey := map[string]bool{}
	for _, f := range fields {
		byKey[f.Key] = true
	}
	for _, want := range []string{"title", "stage", "priority", "created_at", "updated_at", "owner_user_id", "is_won", "is_lost", "closed_at"} {
		if !byKey[want] {
			t.Errorf("catalog missing %q", want)
		}
	}
	if byKey["value"] {
		t.Error("FLS-hidden field leaked into the catalog")
	}
}

func TestReport_GroupLabelsResolved(t *testing.T) {
	env := newReportEnv()
	stageID := uuid.New()
	env.repo.labels = map[uuid.UUID]string{stageID: "Negotiation"}
	env.runner.result = &domain.ReportResult{
		Kind: domain.ReportResultGroups,
		Groups: []domain.ReportGroup{
			{Key: stageID.String(), Value: 5, Count: 5},
			{Key: uuid.New().String(), Value: 2, Count: 2}, // deleted stage
			{Key: nil, Value: 1, Count: 1},                 // stage-less deals
		},
	}
	res, err := env.uc.Preview(context.Background(), env.orgID, "deal", barByStage())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.repo.labelKind != "stage" {
		t.Errorf("labelKind = %q, want stage", env.repo.labelKind)
	}
	if res.Groups[0].Label != "Negotiation" {
		t.Errorf("label[0] = %q", res.Groups[0].Label)
	}
	if res.Groups[1].Label != "(Unknown)" {
		t.Errorf("label[1] = %q", res.Groups[1].Label)
	}
	if res.Groups[2].Label != "(No value)" {
		t.Errorf("label[2] = %q", res.Groups[2].Label)
	}
}

// Grouping a readable object by a relation to an object the caller CANNOT read
// must not resolve — and thus must not leak — the target's display names.
func TestReport_RelationLabelsGatedByTargetOLS(t *testing.T) {
	env := newReportEnv()
	env.authz.deny = map[string]bool{"company:read": true} // deal:read still allowed
	companyID := uuid.New()
	env.repo.labels = map[uuid.UUID]string{companyID: "Acme Corp"}
	env.runner.result = &domain.ReportResult{
		Kind:   domain.ReportResultGroups,
		Groups: []domain.ReportGroup{{Key: companyID.String(), Value: 3, Count: 3}},
	}
	cfg := domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "company"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
	}
	res, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The label repo must not have been consulted for the unreadable target.
	if env.repo.labelKind != "" {
		t.Errorf("ResolveGroupLabels called for an unreadable target (kind=%q)", env.repo.labelKind)
	}
	if res.Groups[0].Label == "Acme Corp" {
		t.Error("leaked a company display name the caller cannot read")
	}
}

func TestReport_GroupedSortByLabelReordersResolvedLabels(t *testing.T) {
	env := newReportEnv()
	// Two stages returned by the runner in value order; sort by label asc must
	// reorder them alphabetically by the RESOLVED name, not the UUID key.
	zoe, aaron := uuid.New(), uuid.New()
	env.repo.labels = map[uuid.UUID]string{zoe: "Zoe", aaron: "Aaron"}
	env.runner.result = &domain.ReportResult{
		Kind: domain.ReportResultGroups,
		Groups: []domain.ReportGroup{
			{Key: zoe.String(), Value: 10, Count: 10},
			{Key: aaron.String(), Value: 5, Count: 5},
		},
	}
	cfg := domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "stage"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
		Sort:      &domain.ReportSort{By: "label", Dir: "asc"},
	}
	res, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Groups[0].Label != "Aaron" || res.Groups[1].Label != "Zoe" {
		t.Errorf("label sort not applied to resolved labels: %q, %q", res.Groups[0].Label, res.Groups[1].Label)
	}
}

func TestReport_TableRelationColumnsLabeled(t *testing.T) {
	env := newReportEnv()
	stageID := uuid.New()
	unknownID := uuid.New()
	env.repo.labels = map[uuid.UUID]string{stageID: "Closed Won"}
	env.runner.result = &domain.ReportResult{
		Kind:    domain.ReportResultRows,
		Columns: []string{"title", "stage"},
		Rows: []map[string]any{
			{"title": "Big Deal", "stage": stageID.String()},
			{"title": "Orphan", "stage": unknownID.String()},
		},
	}
	cfg := domain.ReportConfig{Chart: domain.ReportChartTable, Columns: []string{"title", "stage"}}
	res, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.repo.labelKind != "stage" {
		t.Errorf("labelKind = %q, want stage", env.repo.labelKind)
	}
	if res.Rows[0]["stage"] != "Closed Won" {
		t.Errorf("relation column not labeled: %v", res.Rows[0]["stage"])
	}
	// Unknown ids are left as the raw value (a raw id beats a misleading label).
	if res.Rows[1]["stage"] != unknownID.String() {
		t.Errorf("unknown id should stay raw: %v", res.Rows[1]["stage"])
	}
	// Non-relation columns are untouched.
	if res.Rows[0]["title"] != "Big Deal" {
		t.Errorf("text column mangled: %v", res.Rows[0]["title"])
	}
}

func TestReport_TableRelationColumnsGatedByOLS(t *testing.T) {
	env := newReportEnv()
	env.authz.deny = map[string]bool{"company:read": true}
	companyID := uuid.New()
	env.repo.labels = map[uuid.UUID]string{companyID: "Acme Corp"}
	env.runner.result = &domain.ReportResult{
		Kind:    domain.ReportResultRows,
		Columns: []string{"title", "company"},
		Rows:    []map[string]any{{"title": "Deal", "company": companyID.String()}},
	}
	cfg := domain.ReportConfig{Chart: domain.ReportChartTable, Columns: []string{"title", "company"}}
	res, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Rows[0]["company"] == "Acme Corp" {
		t.Error("leaked a company name into a table column the caller cannot read")
	}
}

func TestReport_DateBucketLabels(t *testing.T) {
	env := newReportEnv()
	jan := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	env.runner.result = &domain.ReportResult{
		Kind:   domain.ReportResultGroups,
		Groups: []domain.ReportGroup{{Key: jan, Value: 10, Count: 3}},
	}
	cfg := domain.ReportConfig{
		Chart:     domain.ReportChartLine,
		GroupBy:   &domain.ReportGroupBy{Field: "created_at", Bucket: "quarter"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
	}
	res, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Groups[0].Label != "Q1 2026" {
		t.Errorf("label = %q, want Q1 2026", res.Groups[0].Label)
	}
}

func TestReport_RunParsesStoredConfig(t *testing.T) {
	env := newReportEnv()
	creator := uuid.New()
	rep := mustCreateReport(t, env, creator, domain.ReportVisibilityPrivate)

	// The stored config round-trips: Run must hand the runner the same shape.
	if _, err := env.uc.Run(context.Background(), env.orgID, creator, rep.ID); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !env.runner.called {
		t.Fatal("runner not called")
	}
	var stored domain.ReportConfig
	if err := json.Unmarshal(rep.Config, &stored); err != nil {
		t.Fatalf("stored config does not parse: %v", err)
	}
	if stored.Chart != domain.ReportChartBar || stored.GroupBy == nil || stored.GroupBy.Field != "stage" {
		t.Errorf("stored config mangled: %+v", stored)
	}
}

func TestReport_DefaultAggregateIsCount(t *testing.T) {
	env := newReportEnv()
	cfg := domain.ReportConfig{Chart: domain.ReportChartKPI}
	if _, err := env.uc.Preview(context.Background(), env.orgID, "deal", cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env.runner.lastCfg.Aggregate == nil || env.runner.lastCfg.Aggregate.Fn != "count" {
		t.Errorf("aggregate not defaulted to count: %+v", env.runner.lastCfg.Aggregate)
	}
}

func TestReport_UnknownObject404(t *testing.T) {
	env := newReportEnv()
	_, err := env.uc.Preview(context.Background(), env.orgID, "unicorn", barByStage())
	if code := appErrCode(t, err); code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", code)
	}
}
