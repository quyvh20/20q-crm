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
//
// NOTE: this cache is per-process. Invalidate only reaches the local replica, so
// a permission edit propagates to OTHER replicas after the 60s TTL, not
// instantly. Acceptable while Railway runs a single replica (P10 §3.1); revisit
// with Redis pub/sub fan-out if replicas ever scale.
type permissionUseCase struct {
	repo       domain.PermissionRepository
	registryUC domain.ObjectRegistryUseCase
	audit      domain.AuthEventWriter // optional; nil in unit tests → grid-edit audit is a no-op
	ttl        time.Duration

	mu    sync.RWMutex
	cache map[uuid.UUID]*orgAccessEntry
}

type orgAccessEntry struct {
	// access is the OLS map: roleID → object → access bits (P5 re-key: grants are
	// looked up by role identity so a rename can never detach or merge them).
	access map[uuid.UUID]map[string]domain.ObjectAccess
	// fieldAccess is the FLS map: roleID → object → fieldKey → level. Empty when
	// no field is restricted (the common case), so FLS costs nothing until used.
	fieldAccess map[uuid.UUID]map[string]map[string]string
	// capabilities is the system-capability map: roleID → capabilityCode → true
	// (P3, D5). An absent role/code default-denies.
	capabilities map[uuid.UUID]map[string]bool
	expiry       time.Time
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
//
// The optional variadic audit writer (P4) records OLS/FLS grid edits without
// breaking the existing two-arg test call sites; when omitted, grid edits aren't
// audited.
func NewPermissionUseCase(repo domain.PermissionRepository, registryUC domain.ObjectRegistryUseCase, audit ...domain.AuthEventWriter) domain.PermissionUseCase {
	uc := &permissionUseCase{
		repo:       repo,
		registryUC: registryUC,
		ttl:        defaultPermissionCacheTTL,
		cache:      make(map[uuid.UUID]*orgAccessEntry),
	}
	if len(audit) > 0 {
		uc.audit = audit[0]
	}
	return uc
}

// ============================================================
// RecordAuthorizer (called by RecordService on every entry)
// ============================================================

// callerIsOwner is the owner check: the IsOwner flag (resolved from
// roles.is_owner by the middleware, never a name string) is authoritative. The
// P5 name-fallback bridge was deleted in P9, so a caller merely NAMED 'owner'
// does not bypass — only the resolved flag does.
func callerIsOwner(caller domain.Caller) bool {
	return caller.IsOwner
}

// resolveRoleID returns the role identity authorization keys off (R1 re-key). A
// caller with a RoleID uses it directly — present or not in the grant maps,
// absence default-denies. Since the P9 bridge removal there is no name fallback:
// a caller carrying no RoleID resolves to nothing and is default-denied.
func (uc *permissionUseCase) resolveRoleID(caller domain.Caller) (uuid.UUID, bool) {
	return caller.RoleID, caller.RoleID != uuid.Nil
}

// Authorize enforces default-deny OLS. A context with no caller is a trusted
// in-process call (automation, AI, seed) and is allowed; the owner bypasses
// OLS; otherwise the caller's role must have the action bit for the object, and
// the absence of any row denies.
func (uc *permissionUseCase) Authorize(ctx context.Context, orgID uuid.UUID, slug string, action domain.RecordAction) error {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return nil // trusted in-process call — middleware always sets a caller for user traffic
	}
	if callerIsOwner(caller) {
		return nil // god-mode
	}

	entry := uc.loadEntry(ctx, orgID)
	if roleID, ok := uc.resolveRoleID(caller); ok {
		if entry.access[roleID][slug].Allows(action) {
			return nil
		}
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

// FieldMask returns the caller's Field-Level Security restrictions for an object
// (P5b). It mirrors Authorize's bypasses: a context with no caller is a trusted
// in-process call, and the owner role bypasses FLS — both get the empty mask. So
// does any role/object with no field_permissions rows, so FLS is free until a
// field is actually restricted. Only 'hidden'/'read' levels produce mask entries;
// 'edit' (the default) never has a stored row.
func (uc *permissionUseCase) FieldMask(ctx context.Context, orgID uuid.UUID, slug string) domain.FieldMask {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return domain.FieldMask{} // trusted in-process call
	}
	if callerIsOwner(caller) {
		return domain.FieldMask{} // god-mode, matching OLS
	}

	entry := uc.loadEntry(ctx, orgID)
	roleID, ok := uc.resolveRoleID(caller)
	if !ok {
		return domain.FieldMask{} // unresolvable role: OLS already default-denies access entirely
	}
	byObject := entry.fieldAccess[roleID]
	if byObject == nil {
		return domain.FieldMask{}
	}
	levels := byObject[slug]
	if len(levels) == 0 {
		return domain.FieldMask{}
	}

	var mask domain.FieldMask
	for key, level := range levels {
		switch domain.FieldLevel(level) {
		case domain.FieldLevelHidden:
			if mask.Hidden == nil {
				mask.Hidden = make(map[string]bool)
			}
			mask.Hidden[key] = true
		case domain.FieldLevelRead:
			if mask.ReadOnly == nil {
				mask.ReadOnly = make(map[string]bool)
			}
			mask.ReadOnly[key] = true
		}
	}
	return mask
}

// HasCapability enforces default-deny system capabilities (P3, D5). It mirrors
// Authorize's bypasses: a context with no caller is a trusted in-process call
// (allowed); the owner role bypasses every capability check (god-mode). Otherwise
// the caller's role must hold the capability code, and the absence of a row
// denies with a 403 that names the missing capability.
func (uc *permissionUseCase) HasCapability(ctx context.Context, orgID uuid.UUID, capability string) error {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return nil // trusted in-process call
	}
	if callerIsOwner(caller) {
		return nil // god-mode
	}

	entry := uc.loadEntry(ctx, orgID)
	if roleID, ok := uc.resolveRoleID(caller); ok {
		if entry.capabilities[roleID][capability] {
			return nil
		}
	}
	return domain.NewAppError(http.StatusForbidden, "you do not have the '"+capability+"' capability")
}

