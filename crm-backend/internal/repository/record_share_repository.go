package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type recordShareRepository struct {
	db *gorm.DB
}

func NewRecordShareRepository(db *gorm.DB) domain.RecordShareRepository {
	return &recordShareRepository{db: db}
}

func (r *recordShareRepository) Create(ctx context.Context, s *domain.RecordShare) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *recordShareRepository) DeleteByID(ctx context.Context, id uuid.UUID, recordType string, recordID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND record_type = ? AND record_id = ?", id, recordType, recordID).
		Delete(&domain.RecordShare{})
	return res.RowsAffected, res.Error
}

func (r *recordShareRepository) ListByRecord(ctx context.Context, recordType string, recordID uuid.UUID) ([]domain.ShareView, error) {
	type row struct {
		ID              uuid.UUID
		GranteeUserID   uuid.UUID
		GranteeName     string
		PermissionLevel string
		CreatedAt       interface{}
	}
	var rows []domain.ShareView
	err := r.db.WithContext(ctx).
		Table("record_shares AS rs").
		Select("rs.id, rs.grantee_user_id, COALESCE(NULLIF(u.full_name, ''), u.email, '') AS grantee_name, rs.permission_level, rs.created_at").
		Joins("LEFT JOIN users u ON u.id = rs.grantee_user_id").
		Where("rs.record_type = ? AND rs.record_id = ?", recordType, recordID).
		Order("rs.created_at DESC").
		Scan(&rows).Error
	if rows == nil {
		rows = []domain.ShareView{}
	}
	return rows, err
}

func (r *recordShareRepository) ExistsForGrantee(ctx context.Context, recordType string, recordID, granteeUserID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.RecordShare{}).
		Where("record_type = ? AND record_id = ? AND grantee_user_id = ?", recordType, recordID, granteeUserID).
		Count(&count).Error
	return count > 0, err
}
