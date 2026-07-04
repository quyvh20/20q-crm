package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// dashboardWidgetRepository persists per-user pinned reports. Every query is
// scoped to (org_id, user_id) — a user can only ever touch their own dashboard.
type dashboardWidgetRepository struct {
	db *gorm.DB
}

func NewDashboardWidgetRepository(db *gorm.DB) domain.DashboardWidgetRepository {
	return &dashboardWidgetRepository{db: db}
}

func (r *dashboardWidgetRepository) ListForUser(ctx context.Context, orgID, userID uuid.UUID) ([]domain.DashboardWidget, error) {
	var out []domain.DashboardWidget
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND user_id = ?", orgID, userID).
		Order("position ASC, created_at ASC").
		Find(&out).Error
	return out, err
}

func (r *dashboardWidgetRepository) FindByReport(ctx context.Context, orgID, userID, reportID uuid.UUID) (*domain.DashboardWidget, error) {
	var w domain.DashboardWidget
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND user_id = ? AND report_id = ?", orgID, userID, reportID).
		First(&w).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *dashboardWidgetRepository) Create(ctx context.Context, w *domain.DashboardWidget) error {
	return r.db.WithContext(ctx).Create(w).Error
}

func (r *dashboardWidgetRepository) UpdateSize(ctx context.Context, orgID, userID, id uuid.UUID, size string) (int64, error) {
	res := r.db.WithContext(ctx).Model(&domain.DashboardWidget{}).
		Where("org_id = ? AND user_id = ? AND id = ?", orgID, userID, id).
		Update("size", size)
	return res.RowsAffected, res.Error
}

func (r *dashboardWidgetRepository) Delete(ctx context.Context, orgID, userID, id uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("org_id = ? AND user_id = ? AND id = ?", orgID, userID, id).
		Delete(&domain.DashboardWidget{})
	return res.RowsAffected, res.Error
}

func (r *dashboardWidgetRepository) Reorder(ctx context.Context, orgID, userID uuid.UUID, ids []uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			if err := tx.Model(&domain.DashboardWidget{}).
				Where("org_id = ? AND user_id = ? AND id = ?", orgID, userID, id).
				Update("position", i).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *dashboardWidgetRepository) NextPosition(ctx context.Context, orgID, userID uuid.UUID) (int, error) {
	var max *int
	err := r.db.WithContext(ctx).Model(&domain.DashboardWidget{}).
		Where("org_id = ? AND user_id = ?", orgID, userID).
		Select("MAX(position)").Scan(&max).Error
	if err != nil {
		return 0, err
	}
	if max == nil {
		return 0, nil
	}
	return *max + 1, nil
}
