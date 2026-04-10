package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type activityRepository struct {
	db *gorm.DB
}

func NewActivityRepository(db *gorm.DB) domain.ActivityRepository {
	return &activityRepository{db: db}
}

func (r *activityRepository) List(ctx context.Context, orgID uuid.UUID, f domain.ActivityFilter) ([]domain.Activity, error) {
	query := r.db.WithContext(ctx).
		Where("org_id = ?", orgID)

	if f.DealID != nil {
		query = query.Where("deal_id = ?", *f.DealID)
	}
	if f.ContactID != nil {
		query = query.Where("contact_id = ?", *f.ContactID)
	}

	var activities []domain.Activity
	err := query.
		Order("occurred_at DESC").
		Limit(100).
		Find(&activities).Error
	return activities, err
}

func (r *activityRepository) Create(ctx context.Context, a *domain.Activity) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(a).Error
}
