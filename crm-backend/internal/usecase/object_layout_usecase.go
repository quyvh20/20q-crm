package usecase

// objectLayoutUseCase implements domain.ObjectLayoutUseCase.
//
// It manages per-role detail layouts for every object (P8). The hot path
// (ResolveLayout) is served from a per-org, 60-second cache so that the extra
// DB work folded into GET /api/registry/objects/:slug/schema is negligible.
// The cache is busted by Invalidate on every mutation, matching the OLS/FLS
// pattern from P5a/P5b.

import (
	"context"
	"net/http"
	"sync"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

const defaultLayoutCacheTTL = 60 * time.Second

// orgLayoutEntry is the per-org cache entry. It holds all layouts per object slug
// and a role→layout mapping, so per-request resolution is O(1) after warm.
type orgLayoutEntry struct {
	// layouts: objectSlug → ordered list of layouts
	layouts map[string][]domain.ObjectLayout
	// roleMap: id-keyed role→layout assignments (R1 re-key). The P5 name-keyed
	// bridge was deleted in P9 — layouts resolve by role id only.
	roleMap *domain.LayoutRoleMap
	expiry  time.Time
}

type objectLayoutUseCase struct {
	repo domain.ObjectLayoutRepository
	ttl  time.Duration

	mu    sync.RWMutex
	cache map[uuid.UUID]*orgLayoutEntry
}

// Compile-time interface check.
var _ domain.ObjectLayoutUseCase = (*objectLayoutUseCase)(nil)

func NewObjectLayoutUseCase(repo domain.ObjectLayoutRepository) domain.ObjectLayoutUseCase {
	return &objectLayoutUseCase{
		repo:  repo,
		ttl:   defaultLayoutCacheTTL,
		cache: make(map[uuid.UUID]*orgLayoutEntry),
	}
}

// ============================================================
// Per-request resolver — the hot path
// ============================================================

// ResolveLayout returns the effective layout sections for the caller on object
// slug, with FLS-hidden fields already stripped from every section.
//
// Three-tier resolver (plan §5.3):
//  1. Role-assigned layout   — caller.RoleID matches an explicit assignment
//  2. Default layout         — is_default=true for this (org, slug)
//  3. nil                    — no layout; renderer synthesises field-order
//
// The result is served from the per-org cache; Invalidate busts it on any write.
func (uc *objectLayoutUseCase) ResolveLayout(ctx context.Context, orgID uuid.UUID, slug string, caller domain.Caller, hiddenKeys map[string]bool) ([]domain.LayoutSection, error) {
	entry := uc.loadEntry(ctx, orgID)

	var resolved *domain.ObjectLayout

	// Tier 1 — role-assigned layout, by role id (R1 re-key; the P5 name bridge was
	// deleted in P9). Layout is presentation only, so a miss just falls to the
	// default layout.
	layoutID, assigned := uuid.Nil, false
	if entry.roleMap != nil && caller.RoleID != uuid.Nil {
		if id, ok := entry.roleMap.ByID[slug][caller.RoleID]; ok {
			layoutID, assigned = id, true
		}
	}
	if assigned {
		for i := range entry.layouts[slug] {
			if entry.layouts[slug][i].ID == layoutID {
				resolved = &entry.layouts[slug][i]
				break
			}
		}
	}

	// Tier 2 — is_default layout.
	if resolved == nil {
		for i := range entry.layouts[slug] {
			if entry.layouts[slug][i].IsDefault {
				resolved = &entry.layouts[slug][i]
				break
			}
		}
	}

	// Tier 3 — no layout configured; renderer uses field order (today's default).
	if resolved == nil || len(resolved.Sections) == 0 {
		return nil, nil
	}

	// FLS intersection: strip hidden fields from every section's field list.
	// An empty section is retained so the admin's grouping intent is preserved.
	// Layout is never a security gate — it is only presentation (plan §5.3).
	if len(hiddenKeys) == 0 {
		// Fast path: no restrictions — return sections as-is.
		return resolved.Sections, nil
	}
	filtered := make([]domain.LayoutSection, len(resolved.Sections))
	for i, sec := range resolved.Sections {
		kept := make([]domain.LayoutField, 0, len(sec.Fields))
		for _, f := range sec.Fields {
			if !hiddenKeys[f.Key] {
				kept = append(kept, f)
			}
		}
		filtered[i] = domain.LayoutSection{
			ID:      sec.ID,
			Label:   sec.Label,
			Columns: sec.Columns,
			Fields:  kept,
		}
	}
	return filtered, nil
}

// loadEntry returns the cached org entry, refreshing when cold or expired.
// On any load error it serves the prior entry (stale-on-error) or an empty
// entry so callers don't see cached data from a different org.
func (uc *objectLayoutUseCase) loadEntry(ctx context.Context, orgID uuid.UUID) *orgLayoutEntry {
	uc.mu.RLock()
	e := uc.cache[orgID]
	uc.mu.RUnlock()
	if e != nil && time.Now().Before(e.expiry) {
		return e
	}

	layouts, err := uc.repo.LoadOrgLayouts(ctx, orgID)
	if err != nil {
		return uc.staleOrEmpty(e)
	}
	roleMap, err := uc.repo.LoadOrgLayoutRoleMap(ctx, orgID)
	if err != nil {
		return uc.staleOrEmpty(e)
	}

	fresh := &orgLayoutEntry{
		layouts: layouts,
		roleMap: roleMap,
		expiry:  time.Now().Add(uc.ttl),
	}
	uc.mu.Lock()
	uc.cache[orgID] = fresh
	uc.mu.Unlock()
	return fresh
}

func (uc *objectLayoutUseCase) staleOrEmpty(e *orgLayoutEntry) *orgLayoutEntry {
	if e != nil {
		return e // serve prior (stale) entry rather than flip-flopping
	}
	return &orgLayoutEntry{
		layouts: map[string][]domain.ObjectLayout{},
		roleMap: &domain.LayoutRoleMap{
			ByID: map[string]map[uuid.UUID]uuid.UUID{},
		},
	}
}

// ============================================================
// Admin CRUD
// ============================================================

func (uc *objectLayoutUseCase) ListLayouts(ctx context.Context, orgID uuid.UUID, slug string) ([]domain.LayoutWithRoles, error) {
	layouts, err := uc.repo.ListLayouts(ctx, orgID, slug)
	if err != nil {
		return nil, err
	}
	out := make([]domain.LayoutWithRoles, 0, len(layouts))
	for _, l := range layouts {
		roleIDs, err := uc.repo.ListLayoutRoleIDs(ctx, orgID, l.ID)
		if err != nil {
			return nil, err
		}
		if roleIDs == nil {
			roleIDs = []uuid.UUID{}
		}
		out = append(out, domain.LayoutWithRoles{ObjectLayout: l, RoleIDs: roleIDs})
	}
	return out, nil
}

func (uc *objectLayoutUseCase) GetLayout(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*domain.LayoutWithRoles, error) {
	layout, err := uc.repo.GetLayout(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if layout == nil || layout.ObjectSlug != slug {
		return nil, domain.NewAppError(http.StatusNotFound, "layout not found")
	}
	roleIDs, err := uc.repo.ListLayoutRoleIDs(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if roleIDs == nil {
		roleIDs = []uuid.UUID{}
	}
	return &domain.LayoutWithRoles{ObjectLayout: *layout, RoleIDs: roleIDs}, nil
}

func (uc *objectLayoutUseCase) CreateLayout(ctx context.Context, orgID uuid.UUID, slug string, in domain.CreateLayoutInput) (*domain.LayoutWithRoles, error) {
	if in.Name == "" {
		return nil, domain.NewAppError(http.StatusBadRequest, "name is required")
	}
	if in.Sections == nil {
		in.Sections = []domain.LayoutSection{}
	}
	layout := &domain.ObjectLayout{
		OrgID:      orgID,
		ObjectSlug: slug,
		Name:       in.Name,
		Sections:   in.Sections,
		IsDefault:  in.IsDefault,
	}
	if err := uc.repo.CreateLayout(ctx, layout); err != nil {
		return nil, err
	}
	roleIDs := in.RoleIDs
	if len(roleIDs) > 0 {
		if err := uc.repo.SetLayoutRoles(ctx, orgID, layout.ID, slug, roleIDs); err != nil {
			return nil, err
		}
	} else {
		roleIDs = []uuid.UUID{}
	}
	uc.Invalidate(orgID)
	return &domain.LayoutWithRoles{ObjectLayout: *layout, RoleIDs: roleIDs}, nil
}

func (uc *objectLayoutUseCase) UpdateLayout(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, in domain.UpdateLayoutInput) (*domain.ObjectLayout, error) {
	layout, err := uc.repo.GetLayout(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if layout == nil || layout.ObjectSlug != slug {
		return nil, domain.NewAppError(http.StatusNotFound, "layout not found")
	}
	if in.Name != nil {
		layout.Name = *in.Name
	}
	if in.Sections != nil {
		layout.Sections = *in.Sections
	}
	if in.IsDefault != nil {
		layout.IsDefault = *in.IsDefault
	}
	if err := uc.repo.UpdateLayout(ctx, layout); err != nil {
		return nil, err
	}
	uc.Invalidate(orgID)
	return layout, nil
}

func (uc *objectLayoutUseCase) DeleteLayout(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	layout, err := uc.repo.GetLayout(ctx, orgID, id)
	if err != nil {
		return err
	}
	if layout == nil || layout.ObjectSlug != slug {
		return domain.NewAppError(http.StatusNotFound, "layout not found")
	}
	if err := uc.repo.DeleteLayout(ctx, orgID, id); err != nil {
		return err
	}
	uc.Invalidate(orgID)
	return nil
}

func (uc *objectLayoutUseCase) SetLayoutRoles(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, roleIDs []uuid.UUID) error {
	layout, err := uc.repo.GetLayout(ctx, orgID, id)
	if err != nil {
		return err
	}
	if layout == nil || layout.ObjectSlug != slug {
		return domain.NewAppError(http.StatusNotFound, "layout not found")
	}
	if roleIDs == nil {
		roleIDs = []uuid.UUID{}
	}
	if err := uc.repo.SetLayoutRoles(ctx, orgID, id, slug, roleIDs); err != nil {
		return err
	}
	uc.Invalidate(orgID)
	return nil
}

// Invalidate drops the per-org cache entry. Called after every write so the
// next schema request picks up the change without waiting for the TTL to expire.
func (uc *objectLayoutUseCase) Invalidate(orgID uuid.UUID) {
	uc.mu.Lock()
	delete(uc.cache, orgID)
	uc.mu.Unlock()
}
