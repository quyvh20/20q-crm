package repository

import (
	"context"
	"encoding/json"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// permissionRepository persists Object-Level Security rows and the audit trail
// (P5a). It deliberately knows which objects exist — the three system slugs plus
// whatever lives in custom_object_defs — so the default seed can cover every
// current object without the hot OLS path calling back into the registry usecase.
type permissionRepository struct {
	db *gorm.DB
}

func NewPermissionRepository(db *gorm.DB) domain.PermissionRepository {
	return &permissionRepository{db: db}
}

// permSystemObjectSlugs are the always-present system objects. (Custom slugs are
// read from object_defs at seed time, post-P7 convergence.)
var permSystemObjectSlugs = []string{"contact", "company", "deal"}

// defaultRoleAccess is the non-breaking default matrix seeded for the system
// roles. It mirrors the legacy RequireRole gates exactly (router.go): read for
// everyone, create/edit for sales+, delete for manager+ — so flipping OLS on
// changes nothing for existing roles. owner is seeded all-true for grid clarity
// even though it bypasses OLS in code.
var defaultRoleAccess = map[string]domain.ObjectAccess{
	domain.RoleOwner:   {Read: true, Create: true, Edit: true, Delete: true},
	domain.RoleAdmin:   {Read: true, Create: true, Edit: true, Delete: true},
	domain.RoleManager: {Read: true, Create: true, Edit: true, Delete: true},
	domain.RoleSales:   {Read: true, Create: true, Edit: true, Delete: false},
	domain.RoleViewer:  {Read: true, Create: false, Edit: false, Delete: false},
}

// EnsureDefaults seeds the default matrix for every current object that has zero
// permission rows, idempotently and concurrency-safely. An object is seeded at
// most once in its lifetime: once any row exists for it (even an admin's
// all-false lock-down), it is left alone. Mirrors the EnsureSystemObjects pattern
// (object_registry_repository.go): a cheap pre-check, then a per-org advisory lock
// with a re-check so concurrent first-loads seed once.
func (r *permissionRepository) EnsureDefaults(ctx context.Context, orgID uuid.UUID) error {
	unseeded, err := r.unseededSlugs(ctx, r.db, orgID)
	if err != nil {
		return err
	}
	if len(unseeded) == 0 {
		return nil // fast path — nothing to seed
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Distinct lock key from the registry seed so the two never serialize on
		// each other. Released on commit.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", orgID.String()+":object_permissions").Error; err != nil {
			return err
		}
		unseeded, err := r.unseededSlugs(ctx, tx, orgID)
		if err != nil {
			return err
		}
		if len(unseeded) == 0 {
			return nil // lost the race — winner already seeded
		}

		roleIDByName, err := r.systemRoleIDs(ctx, tx)
		if err != nil {
			return err
		}

		now := time.Now()
		rows := make([]domain.ObjectPermission, 0, len(unseeded)*len(defaultRoleAccess))
		for _, slug := range unseeded {
			for roleName, access := range defaultRoleAccess {
				roleID, ok := roleIDByName[roleName]
				if !ok {
					continue // a system role not seeded yet — skip, will be covered next pass
				}
				rows = append(rows, domain.ObjectPermission{
					OrgID:      orgID,
					RoleID:     roleID,
					ObjectSlug: slug,
					CanRead:    access.Read,
					CanCreate:  access.Create,
					CanEdit:    access.Edit,
					CanDelete:  access.Delete,
					CreatedAt:  now,
					UpdatedAt:  now,
				})
			}
		}
		if len(rows) == 0 {
			return nil
		}
		// DO NOTHING on the PK so a partial prior seed (or a race the lock didn't
		// cover) can't error.
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	})
}