// CallerCapabilities returns the caller's effective capability codes for the org
// — the full vocabulary for owner (god-mode), otherwise the role's granted set.
// Empty for a callerless (trusted in-process) context. Used by the SPA to gate
// permission-aware UI; the server still enforces every action independently.
func (uc *permissionUseCase) CallerCapabilities(ctx context.Context, orgID uuid.UUID) []string {
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return []string{}
	}
	if callerIsOwner(caller) {
		return append([]string{}, domain.AllCapabilities...)
	}
	entry := uc.loadEntry(ctx, orgID)
	roleID, ok := uc.resolveRoleID(caller)
	if !ok {
		return []string{}
	}
	byCode := entry.capabilities[roleID]
	out := make([]string, 0, len(byCode))
	for code, granted := range byCode {
		if granted {
			out = append(out, code)
		}
	}
	return out
}

// loadEntry returns the org's cached OLS + FLS maps, refreshing on a cold/expired
// entry. On the cold path it first ensures the default seed so a never-configured
// org (or a freshly added object) isn't accidentally locked out. The two maps are
// loaded and cached together so the OLS and FLS views can never diverge: on any
// load error it serves the prior entry if present, else an empty entry whose empty
// OLS map default-denies. (Crucially, a field-access load failure must not leave a
// permissive OLS map with FLS silently emptied — that would leak sensitive fields,
// so a partial failure falls back to the coherent prior/empty entry.)
func (uc *permissionUseCase) loadEntry(ctx context.Context, orgID uuid.UUID) *orgAccessEntry {
	uc.mu.RLock()
	e := uc.cache[orgID]
	uc.mu.RUnlock()
	if e != nil && time.Now().Before(e.expiry) {
		return e
	}

	if err := uc.repo.EnsureDefaults(ctx, orgID); err != nil {
		// Non-fatal: a previously seeded org still loads its existing rows below.
		log.Printf("object_permissions: ensure defaults for org %s: %v", orgID, err)
	}
	access, err := uc.repo.LoadOrgAccess(ctx, orgID)
	if err != nil {
		log.Printf("object_permissions: load access for org %s: %v", orgID, err)
		return uc.staleOrEmpty(e)
	}
	fieldAccess, err := uc.repo.LoadOrgFieldAccess(ctx, orgID)
	if err != nil {
		log.Printf("field_permissions: load access for org %s: %v", orgID, err)
		return uc.staleOrEmpty(e)
	}
	capabilities, err := uc.repo.LoadOrgCapabilities(ctx, orgID)
	if err != nil {
		log.Printf("role_permissions: load capabilities for org %s: %v", orgID, err)
		return uc.staleOrEmpty(e)
	}

	entry := &orgAccessEntry{
		access:       access,
		fieldAccess:  fieldAccess,
		capabilities: capabilities,
		expiry:       time.Now().Add(uc.ttl),
	}
	uc.mu.Lock()
	uc.cache[orgID] = entry
	uc.mu.Unlock()
	return entry
}

