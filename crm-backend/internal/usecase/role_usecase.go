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
}

func NewRoleUseCase(repo domain.RoleRepository, cache permissionCache) domain.RoleUseCase {
	return &roleUseCase{repo: repo, cache: cache}
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
		OrgID:     &org,
		Name:      name,
		IsSystem:  false,
		DataScope: dataScope,
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
	return role, nil
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
		}
	}
	if in.DataScope != nil {
		if *in.DataScope != domain.DataScopeOwn && *in.DataScope != domain.DataScopeAll {
			return domain.NewAppError(http.StatusBadRequest, "data_scope must be 'own' or 'all'")
		}
		role.DataScope = *in.DataScope
	}

	if err := uc.repo.UpdateRole(ctx, role); err != nil {
		return domain.ErrInternal
	}
	uc.cache.Invalidate(orgID)
	return nil
}

// Delete removes a custom role that no active member holds. System roles cannot
// be deleted.
func (uc *roleUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
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
		return domain.NewAppError(http.StatusConflict, "reassign the members holding this role before deleting it")
	}
	if err := uc.repo.DeleteRole(ctx, orgID, id); err != nil {
		return domain.ErrInternal
	}
	uc.cache.Invalidate(orgID)
	return nil
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
	if err := uc.repo.SetCapabilities(ctx, orgID, id, caps); err != nil {
		return domain.ErrInternal
	}
	uc.cache.Invalidate(orgID)
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
