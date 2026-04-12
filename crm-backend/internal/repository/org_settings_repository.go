package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type orgSettingsRepository struct {
	db *gorm.DB
}

func NewOrgSettingsRepository(db *gorm.DB) domain.OrgSettingsRepository {
	return &orgSettingsRepository{db: db}
}

func (r *orgSettingsRepository) GetByOrgID(ctx context.Context, orgID uuid.UUID) (*domain.OrgSettings, error) {
	var settings domain.OrgSettings
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).First(&settings).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &settings, nil
}

func (r *orgSettingsRepository) Upsert(ctx context.Context, settings *domain.OrgSettings) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "org_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"custom_field_defs", "industry_template_slug", "ai_context_override", "onboarding_completed", "updated_at"}),
		}).
		Create(settings).Error
}
