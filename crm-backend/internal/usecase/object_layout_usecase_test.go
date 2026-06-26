package usecase_test

import (
	"context"
	"net/http"
	"testing"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// stub repository
// ============================================================

// stubLayoutRepo is an in-memory implementation of ObjectLayoutRepository for
// unit tests. It uses a simple slice so tests can inspect it directly.
type stubLayoutRepo struct {
	layouts []domain.ObjectLayout
	roles   []domain.ObjectLayoutRole
}

func newStubRepo(layouts ...domain.ObjectLayout) *stubLayoutRepo {
	return &stubLayoutRepo{layouts: layouts}
}

// LoadOrgLayouts groups layouts by slug (matches the real repository contract).
func (s *stubLayoutRepo) LoadOrgLayouts(_ context.Context, orgID uuid.UUID) (map[string][]domain.ObjectLayout, error) {
	m := map[string][]domain.ObjectLayout{}
	for _, l := range s.layouts {
		if l.OrgID == orgID && l.DeletedAt.Time.IsZero() {
			m[l.ObjectSlug] = append(m[l.ObjectSlug], l)
		}
	}
	return m, nil
}

// LoadOrgLayoutRoleMap joins layouts → roles in memory.
func (s *stubLayoutRepo) LoadOrgLayoutRoleMap(_ context.Context, orgID uuid.UUID) (map[string]map[string]uuid.UUID, error) {
	// Build a lookup: role_id → RoleID's "name" field (we embed the name in RoleID for
	// testing convenience — see helpers below).
	result := map[string]map[string]uuid.UUID{}
	for _, r := range s.roles {
		if r.OrgID != orgID {
			continue
		}
		// Find the layout and check it's not deleted.
		var found bool
		for _, l := range s.layouts {
			if l.ID == r.LayoutID && l.DeletedAt.Time.IsZero() {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		if result[r.ObjectSlug] == nil {
			result[r.ObjectSlug] = map[string]uuid.UUID{}
		}
		// We store the role name in the ObjectSlug field of a sentinel
		// ObjectLayoutRole, keyed by roleName.
		result[r.ObjectSlug][r.ObjectSlug+"::"+r.RoleID.String()] = r.LayoutID
	}
	return result, nil
}

func (s *stubLayoutRepo) GetLayout(_ context.Context, orgID, id uuid.UUID) (*domain.ObjectLayout, error) {
	for i := range s.layouts {
		if s.layouts[i].OrgID == orgID && s.layouts[i].ID == id && s.layouts[i].DeletedAt.Time.IsZero() {
			l := s.layouts[i]
			return &l, nil
		}
	}
	return nil, nil
}

func (s *stubLayoutRepo) ListLayouts(_ context.Context, orgID uuid.UUID, slug string) ([]domain.ObjectLayout, error) {
	var out []domain.ObjectLayout
	for _, l := range s.layouts {
		if l.OrgID == orgID && l.ObjectSlug == slug && l.DeletedAt.Time.IsZero() {
			out = append(out, l)
		}
	}
	return out, nil
}

func (s *stubLayoutRepo) CreateLayout(_ context.Context, layout *domain.ObjectLayout) error {
	if layout.ID == uuid.Nil {
		layout.ID = uuid.New()
	}
	if layout.IsDefault {
		for i := range s.layouts {
			if s.layouts[i].OrgID == layout.OrgID && s.layouts[i].ObjectSlug == layout.ObjectSlug && s.layouts[i].DeletedAt.Time.IsZero() {
				s.layouts[i].IsDefault = false
			}
		}
	}
	s.layouts = append(s.layouts, *layout)
	return nil
}

func (s *stubLayoutRepo) UpdateLayout(_ context.Context, layout *domain.ObjectLayout) error {
	if layout.IsDefault {
		for i := range s.layouts {
			if s.layouts[i].OrgID == layout.OrgID && s.layouts[i].ObjectSlug == layout.ObjectSlug &&
				s.layouts[i].ID != layout.ID && s.layouts[i].DeletedAt.Time.IsZero() {
				s.layouts[i].IsDefault = false
			}
		}
	}
	for i := range s.layouts {
		if s.layouts[i].ID == layout.ID {
			s.layouts[i] = *layout
			return nil
		}
	}
	return nil
}

func (s *stubLayoutRepo) DeleteLayout(_ context.Context, orgID, id uuid.UUID) error {
	for i := range s.layouts {
		if s.layouts[i].OrgID == orgID && s.layouts[i].ID == id {
			s.layouts[i].DeletedAt.Valid = true
			return nil
		}
	}
	return nil
}

func (s *stubLayoutRepo) SetLayoutRoles(_ context.Context, orgID uuid.UUID, layoutID uuid.UUID, slug string, roleIDs []uuid.UUID) error {
	// Remove existing assignments for this layout.
	filtered := s.roles[:0]
	for _, r := range s.roles {
		if !(r.OrgID == orgID && r.LayoutID == layoutID) {
			filtered = append(filtered, r)
		}
	}
	s.roles = filtered
	for _, rid := range roleIDs {
		s.roles = append(s.roles, domain.ObjectLayoutRole{
			ID: uuid.New(), OrgID: orgID, LayoutID: layoutID, ObjectSlug: slug, RoleID: rid,
		})
	}
	return nil
}

func (s *stubLayoutRepo) ListLayoutRoleIDs(_ context.Context, orgID, layoutID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	for _, r := range s.roles {
		if r.OrgID == orgID && r.LayoutID == layoutID {
			ids = append(ids, r.RoleID)
		}
	}
	return ids, nil
}

// ============================================================
// helpers
// ============================================================

func makeLayout(orgID uuid.UUID, slug, name string, isDefault bool, sections ...domain.LayoutSection) domain.ObjectLayout {
	return domain.ObjectLayout{
		ID:         uuid.New(),
		OrgID:      orgID,
		ObjectSlug: slug,
		Name:       name,
		IsDefault:  isDefault,
		Sections:   sections,
	}
}

func makeSection(id, label string, cols int, keys ...string) domain.LayoutSection {
	fields := make([]domain.LayoutField, len(keys))
	for i, k := range keys {
		fields[i] = domain.LayoutField{Key: k}
	}
	return domain.LayoutSection{ID: id, Label: label, Columns: cols, Fields: fields}
}

// ============================================================
// Tests — resolver precedence
// ============================================================

// TestResolveLayout_RoleAssigned verifies that the role-assigned layout (Tier 1)
// wins over an is_default layout when both exist.
func TestResolveLayout_RoleAssigned(t *testing.T) {
	orgID := uuid.New()
	roleLayout := makeLayout(orgID, "contact", "Sales layout", false, makeSection("s1", "Core", 1, "email", "phone"))
	defaultLayout := makeLayout(orgID, "contact", "Default layout", true, makeSection("s2", "All Fields", 1, "name", "company"))

	repo := newStubRepo(roleLayout, defaultLayout)
	// Assign roleLayout to the "sales" role by seeding role map via SetLayoutRoles.
	// We use a sentinel roleID so LoadOrgLayoutRoleMap can match it.
	salesRoleID := uuid.New()
	repo.roles = append(repo.roles, domain.ObjectLayoutRole{
		ID: uuid.New(), OrgID: orgID, LayoutID: roleLayout.ID, ObjectSlug: "contact", RoleID: salesRoleID,
	})

	// Patch LoadOrgLayoutRoleMap to return slug→roleName→layoutID using a custom
	// roleNameRepo (simpler: use a thin wrapper so we can control the role name).
	// Instead of that complexity, test via the usecase's ResolveLayout directly
	// after seeding cache. We do that by using a custom stub that overrides
	// LoadOrgLayoutRoleMap to return the correct mapping.
	type mappedRepo struct {
		*stubLayoutRepo
		roleMap map[string]map[string]uuid.UUID
	}
	mapped := &struct {
		domain.ObjectLayoutRepository
		roleMap map[string]map[string]uuid.UUID
	}{
		ObjectLayoutRepository: repo,
		roleMap: map[string]map[string]uuid.UUID{
			"contact": {"sales": roleLayout.ID},
		},
	}
	_ = mapped // used below via fullRepo

	// Use a real implementation via fullStubRepo that overrides LoadOrgLayoutRoleMap.
	fullRepo := &fullStubRepo{stubLayoutRepo: repo, roleMapOverride: map[string]map[string]uuid.UUID{
		"contact": {"sales": roleLayout.ID},
	}}

	uc := usecase.NewObjectLayoutUseCase(fullRepo)
	sections, err := uc.ResolveLayout(context.Background(), orgID, "contact", "sales", nil)
	require.NoError(t, err)
	require.Len(t, sections, 1)
	assert.Equal(t, "s1", sections[0].ID, "role-assigned layout should win over default")
}

// TestResolveLayout_DefaultFallback verifies that the is_default layout (Tier 2)
// is served when the caller's role has no explicit assignment.
func TestResolveLayout_DefaultFallback(t *testing.T) {
	orgID := uuid.New()
	defaultLayout := makeLayout(orgID, "contact", "Default layout", true, makeSection("s2", "All Fields", 1, "name", "email"))

	fullRepo := &fullStubRepo{stubLayoutRepo: newStubRepo(defaultLayout), roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)
	sections, err := uc.ResolveLayout(context.Background(), orgID, "contact", "manager", nil)
	require.NoError(t, err)
	require.Len(t, sections, 1)
	assert.Equal(t, "s2", sections[0].ID, "default layout should be returned when role has no assignment")
}

// TestResolveLayout_NoLayoutFallback verifies Tier 3: nil sections when neither
// a role-assigned nor a default layout exists. The renderer synthesises field order.
func TestResolveLayout_NoLayoutFallback(t *testing.T) {
	orgID := uuid.New()
	fullRepo := &fullStubRepo{stubLayoutRepo: newStubRepo(), roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)
	sections, err := uc.ResolveLayout(context.Background(), orgID, "contact", "admin", nil)
	require.NoError(t, err)
	assert.Nil(t, sections, "nil sections signal no-layout: renderer uses flat field order")
}

// TestResolveLayout_FLSIntersection verifies that fields in hiddenKeys are
// stripped from the resolved layout sections before the response leaves the server.
func TestResolveLayout_FLSIntersection(t *testing.T) {
	orgID := uuid.New()
	layout := makeLayout(orgID, "contact", "Default", true,
		makeSection("s1", "Core", 1, "email", "phone", "revenue"),
	)
	fullRepo := &fullStubRepo{stubLayoutRepo: newStubRepo(layout), roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)

	hiddenKeys := map[string]bool{"revenue": true}
	sections, err := uc.ResolveLayout(context.Background(), orgID, "contact", "sales", hiddenKeys)
	require.NoError(t, err)
	require.Len(t, sections, 1)

	keys := make([]string, len(sections[0].Fields))
	for i, f := range sections[0].Fields {
		keys[i] = f.Key
	}
	assert.Contains(t, keys, "email")
	assert.Contains(t, keys, "phone")
	assert.NotContains(t, keys, "revenue", "FLS-hidden key must be stripped from layout sections")
}

// ============================================================
// Tests — admin CRUD
// ============================================================

func TestCreateLayout_NameRequired(t *testing.T) {
	orgID := uuid.New()
	fullRepo := &fullStubRepo{stubLayoutRepo: newStubRepo(), roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)
	_, err := uc.CreateLayout(context.Background(), orgID, "contact", domain.CreateLayoutInput{Name: ""})
	require.Error(t, err)
	ae, ok := err.(*domain.AppError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, ae.Code)
}

func TestCreateLayout_DefaultCleared(t *testing.T) {
	orgID := uuid.New()
	existing := makeLayout(orgID, "contact", "Old default", true, makeSection("s0", "Old", 1, "name"))
	repo := newStubRepo(existing)
	fullRepo := &fullStubRepo{stubLayoutRepo: repo, roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)

	_, err := uc.CreateLayout(context.Background(), orgID, "contact", domain.CreateLayoutInput{
		Name:      "New default",
		IsDefault: true,
		Sections:  []domain.LayoutSection{makeSection("s1", "New", 1, "email")},
	})
	require.NoError(t, err)

	// The old default must have been cleared.
	for _, l := range repo.layouts {
		if l.ID == existing.ID {
			assert.False(t, l.IsDefault, "existing default must be cleared when new default is created")
			return
		}
	}
	t.Fatal("existing layout not found in repo")
}

func TestDeleteLayout_NotFound(t *testing.T) {
	orgID := uuid.New()
	fullRepo := &fullStubRepo{stubLayoutRepo: newStubRepo(), roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)
	err := uc.DeleteLayout(context.Background(), orgID, "contact", uuid.New())
	require.Error(t, err)
	ae, ok := err.(*domain.AppError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, ae.Code)
}

func TestSetLayoutRoles_CacheInvalidated(t *testing.T) {
	orgID := uuid.New()
	layout := makeLayout(orgID, "contact", "L1", false, makeSection("s1", "Core", 1, "name"))
	repo := newStubRepo(layout)
	fullRepo := &fullStubRepo{stubLayoutRepo: repo, roleMapOverride: map[string]map[string]uuid.UUID{}}
	uc := usecase.NewObjectLayoutUseCase(fullRepo)

	// Warm the cache by resolving once (no layout configured → nil).
	_, err := uc.ResolveLayout(context.Background(), orgID, "contact", "sales", nil)
	require.NoError(t, err)

	// Now set this layout as the default, then verify the next resolve picks it up
	// (proving Invalidate was called).
	_, err = uc.UpdateLayout(context.Background(), orgID, "contact", layout.ID, domain.UpdateLayoutInput{IsDefault: boolPtr(true)})
	require.NoError(t, err)

	// Must reload after invalidation — update the role map override too.
	fullRepo.roleMapOverride = map[string]map[string]uuid.UUID{}
	sections, err := uc.ResolveLayout(context.Background(), orgID, "contact", "sales", nil)
	require.NoError(t, err)
	assert.NotNil(t, sections, "after Invalidate, updated default layout should be served")
}

// ============================================================
// fullStubRepo — wraps stubLayoutRepo + overrides LoadOrgLayoutRoleMap
// ============================================================

type fullStubRepo struct {
	*stubLayoutRepo
	roleMapOverride map[string]map[string]uuid.UUID
}

func (f *fullStubRepo) LoadOrgLayoutRoleMap(_ context.Context, _ uuid.UUID) (map[string]map[string]uuid.UUID, error) {
	return f.roleMapOverride, nil
}

func boolPtr(b bool) *bool { return &b }
