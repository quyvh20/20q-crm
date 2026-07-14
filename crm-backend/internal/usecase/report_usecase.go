package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// reportUseCase is the report surface (P9). Definitions are cheap rows; the
// value — and the risk — is in execution, so every run re-derives the caller's
// view from scratch in a fixed order:
//
//	visibility (may I see this DEFINITION?) →
//	OLS        (may I read this OBJECT at all?) →
//	FLS        (does the config reference a field hidden from me?) →
//	validate   (does every key/op/fn resolve in the catalog?) →
//	run        (data scope rides ctx into the runner) →
//	label      (resolve UUID group keys to names)
//
// FLS on filters/group/aggregate REJECTS instead of silently dropping: an
// aggregate over a hidden field is an oracle (filter salary > X, count rows),
// so the run must fail loudly rather than return a subtly different report.
type reportUseCase struct {
	repo     domain.ReportRepository
	runner   domain.ReportRunner
	registry domain.ObjectRegistryRepository
	authz    domain.RecordAuthorizer
	caps     domain.CapabilityChecker
	shares   domain.ReportShareRepository
}

func NewReportUseCase(repo domain.ReportRepository, runner domain.ReportRunner, registry domain.ObjectRegistryRepository, authz domain.RecordAuthorizer, caps domain.CapabilityChecker, shares domain.ReportShareRepository) domain.ReportUseCase {
	return &reportUseCase{repo: repo, runner: runner, registry: registry, authz: authz, caps: caps, shares: shares}
}

var (
	errReportObjectNotFound = domain.NewAppError(http.StatusNotFound, "object not found")
	errReportFieldHidden    = domain.NewAppError(http.StatusForbidden, "report references a field you cannot access")
)

// ============================================================
// Definition CRUD
// ============================================================

