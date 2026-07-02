package usecase

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeRoleRepo is an in-memory RoleRepository for the role-usecase guardrail
// tests. It stores roles by id and capabilities by role id.
type fakeRoleRepo struct {
	roles        map[uuid.UUID]*domain.Role
	byName       map[string]*domain.Role
	caps         map[uuid.UUID][]string
	memberCounts map[uuid.UUID]int64
	cloned       [][2]uuid.UUID // (src, dst) pairs passed to ClonePermissions
}

func newFakeRoleRepo() *fakeRoleRepo {
	return &fakeRoleRepo{
		roles:        map[uuid.UUID]*domain.Role{},
		byName:       map[string]*domain.Role{},
		caps:         map[uuid.UUID][]string{},
		memberCounts: map[uuid.UUID]int64{},
	}
}

func (f *fakeRoleRepo) add(r *domain.Role) *domain.Role {
	f.roles[r.ID] = r
	f.byName[r.Name] = r
	return r
}

func (f *fakeRoleRepo) ListDetailed(context.Context, uuid.UUID) ([]domain.RoleDetail, error) {
	return nil, nil
}
func (f *fakeRoleRepo) GetInOrg(_ context.Context, _, id uuid.UUID) (*domain.Role, error) {
	return f.roles[id], nil
}
func (f *fakeRoleRepo) FindByNameInOrg(_ context.Context, _ uuid.UUID, name string) (*domain.Role, error) {
	return f.byName[name], nil
}
func (f *fakeRoleRepo) CreateRole(_ context.Context, r *domain.Role) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	f.add(r)
	return nil
}
func (f *fakeRoleRepo) UpdateRole(_ context.Context, r *domain.Role) error { f.add(r); return nil }
func (f *fakeRoleRepo) DeleteRole(_ context.Context, _, id uuid.UUID) error {
	delete(f.roles, id)
	return nil
}
func (f *fakeRoleRepo) GetCapabilities(_ context.Context, id uuid.UUID) ([]string, error) {
	return f.caps[id], nil
}
func (f *fakeRoleRepo) SetCapabilities(_ context.Context, _, id uuid.UUID, codes []string) error {
	f.caps[id] = codes
	return nil
}
func (f *fakeRoleRepo) ClonePermissions(_ context.Context, _, src, dst uuid.UUID) error {
	f.cloned = append(f.cloned, [2]uuid.UUID{src, dst})
	return nil
}
func (f *fakeRoleRepo) CountActiveMembers(_ context.Context, _, id uuid.UUID) (int64, error) {
	return f.memberCounts[id], nil
}

type fakeInvalidator struct{ calls int }

func (f *fakeInvalidator) Invalidate(uuid.UUID)                          { f.calls++ }
func (f *fakeInvalidator) EnsureSeeded(context.Context, uuid.UUID) error { return nil }

func newRoleUC(repo *fakeRoleRepo) (domain.RoleUseCase, *fakeInvalidator) {
	inv := &fakeInvalidator{}
	return NewRoleUseCase(repo, inv), inv
}

func assertRoleErr(t *testing.T, err error, want int, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error %d, got nil", ctx, want)
	}
	appErr, ok := err.(*domain.AppError)
	if !ok || appErr.Code != want {
		t.Fatalf("%s: expected AppError %d, got %v", ctx, want, err)
	}
}

func TestRoleCreate_RejectsUnknownCapability(t *testing.T) {
	repo := newFakeRoleRepo()
	uc, _ := newRoleUC(repo)
	_, err := uc.Create(context.Background(), uuid.New(), domain.CreateRoleInput{
		Name:         "Support Agent",
		Capabilities: []string{"totally.made.up"},
	})
	assertRoleErr(t, err, 400, "unknown capability")
}

