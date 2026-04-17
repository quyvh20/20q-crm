package repository

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type dealRepository struct {
	db *gorm.DB
}

func NewDealRepository(db *gorm.DB) domain.DealRepository {
	return &dealRepository{db: db}
}

// ============================================================
// List — keyset pagination + filters
// ============================================================

func (r *dealRepository) List(ctx context.Context, orgID uuid.UUID, f domain.DealFilter) ([]domain.Deal, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "deals").
		Preload("Contact").
		Preload("Company").
		Preload("Stage").
		Preload("Owner")

	if f.Q != "" {
		q := "%" + strings.ToLower(f.Q) + "%"
		query = query.Where("LOWER(deals.title) LIKE ?", q)
	}
	if f.StageID != nil {
		query = query.Where("deals.stage_id = ?", *f.StageID)
	}
	if f.OwnerUserID != nil {
		query = query.Where("deals.owner_user_id = ?", *f.OwnerUserID)
	}
	if f.ContactID != nil {
		query = query.Where("deals.contact_id = ?", *f.ContactID)
	}

	if f.Cursor != "" {
		decoded, err := base64.StdEncoding.DecodeString(f.Cursor)
		if err == nil {
			query = query.Where("deals.id < ?", string(decoded))
		}
	}

	var deals []domain.Deal
	if err := query.
		Order("deals.created_at DESC, deals.id DESC").
		Limit(limit + 1).
		Find(&deals).Error; err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(deals) > limit {
		deals = deals[:limit]
		last := deals[len(deals)-1]
		nextCursor = base64.StdEncoding.EncodeToString([]byte(last.ID.String()))
	}

	return deals, nextCursor, nil
}

// ============================================================
// GetByID
// ============================================================

func (r *dealRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Deal, error) {
	var deal domain.Deal
	err := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "deals").
		Where("deals.id = ?", id).
		Preload("Contact").
		Preload("Company").
		Preload("Stage").
		Preload("Owner").
		First(&deal).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &deal, err
}

// ============================================================
// Create
// ============================================================

func (r *dealRepository) Create(ctx context.Context, d *domain.Deal) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(d).Error
}

// ============================================================
// Update
// ============================================================

func (r *dealRepository) Update(ctx context.Context, d *domain.Deal) error {
	return r.db.WithContext(ctx).
		Model(d).
		Select(
			"title", "contact_id", "company_id", "stage_id",
			"value", "probability", "owner_user_id",
			"expected_close_at", "is_won", "is_lost", "closed_at",
			"custom_fields", "updated_at",
		).
		Updates(d).Error
}

// ============================================================
// SoftDelete
// ============================================================

func (r *dealRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	result := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "deals").
		Where("deals.id = ?", id).
		Delete(&domain.Deal{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("deal not found")
	}
	return nil
}

// ============================================================
// Count
// ============================================================

func (r *dealRepository) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	err := applyScopeFromCtx(r.db.WithContext(ctx).Model(&domain.Deal{}), ctx, orgID, "deals").
		Count(&count).Error
	return count, err
}

// ============================================================
// Forecast — 12-month rolling revenue forecast
// ============================================================

func (r *dealRepository) Forecast(ctx context.Context, orgID uuid.UUID) ([]domain.ForecastRow, error) {
	var rows []domain.ForecastRow
	sql := `
		SELECT
			TO_CHAR(expected_close_at, 'YYYY-MM') AS month,
			COALESCE(SUM(value * probability / 100.0), 0) AS expected_revenue,
			COUNT(*) AS deals_count
		FROM deals
		WHERE org_id = ?
		  AND is_won = false
		  AND is_lost = false
		  AND deleted_at IS NULL
		  AND expected_close_at IS NOT NULL
		  AND expected_close_at >= DATE_TRUNC('month', NOW())
		  AND expected_close_at < DATE_TRUNC('month', NOW()) + INTERVAL '12 months'
		GROUP BY TO_CHAR(expected_close_at, 'YYYY-MM')
		ORDER BY month ASC
	`
	err := r.db.WithContext(ctx).Raw(sql, orgID).Scan(&rows).Error
	return rows, err
}
