package repository

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type customObjectRepository struct {
	db *gorm.DB
}

func NewCustomObjectRepository(db *gorm.DB) domain.CustomObjectRepository {
	return &customObjectRepository{db: db}
}

// ============================================================
// Definitions
// ============================================================

func (r *customObjectRepository) ListDefs(ctx context.Context, orgID uuid.UUID) ([]domain.CustomObjectDef, error) {
	var defs []domain.CustomObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at ASC").
		Find(&defs).Error
	return defs, err
}

func (r *customObjectRepository) GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*domain.CustomObjectDef, error) {
	var def domain.CustomObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND slug = ?", orgID, slug).
		First(&def).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &def, nil
}

func (r *customObjectRepository) GetDefByID(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*domain.CustomObjectDef, error) {
	var def domain.CustomObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&def).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &def, nil
}

func (r *customObjectRepository) CreateDef(ctx context.Context, def *domain.CustomObjectDef) error {
	return r.db.WithContext(ctx).Create(def).Error
}

func (r *customObjectRepository) UpdateDef(ctx context.Context, def *domain.CustomObjectDef) error {
	return r.db.WithContext(ctx).Save(def).Error
}

func (r *customObjectRepository) SoftDeleteDef(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.CustomObjectDef{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ============================================================
// Records
// ============================================================

func (r *customObjectRepository) ListRecords(ctx context.Context, orgID uuid.UUID, defID uuid.UUID, f domain.RecordFilter) ([]domain.CustomObjectRecord, int64, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	query := r.db.WithContext(ctx).
		Where("custom_object_records.org_id = ? AND custom_object_records.object_def_id = ?", orgID, defID).
		Preload("Contact").
		Preload("Deal")

	// Search by display_name
	if f.Q != "" {
		query = query.Where("custom_object_records.display_name ILIKE ?", "%"+f.Q+"%")
	}

	// Count total
	var total int64
	countQ := r.db.WithContext(ctx).Model(&domain.CustomObjectRecord{}).
		Where("org_id = ? AND object_def_id = ?", orgID, defID)
	if f.Q != "" {
		countQ = countQ.Where("display_name ILIKE ?", "%"+f.Q+"%")
	}
	countQ.Count(&total)

	var records []domain.CustomObjectRecord
	err := query.
		Order("custom_object_records.created_at DESC").
		Offset(f.Offset).
		Limit(limit).
		Find(&records).Error

	return records, total, err
}

func (r *customObjectRepository) GetRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) (*domain.CustomObjectRecord, error) {
	var rec domain.CustomObjectRecord
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Preload("Contact").
		Preload("Deal").
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *customObjectRepository) CreateRecord(ctx context.Context, rec *domain.CustomObjectRecord) error {
	return r.db.WithContext(ctx).Create(rec).Error
}

func (r *customObjectRepository) UpdateRecord(ctx context.Context, rec *domain.CustomObjectRecord) error {
	return r.db.WithContext(ctx).Save(rec).Error
}

func (r *customObjectRepository) SoftDeleteRecord(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.CustomObjectRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
