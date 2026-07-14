package usecase

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
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

// warnCallerlessHTTP makes the designed callerless fail-open observable
// (U0.10): a context with no caller is trusted by design for in-process calls
// (automation, AI, seed), but an HTTP-originated request should ALWAYS carry a
// caller — reaching authorization without one means a route is mounted outside
// AuthMiddleware and is silently passing every check. Logged, not denied, so a
// wiring bug surfaces in ops without changing behavior for legitimate callers.
func warnCallerlessHTTP(ctx context.Context, what string) {
	if domain.IsHTTPTransport(ctx) {
		log.Printf("authz: callerless HTTP request passed %s — fail-open invariant violated (route mounted outside AuthMiddleware?)", what)
	}
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
		warnCallerlessHTTP(ctx, "Authorize "+string(action)+" "+slug)
		return nil // trusted in-process call — middleware always sets a caller for user traffic
	}
	// An API token's scopes bind BEFORE the owner bypass (U6.5). This lives in the
	// permission engine rather than in the route middleware because most record READ
	// routes carry no route-level gate at all — they rely on RecordService calling
	// Authorize. Gating only in the middleware left every uniform read route
	// reachable by any token, no matter how narrowly scoped: exactly the leak
	// ScopeRecordsRead exists to prevent.
	if err := tokenScopeAllows(caller, recordScopeFor(action)); err != nil {
		return err
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
	return domain.NewAppError(http.StatusForbidden, "your role can't "+string(action)+" "+slug+" records — ask an admin for access")
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
		warnCallerlessHTTP(ctx, "HasCapability "+capability)
		return nil // trusted in-process call
	}
	// The token-scope intersection runs BEFORE the owner bypass (U6.5): applied
	// after, a leaked owner token would be god-mode regardless of the scopes its
	// creator picked. Enforcing it here rather than only in RequireCapability also
	// covers the callers that check capabilities directly — the AI command centre,
	// the report usecase, the workspace usecase — which the middleware never sees.
	if err := tokenScopeAllows(caller, capability); err != nil {
		return err
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
	return domain.NewAppError(http.StatusForbidden, "you need the \""+domain.CapabilityLabel(capability)+"\" permission to do this — ask an admin")
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

// CallerObjectAccess returns the caller's effective OLS bits per object slug —
// every registry object all-true for the owner (god-mode), otherwise the
// caller's role rows from the shared cached entry (same freshness class as
// HasCapability, so this is cheap enough to serve on every login). An object
// with no row is simply absent; the SPA treats a missing slug as denied. Empty
// for a callerless (trusted in-process) context.
func (uc *permissionUseCase) CallerObjectAccess(ctx context.Context, orgID uuid.UUID) map[string]domain.ObjectAccess {
	out := map[string]domain.ObjectAccess{}
	caller, ok := domain.CallerFromContext(ctx)
	if !ok {
		return out
	}
	if callerIsOwner(caller) {
		objects, err := uc.registryUC.ListObjects(ctx, orgID)
		if err != nil {
			log.Printf("object_permissions: list objects for owner access map, org %s: %v", orgID, err)
			return out
		}
		for _, o := range objects {
			out[o.Slug] = domain.ObjectAccess{Read: true, Create: true, Edit: true, Delete: true}
		}
		return out
	}
	roleID, ok := uc.resolveRoleID(caller)
	if !ok {
		return out
	}
	// Copy rather than alias the cached map so a caller can't mutate the cache.
	for slug, access := range uc.loadEntry(ctx, orgID).access[roleID] {
		out[slug] = access
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

	// The owner always has full access — rows for it would be pure phantoms
	// (FieldMask bypasses on IsOwner), so reject it like the bulk path does.
	roles, err := uc.repo.ListRoles(ctx, orgID)
	if err != nil {
		return err
	}
	for i := range roles {
		if roles[i].ID == in.RoleID && domain.IsOwnerRole(&roles[i]) {
			return domain.NewAppError(http.StatusBadRequest, "the workspace owner always has full access")
		}
	}

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

// EffectiveAccess renders one role's merged access over every registry object
// (U3): the OLS bits per object (an absent row renders all-false, making the
// default-deny visible) plus the role's FLS restrictions joined to field labels
// via the registry schema. Like GetGrid it reads repo-direct after
// EnsureDefaults — an admin-facing read must see a just-made edit, never a
// stale cached entry.
//
// OWNER TRAP: the owner role has no rows in any table by design (Authorize /
// FieldMask / HasCapability bypass on IsOwner), so it is synthesized here as
// full access on every object with no field restrictions.
func (uc *permissionUseCase) EffectiveAccess(ctx context.Context, orgID, roleID uuid.UUID) ([]domain.RoleObjectAccess, error) {
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
	var role *domain.Role
	for i := range roles {
		if roles[i].ID == roleID {
			role = &roles[i]
			break
		}
	}
	if role == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "role not found")
	}

	if domain.IsOwnerRole(role) {
		out := make([]domain.RoleObjectAccess, 0, len(objects))
		for _, o := range objects {
			out = append(out, domain.RoleObjectAccess{
				Slug: o.Slug, Label: o.Label, Icon: o.Icon, IsSystem: o.IsSystem,
				ObjectAccess:     domain.ObjectAccess{Read: true, Create: true, Edit: true, Delete: true},
				RestrictedFields: []domain.RoleRestrictedField{},
			})
		}
		return out, nil
	}

	// OLS bits for this role, by object.
	perms, err := uc.repo.ListPermissions(ctx, orgID)
	if err != nil {
		return nil, err
	}
	accessBySlug := make(map[string]domain.ObjectAccess)
	for _, p := range perms {
		if p.RoleID != roleID {
			continue
		}
		accessBySlug[p.ObjectSlug] = domain.ObjectAccess{
			Read: p.CanRead, Create: p.CanCreate, Edit: p.CanEdit, Delete: p.CanDelete,
		}
	}

	// FLS restrictions for this role, by object (org-wide in one query).
	fieldAccess, err := uc.repo.LoadOrgFieldAccess(ctx, orgID)
	if err != nil {
		return nil, err
	}
	levelsBySlug := fieldAccess[roleID]

	out := make([]domain.RoleObjectAccess, 0, len(objects))
	for _, o := range objects {
		entry := domain.RoleObjectAccess{
			Slug: o.Slug, Label: o.Label, Icon: o.Icon, IsSystem: o.IsSystem,
			ObjectAccess:     accessBySlug[o.Slug], // absent row → zero value = all-false
			RestrictedFields: []domain.RoleRestrictedField{},
		}
		if levels := levelsBySlug[o.Slug]; len(levels) > 0 {
			// Join keys to display labels via the registry schema (the GetFieldGrid
			// pattern). A restriction whose key no longer exists in the schema is a
			// stale leftover — skipped, never surfaced.
			schema, err := uc.registryUC.GetSchema(ctx, orgID, o.Slug)
			if err != nil {
				return nil, err
			}
			labelByKey := make(map[string]string, len(schema.Fields))
			for _, f := range schema.Fields {
				labelByKey[f.Key] = f.Label
			}
			for key, level := range levels {
				label, ok := labelByKey[key]
				if !ok {
					continue // stale key — the field was deleted after the restriction
				}
				entry.RestrictedFields = append(entry.RestrictedFields, domain.RoleRestrictedField{
					Key: key, Label: label, Level: level,
				})
			}
			sort.Slice(entry.RestrictedFields, func(i, j int) bool {
				return entry.RestrictedFields[i].Key < entry.RestrictedFields[j].Key
			})
		}
		out = append(out, entry)
	}
	return out, nil
}

// maxBulkFieldKeys caps one bulk FLS write — enough for any real object, small
// enough that a malicious payload can't turn one request into a huge statement.
const maxBulkFieldKeys = 200

// SetFieldPermissionsBulk applies one level to many fields of one (role, object)
// in a single transaction, with ONE cache invalidation and ONE audit event —
// the "restrict these 12 fields" path that would otherwise be 12 requests, 12
// invalidations, and 12 audit rows (U3). Level 'edit' deletes the rows; the
// owner role is rejected outright (it always has full access; storing rows for
// it would only create phantom grid state).
func (uc *permissionUseCase) SetFieldPermissionsBulk(ctx context.Context, orgID uuid.UUID, in domain.SetFieldPermissionsBulkInput) error {
	if in.RoleID == uuid.Nil || in.ObjectSlug == "" {
		return domain.NewAppError(http.StatusBadRequest, "role_id and object_slug are required")
	}
	level := domain.FieldLevel(in.Level)
	if !level.Valid() {
		return domain.NewAppError(http.StatusBadRequest, "level must be one of: hidden, read, edit")
	}
	if len(in.FieldKeys) > maxBulkFieldKeys {
		return domain.NewAppError(http.StatusBadRequest, "too many field_keys — at most 200 per request")
	}
	// Normalize: drop blanks and duplicates so the batch statement is clean.
	seen := make(map[string]bool, len(in.FieldKeys))
	keys := make([]string, 0, len(in.FieldKeys))
	for _, k := range in.FieldKeys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return domain.NewAppError(http.StatusBadRequest, "field_keys must not be empty")
	}

	// The owner always has full access — rows for it would be pure phantoms.
	roles, err := uc.repo.ListRoles(ctx, orgID)
	if err != nil {
		return err
	}
	for i := range roles {
		if roles[i].ID == in.RoleID && domain.IsOwnerRole(&roles[i]) {
			return domain.NewAppError(http.StatusBadRequest, "the workspace owner always has full access")
		}
	}

	if err := uc.repo.BulkSetFieldPermissions(ctx, orgID, in.RoleID, in.ObjectSlug, keys, in.Level); err != nil {
		return err
	}
	uc.Invalidate(orgID)

	roleID := in.RoleID
	recordAdminEvent(ctx, uc.audit, orgID, "permission.fls_bulk_changed", &roleID,
		map[string]interface{}{
			"role_id":     in.RoleID.String(),
			"object_slug": in.ObjectSlug,
			"level":       in.Level,
			"field_count": len(keys),
		})
	return nil
}

