package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// reportRepository persists saved report definitions (reports table) and
// resolves grouped UUID keys to display labels. Execution lives in the runner
// (report_runner_repository.go); this type never touches record data.
type reportRepository struct {
	db *gorm.DB
}

func NewReportRepository(db *gorm.DB) domain.ReportRepository {
	return &reportRepository{db: db}
}

func (r *reportRepository) Create(ctx context.Context, rep *domain.Report) error {
	return r.db.WithContext(ctx).Create(rep).Error
}

func (r *reportRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Report, error) {
	var rep domain.Report
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&rep).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rep, nil
}

func (r *reportRepository) GetByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*domain.Report, error) {
	out := make(map[uuid.UUID]*domain.Report, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var reps []domain.Report
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND id IN ?", orgID, ids).
		Find(&reps).Error; err != nil {
		return nil, err
	}
	for i := range reps {
		out[reps[i].ID] = &reps[i]
	}
	return out, nil
}

// ListVisible returns the caller's own reports plus the org-shared ones,
// newest first. Private reports of OTHER users never leave the database.
func (r *reportRepository) ListVisible(ctx context.Context, orgID, userID uuid.UUID) ([]domain.Report, error) {
	var reps []domain.Report
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND (visibility = ? OR created_by = ?)", orgID, domain.ReportVisibilityOrg, userID).
		Order("updated_at DESC").
		Find(&reps).Error
	return reps, err
}

func (r *reportRepository) Update(ctx context.Context, rep *domain.Report) error {
	return r.db.WithContext(ctx).Save(rep).Error
}

func (r *reportRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.Report{}).Error
}

// ResolveGroupLabels maps UUID group keys to display labels for one kind.
// Kinds mirror ReportField.LabelKind: "stage" and "user" are special cases
// (pipeline_stages isn't a registry object; users aren't records at all), the
// three system slugs read their typed tables, and anything else is a custom
// object resolved via display_name. Unknown ids are simply absent from the map
// — the caller falls back to the raw id.
func (r *reportRepository) ResolveGroupLabels(ctx context.Context, orgID uuid.UUID, kind string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	type row struct {
		ID    uuid.UUID
		Label string
	}
	var rows []row
	var err error

	switch kind {
	case "stage":
		err = r.db.WithContext(ctx).Raw(
			`SELECT id, name AS label FROM pipeline_stages WHERE org_id = ? AND id IN ?`,
			orgID, ids).Scan(&rows).Error
	case "user":
		err = r.db.WithContext(ctx).Raw(
			`SELECT id, COALESCE(NULLIF(full_name, ''), NULLIF(TRIM(first_name || ' ' || last_name), ''), email) AS label
			 FROM users WHERE org_id = ? AND id IN ?`,
			orgID, ids).Scan(&rows).Error
	case "contact":
		err = r.db.WithContext(ctx).Raw(
			`SELECT id, TRIM(COALESCE(first_name, '') || ' ' || COALESCE(last_name, '')) AS label
			 FROM contacts WHERE org_id = ? AND id IN ?`,
			orgID, ids).Scan(&rows).Error
	case "company":
		err = r.db.WithContext(ctx).Raw(
			`SELECT id, name AS label FROM companies WHERE org_id = ? AND id IN ?`,
			orgID, ids).Scan(&rows).Error
	case "deal":
		err = r.db.WithContext(ctx).Raw(
			`SELECT id, title AS label FROM deals WHERE org_id = ? AND id IN ?`,
			orgID, ids).Scan(&rows).Error
	default:
		// A custom object slug: labels come from the record's display_name.
		err = r.db.WithContext(ctx).Raw(
			`SELECT r.id, r.display_name AS label
			 FROM custom_object_records r
			 JOIN object_defs d ON d.id = r.object_def_id AND d.deleted_at IS NULL
			 WHERE r.org_id = ? AND d.slug = ? AND r.id IN ?`,
			orgID, kind, ids).Scan(&rows).Error
	}
	if err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.ID] = rw.Label
	}
	return out, nil
}
