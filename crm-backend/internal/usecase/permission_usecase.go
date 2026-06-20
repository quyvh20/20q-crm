package usecase

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// permissionUseCase is the Object-Level Security engine (P5a). One concrete type
// implements two interfaces:
//
//   - domain.RecordAuthorizer — the narrow port RecordService calls to enforce
//     OLS (Authorize) and append the audit trail (Audit).
//   - domain.PermissionUseCase — the admin-facing role × object grid + per-record
//     audit view.
//
// The org → (role → object → access) map is cached in-process with a short TTL
// and an explicit Invalidate, so a permission edit applies on the next request
// without a restart (plan R5: O(1) after warm).
type permissionUseCase struct {
	repo       domain.PermissionRepository
	registryUC domain.ObjectRegistryUseCase
	ttl        time.Duration

	mu    sync.RWMutex
	cache map[uuid.UUID]*orgAccessEntry
}

type orgAccessEntry struct {
	access map[string]map[string]domain.ObjectAccess
	expiry time.Time
}

// Compile-time proof the one type satisfies both ports.
var (
	_ domain.PermissionUseCase = (*permissionUseCase)(nil)
	_ domain.RecordAuthorizer  = (*permissionUseCase)(nil)
)

const defaultPermissionCacheTTL = 60 * time.Second

// NewPermissionUseCase returns the OLS engine as a PermissionUseCase. Because
// that interface embeds RecordAuthorizer, the same value is handed to
// RecordService as its authorizer (see main.go) — no second constructor, no type
// assertion.
func NewPermissionUseCase(repo domain.PermissionRepository, registryUC domain.ObjectRegistryUseCase) domain.PermissionUseCase {
	return &permissionUseCase{
		repo:       repo,
		registryUC: registryUC,
		ttl:        defaultPermissionCacheTTL,
		cache:      make(map[uuid.UUID]*orgAccessEntry),
	}
}

// ============================================================
// RecordAuthorizer (called by RecordService on every entry)
// ============================================================

// Authorize enforces default-deny OLS. A context with no caller is a trusted
// in-process call (automation, AI, seed) and is allowed; the owner role bypasses
// OLS; otherwise the caller's role must have the action bit for the object, and
// the absence of any row denies.
func (uc *permissionUseCase) Authorize(ctx context.Context, orgID uuid.UUID, slug string, action domain.RecordAction) error {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return nil // trusted in-process call — middleware always sets a caller for user traffic
	}
	if caller.Role == domain.RoleOwner {
		return nil // god-mode, matching RequireRole
	}

	access := uc.accessFor(ctx, orgID, caller.Role, slug)
	if access.Allows(action) {
		return nil
	}
	return domain.NewAppError(http.StatusForbidden, "you do not have permission to "+string(action)+" "+slug+" records")
}

// Audit appends one audit row. Best-effort: a failure is logged but never
// surfaced, so an audit hiccup can't undo a successful write. Synchronous (not a
// goroutine) so it shares the request's transaction visibility and ordering; the
// write is a single fast insert.
func (uc *permissionUseCase) Audit(ctx context.Context, e domain.AuditEntry) {
	changes := e.Changes
	if changes == nil {
		changes = map[string]interface{}{}
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		raw = []byte("{}")
	}
	row := &domain.ObjectAudit{
		OrgID:      e.OrgID,
		ObjectSlug: e.ObjectSlug,
		RecordID:   e.RecordID,
		Action:     string(e.Action),
		Changes:    domain.JSON(raw),
	}
	if e.ActorID != uuid.Nil {
		id := e.ActorID
		row.ActorID = &id
	}
	if err := uc.repo.WriteAudit(ctx, row); err != nil {
		log.Printf("object_audit: failed to record %s on %s/%s: %v", e.Action, e.ObjectSlug, e.RecordID, err)
	}
}

// accessFor returns the cached access for one role+object. A missing role or
// missing object entry is the zero ObjectAccess — i.e. default-deny.
func (uc *permissionUseCase) accessFor(ctx context.Context, orgID uuid.UUID, role, slug string) domain.ObjectAccess {
	m := uc.loadCached(ctx, orgID)
	if byObject, ok := m[role]; ok {
		return byObject[slug]
	}
	return domain.ObjectAccess{}
}

