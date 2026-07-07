package repository

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type objectLayoutRepository struct {
	db *gorm.DB
}

// NewObjectLayoutRepository returns the concrete ObjectLayoutRepository backed by
// the given GORM connection. The P8 tables (object_layouts, object_layout_roles)
// must exist before any method is called — they are created by the boot guard in
// main.go (mirrors the golang-migrate migration 000022_object_layouts.up.sql).
func NewObjectLayoutRepository(db *gorm.DB) domain.ObjectLayoutRepository {
	return &objectLayoutRepository{db: db}
}

// ============================================================
// Bulk loaders (used to warm the per-org cache in one pass)
// ============================================================

// LoadOrgLayouts returns all non-deleted layouts for the org, grouped by
// object_slug, with Sections decoded from the stored JSONB.
func (r *objectLayoutRepository) LoadOrgLayouts(ctx context.Context, orgID uuid.UUID) (map[string][]domain.ObjectLayout, error) {
	var rows []domain.ObjectLayout
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND deleted_at IS NULL", orgID).
		Order("created_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]domain.ObjectLayout, len(rows))
	for i := range rows {
		if err := rows[i].UnmarshalSections(); err != nil {
			return nil, err
		}
		result[rows[i].ObjectSlug] = append(result[rows[i].ObjectSlug], rows[i])
	}
	return result, nil
}

// LoadOrgLayoutRoleMap returns the org's role→layout assignment index in one
// query, joining object_layout_roles → roles → object_layouts. Only roles
// assigned to non-deleted layouts are included (the roles JOIN also drops
// assignments whose role was deleted). Keyed by role_id (R1 re-key) so a role
// rename can't misroute layouts; the P5 name bridge was deleted in P9.
func (r *objectLayoutRepository) LoadOrgLayoutRoleMap(ctx context.Context, orgID uuid.UUID) (*domain.LayoutRoleMap, error) {
	type row struct {
		ObjectSlug string    `gorm:"column:object_slug"`
		RoleID     uuid.UUID `gorm:"column:role_id"`
		LayoutID   uuid.UUID `gorm:"column:layout_id"`
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(`
		SELECT olr.object_slug, olr.role_id, olr.layout_id
		FROM object_layout_roles olr
		JOIN roles ro ON ro.id = olr.role_id
		JOIN object_layouts ol ON ol.id = olr.layout_id
		WHERE olr.org_id = ? AND ol.deleted_at IS NULL`, orgID).Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := &domain.LayoutRoleMap{
		ByID: make(map[string]map[uuid.UUID]uuid.UUID),
	}
	for _, r := range rows {
		if result.ByID[r.ObjectSlug] == nil {
			result.ByID[r.ObjectSlug] = make(map[uuid.UUID]uuid.UUID)
		}
		result.ByID[r.ObjectSlug][r.RoleID] = r.LayoutID
	}
	return result, nil
}

// ============================================================
// Single-record reads
// ============================================================

// GetLayout returns one layout (with Sections decoded) owned by the org, or nil.
func (r *objectLayoutRepository) GetLayout(ctx context.Context, orgID, id uuid.UUID) (*domain.ObjectLayout, error) {
	var layout domain.ObjectLayout
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ? AND deleted_at IS NULL", orgID, id).
		First(&layout).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := layout.UnmarshalSections(); err != nil {
		return nil, err
	}
	return &layout, nil
}

// ListLayouts returns all non-deleted layouts for an object, ordered by creation time.
func (r *objectLayoutRepository) ListLayouts(ctx context.Context, orgID uuid.UUID, slug string) ([]domain.ObjectLayout, error) {
	var layouts []domain.ObjectLayout
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND object_slug = ? AND deleted_at IS NULL", orgID, slug).
		Order("created_at ASC").
		Find(&layouts).Error; err != nil {
		return nil, err
	}
	for i := range layouts {
		if err := layouts[i].UnmarshalSections(); err != nil {
			return nil, err
		}
	}
	return layouts, nil
}

// ============================================================
// Writes
// ============================================================

// CreateLayout inserts a new layout. If IsDefault is true, the existing default
// for the same (org, slug) is cleared in the same transaction so the unique
// partial index uix_object_layouts_default is never violated.
func (r *objectLayoutRepository) CreateLayout(ctx context.Context, layout *domain.ObjectLayout) error {
	if err := layout.MarshalSections(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if layout.IsDefault {
			if err := tx.Model(&domain.ObjectLayout{}).
				Where("org_id = ? AND object_slug = ? AND is_default = true AND deleted_at IS NULL",
					layout.OrgID, layout.ObjectSlug).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Create(layout).Error
	})
}

// UpdateLayout saves edits to an existing layout. If IsDefault is now true,
// the previous default for the same (org, slug) is cleared in the same
// transaction.
func (r *objectLayoutRepository) UpdateLayout(ctx context.Context, layout *domain.ObjectLayout) error {
	if err := layout.MarshalSections(); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if layout.IsDefault {
			if err := tx.Model(&domain.ObjectLayout{}).
				Where("org_id = ? AND object_slug = ? AND id != ? AND is_default = true AND deleted_at IS NULL",
					layout.OrgID, layout.ObjectSlug, layout.ID).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		return tx.Save(layout).Error
	})
}

// DeleteLayout soft-deletes a layout. GORM's soft-delete sets deleted_at instead
// of issuing a SQL DELETE, so the ON DELETE CASCADE FK on object_layout_roles
// never fires. We must hard-delete role assignments explicitly in the same
// transaction; otherwise orphan rows block future role reassignments via the
// unique index uix_object_layout_roles_one_per_role.
func (r *objectLayoutRepository) DeleteLayout(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND layout_id = ?", orgID, id).
			Delete(&domain.ObjectLayoutRole{}).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND id = ?", orgID, id).
			Delete(&domain.ObjectLayout{}).Error
	})
}

