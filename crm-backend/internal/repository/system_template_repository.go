package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type systemTemplateRepository struct {
	db *gorm.DB
}

func NewSystemTemplateRepository(db *gorm.DB) domain.SystemTemplateRepository {
	return &systemTemplateRepository{db: db}
}

// List returns the catalog. NOTE: no org filter — system_templates is a GLOBAL
// table with no org_id column, so the usual per-org scoping rule does not apply
// here. The org-scoped half of this feature is the application ledger below.
func (r *systemTemplateRepository) List(ctx context.Context, activeOnly bool) ([]domain.SystemTemplate, error) {
	var out []domain.SystemTemplate
	q := r.db.WithContext(ctx).Order("sort_order ASC, name ASC")
	if activeOnly {
		q = q.Where("is_active = ?", true)
	}
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *systemTemplateRepository) GetBySlug(ctx context.Context, slug string) (*domain.SystemTemplate, error) {
	var t domain.SystemTemplate
	err := r.db.WithContext(ctx).Where("slug = ?", slug).First(&t).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// RecordApplication upserts the ledger row. Conflict target is the
// (org_id, template_slug) unique index — a re-apply overwrites the stored result
// rather than accumulating rows.
func (r *systemTemplateRepository) RecordApplication(ctx context.Context, app *domain.OrgTemplateApplication) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "org_id"}, {Name: "template_slug"}},
			DoUpdates: clause.AssignmentColumns([]string{"spec_version", "status", "result", "applied_by", "updated_at"}),
		}).
		Create(app).Error
}

func (r *systemTemplateRepository) GetApplication(ctx context.Context, orgID uuid.UUID, slug string) (*domain.OrgTemplateApplication, error) {
	var app domain.OrgTemplateApplication
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND template_slug = ?", orgID, slug).
		First(&app).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &app, nil
}

func (r *systemTemplateRepository) ListApplications(ctx context.Context, orgID uuid.UUID) ([]domain.OrgTemplateApplication, error) {
	var out []domain.OrgTemplateApplication
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