// loadCached returns the org's access map, refreshing on a cold/expired entry.
// On the cold path it first ensures the default seed so a never-configured org
// (or a freshly added object) isn't accidentally locked out. On a load error it
// serves the stale entry if present, else an empty map (fail closed).
func (uc *permissionUseCase) loadCached(ctx context.Context, orgID uuid.UUID) map[string]map[string]domain.ObjectAccess {
	uc.mu.RLock()
	e := uc.cache[orgID]
	uc.mu.RUnlock()
	if e != nil && time.Now().Before(e.expiry) {
		return e.access
	}

	if err := uc.repo.EnsureDefaults(ctx, orgID); err != nil {
		// Non-fatal: a previously seeded org still loads its existing rows below.
		log.Printf("object_permissions: ensure defaults for org %s: %v", orgID, err)
	}
	access, err := uc.repo.LoadOrgAccess(ctx, orgID)
	if err != nil {
		log.Printf("object_permissions: load access for org %s: %v", orgID, err)
		if e != nil {
			return e.access // serve stale rather than lock everyone out on a transient error
		}
		return map[string]map[string]domain.ObjectAccess{}
	}

	uc.mu.Lock()
	uc.cache[orgID] = &orgAccessEntry{access: access, expiry: time.Now().Add(uc.ttl)}
	uc.mu.Unlock()
	return access
}

// ============================================================
// PermissionUseCase (admin grid + audit view)
// ============================================================

// GetGrid returns the role × object matrix. It ensures defaults first so every
// current object (including ones added since the last seed) appears with its
// effective access rather than as an empty (default-deny) column.
func (uc *permissionUseCase) GetGrid(ctx context.Context, orgID uuid.UUID) (*domain.PermissionGrid, error) {
	if err := uc.repo.EnsureDefaults(ctx, orgID); err != nil {
		return nil, err
	}

	objects, err := uc.registryUC.ListObjects(ctx, orgID)
	if err != nil {
		return nil, err
	}
	roles, err := uc.repo.ListRoles(ctx, orgID)
	if err != nil {
		return nil, err
	}
	perms, err := uc.repo.ListPermissions(ctx, orgID)
	if err != nil {
		return nil, err
	}

	grid := &domain.PermissionGrid{
		Objects: make([]domain.PermObjectInfo, 0, len(objects)),
		Roles:   make([]domain.PermRoleInfo, 0, len(roles)),
		Matrix:  make([]domain.PermissionMatrixCell, 0, len(perms)),
	}
	for _, o := range objects {
		grid.Objects = append(grid.Objects, domain.PermObjectInfo{
			Slug:     o.Slug,
			Label:    o.Label,
			Icon:     o.Icon,
			IsSystem: o.IsSystem,
		})
	}
	for _, role := range roles {
		grid.Roles = append(grid.Roles, domain.PermRoleInfo{
			ID:       role.ID,
			Name:     role.Name,
			IsSystem: role.IsSystem,
			IsOwner:  role.Name == domain.RoleOwner,
		})
	}
	for _, p := range perms {
		grid.Matrix = append(grid.Matrix, domain.PermissionMatrixCell{
			RoleID:     p.RoleID,
			ObjectSlug: p.ObjectSlug,
			ObjectAccess: domain.ObjectAccess{
				Read:   p.CanRead,
				Create: p.CanCreate,
				Edit:   p.CanEdit,
				Delete: p.CanDelete,
			},
		})
	}
	return grid, nil
}

// SetPermission upserts one cell and busts the cache so the change applies on the
// next request.
func (uc *permissionUseCase) SetPermission(ctx context.Context, orgID uuid.UUID, in domain.SetPermissionInput) error {
	if in.RoleID == uuid.Nil || in.ObjectSlug == "" {
		return domain.NewAppError(http.StatusBadRequest, "role_id and object_slug are required")
	}
	err := uc.repo.UpsertPermission(ctx, domain.ObjectPermission{
		OrgID:      orgID,
		RoleID:     in.RoleID,
		ObjectSlug: in.ObjectSlug,
		CanRead:    in.CanRead,
		CanCreate:  in.CanCreate,
		CanEdit:    in.CanEdit,
		CanDelete:  in.CanDelete,
	})
	if err != nil {
		return err
	}
	uc.Invalidate(orgID)
	return nil
}

// ListRecordAudit returns a record's change history. Viewing the trail requires
// OLS read on the object (defense in depth on top of the route's manager+ floor),
// so you can't inspect history for an object you can't otherwise read.
func (uc *permissionUseCase) ListRecordAudit(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) ([]domain.AuditView, error) {
	if err := uc.Authorize(ctx, orgID, slug, domain.ActionRead); err != nil {
		return nil, err
	}
	return uc.repo.ListAudit(ctx, orgID, slug, recordID, 100)
}

// Invalidate drops the cached access map for an org (on a permission edit, or
// when the object set changes).
func (uc *permissionUseCase) Invalidate(orgID uuid.UUID) {
	uc.mu.Lock()
	delete(uc.cache, orgID)
	uc.mu.Unlock()
}