// ============================================================
// Role assignments
// ============================================================

// SetLayoutRoles replaces all role-assignment rows for a layout. The operation
// is transactional: existing assignments are deleted first, then new ones are
// inserted. The unique index on (org_id, object_slug, role_id) prevents two
// layouts from claiming the same role.
func (r *objectLayoutRepository) SetLayoutRoles(ctx context.Context, orgID uuid.UUID, layoutID uuid.UUID, slug string, roleIDs []uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Remove all existing assignments for THIS layout.
		if err := tx.Where("org_id = ? AND layout_id = ?", orgID, layoutID).
			Delete(&domain.ObjectLayoutRole{}).Error; err != nil {
			return err
		}
		// 2. Remove conflicting assignments from OTHER layouts for the same
		//    (org, slug, role) tuples. The unique index
		//    uix_object_layout_roles_one_per_role forbids two layouts from
		//    claiming the same role, so we must clear the old owner first.
		if len(roleIDs) > 0 {
			if err := tx.Where("org_id = ? AND object_slug = ? AND role_id IN ?", orgID, slug, roleIDs).
				Delete(&domain.ObjectLayoutRole{}).Error; err != nil {
				return err
			}
		}
		// 3. Insert new assignments.
		for _, roleID := range roleIDs {
			row := &domain.ObjectLayoutRole{
				OrgID:      orgID,
				LayoutID:   layoutID,
				ObjectSlug: slug,
				RoleID:     roleID,
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListLayoutRoleIDs returns the role UUIDs currently assigned to a layout.
func (r *objectLayoutRepository) ListLayoutRoleIDs(ctx context.Context, orgID, layoutID uuid.UUID) ([]uuid.UUID, error) {
	var rows []domain.ObjectLayoutRole
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND layout_id = ?", orgID, layoutID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ids[i] = row.RoleID
	}
	return ids, nil
}