// staleOrEmpty serves the prior entry on a transient load error rather than
// flip-flopping the org's security state; with no prior entry it returns an empty
// entry whose empty OLS map default-denies (so reads are blocked, not leaked).
func (uc *permissionUseCase) staleOrEmpty(e *orgAccessEntry) *orgAccessEntry {
	if e != nil {
		return e
	}
	return &orgAccessEntry{access: map[uuid.UUID]map[string]domain.ObjectAccess{}}
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
	for i := range roles {
		grid.Roles = append(grid.Roles, domain.PermRoleInfo{
			ID:       roles[i].ID,
			Name:     roles[i].Name,
			IsSystem: roles[i].IsSystem,
			IsOwner:  domain.IsOwnerRole(&roles[i]),
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

	roleID := in.RoleID
	recordAdminEvent(ctx, uc.audit, orgID, "permission.ols_changed", &roleID,
		map[string]interface{}{
			"role_id":     in.RoleID.String(),
			"object_slug": in.ObjectSlug,
			"read":        in.CanRead,
			"create":      in.CanCreate,
			"edit":        in.CanEdit,
			"delete":      in.CanDelete,
		})
	return nil
}

// GetFieldGrid returns the field × role level matrix for one object — the admin
// Field-Level Security UI. Fields come from the registry schema (so system and
// custom objects expose the same field list the records are keyed by); the matrix
// holds only the non-default (read/hidden) cells, everything else being edit.
func (uc *permissionUseCase) GetFieldGrid(ctx context.Context, orgID uuid.UUID, slug string) (*domain.FieldPermissionGrid, error) {
	schema, err := uc.registryUC.GetSchema(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	roles, err := uc.repo.ListRoles(ctx, orgID)
	if err != nil {
		return nil, err
	}
	perms, err := uc.repo.ListFieldPermissions(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}

	grid := &domain.FieldPermissionGrid{
		Slug:   schema.Slug,
		Label:  schema.Label,
		Fields: make([]domain.FieldPermFieldInfo, 0, len(schema.Fields)),
		Roles:  make([]domain.PermRoleInfo, 0, len(roles)),
		Matrix: make([]domain.FieldPermissionMatrix, 0, len(perms)),
	}
	for _, f := range schema.Fields {
		grid.Fields = append(grid.Fields, domain.FieldPermFieldInfo{
			Key:      f.Key,
			Label:    f.Label,
			Type:     f.Type,
			IsSystem: f.IsSystem,
		})
	}
	for i := range roles {
		grid.Roles = append(grid.Roles, domain.PermRoleInfo{
			ID:       roles[i].ID,
			Name:     roles[i].Name,
			IsSystem: roles[i].IsSystem,
			IsOwner:  domain.IsOwnerRole(&roles[i]),
		})
	}
	for _, p := range perms {
		grid.Matrix = append(grid.Matrix, domain.FieldPermissionMatrix{
			RoleID:   p.RoleID,
			FieldKey: p.FieldKey,
			Level:    p.Level,
		})
	}
	return grid, nil
}

// SetFieldPermission sets one (role, field) level and busts the cache so the
// change applies on the next request. Level 'edit' is the default and is stored
// as the *absence* of a row — so it deletes any existing restriction, keeping the
// table to genuine restrictions only.
func (uc *permissionUseCase) SetFieldPermission(ctx context.Context, orgID uuid.UUID, in domain.SetFieldPermissionInput) error {
	if in.RoleID == uuid.Nil || in.ObjectSlug == "" || in.FieldKey == "" {
		return domain.NewAppError(http.StatusBadRequest, "role_id, object_slug and field_key are required")
	}
	level := domain.FieldLevel(in.Level)
	if !level.Valid() {
		return domain.NewAppError(http.StatusBadRequest, "level must be one of: hidden, read, edit")
	}

	var err error
	if level == domain.FieldLevelEdit {
		err = uc.repo.DeleteFieldPermission(ctx, orgID, in.RoleID, in.ObjectSlug, in.FieldKey)
	} else {
		err = uc.repo.UpsertFieldPermission(ctx, domain.FieldPermission{
			OrgID:      orgID,
			RoleID:     in.RoleID,
			ObjectSlug: in.ObjectSlug,
			FieldKey:   in.FieldKey,
			Level:      in.Level,
		})
	}
	if err != nil {
		return err
	}
	uc.Invalidate(orgID)

	roleID := in.RoleID
	recordAdminEvent(ctx, uc.audit, orgID, "permission.fls_changed", &roleID,
		map[string]interface{}{
			"role_id":     in.RoleID.String(),
			"object_slug": in.ObjectSlug,
			"field_key":   in.FieldKey,
			"level":       in.Level,
		})
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

// EnsureSeeded idempotently materializes the org's default OLS grid (delegating
// to the repo's advisory-locked seed), so a role clone has explicit source rows
// to copy rather than relying on the yet-unseeded default.
func (uc *permissionUseCase) EnsureSeeded(ctx context.Context, orgID uuid.UUID) error {
	return uc.repo.EnsureDefaults(ctx, orgID)
}

// Invalidate drops the cached access map for an org (on a permission edit, or
// when the object set changes).
func (uc *permissionUseCase) Invalidate(orgID uuid.UUID) {
	uc.mu.Lock()
	delete(uc.cache, orgID)
	uc.mu.Unlock()
}
