package usecase

import (
	"context"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// permissionCache lets the role usecase (a) bust the permission/capability cache
// after a change so edits apply on the next request, and (b) materialize the
// org's default OLS grid before a clone so there are source rows to copy.
// permissionUseCase satisfies both.
type permissionCache interface {
	Invalidate(orgID uuid.UUID)
	EnsureSeeded(ctx context.Context, orgID uuid.UUID) error
}

type roleUseCase struct {
	repo  domain.RoleRepository
	cache permissionCache
	audit domain.AuthEventWriter // optional; nil in unit tests → audit is a no-op
	// sessions evicts members' cached sessions when a role is renamed or
	// rescoped: the session cache carries roleName + data_scope, so without
	// eviction every member of an edited role keeps the old name/scope (and, on
	// rename, gets default-denied against the re-keyed permission cache) for up
	// to 5 minutes (P10 P0). Optional; nil → no-op.
	sessions SessionEvictor
}

// NewRoleUseCase wires the custom-role usecase. audit (P4) and sessions (P10 P0)
// may be nil in unit tests — both degrade to no-ops.
func NewRoleUseCase(repo domain.RoleRepository, cache permissionCache, audit domain.AuthEventWriter, sessions SessionEvictor) domain.RoleUseCase {
	return &roleUseCase{repo: repo, cache: cache, audit: audit, sessions: sessions}
}

// evictMemberSessions busts the session-cache entry of every member holding the
// role, so a rename/rescope applies on their next request.
func (uc *roleUseCase) evictMemberSessions(ctx context.Context, orgID, roleID uuid.UUID) {
	if uc.sessions == nil {
		return
	}
	ids, err := uc.repo.ListMemberIDs(ctx, orgID, roleID)
	if err != nil {
		return
	}
	for _, uid := range ids {
		uc.sessions.EvictOrgSession(ctx, uid, orgID)
	}
}

// evictSessions evicts a given set of members' (user, org) session-cache entries
// — used after a reassign so the moved members pick up their new role on the next
// request instead of after the 5-minute TTL.
func (uc *roleUseCase) evictSessions(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID) {
	if uc.sessions == nil {
		return
	}
	for _, uid := range userIDs {
		uc.sessions.EvictOrgSession(ctx, uid, orgID)
	}
}

func (uc *roleUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.RoleDetail, error) {
	return uc.repo.ListDetailed(ctx, orgID)
}

// Create makes a custom role, optionally cloning another role's OLS/FLS grids,
// capabilities, and data_scope. The name must be unique in the org and must not
// shadow a system role. Capabilities are validated against the fixed vocabulary.
func (uc *roleUseCase) Create(ctx context.Context, orgID uuid.UUID, in domain.CreateRoleInput) (*domain.Role, error) {
	name := strings.TrimSpace(in.Name)
	if len(name) < 2 {
		return nil, domain.NewAppError(http.StatusBadRequest, "role name must be at least 2 characters")
	}

	existing, err := uc.repo.FindByNameInOrg(ctx, orgID, name)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if existing != nil {
		return nil, domain.NewAppError(http.StatusConflict, "a role named '"+name+"' already exists")
	}

	// Resolve the seed source (clone) if requested.
	var source *domain.Role
	if in.CloneFromID != nil && *in.CloneFromID != uuid.Nil {
		// Materialize the org's default OLS grid first, so the source role has
		// explicit rows to copy (a never-configured org relies on the lazy default
		// seed, which otherwise wouldn't have run yet).
		if err := uc.cache.EnsureSeeded(ctx, orgID); err != nil {
			return nil, domain.ErrInternal
		}
		source, err = uc.repo.GetInOrg(ctx, orgID, *in.CloneFromID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if source == nil {
			return nil, domain.NewAppError(http.StatusBadRequest, "clone_from_id role not found")
		}
	}

	dataScope := in.DataScope
	if dataScope == "" && source != nil {
		dataScope = source.DataScope
	}
	if dataScope == "" {
		dataScope = domain.DataScopeAll
	}
	if dataScope != domain.DataScopeOwn && dataScope != domain.DataScopeAll {
		return nil, domain.NewAppError(http.StatusBadRequest, "data_scope must be 'own' or 'all'")
	}

	// Capabilities: explicit input wins; else inherit the clone source's; else none.
	caps := in.Capabilities
	if caps == nil && source != nil {
		if caps, err = uc.repo.GetCapabilities(ctx, source.ID); err != nil {
			return nil, domain.ErrInternal
		}
	}
	caps, err = sanitizeCapabilities(caps)
	if err != nil {
		return nil, err
	}

	org := orgID
	role := &domain.Role{
		OrgID:       &org,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		IsSystem:    false,
		DataScope:   dataScope,
	}
	// Record the wizard's seed lineage (P6): the concrete source and the system
	// template it resolves to, so objects added later inherit the template's OLS
	// via EnsureDefaults instead of leaving this role invisibly denied.
	if source != nil {
		src := source.ID
		role.SeededFromRoleID = &src
		if tk := templateKeyOf(source); tk != "" {
			role.TemplateKey = &tk
		}
	}
	if err := uc.repo.CreateRole(ctx, role); err != nil {
		return nil, domain.ErrInternal
	}
	if source != nil {
		if err := uc.repo.ClonePermissions(ctx, orgID, source.ID, role.ID); err != nil {
			return nil, domain.ErrInternal
		}
	}
	if len(caps) > 0 {
		if err := uc.repo.SetCapabilities(ctx, orgID, role.ID, caps); err != nil {
			return nil, domain.ErrInternal
		}
	}
	uc.cache.Invalidate(orgID)

	meta := map[string]interface{}{"name": role.Name, "data_scope": role.DataScope}
	if source != nil {
		meta["cloned_from"] = source.Name
	}
	recordAdminEvent(ctx, uc.audit, orgID, "role.created", &role.ID, meta)
	return role, nil
}

// templateKeyOf resolves the system template a role descends from, for recording
// on a clone/duplicate's TemplateKey: a system role IS its own template; a custom
// role passes through its own TemplateKey (already the resolved system name).
// Empty when the source is a lineage-less custom role.
func templateKeyOf(role *domain.Role) string {
	if role == nil {
		return ""
	}
	if role.IsSystem {
		return role.Name
	}
	if role.TemplateKey != nil {
		return *role.TemplateKey
	}
	return ""
}

// Options returns the minimal role list any member may read to populate the role
// pickers (P6) — no capabilities, so it's safe outside roles.manage.
func (uc *roleUseCase) Options(ctx context.Context, orgID uuid.UUID) ([]domain.RoleOption, error) {
	return uc.repo.ListOptions(ctx, orgID)
}

// Update renames a custom role and/or changes its data_scope. System roles are
// immutable here (their name and default scope are fixed).
func (uc *roleUseCase) Update(ctx context.Context, orgID, id uuid.UUID, in domain.UpdateRoleInput) error {
	role, err := uc.repo.GetInOrg(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if role == nil {
		return domain.NewAppError(http.StatusNotFound, "role not found")
	}
	if role.IsSystem {
		return domain.NewAppError(http.StatusForbidden, "system roles cannot be renamed or rescoped")
	}

	changed := false
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if len(name) < 2 {
			return domain.NewAppError(http.StatusBadRequest, "role name must be at least 2 characters")
		}
		if name != role.Name {
			existing, err := uc.repo.FindByNameInOrg(ctx, orgID, name)
			if err != nil {
				return domain.ErrInternal
			}
			if existing != nil {
				return domain.NewAppError(http.StatusConflict, "a role named '"+name+"' already exists")
			}
			role.Name = name
			changed = true
		}
	}
	if in.Description != nil {
		desc := strings.TrimSpace(*in.Description)
		if desc != role.Description {
			role.Description = desc
			changed = true
		}
	}
	if in.DataScope != nil {
		if *in.DataScope != domain.DataScopeOwn && *in.DataScope != domain.DataScopeAll {
			return domain.NewAppError(http.StatusBadRequest, "data_scope must be 'own' or 'all'")
		}
		if role.DataScope != *in.DataScope {
			role.DataScope = *in.DataScope
			changed = true
		}
	}

	if err := uc.repo.UpdateRole(ctx, role); err != nil {
		return domain.ErrInternal
	}
	uc.cache.Invalidate(orgID)
	if changed {
		uc.evictMemberSessions(ctx, orgID, id)
	}

	changes := map[string]interface{}{"name": role.Name, "data_scope": role.DataScope}
	recordAdminEvent(ctx, uc.audit, orgID, "role.updated", &role.ID, changes)
	return nil
}

// Delete removes a custom role. System roles cannot be deleted. When members
// still hold the role, they must be reassigned first: reassignTo names the role
// to move them onto — the reassign is a single atomic UPDATE that commits before
// the role is deleted, so a delete failure leaves the members safely on the target
// (already evicted) and a now-empty source role that is re-deletable on retry. A
// nil reassignTo with members present is a 409 that drives the UI's "N people have
// this role — move them to:" picker (P6 delete-with-reassign).
func (uc *roleUseCase) Delete(ctx context.Context, orgID, id uuid.UUID, reassignTo *uuid.UUID) error {
	role, err := uc.repo.GetInOrg(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if role == nil {
		return domain.NewAppError(http.StatusNotFound, "role not found")
	}
	if role.IsSystem {
		return domain.NewAppError(http.StatusForbidden, "system roles cannot be deleted")
	}
	count, err := uc.repo.CountActiveMembers(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if count > 0 {
		if reassignTo == nil {
			return domain.NewAppError(http.StatusConflict, "reassign the members holding this role before deleting it")
		}
		if *reassignTo == id {
			return domain.NewAppError(http.StatusBadRequest, "cannot reassign members to the role being deleted")
		}
		target, err := uc.repo.GetInOrg(ctx, orgID, *reassignTo)
		if err != nil {
			return domain.ErrInternal
		}
		if target == nil {
			return domain.NewAppError(http.StatusBadRequest, "reassign target role not found")
		}
		if domain.IsOwnerRole(target) {
			return domain.NewAppError(http.StatusForbidden, "members cannot be reassigned into the owner role")
		}
		// The move is one atomic UPDATE ... RETURNING, so the moved set we evict is
		// exactly who was reassigned (no capture-then-move race). The caller holds
		// roles.manage via the route gate, so no escalation re-check is needed to
		// move members onto the target. The move commits before DeleteRole; if the
		// delete then fails, members are already on the target (and evicted) and the
		// now-empty source role is safely re-deletable on retry.
		movedIDs, err := uc.repo.ReassignMembers(ctx, orgID, id, *reassignTo)
		if err != nil {
			return domain.ErrInternal
		}
		uc.evictSessions(ctx, orgID, movedIDs)
	}
	if err := uc.repo.DeleteRole(ctx, orgID, id); err != nil {
		return domain.ErrInternal
	}
	uc.cache.Invalidate(orgID)

	meta := map[string]interface{}{"name": role.Name, "members": count}
	if reassignTo != nil {
		meta["reassigned_to"] = reassignTo.String()
	}
	recordAdminEvent(ctx, uc.audit, orgID, "role.deleted", &id, meta)
	return nil
}

// Duplicate clones a role (system or custom) into a new org-scoped custom role the
// admin can then tune — the in-place-edit substitute for the immutable system
// templates (plan §3.8, §5 #1). It copies capabilities, OLS/FLS grids, data_scope,
// and description, records the source as lineage (SeededFromRoleID + TemplateKey),
// and — when in.ReassignMembers — moves the source's members onto the copy.
func (uc *roleUseCase) Duplicate(ctx context.Context, orgID, id uuid.UUID, in domain.DuplicateRoleInput) (*domain.Role, error) {
	name := strings.TrimSpace(in.Name)
	if len(name) < 2 {
		return nil, domain.NewAppError(http.StatusBadRequest, "role name must be at least 2 characters")
	}
	source, err := uc.repo.GetInOrg(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if source == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "role not found")
	}
	existing, err := uc.repo.FindByNameInOrg(ctx, orgID, name)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if existing != nil {
		return nil, domain.NewAppError(http.StatusConflict, "a role named '"+name+"' already exists")
	}
	// Materialize the org's default grid so a system source has explicit OLS rows
	// to copy (the never-configured org otherwise relies on the lazy default seed).
	if err := uc.cache.EnsureSeeded(ctx, orgID); err != nil {
		return nil, domain.ErrInternal
	}

	org := orgID
	src := source.ID
	role := &domain.Role{
		OrgID:            &org,
		Name:             name,
		Description:      source.Description,
		IsSystem:         false,
		DataScope:        source.DataScope,
		SeededFromRoleID: &src,
	}
	if tk := templateKeyOf(source); tk != "" {
		role.TemplateKey = &tk
	}
	if err := uc.repo.CreateRole(ctx, role); err != nil {
		return nil, domain.ErrInternal
	}
	if err := uc.repo.ClonePermissions(ctx, orgID, source.ID, role.ID); err != nil {
		return nil, domain.ErrInternal
	}
	caps, err := uc.repo.GetCapabilities(ctx, source.ID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if len(caps) > 0 {
		if err := uc.repo.SetCapabilities(ctx, orgID, role.ID, caps); err != nil {
			return nil, domain.ErrInternal
		}
	}

	movedCount := 0
	if in.ReassignMembers {
		movedIDs, err := uc.repo.ReassignMembers(ctx, orgID, source.ID, role.ID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		movedCount = len(movedIDs)
		uc.evictSessions(ctx, orgID, movedIDs)
	}
	uc.cache.Invalidate(orgID)

	recordAdminEvent(ctx, uc.audit, orgID, "role.duplicated", &role.ID,
		map[string]interface{}{"name": role.Name, "duplicated_from": source.Name, "reassigned_members": movedCount})
	return role, nil
}

func (uc *roleUseCase) GetCapabilities(ctx context.Context, orgID, id uuid.UUID) ([]string, error) {
	role, err := uc.repo.GetInOrg(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if role == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "role not found")
	}
	return uc.repo.GetCapabilities(ctx, id)
}

// SetCapabilities replaces a CUSTOM role's capability set. System roles are
// global singletons shared by every org, so editing their capabilities per-org is
// refused (it would corrupt other tenants + risk cascade deletion) — clone the
// system role into an org-scoped custom role and edit that instead. owner is a
// system role too, so it is covered by the same guard.
func (uc *roleUseCase) SetCapabilities(ctx context.Context, orgID, id uuid.UUID, in domain.SetCapabilitiesInput) error {
	role, err := uc.repo.GetInOrg(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if role == nil {
		return domain.NewAppError(http.StatusNotFound, "role not found")
	}
	if role.IsSystem {
		return domain.NewAppError(http.StatusForbidden, "system roles can't be edited — clone this role to customize its capabilities")
	}
	caps, err := sanitizeCapabilities(in.Capabilities)
	if err != nil {
		return err
	}

	// Self-lockout guard (U0.5): an admin editing THEIR OWN role must not strip
	// roles.manage from it — one click would lock everyone but the owner out of
	// the entire permissions surface, with no one left able to undo it. The
	// escalation guard covers assignment; this covers self-mutation. Owner
	// bypasses (god-mode never depends on capability rows).
	if caller, ok := domain.CallerFromContext(ctx); ok && !caller.IsOwner && caller.RoleID == id {
		keeps := false
		for _, c := range caps {
			if c == domain.CapRolesManage {
				keeps = true
				break
			}
		}
		if !keeps {
			return domain.NewAppError(http.StatusConflict, "you can't remove 'Manage roles & permissions' from your own role — you would lock yourself out of this page. Have another admin change it, or edit a different role.")
		}
	}

	oldCaps, _ := uc.repo.GetCapabilities(ctx, id) // best-effort, for the audit diff
	if err := uc.repo.SetCapabilities(ctx, orgID, id, caps); err != nil {
		return domain.ErrInternal
	}
	uc.cache.Invalidate(orgID)

	recordAdminEvent(ctx, uc.audit, orgID, "role.capabilities_changed", &id,
		map[string]interface{}{"role": role.Name, "old": oldCaps, "new": caps})
	return nil
}

// sanitizeCapabilities validates + de-duplicates capability codes against the
// fixed vocabulary, rejecting anything unknown so a typo can't silently grant
// (or store) a non-capability.
func sanitizeCapabilities(codes []string) ([]string, error) {
	seen := make(map[string]bool, len(codes))
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		if !domain.IsCapability(c) {
			return nil, domain.NewAppError(http.StatusBadRequest, "unknown capability: "+c)
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}