func TestRoleCreate_RejectsDuplicateName(t *testing.T) {
	repo := newFakeRoleRepo()
	repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleManager, IsSystem: true})
	uc, _ := newRoleUC(repo)
	_, err := uc.Create(context.Background(), uuid.New(), domain.CreateRoleInput{Name: "manager"})
	assertRoleErr(t, err, 409, "shadowing a system role name")
}

func TestRoleCreate_CloneCopiesPermissionsAndCaps(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	viewer := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleViewer, IsSystem: true, DataScope: domain.DataScopeAll})
	repo.caps[viewer.ID] = []string{domain.CapAuditView}

	uc, inv := newRoleUC(repo)
	role, err := uc.Create(context.Background(), orgID, domain.CreateRoleInput{
		Name:        "Support Agent",
		CloneFromID: &viewer.ID,
		DataScope:   domain.DataScopeOwn,
	})
	if err != nil {
		t.Fatalf("clone create failed: %v", err)
	}
	if role.DataScope != domain.DataScopeOwn {
		t.Fatalf("expected data_scope own, got %s", role.DataScope)
	}
	if len(repo.cloned) != 1 || repo.cloned[0][0] != viewer.ID {
		t.Fatalf("expected OLS/FLS cloned from viewer, got %v", repo.cloned)
	}
	if got := repo.caps[role.ID]; len(got) != 1 || got[0] != domain.CapAuditView {
		t.Fatalf("expected inherited audit.view capability, got %v", got)
	}
	if inv.calls == 0 {
		t.Fatalf("expected cache invalidation after create")
	}
}

func TestRoleUpdate_SystemRoleRefused(t *testing.T) {
	repo := newFakeRoleRepo()
	admin := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleAdmin, IsSystem: true})
	uc, _ := newRoleUC(repo)
	newName := "root"
	err := uc.Update(context.Background(), uuid.New(), admin.ID, domain.UpdateRoleInput{Name: &newName})
	assertRoleErr(t, err, 403, "renaming a system role")
}

func TestRoleSetCapabilities_SystemRolesRefused(t *testing.T) {
	// System roles are global singletons — editing their capabilities per-org would
	// corrupt other tenants, so it is refused for ALL system roles (owner + the rest);
	// admins customize by cloning into a custom role.
	repo := newFakeRoleRepo()
	owner := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleOwner, IsSystem: true})
	admin := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleAdmin, IsSystem: true})
	manager := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleManager, IsSystem: true})
	uc, _ := newRoleUC(repo)
	for _, r := range []*domain.Role{owner, admin, manager} {
		err := uc.SetCapabilities(context.Background(), uuid.New(), r.ID, domain.SetCapabilitiesInput{
			Capabilities: []string{domain.CapAuditView},
		})
		assertRoleErr(t, err, 403, "editing system role "+r.Name+" capabilities")
	}
}

func TestRoleSetCapabilities_CustomAllowed(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	custom := repo.add(&domain.Role{ID: uuid.New(), Name: "Support Agent", IsSystem: false})
	uc, inv := newRoleUC(repo)
	if err := uc.SetCapabilities(context.Background(), orgID, custom.ID, domain.SetCapabilitiesInput{
		Capabilities: []string{domain.CapRecordsWrite, domain.CapAuditView},
	}); err != nil {
		t.Fatalf("custom role SetCapabilities should succeed, got %v", err)
	}
	if got := repo.caps[custom.ID]; len(got) != 2 {
		t.Fatalf("expected 2 capabilities set, got %v", got)
	}
	if inv.calls == 0 {
		t.Fatalf("expected cache invalidation after SetCapabilities")
	}
}

func TestRoleDelete_InUseRefused(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	custom := repo.add(&domain.Role{ID: uuid.New(), Name: "Support Agent", IsSystem: false})
	repo.memberCounts[custom.ID] = 2
	uc, _ := newRoleUC(repo)
	err := uc.Delete(context.Background(), orgID, custom.ID)
	assertRoleErr(t, err, 409, "deleting an in-use role")
}