func (uc *reportUseCase) List(ctx context.Context, orgID, userID uuid.UUID) ([]domain.Report, error) {
	ident, err := uc.shares.GetShareIdentity(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	return uc.repo.ListVisible(ctx, orgID, ident)
}

func (uc *reportUseCase) Create(ctx context.Context, orgID, userID uuid.UUID, in domain.ReportInput) (*domain.Report, error) {
	cfgJSON, err := uc.validateInput(ctx, orgID, &in)
	if err != nil {
		return nil, err
	}
	rep := &domain.Report{
		OrgID:       orgID,
		Name:        strings.TrimSpace(in.Name),
		Description: in.Description,
		ObjectSlug:  in.ObjectSlug,
		Config:      cfgJSON,
		Visibility:  in.Visibility,
		CreatedBy:   &userID,
	}
	if err := uc.repo.Create(ctx, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func (uc *reportUseCase) Get(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*domain.Report, error) {
	rep, level, err := uc.getVisibleWithLevel(ctx, orgID, userID, id)
	if err != nil {
		return nil, err
	}
	rep.AccessLevel = level // drives the frontend (show Share/Edit at the right level)
	return rep, nil
}

func (uc *reportUseCase) Update(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID, in domain.ReportInput) (*domain.Report, error) {
	rep, level, err := uc.getVisibleWithLevel(ctx, orgID, userID, id)
	if err != nil {
		return nil, err
	}
	// An 'edit' share (or manage) may modify the definition.
	if !domain.ShareLevelAtLeast(level, domain.ShareLevelEdit) {
		return nil, domain.ErrForbidden
	}
	cfgJSON, err := uc.validateInput(ctx, orgID, &in)
	if err != nil {
		return nil, err
	}
	rep.Name = strings.TrimSpace(in.Name)
	rep.Description = in.Description
	rep.ObjectSlug = in.ObjectSlug
	rep.Config = cfgJSON
	rep.Visibility = in.Visibility
	if err := uc.repo.Update(ctx, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func (uc *reportUseCase) Delete(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) error {
	_, level, err := uc.getVisibleWithLevel(ctx, orgID, userID, id)
	if err != nil {
		return err
	}
	// Only an owner/creator/reports.manage (manage) may delete — an 'edit' share
	// can change a report but not destroy someone else's.
	if level != domain.ShareLevelManage {
		return domain.ErrForbidden
	}
	return uc.repo.SoftDelete(ctx, orgID, id)
}

// getVisible loads a report the caller may SEE (any level ≥ view); everything
// else 404s — a report the caller has no grant on is not disclosed.
func (uc *reportUseCase) getVisible(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*domain.Report, error) {
	rep, _, err := uc.getVisibleWithLevel(ctx, orgID, userID, id)
	return rep, err
}

// getVisibleWithLevel loads a report plus the caller's effective level, 404ing
// when the level is 'none'.
func (uc *reportUseCase) getVisibleWithLevel(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*domain.Report, string, error) {
	rep, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, "", err
	}
	if rep == nil {
		return nil, "", domain.ErrReportNotFound
	}
	level := uc.effectiveLevel(ctx, orgID, rep, userID)
	if level == domain.ShareLevelNone {
		return nil, "", domain.ErrReportNotFound
	}
	return rep, level, nil
}

// effectiveLevel resolves the caller's highest access level on a report:
// creator / reports.manage / owner → manage; otherwise the max of org-visibility
// (view) and any share matching the caller directly, by role, or by group.
func (uc *reportUseCase) effectiveLevel(ctx context.Context, orgID uuid.UUID, rep *domain.Report, userID uuid.UUID) string {
	if rep.CreatedBy != nil && *rep.CreatedBy == userID {
		return domain.ShareLevelManage
	}
	if uc.caps.HasCapability(ctx, orgID, domain.CapReportsManage) == nil {
		return domain.ShareLevelManage
	}

	best := domain.ShareLevelNone
	if rep.Visibility == domain.ReportVisibilityOrg {
		best = domain.ShareLevelView
	}

	ident, err := uc.shares.GetShareIdentity(ctx, orgID, userID)
	if err != nil {
		return best
	}
	rawShares, err := uc.shares.ListRawByReport(ctx, orgID, rep.ID)
	if err != nil {
		return best
	}
	groupSet := make(map[uuid.UUID]bool, len(ident.GroupIDs))
	for _, g := range ident.GroupIDs {
		groupSet[g] = true
	}
	for _, s := range rawShares {
		matches := (s.TargetType == domain.ShareTargetUser && s.TargetID == userID) ||
			(s.TargetType == domain.ShareTargetRole && s.TargetID == ident.RoleID) ||
			(s.TargetType == domain.ShareTargetGroup && groupSet[s.TargetID])
		if matches && domain.ShareLevelRank(s.Level) > domain.ShareLevelRank(best) {
			best = s.Level
		}
	}
	return best
}

// ResolveAccess loads a report plus the caller's effective level (404 when
// none) — the gate sibling usecases (share, comments) use.
func (uc *reportUseCase) ResolveAccess(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*domain.Report, string, error) {
	return uc.getVisibleWithLevel(ctx, orgID, userID, id)
}

// validateInput normalizes visibility, resolves the object, and validates the
// config against the caller's catalog + field mask, returning the normalized
// config JSON to store. Creating a report already requires OLS read on the
// object — you can't define reports over data you can't see.
func (uc *reportUseCase) validateInput(ctx context.Context, orgID uuid.UUID, in *domain.ReportInput) (domain.JSON, error) {
	switch in.Visibility {
	case "":
		in.Visibility = domain.ReportVisibilityPrivate
	case domain.ReportVisibilityPrivate, domain.ReportVisibilityOrg:
	default:
		return nil, domain.NewAppError(http.StatusBadRequest, "visibility must be 'private' or 'org'")
	}

	if err := uc.authz.Authorize(ctx, orgID, in.ObjectSlug, domain.ActionRead); err != nil {
		return nil, err
	}
	_, catalog, err := uc.catalogFor(ctx, orgID, in.ObjectSlug)
	if err != nil {
		return nil, err
	}
	mask := uc.authz.FieldMask(ctx, orgID, in.ObjectSlug)
	if err := validateReportConfig(&in.Config, reportFieldMap(catalog), mask); err != nil {
		return nil, err
	}

	raw, err := json.Marshal(in.Config)
	if err != nil {
		return nil, domain.ErrReportInvalidConfig
	}
	return domain.JSON(raw), nil
}

// ============================================================
// Execution
// ============================================================

func (uc *reportUseCase) Run(ctx context.Context, orgID, userID uuid.UUID, id uuid.UUID) (*domain.ReportResult, error) {
	rep, err := uc.getVisible(ctx, orgID, userID, id)
	if err != nil {
		return nil, err
	}
	var cfg domain.ReportConfig
	if err := json.Unmarshal(rep.Config, &cfg); err != nil {
		return nil, domain.ErrReportInvalidConfig
	}
	return uc.execute(ctx, orgID, rep.ObjectSlug, cfg)
}

func (uc *reportUseCase) Preview(ctx context.Context, orgID uuid.UUID, slug string, cfg domain.ReportConfig) (*domain.ReportResult, error) {
	return uc.execute(ctx, orgID, slug, cfg)
}

func (uc *reportUseCase) execute(ctx context.Context, orgID uuid.UUID, slug string, cfg domain.ReportConfig) (*domain.ReportResult, error) {
	if err := uc.authz.Authorize(ctx, orgID, slug, domain.ActionRead); err != nil {
		return nil, err
	}
	def, catalog, err := uc.catalogFor(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	fields := reportFieldMap(catalog)
	mask := uc.authz.FieldMask(ctx, orgID, slug)
	if err := validateReportConfig(&cfg, fields, mask); err != nil {
		return nil, err
	}

	res, err := uc.runner.Run(ctx, orgID, def, catalog, cfg)
	if err != nil {
		var appErr *domain.AppError
		if errors.As(err, &appErr) {
			return nil, err
		}
		// The builder prefixes its (defense-in-depth) validation failures;
		// anything else is a genuine database error.
		if strings.HasPrefix(err.Error(), "report:") {
			return nil, domain.NewAppError(http.StatusBadRequest, err.Error())
		}
		return nil, err
	}

	uc.labelGroups(ctx, orgID, res, fields, cfg)
	uc.labelTableRows(ctx, orgID, res, fields, cfg)
	return res, nil
}

// labelTableRows resolves relation columns in a table result to display names,
// so a `stage` / `owner` / `company` column shows "Closed Won" instead of a raw
// UUID. Same OLS gate as group labels: a relation to an object the caller can't
// read is left as the raw id rather than leaking a name. Non-relation columns
// (text/number/date/select) are already human-readable and untouched.
func (uc *reportUseCase) labelTableRows(ctx context.Context, orgID uuid.UUID, res *domain.ReportResult, fields map[string]domain.ReportField, cfg domain.ReportConfig) {
	if res.Kind != domain.ReportResultRows {
		return
	}
	for _, col := range cfg.Columns {
		f, ok := fields[col]
		if !ok || f.LabelKind == "" || !uc.canResolveLabels(ctx, orgID, f.LabelKind) {
			continue
		}
		var ids []uuid.UUID
		for _, row := range res.Rows {
			if s, ok := row[col].(string); ok {
				if id, err := uuid.Parse(s); err == nil {
					ids = append(ids, id)
				}
			}
		}
		if len(ids) == 0 {
			continue
		}
		labels, err := uc.repo.ResolveGroupLabels(ctx, orgID, f.LabelKind, ids)
		if err != nil {
			continue
		}
		for _, row := range res.Rows {
			if s, ok := row[col].(string); ok {
				if id, err := uuid.Parse(s); err == nil {
					if l, ok := labels[id]; ok && l != "" {
						row[col] = l
					}
				}
			}
		}
	}
}

func (uc *reportUseCase) ListFields(ctx context.Context, orgID uuid.UUID, slug string) ([]domain.ReportFieldDescriptor, error) {
	if err := uc.authz.Authorize(ctx, orgID, slug, domain.ActionRead); err != nil {
		return nil, err
	}
	_, catalog, err := uc.catalogFor(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	mask := uc.authz.FieldMask(ctx, orgID, slug)
	out := make([]domain.ReportFieldDescriptor, 0, len(catalog))
	for _, f := range catalog {
		if mask.IsHidden(f.Key) {
			continue
		}
		out = append(out, domain.ReportFieldDescriptor{Key: f.Key, Label: f.Label, Type: f.Type, Options: f.Options})
	}
	return out, nil
}

// ============================================================
// Field catalog (registry + virtual fields)
// ============================================================

func (uc *reportUseCase) catalogFor(ctx context.Context, orgID uuid.UUID, slug string) (*domain.ObjectDef, []domain.ReportField, error) {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, nil, err
	}
	def, err := uc.registry.GetDefBySlug(ctx, orgID, slug)
	if err != nil {
		return nil, nil, err
	}
	if def == nil {
		return nil, nil, errReportObjectNotFound
	}
	fields, err := uc.registry.ListFields(ctx, def.ID)
	if err != nil {
		return nil, nil, err
	}
	return def, reportCatalogForDef(def, fields), nil
}

// reportCatalogForDef turns registry fields into report catalog entries and
// appends the VIRTUAL fields — native columns the registry deliberately
// doesn't describe (created_at, owner, deal lifecycle) but reporting can't
// live without ("Revenue won by month" is exactly is_won + closed_at).
func reportCatalogForDef(def *domain.ObjectDef, fields []domain.ObjectField) []domain.ReportField {
	out := make([]domain.ReportField, 0, len(fields)+6)
	seen := make(map[string]bool, len(fields)+6)

	for _, f := range fields {
		// Mirror fields display another record's value; they have no storage of
		// their own to query.
		if f.ViaField != nil && *f.ViaField != "" {
			continue
		}
		rf := domain.ReportField{Key: f.Key, Label: f.Label, Type: f.Type, Options: parseOptions(f.Options)}
		if f.StorageKind == "column" && f.MapsToColumn != nil && *f.MapsToColumn != "" {
			rf.Column = *f.MapsToColumn
		} else {
			rf.JSONKey = f.Key
		}
		if f.Type == "relation" {
			if f.TargetSlug != nil && *f.TargetSlug != "" {
				rf.LabelKind = *f.TargetSlug
			} else if def.Slug == "deal" && f.Key == "stage" {
				// pipeline_stages isn't a registry object, so the stage field
				// carries no target_slug (P2) — labels resolve specially.
				rf.LabelKind = "stage"
			}
		}
		out = append(out, rf)
		seen[rf.Key] = true
	}

	addVirtual := func(f domain.ReportField) {
		if !seen[f.Key] {
			out = append(out, f)
			seen[f.Key] = true
		}
	}

	// Every reportable table (typed + custom_object_records) has timestamps.
	addVirtual(domain.ReportField{Key: "created_at", Label: "Created At", Type: "date", Column: "created_at"})
	addVirtual(domain.ReportField{Key: "updated_at", Label: "Updated At", Type: "date", Column: "updated_at"})

	// Owner is a real column on every object that has one (contact, deal, and — as
	// of U6.3 — every custom object), so it is reportable on all of them. Company is
	// the exception: it is org-wide and carries no owner.
	if def.Slug != "company" {
		addVirtual(domain.ReportField{Key: "owner_user_id", Label: "Owner", Type: "relation", Column: "owner_user_id", LabelKind: "user"})
	}

	if def.Slug == "deal" {
		addVirtual(domain.ReportField{Key: "is_won", Label: "Is Won", Type: "boolean", Column: "is_won"})
		addVirtual(domain.ReportField{Key: "is_lost", Label: "Is Lost", Type: "boolean", Column: "is_lost"})
		addVirtual(domain.ReportField{Key: "closed_at", Label: "Closed At", Type: "date", Column: "closed_at"})
	}
	return out
}

func reportFieldMap(catalog []domain.ReportField) map[string]domain.ReportField {
	m := make(map[string]domain.ReportField, len(catalog))
	for _, f := range catalog {
		m[f.Key] = f
	}
	return m
}

// ============================================================
// Config validation
// ============================================================

// validateReportConfig checks the config against the catalog and the caller's
// field mask, normalizing defaults in place (aggregate → count, visibility of
// hidden table columns). Hidden fields in filters/group/aggregate/sort are a
// hard 403 (see the type comment); hidden TABLE COLUMNS are silently dropped —
// a column is presentation, omitting it changes nothing else.
func validateReportConfig(cfg *domain.ReportConfig, fields map[string]domain.ReportField, mask domain.FieldMask) error {
	switch cfg.Chart {
	case domain.ReportChartBar, domain.ReportChartLine, domain.ReportChartPie, domain.ReportChartDonut:
		if cfg.GroupBy == nil || cfg.GroupBy.Field == "" {
			return domain.NewAppError(http.StatusBadRequest, "group_by is required for "+cfg.Chart+" charts")
		}
	case domain.ReportChartKPI:
		cfg.GroupBy = nil
	case domain.ReportChartTable:
		kept := cfg.Columns[:0]
		for _, key := range cfg.Columns {
			if _, ok := fields[key]; !ok {
				return domain.NewAppError(http.StatusBadRequest, "unknown column: "+key)
			}
			if mask.IsHidden(key) {
				continue
			}
			kept = append(kept, key)
		}
		cfg.Columns = kept
		if len(cfg.Columns) == 0 {
			return domain.NewAppError(http.StatusBadRequest, "table reports need at least one visible column")
		}
	default:
		return domain.NewAppError(http.StatusBadRequest, "unknown chart type: "+cfg.Chart)
	}

	if cfg.Chart != domain.ReportChartTable && (cfg.Aggregate == nil || cfg.Aggregate.Fn == "") {
		cfg.Aggregate = &domain.ReportAggregate{Fn: "count"}
	}

	// Every field the query will TOUCH (not merely display) must exist and be
	// visible to the caller.
	touched := make(map[string]bool)
	collectReportFilterFields(cfg.Filters, touched)
	if cfg.GroupBy != nil && cfg.GroupBy.Field != "" {
		touched[cfg.GroupBy.Field] = true
	}
	if cfg.Aggregate != nil && cfg.Aggregate.Field != "" {
		touched[cfg.Aggregate.Field] = true
	}
	if cfg.Sort != nil && cfg.Sort.By != "" {
		if cfg.Chart == domain.ReportChartTable {
			touched[cfg.Sort.By] = true
		} else if cfg.Sort.By != "value" && cfg.Sort.By != "label" {
			return domain.NewAppError(http.StatusBadRequest, `sort.by must be "value" or "label" for grouped charts`)
		}
	}

	for key := range touched {
		if _, ok := fields[key]; !ok {
			return domain.NewAppError(http.StatusBadRequest, "unknown field: "+key)
		}
		if mask.IsHidden(key) {
			return errReportFieldHidden
		}
	}
	return nil
}

func collectReportFilterFields(g *domain.ReportFilterGroup, keys map[string]bool) {
	if g == nil {
		return
	}
	if g.Field != "" {
		keys[g.Field] = true
	}
	var walk func(rules []domain.ReportFilterRule)
	walk = func(rules []domain.ReportFilterRule) {
		for _, r := range rules {
			if r.IsGroup() {
				walk(r.Rules)
			} else if r.Field != "" {
				keys[r.Field] = true
			}
		}
	}
	walk(g.Rules)
}

// ============================================================
// Group labeling
// ============================================================

// labelGroups fills each group's display label: UUID keys resolve through the
// label repository (stage names, user names, record displays); everything else
// labels itself (date buckets formatted per bucket, booleans as Yes/No, NULL
// as "(No value)"). Label failures never fail the report — worst case the raw
// key shows.
func (uc *reportUseCase) labelGroups(ctx context.Context, orgID uuid.UUID, res *domain.ReportResult, fields map[string]domain.ReportField, cfg domain.ReportConfig) {
	if res.Kind != domain.ReportResultGroups || cfg.GroupBy == nil {
		return
	}
	gf := fields[cfg.GroupBy.Field]
	bucket := cfg.GroupBy.Bucket
	if bucket == "" {
		bucket = "month"
	}

	var labels map[uuid.UUID]string
	if gf.LabelKind != "" && uc.canResolveLabels(ctx, orgID, gf.LabelKind) {
		var ids []uuid.UUID
		for _, g := range res.Groups {
			if s, ok := g.Key.(string); ok {
				if id, err := uuid.Parse(s); err == nil {
					ids = append(ids, id)
				}
			}
		}
		if resolved, err := uc.repo.ResolveGroupLabels(ctx, orgID, gf.LabelKind, ids); err == nil {
			labels = resolved
		}
	}

	for i := range res.Groups {
		key := res.Groups[i].Key
		if labels != nil {
			if s, ok := key.(string); ok {
				if id, err := uuid.Parse(s); err == nil {
					if l, ok := labels[id]; ok && l != "" {
						res.Groups[i].Label = l
					} else {
						res.Groups[i].Label = "(Unknown)"
					}
					continue
				}
			}
		}
		res.Groups[i].Label = reportSelfLabel(key, bucket)
	}

	// The SQL can only ORDER BY the raw group key, which for a relation group is
	// an opaque UUID — so a "sort by label" on such a group comes back in UUID
	// order. Re-sort by the resolved label here so the rendered order matches
	// what the axis reads. (Group cardinality is capped at MaxReportGroups, so
	// this is a stable in-memory sort over a small slice.)
	if cfg.Sort != nil && cfg.Sort.By == "label" {
		desc := strings.EqualFold(cfg.Sort.Dir, "desc")
		sort.SliceStable(res.Groups, func(i, j int) bool {
			if desc {
				return res.Groups[i].Label > res.Groups[j].Label
			}
			return res.Groups[i].Label < res.Groups[j].Label
		})
	}
}

// canResolveLabels gates group-label resolution behind the caller's OLS. Label
// kinds "stage" and "user" are workspace config / membership, not OLS objects,
// so they always resolve. A relation LabelKind is a registry object slug: the
// caller must have read on it, otherwise grouping a readable object by a
// relation to an UNreadable one would leak the target's display names. On
// denial the labels fall through to "(Unknown)".
func (uc *reportUseCase) canResolveLabels(ctx context.Context, orgID uuid.UUID, kind string) bool {
	if kind == "stage" || kind == "user" {
		return true
	}
	return uc.authz.Authorize(ctx, orgID, kind, domain.ActionRead) == nil
}

func reportSelfLabel(key any, bucket string) string {
	if key == nil {
		return "(No value)"
	}
	switch v := key.(type) {
	case time.Time:
		switch bucket {
		case "day":
			return v.Format("2006-01-02")
		case "week":
			return "Wk of " + v.Format("Jan 2, 2006")
		case "quarter":
			return "Q" + strconv.Itoa((int(v.Month())-1)/3+1) + " " + strconv.Itoa(v.Year())
		case "year":
			return strconv.Itoa(v.Year())
		default:
			return v.Format("Jan 2006")
		}
	case bool:
		if v {
			return "Yes"
		}
		return "No"
	case string:
		if v == "" {
			return "(No value)"
		}
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return fmt.Sprintf("%v", key)
}
