package repository

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// roleRepository persists custom roles, their capability grants (role_permissions
// as the capability store, plan D5), and the clone-copy of the OLS/FLS grids.
type roleRepository struct {
	db *gorm.DB
}

func NewRoleRepository(db *gorm.DB) domain.RoleRepository {
	return &roleRepository{db: db}
}

// ListDetailed returns system + this org's custom roles with capabilities,
// data_scope, and active member counts. System roles first, then by name.
func (r *roleRepository) ListDetailed(ctx context.Context, orgID uuid.UUID) ([]domain.RoleDetail, error) {
	var roles []domain.Role
	if err := r.db.WithContext(ctx).
		Where("org_id IS NULL OR org_id = ?", orgID).
		Order("is_system DESC, name ASC").
		Find(&roles).Error; err != nil {
		return nil, err
	}
	if len(roles) == 0 {
		return []domain.RoleDetail{}, nil
	}

	roleIDs := make([]uuid.UUID, 0, len(roles))
	for _, role := range roles {
		roleIDs = append(roleIDs, role.ID)
	}

	// Capabilities for all roles in one query.
	var caps []domain.RolePermission
	if err := r.db.WithContext(ctx).
		Where("role_id IN ?", roleIDs).
		Find(&caps).Error; err != nil {
		return nil, err
	}
	capsByRole := make(map[uuid.UUID][]string, len(roles))
	for _, cp := range caps {
		if !domain.IsCapability(cp.PermissionCode) {
			continue // retired code — never surface it (see GetCapabilities below)
		}
		capsByRole[cp.RoleID] = append(capsByRole[cp.RoleID], cp.PermissionCode)
	}

	// Active member counts per role in one grouped query.
	type countRow struct {
		RoleID uuid.UUID
		Cnt    int64
	}
	var counts []countRow
	if err := r.db.WithContext(ctx).
		Table("org_users").
		Select("role_id, COUNT(*) AS cnt").
		Where("org_id = ? AND status = ? AND deleted_at IS NULL", orgID, domain.StatusActive).
		Group("role_id").
		Scan(&counts).Error; err != nil {
		return nil, err
	}
	countByRole := make(map[uuid.UUID]int64, len(counts))
	for _, c := range counts {
		countByRole[c.RoleID] = c.Cnt
	}

	out := make([]domain.RoleDetail, 0, len(roles))
	for _, role := range roles {
		codes := capsByRole[role.ID]
		if codes == nil {
			codes = []string{}
		}
		out = append(out, domain.RoleDetail{
			ID:           role.ID,
			Name:         role.Name,
			Description:  role.Description,
			IsSystem:     role.IsSystem,
			IsOwner:      domain.IsOwnerRole(&role),
			DataScope:    role.DataScope,
			TemplateKey:  role.TemplateKey,
			SeededFrom:   role.SeededFromRoleID,
			Capabilities: codes,
			MemberCount:  countByRole[role.ID],
		})
	}
	return out, nil
}

// ListOptions returns the minimal role identities (no capabilities, no member
// counts) any member may read for the role pickers (P6). System roles first,
// then by name — same ordering as ListDetailed.
func (r *roleRepository) ListOptions(ctx context.Context, orgID uuid.UUID) ([]domain.RoleOption, error) {
	var roles []domain.Role
	if err := r.db.WithContext(ctx).
		Where("org_id IS NULL OR org_id = ?", orgID).
		Order("is_system DESC, name ASC").
		Find(&roles).Error; err != nil {
		return nil, err
	}
	out := make([]domain.RoleOption, 0, len(roles))
	for i := range roles {
		out = append(out, domain.RoleOption{
			ID:          roles[i].ID,
			Name:        roles[i].Name,
			Description: roles[i].Description,
			IsSystem:    roles[i].IsSystem,
			IsOwner:     domain.IsOwnerRole(&roles[i]),
			DataScope:   roles[i].DataScope,
		})
	}
	return out, nil
}

func (r *roleRepository) GetInOrg(ctx context.Context, orgID, id uuid.UUID) (*domain.Role, error) {
	var role domain.Role
	err := r.db.WithContext(ctx).
		Where("id = ? AND (org_id = ? OR (org_id IS NULL AND is_system = TRUE))", id, orgID).
		First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *roleRepository) FindByNameInOrg(ctx context.Context, orgID uuid.UUID, name string) (*domain.Role, error) {
	var role domain.Role
	err := r.db.WithContext(ctx).
		Where("name = ? AND (org_id = ? OR (org_id IS NULL AND is_system = TRUE))", name, orgID).
		First(&role).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &role, nil
}

func (r *roleRepository) CreateRole(ctx context.Context, role *domain.Role) error {
	return r.db.WithContext(ctx).Create(role).Error
}

func (r *roleRepository) UpdateRole(ctx context.Context, role *domain.Role) error {
	return r.db.WithContext(ctx).
		Model(&domain.Role{}).
		Where("id = ?", role.ID).
		Updates(map[string]interface{}{
			"name":        role.Name,
			"description": role.Description,
			"data_scope":  role.DataScope,
		}).Error
}