// FieldRestrictionSummary returns object_slug → FLS restriction-row count for
// the org's objects page badges (U3). Owner-role rows are excluded in the query
// so phantom rows (seeded or leftover) never inflate the numbers.
func (uc *permissionUseCase) FieldRestrictionSummary(ctx context.Context, orgID uuid.UUID) (map[string]int, error) {
	return uc.repo.CountFieldRestrictionsByObject(ctx, orgID)
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

// ============================================================
// API-token scope intersection (U6.5)
// ============================================================

// recordScopeFor maps a record action to the token scope that gates it. Record
// routes are governed by OLS, not by a capability, so there is no role capability
// to intersect with — record access is opt-in for tokens instead.
func recordScopeFor(action domain.RecordAction) string {
	if action == domain.ActionRead {
		return domain.ScopeRecordsRead
	}
	return domain.CapRecordsWrite
}

// tokenScopeAllows enforces a personal access token's own scope list. A normal
// session is unaffected (nil). For a token, the request passes only if the token
// was granted this scope — so a token is always a SUBSET of its owner, never equal
// to them and never more.
func tokenScopeAllows(caller domain.Caller, scope string) error {
	if !caller.IsAPIToken {
		return nil
	}
	for _, s := range caller.TokenScopes {
		if s == scope {
			return nil
		}
	}
	return domain.NewAppError(http.StatusForbidden,
		"this API token doesn't have permission to "+domain.CapabilityLabel(scope))
}