// unseededSlugs returns the current object slugs (system + custom) that have no
// permission rows yet for the org. Accepts the db/tx handle so it runs both on
// the fast path and inside the seeding transaction.
func (r *permissionRepository) unseededSlugs(ctx context.Context, db *gorm.DB, orgID uuid.UUID) ([]string, error) {
	current := append([]string{}, permSystemObjectSlugs...)
	// Custom objects live in object_defs (is_system=false) after the P7 convergence.
	var customSlugs []string
	if err := db.WithContext(ctx).Model(&domain.ObjectDef{}).
		Where("org_id = ? AND is_system = ?", orgID, false).
		Pluck("slug", &customSlugs).Error; err != nil {
		return nil, err
	}
	current = append(current, customSlugs...)

	var seeded []string
	if err := db.WithContext(ctx).Model(&domain.ObjectPermission{}).
		Where("org_id = ?", orgID).
		Distinct("object_slug").
		Pluck("object_slug", &seeded).Error; err != nil {
		return nil, err
	}
	seenSet := make(map[string]bool, len(seeded))
	for _, s := range seeded {
		seenSet[s] = true
	}

	out := make([]string, 0)
	added := make(map[string]bool, len(current))
	for _, s := range current {
		if !seenSet[s] && !added[s] {
			out = append(out, s)
			added[s] = true
		}
	}
	return out, nil
}

// systemRoleIDs returns role name → id for the global system roles.
func (r *permissionRepository) systemRoleIDs(ctx context.Context, db *gorm.DB) (map[string]uuid.UUID, error) {
	var roles []domain.Role
	if err := db.WithContext(ctx).
		Where("org_id IS NULL AND is_system = ?", true).
		Find(&roles).Error; err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(roles))
	for _, role := range roles {
		out[role.Name] = role.ID
	}
	return out, nil
}

// LoadOrgAccess returns roleName → objectSlug → access in one query, joining
// permissions to roles so the OLS check (which only knows the caller's role name)
// can look up directly.
func (r *permissionRepository) LoadOrgAccess(ctx context.Context, orgID uuid.UUID) (map[string]map[string]domain.ObjectAccess, error) {
	type row struct {
		RoleName   string
		ObjectSlug string
		CanRead    bool
		CanCreate  bool
		CanEdit    bool
		CanDelete  bool
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("object_permissions AS op").
		Select("r.name AS role_name, op.object_slug, op.can_read, op.can_create, op.can_edit, op.can_delete").
		Joins("JOIN roles r ON r.id = op.role_id").
		Where("op.org_id = ?", orgID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[string]map[string]domain.ObjectAccess)
	for _, row := range rows {
		if out[row.RoleName] == nil {
			out[row.RoleName] = make(map[string]domain.ObjectAccess)
		}
		out[row.RoleName][row.ObjectSlug] = domain.ObjectAccess{
			Read:   row.CanRead,
			Create: row.CanCreate,
			Edit:   row.CanEdit,
			Delete: row.CanDelete,
		}
	}
	return out, nil
}

// ListRoles returns the org's roles: the global system roles plus any org-scoped
// custom roles. System roles first, then alphabetical.
func (r *permissionRepository) ListRoles(ctx context.Context, orgID uuid.UUID) ([]domain.Role, error) {
	var roles []domain.Role
	err := r.db.WithContext(ctx).
		Where("org_id IS NULL OR org_id = ?", orgID).
		Order("is_system DESC, name ASC").
		Find(&roles).Error
	return roles, err
}

func (r *permissionRepository) ListPermissions(ctx context.Context, orgID uuid.UUID) ([]domain.ObjectPermission, error) {
	var perms []domain.ObjectPermission
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Find(&perms).Error
	return perms, err
}

// UpsertPermission inserts or updates one (role, object) cell. Rows are never
// deleted, so an all-false cell is a durable explicit denial that survives the
// idempotent default seed.
func (r *permissionRepository) UpsertPermission(ctx context.Context, p domain.ObjectPermission) error {
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "org_id"}, {Name: "role_id"}, {Name: "object_slug"}},
		DoUpdates: clause.AssignmentColumns([]string{"can_read", "can_create", "can_edit", "can_delete", "updated_at"}),
	}).Create(&p).Error
}

