package repository

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ListViewRepository persists saved list views. It implements
// domain.ListViewRepository. The list_views table must exist before any method
// is called — it is created by the boot guard in main.go (mirrors
// migrations/000078_list_views.up.sql; golang-migrate is dead on prod).
//
// Soft delete rides gorm.DeletedAt on the model, so every query here is
// implicitly `deleted_at IS NULL` — no method may switch to Unscoped without
// re-adding that predicate by hand.
type ListViewRepository struct {
	db *gorm.DB
}

// NewListViewRepository builds the repository.
func NewListViewRepository(db *gorm.DB) *ListViewRepository { return &ListViewRepository{db: db} }

var _ domain.ListViewRepository = (*ListViewRepository)(nil)

// List returns the caller's own views plus org-shared ones for the object,
// name asc. The (owner OR shared) predicate lives HERE, in the one query, so
// visibility can't drift between callers.
func (r *ListViewRepository) List(ctx context.Context, orgID uuid.UUID, objectSlug string, userID uuid.UUID) ([]domain.ListView, error) {
	var rows []domain.ListView
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND object_slug = ? AND (owner_user_id = ? OR shared = true)", orgID, objectSlug, userID).
		Order("name asc").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetByID is org-scoped; a miss (including a foreign org's id) is (nil, nil),
// never an error, so the usecase owns the 404.
func (r *ListViewRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.ListView, error) {
	var v domain.ListView
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&v).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (r *ListViewRepository) Create(ctx context.Context, v *domain.ListView) error {
	// GORM omits zero-values on insert; an empty blob must still land as '{}'
	// (the column is NOT NULL) rather than relying on the DDL default racing a
	// later Save that would write NULL.
	if len(v.Definition) == 0 {
		v.Definition = []byte("{}")
	}
	return r.db.WithContext(ctx).Create(v).Error
}

func (r *ListViewRepository) Update(ctx context.Context, v *domain.ListView) error {
	return r.db.WithContext(ctx).Save(v).Error
}

// SoftDelete reports whether a live row was removed — false lets the usecase
// 404 an already-deleted or foreign id without a second query.
func (r *ListViewRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&domain.ListView{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// CountForObject counts live views per (org, object) — the Create cap's input.
func (r *ListViewRepository) CountForObject(ctx context.Context, orgID uuid.UUID, objectSlug string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.ListView{}).
		Where("org_id = ? AND object_slug = ?", orgID, objectSlug).
		Count(&n).Error
	return n, err
}
