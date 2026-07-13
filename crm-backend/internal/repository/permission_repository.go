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

		// roleID → the default access to seed. Covers the system roles (by name) AND
		// custom roles resolved through their template lineage (P6), so an object
		// added after a custom role was created still inherits that role's template
		// access instead of leaving it invisibly denied (the zero-OLS trap).
		seedAccess, err := r.seedAccessByRole(ctx, tx, orgID)
		if err != nil {
			return err
		}

		now := time.Now()
		rows := make([]domain.ObjectPermission, 0, len(unseeded)*len(seedAccess))
		for _, slug := range unseeded {
			for roleID, access := range seedAccess {
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

// seedAccessByRole returns roleID → the default ObjectAccess to seed for a new
// object, covering both the global system roles and this org's custom roles (P6).
// A system role IS its own template; a custom role resolves through its lineage
// (seedTemplateName). A role with no resolvable template (a legacy custom role
// created before the wizard recorded lineage) is omitted — left at zero rows,
// matching the pre-P6 behavior rather than silently granting it access.
func (r *permissionRepository) seedAccessByRole(ctx context.Context, db *gorm.DB, orgID uuid.UUID) (map[uuid.UUID]domain.ObjectAccess, error) {
	var roles []domain.Role
	if err := db.WithContext(ctx).
		Where("org_id IS NULL OR org_id = ?", orgID).
		Find(&roles).Error; err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]domain.Role, len(roles))
	for i := range roles {
		byID[roles[i].ID] = roles[i]
	}
	out := make(map[uuid.UUID]domain.ObjectAccess, len(roles))
	for i := range roles {
		name := seedTemplateName(roles[i], byID)
		if name == "" {
			continue
		}
		if access, ok := defaultRoleAccess[name]; ok {
			out[roles[i].ID] = access
		}
	}
	return out, nil
}

// seedTemplateName resolves the system-role template whose default OLS a role
// should inherit on a new object. A system role is its own template; a custom
// role follows its denormalized template_key, or one hop through
// seeded_from_role_id to a system (or template-bearing) source. Returns "" when
// unresolvable, so a lineage-less custom role is left untouched.
func seedTemplateName(role domain.Role, byID map[uuid.UUID]domain.Role) string {
	if role.IsSystem {
		return role.Name
	}
	if role.TemplateKey != nil && *role.TemplateKey != "" {
		return *role.TemplateKey
	}
	if role.SeededFromRoleID != nil {
		if src, ok := byID[*role.SeededFromRoleID]; ok {
			if src.IsSystem {
				return src.Name
			}
			if src.TemplateKey != nil && *src.TemplateKey != "" {
				return *src.TemplateKey
			}
		}
	}
	return ""
}

// LoadOrgAccess returns roleID → objectSlug → access in one query. Keyed by
// role_id (P5 re-key) — the grant table already stores role_id, so no JOIN is
// needed and a role rename can never detach a role from its grants.
func (r *permissionRepository) LoadOrgAccess(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]map[string]domain.ObjectAccess, error) {
	var rows []domain.ObjectPermission
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]map[string]domain.ObjectAccess)
	for _, row := range rows {
		if out[row.RoleID] == nil {
			out[row.RoleID] = make(map[string]domain.ObjectAccess)
		}
		out[row.RoleID][row.ObjectSlug] = domain.ObjectAccess{
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

// LoadOrgCapabilities returns roleID → capabilityCode → true, keyed by role_id
// (P5 re-key). The JOIN to roles remains only to scope the rows to the global
// system roles plus this org's custom roles.
func (r *permissionRepository) LoadOrgCapabilities(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]map[string]bool, error) {
	type row struct {
		RoleID         uuid.UUID
		PermissionCode string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("role_permissions AS rp").
		Select("rp.role_id, rp.permission_code").
		Joins("JOIN roles r ON r.id = rp.role_id").
		Where("r.org_id IS NULL OR r.org_id = ?", orgID).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]map[string]bool)
	for _, row := range rows {
		if out[row.RoleID] == nil {
			out[row.RoleID] = make(map[string]bool)
		}
		out[row.RoleID][row.PermissionCode] = true
	}
	return out, nil
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

// LoadOrgFieldAccess returns roleID → objectSlug → fieldKey → level in one
// query, keyed by role_id (P5 re-key) — the restriction table stores role_id, so
// no JOIN is needed. An org with no restrictions returns an empty map, so the
// FLS half of the cache stays empty and free.
func (r *permissionRepository) LoadOrgFieldAccess(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]map[string]map[string]string, error) {
	var rows []domain.FieldPermission
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[uuid.UUID]map[string]map[string]string)
	for _, row := range rows {
		if out[row.RoleID] == nil {
			out[row.RoleID] = make(map[string]map[string]string)
		}
		if out[row.RoleID][row.ObjectSlug] == nil {
			out[row.RoleID][row.ObjectSlug] = make(map[string]string)
		}
		out[row.RoleID][row.ObjectSlug][row.FieldKey] = row.Level
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

// BulkSetFieldPermissions applies one level to many fields of one (role, object)
// in a single transaction with batch statements (U3): level 'edit' — the
// default, stored as the absence of a row — deletes the rows in one DELETE;
// any other level batch-upserts them in one INSERT ... ON CONFLICT.
func (r *permissionRepository) BulkSetFieldPermissions(ctx context.Context, orgID, roleID uuid.UUID, slug string, fieldKeys []string, level string) error {
	if len(fieldKeys) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if domain.FieldLevel(level) == domain.FieldLevelEdit {
			return tx.
				Where("org_id = ? AND role_id = ? AND object_slug = ? AND field_key IN ?", orgID, roleID, slug, fieldKeys).
				Delete(&domain.FieldPermission{}).Error
		}
		now := time.Now()
		rows := make([]domain.FieldPermission, 0, len(fieldKeys))
		for _, key := range fieldKeys {
			rows = append(rows, domain.FieldPermission{
				OrgID:      orgID,
				RoleID:     roleID,
				ObjectSlug: slug,
				FieldKey:   key,
				Level:      level,
				CreatedAt:  now,
				UpdatedAt:  now,
			})
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "org_id"}, {Name: "role_id"}, {Name: "object_slug"}, {Name: "field_key"}},
			DoUpdates: clause.AssignmentColumns([]string{"level", "updated_at"}),
		}).Create(&rows).Error
	})
}

// CountFieldRestrictionsByObject returns object_slug → FLS restriction-row count
// for the org in one GROUP BY query. Rows belonging to the owner role are
// excluded (JOIN roles, is_owner = FALSE): the owner bypasses FLS entirely, so
// any owner rows are phantoms that must never inflate the admin badges (U3).
func (r *permissionRepository) CountFieldRestrictionsByObject(ctx context.Context, orgID uuid.UUID) (map[string]int, error) {
	type row struct {
		ObjectSlug string
		Cnt        int
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("field_permissions AS fp").
		Select("fp.object_slug, COUNT(*) AS cnt").
		Joins("JOIN roles ro ON ro.id = fp.role_id").
		Where("fp.org_id = ? AND ro.is_owner = FALSE", orgID).
		Group("fp.object_slug").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.ObjectSlug] = r.Cnt
	}
	return out, nil
}