func (r *permissionRepository) WriteAudit(ctx context.Context, a *domain.ObjectAudit) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListAudit returns a record's audit rows newest-first, resolving the actor's
// display name (full name, then email) via a left join so a deleted user still
// renders.
func (r *permissionRepository) ListAudit(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID, limit int) ([]domain.AuditView, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	type row struct {
		ID         uuid.UUID
		Action     string
		ActorID    *uuid.UUID
		ActorName  string
		Changes    domain.JSON
		CreatedAt  time.Time
		ObjectSlug string
		RecordID   uuid.UUID
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("object_audit AS a").
		Select("a.id, a.action, a.actor_id, COALESCE(NULLIF(u.full_name, ''), u.email, '') AS actor_name, a.changes, a.created_at, a.object_slug, a.record_id").
		Joins("LEFT JOIN users u ON u.id = a.actor_id").
		Where("a.org_id = ? AND a.object_slug = ? AND a.record_id = ?", orgID, slug, recordID).
		Order("a.created_at DESC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]domain.AuditView, 0, len(rows))
	for _, row := range rows {
		changes := map[string]interface{}{}
		if len(row.Changes) > 0 {
			_ = json.Unmarshal(row.Changes, &changes)
		}
		out = append(out, domain.AuditView{
			ID:         row.ID,
			Action:     row.Action,
			ActorID:    row.ActorID,
			ActorName:  row.ActorName,
			Changes:    changes,
			CreatedAt:  row.CreatedAt,
			ObjectSlug: row.ObjectSlug,
			RecordID:   row.RecordID,
		})
	}
	return out, nil
}

// ============================================================
// Field-Level Security (P5b)
// ============================================================

// LoadOrgFieldAccess returns roleName → objectSlug → fieldKey → level in one
// query, joining field_permissions to roles so the FLS check (which only knows
// the caller's role name) can look up directly. An org with no restrictions
// returns an empty map, so the FLS half of the cache stays empty and free.
func (r *permissionRepository) LoadOrgFieldAccess(ctx context.Context, orgID uuid.UUID) (map[string]map[string]map[string]string, error) {
	type row struct {
		RoleName   string
		ObjectSlug string
		FieldKey   string
		Level      string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("field_permissions AS fp").
		Select("r.name AS role_name, fp.object_slug, fp.field_key, fp.level").
		Joins("JOIN roles r ON r.id = fp.role_id").
		Where("fp.org_id = ?", orgID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[string]map[string]map[string]string)
	for _, row := range rows {
		if out[row.RoleName] == nil {
			out[row.RoleName] = make(map[string]map[string]string)
		}
		if out[row.RoleName][row.ObjectSlug] == nil {
			out[row.RoleName][row.ObjectSlug] = make(map[string]string)
		}
		out[row.RoleName][row.ObjectSlug][row.FieldKey] = row.Level
	}
	return out, nil
}

func (r *permissionRepository) ListFieldPermissions(ctx context.Context, orgID uuid.UUID, slug string) ([]domain.FieldPermission, error) {
	var perms []domain.FieldPermission
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND object_slug = ?", orgID, slug).
		Find(&perms).Error
	return perms, err
}

// UpsertFieldPermission inserts or updates one (role, field) restriction.
func (r *permissionRepository) UpsertFieldPermission(ctx context.Context, p domain.FieldPermission) error {
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "org_id"}, {Name: "role_id"}, {Name: "object_slug"}, {Name: "field_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"level", "updated_at"}),
	}).Create(&p).Error
}

// DeleteFieldPermission removes one restriction, returning the field to its
// default (fully accessible). No-op if the row doesn't exist.
func (r *permissionRepository) DeleteFieldPermission(ctx context.Context, orgID, roleID uuid.UUID, slug, fieldKey string) error {
	return r.db.WithContext(ctx).
		Where("org_id = ? AND role_id = ? AND object_slug = ? AND field_key = ?", orgID, roleID, slug, fieldKey).
		Delete(&domain.FieldPermission{}).Error
}
