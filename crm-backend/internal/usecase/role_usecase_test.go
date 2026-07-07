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
	memberIDs    map[uuid.UUID][]uuid.UUID
	cloned       [][2]uuid.UUID // (src, dst) pairs passed to ClonePermissions
}

func newFakeRoleRepo() *fakeRoleRepo {
	return &fakeRoleRepo{
		roles:        map[uuid.UUID]*domain.Role{},
		byName:       map[string]*domain.Role{},
		caps:         map[uuid.UUID][]string{},
		memberCounts: map[uuid.UUID]int64{},
		memberIDs:    map[uuid.UUID][]uuid.UUID{},
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
func (f *fakeRoleRepo) ListOptions(_ context.Context, _ uuid.UUID) ([]domain.RoleOption, error) {
	out := make([]domain.RoleOption, 0, len(f.roles))
	for _, r := range f.roles {
		out = append(out, domain.RoleOption{
			ID: r.ID, Name: r.Name, Description: r.Description,
			IsSystem: r.IsSystem, IsOwner: domain.IsOwnerRole(r), DataScope: r.DataScope,
		})
	}
	return out, nil
}
func (f *fakeRoleRepo) ReassignMembers(_ context.Context, _, fromRoleID, toRoleID uuid.UUID) ([]uuid.UUID, error) {
	ids := f.memberIDs[fromRoleID]
	f.memberIDs[toRoleID] = append(f.memberIDs[toRoleID], ids...)
	delete(f.memberIDs, fromRoleID)
	f.memberCounts[toRoleID] += f.memberCounts[fromRoleID]
	f.memberCounts[fromRoleID] = 0
	return ids, nil
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
func (f *fakeRoleRepo) ListMemberIDs(_ context.Context, _, id uuid.UUID) ([]uuid.UUID, error) {
	return f.memberIDs[id], nil
}

type fakeInvalidator struct{ calls int }

func (f *fakeInvalidator) Invalidate(uuid.UUID)                          { f.calls++ }
func (f *fakeInvalidator) EnsureSeeded(context.Context, uuid.UUID) error { return nil }

// fakeEvictor counts per-(user, org) session evictions (P10 P0).
type fakeEvictor struct{ evicted [][2]uuid.UUID }

func (f *fakeEvictor) EvictOrgSession(_ context.Context, userID, orgID uuid.UUID) {
	f.evicted = append(f.evicted, [2]uuid.UUID{userID, orgID})
}

func newRoleUC(repo *fakeRoleRepo) (domain.RoleUseCase, *fakeInvalidator) {
	inv := &fakeInvalidator{}
	return NewRoleUseCase(repo, inv, nil, nil), inv
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
	err := uc.Delete(context.Background(), orgID, custom.ID, nil)
	assertRoleErr(t, err, 409, "deleting an in-use role without a reassign target")
}

// TestRoleUpdate_RenameEvictsMemberSessions: the session cache carries roleName
// + data_scope, so renaming or rescoping a role must evict every member's entry
// or they authorize under stale data for the cache TTL (P10 P0).
func TestRoleUpdate_RenameEvictsMemberSessions(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	custom := repo.add(&domain.Role{ID: uuid.New(), Name: "Support Agent", IsSystem: false, DataScope: domain.DataScopeAll})
	m1, m2 := uuid.New(), uuid.New()
	repo.memberIDs[custom.ID] = []uuid.UUID{m1, m2}
	ev := &fakeEvictor{}
	uc := NewRoleUseCase(repo, &fakeInvalidator{}, nil, ev)

	newName := "Support Tier 2"
	if err := uc.Update(context.Background(), orgID, custom.ID, domain.UpdateRoleInput{Name: &newName}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if len(ev.evicted) != 2 {
		t.Fatalf("expected both members' sessions evicted on rename, got %d", len(ev.evicted))
	}

	// A no-op update (same scope, no rename) must not evict anyone.
	ev.evicted = nil
	scope := domain.DataScopeAll
	if err := uc.Update(context.Background(), orgID, custom.ID, domain.UpdateRoleInput{DataScope: &scope}); err != nil {
		t.Fatalf("no-op update: %v", err)
	}
	if len(ev.evicted) != 0 {
		t.Fatalf("no-op update must not evict sessions, got %d", len(ev.evicted))
	}

	// Rescoping own↔all changes the session value's ds field — must evict.
	own := domain.DataScopeOwn
	if err := uc.Update(context.Background(), orgID, custom.ID, domain.UpdateRoleInput{DataScope: &own}); err != nil {
		t.Fatalf("rescope: %v", err)
	}
	if len(ev.evicted) != 2 {
		t.Fatalf("expected both members' sessions evicted on rescope, got %d", len(ev.evicted))
	}
}

// TestRoleDelete_WithReassignMovesMembers: deleting an in-use role while naming a
// reassign target moves every member onto it (transactional), evicts their
// sessions, then deletes the source (P6 delete-with-reassign).
func TestRoleDelete_WithReassignMovesMembers(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	src := repo.add(&domain.Role{ID: uuid.New(), Name: "Support Agent", OrgID: &orgID})
	dst := repo.add(&domain.Role{ID: uuid.New(), Name: "Support Lead", OrgID: &orgID})
	repo.memberCounts[src.ID] = 3
	repo.memberIDs[src.ID] = []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	ev := &fakeEvictor{}
	uc := NewRoleUseCase(repo, &fakeInvalidator{}, nil, ev)

	if err := uc.Delete(context.Background(), orgID, src.ID, &dst.ID); err != nil {
		t.Fatalf("delete-with-reassign: %v", err)
	}
	if _, ok := repo.roles[src.ID]; ok {
		t.Fatal("source role must be deleted after reassign")
	}
	if repo.memberCounts[dst.ID] != 3 {
		t.Fatalf("members should have moved to the target, got %d", repo.memberCounts[dst.ID])
	}
	if len(ev.evicted) != 3 {
		t.Fatalf("expected 3 session evictions for moved members, got %d", len(ev.evicted))
	}
}

// TestRoleDelete_ReassignGuards: the reassign target can't be the role being
// deleted, and members can't be moved into the owner role.
func TestRoleDelete_ReassignGuards(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	src := repo.add(&domain.Role{ID: uuid.New(), Name: "Agent", OrgID: &orgID})
	owner := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleOwner, IsSystem: true, IsOwner: true})
	repo.memberCounts[src.ID] = 1
	uc := NewRoleUseCase(repo, &fakeInvalidator{}, nil, &fakeEvictor{})

	if err := uc.Delete(context.Background(), orgID, src.ID, &src.ID); err == nil {
		t.Fatal("reassigning members to the role being deleted must be rejected")
	}
	if err := uc.Delete(context.Background(), orgID, src.ID, &owner.ID); err == nil {
		t.Fatal("reassigning members into the owner role must be rejected")
	}
	if _, ok := repo.roles[src.ID]; !ok {
		t.Fatal("the role must survive a rejected reassign")
	}
}

// TestRoleDuplicate_ClonesAndRecordsLineage: duplicate copies capabilities +
// OLS/FLS + scope, records the source as lineage (seeded_from + template_key),
// and — with ReassignMembers — moves the source's members onto the copy (P6).
func TestRoleDuplicate_ClonesAndRecordsLineage(t *testing.T) {
	repo := newFakeRoleRepo()
	orgID := uuid.New()
	src := repo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleManager, IsSystem: true, DataScope: domain.DataScopeAll})
	repo.caps[src.ID] = []string{domain.CapRecordsWrite, domain.CapReportsManage}
	repo.memberIDs[src.ID] = []uuid.UUID{uuid.New(), uuid.New()}
	repo.memberCounts[src.ID] = 2
	ev := &fakeEvictor{}
	uc := NewRoleUseCase(repo, &fakeInvalidator{}, nil, ev)

	role, err := uc.Duplicate(context.Background(), orgID, src.ID, domain.DuplicateRoleInput{Name: "Regional Manager", ReassignMembers: true})
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if role.SeededFromRoleID == nil || *role.SeededFromRoleID != src.ID {
		t.Fatal("duplicate must record seeded_from_role_id = source")
	}
	if role.TemplateKey == nil || *role.TemplateKey != domain.RoleManager {
		t.Fatal("duplicate must inherit the source's system-template lineage")
	}
	if len(repo.caps[role.ID]) != 2 {
		t.Fatalf("expected 2 capabilities copied, got %d", len(repo.caps[role.ID]))
	}
	if len(repo.cloned) != 1 || repo.cloned[0] != [2]uuid.UUID{src.ID, role.ID} {
		t.Fatalf("expected ClonePermissions(src→copy), got %v", repo.cloned)
	}
	if repo.memberCounts[role.ID] != 2 {
		t.Fatalf("members should be reassigned to the copy, got %d", repo.memberCounts[role.ID])
	}
	if len(ev.evicted) != 2 {
		t.Fatalf("expected 2 evictions for reassigned members, got %d", len(ev.evicted))
	}
}