// DeleteRole removes a custom role and its dependent rows in one transaction.
// The caller (usecase) guarantees the role is custom and unused.
func (r *roleRepository) DeleteRole(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", id).Delete(&domain.RolePermission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("org_id = ? AND role_id = ?", orgID, id).Delete(&domain.ObjectPermission{}).Error; err != nil {
			return err
		}
		if err := tx.Where("org_id = ? AND role_id = ?", orgID, id).Delete(&domain.FieldPermission{}).Error; err != nil {
			return err
		}
		// Role-targeted report shares have no FK; left behind they render as
		// "(removed)" forever and a same-named future role never inherits them.
		if err := tx.Exec(`DELETE FROM report_shares WHERE org_id = ? AND target_type = 'role' AND target_id = ?`, orgID, id).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND org_id = ? AND is_system = FALSE", id, orgID).Delete(&domain.Role{}).Error
	})
}

func (r *roleRepository) ListMemberIDs(ctx context.Context, orgID, roleID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("org_id = ? AND role_id = ? AND deleted_at IS NULL", orgID, roleID).
		Pluck("user_id", &ids).Error
	return ids, err
}

func (r *roleRepository) GetCapabilities(ctx context.Context, roleID uuid.UUID) ([]string, error) {
	var codes []string
	err := r.db.WithContext(ctx).
		Model(&domain.RolePermission{}).
		Where("role_id = ?", roleID).
		Pluck("permission_code", &codes).Error
	// Drop codes retired from the vocabulary (e.g. billing.manage, deleted in
	// U0.3): a stored row for a retired code must not round-trip into the roles
	// UI, or the next SetCapabilities save would re-submit it and be rejected by
	// sanitizeCapabilities — leaving the role permanently uneditable. The stale
	// row itself disappears on the role's next capability save (replace-set).
	valid := codes[:0]
	for _, c := range codes {
		if domain.IsCapability(c) {
			valid = append(valid, c)
		}
	}
	codes = valid
	if codes == nil {
		codes = []string{}
	}
	return codes, err
}

// SetCapabilities replaces a role's capability rows with codes (org-scoped rows
// for custom roles) in one transaction.
func (r *roleRepository) SetCapabilities(ctx context.Context, orgID, roleID uuid.UUID, codes []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&domain.RolePermission{}).Error; err != nil {
			return err
		}
		if len(codes) == 0 {
			return nil
		}
		rows := make([]domain.RolePermission, 0, len(codes))
		org := orgID
		for _, code := range codes {
			rows = append(rows, domain.RolePermission{RoleID: roleID, PermissionCode: code, OrgID: &org})
		}
		return tx.Create(&rows).Error
	})
}

// ClonePermissions copies the source role's OLS + FLS rows to the destination
// role within the org, so a cloned role starts from the source's data grids.
func (r *roleRepository) ClonePermissions(ctx context.Context, orgID, srcRoleID, dstRoleID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ols []domain.ObjectPermission
		if err := tx.Where("org_id = ? AND role_id = ?", orgID, srcRoleID).Find(&ols).Error; err != nil {
			return err
		}
		for i := range ols {
			ols[i].RoleID = dstRoleID
		}
		if len(ols) > 0 {
			if err := tx.Create(&ols).Error; err != nil {
				return err
			}
		}

		var fls []domain.FieldPermission
		if err := tx.Where("org_id = ? AND role_id = ?", orgID, srcRoleID).Find(&fls).Error; err != nil {
			return err
		}
		for i := range fls {
			fls[i].RoleID = dstRoleID
		}
		if len(fls) > 0 {
			if err := tx.Create(&fls).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *roleRepository) CountActiveMembers(ctx context.Context, orgID, roleID uuid.UUID) (int64, error) {
	var cnt int64
	err := r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("org_id = ? AND role_id = ? AND status = ? AND deleted_at IS NULL", orgID, roleID, domain.StatusActive).
		Count(&cnt).Error
	return cnt, err
}

// ReassignMembers moves every (non-deleted) org_users row from fromRoleID to
// toRoleID within the org in one atomic UPDATE ... RETURNING, returning the user
// ids moved. RETURNING makes the move and the moved-set observation a single
// statement, so a member who joins the source role concurrently is either fully
// included (moved AND returned for eviction) or not moved at all — no window where
// a late joiner is reassigned but missed for session eviction. The caller
// validates both roles are usable by the org.
func (r *roleRepository) ReassignMembers(ctx context.Context, orgID, fromRoleID, toRoleID uuid.UUID) ([]uuid.UUID, error) {
	var moved []uuid.UUID
	err := r.db.WithContext(ctx).Raw(
		`UPDATE org_users SET role_id = ? WHERE org_id = ? AND role_id = ? AND deleted_at IS NULL RETURNING user_id`,
		toRoleID, orgID, fromRoleID,
	).Scan(&moved).Error
	return moved, err
}
