package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Fakes — a workspace-capable AuthRepository built on fakeAuthRepo
// ============================================================

// fakeWorkspaceRepo embeds fakeAuthRepo (recovery flows + auth events) and adds
// working org-membership behavior for the P10 P0 guard tests: owner-promotion
// block, owner-only transfer, and session eviction on membership mutations.
type fakeWorkspaceRepo struct {
	*fakeAuthRepo
	orgUsers  map[string]*domain.OrgUser
	roles     map[string]*domain.Role
	rolesByID map[uuid.UUID]*domain.Role
	roleCaps  map[uuid.UUID][]string
	invites   []*domain.OrgInvitation
	// orgs backs GetOrganizationByID; an org absent from the map models a
	// soft-deleted / non-existent workspace (the real repo returns nil for those).
	orgs map[uuid.UUID]*domain.Organization
}

func newFakeWorkspaceRepo() *fakeWorkspaceRepo {
	return &fakeWorkspaceRepo{
		fakeAuthRepo: newFakeAuthRepo(),
		orgUsers:     map[string]*domain.OrgUser{},
		roles:        map[string]*domain.Role{},
		rolesByID:    map[uuid.UUID]*domain.Role{},
		roleCaps:     map[uuid.UUID][]string{},
		orgs:         map[uuid.UUID]*domain.Organization{},
	}
}

// addOrg registers a live workspace so GetOrganizationByID returns it (U4 item 6
// consent tests). Omit an org to model it as soft-deleted / gone.
func (f *fakeWorkspaceRepo) addOrg(id uuid.UUID, name string) *domain.Organization {
	o := &domain.Organization{ID: id, Name: name, Type: "company"}
	f.orgs[id] = o
	return o
}

func (f *fakeWorkspaceRepo) GetOrganizationByID(_ context.Context, id uuid.UUID) (*domain.Organization, error) {
	return f.orgs[id], nil
}

// ListValidInvitationsByEmail mirrors the real repo: pending, not revoked, not
// expired, to a live workspace (present in orgs), newest first, across all orgs.
func (f *fakeWorkspaceRepo) ListValidInvitationsByEmail(_ context.Context, email string) ([]domain.OrgInvitation, error) {
	var out []domain.OrgInvitation
	for _, inv := range f.invites {
		if strings.EqualFold(inv.Email, email) && inv.Status == "pending" && inv.RevokedAt == nil &&
			inv.ExpiresAt.After(time.Now()) && f.orgs[inv.OrgID] != nil {
			out = append(out, *inv)
		}
	}
	return out, nil
}

func (f *fakeWorkspaceRepo) GetOrgInvitationByIDUnscoped(_ context.Context, id uuid.UUID) (*domain.OrgInvitation, error) {
	for _, inv := range f.invites {
		if inv.ID == id {
			return inv, nil
		}
	}
	return nil, nil
}

func wkey(userID, orgID uuid.UUID) string { return userID.String() + "|" + orgID.String() }

func (f *fakeWorkspaceRepo) addRole(name string, isOwner bool) *domain.Role {
	r := &domain.Role{ID: uuid.New(), Name: name, IsSystem: true, IsOwner: isOwner, DataScope: domain.DataScopeAll}
	f.roles[name] = r
	f.rolesByID[r.ID] = r
	return r
}

// addRoleWithCaps registers a role (system or custom) carrying capability codes,
// for the escalation-guard #2 tests. orgScoped=false ⇒ a global system role.
func (f *fakeWorkspaceRepo) addRoleWithCaps(name string, orgID *uuid.UUID, caps ...string) *domain.Role {
	r := &domain.Role{ID: uuid.New(), Name: name, OrgID: orgID, IsSystem: orgID == nil, DataScope: domain.DataScopeAll}
	f.roles[name] = r
	f.rolesByID[r.ID] = r
	f.roleCaps[r.ID] = caps
	return r
}

// GetCapabilities satisfies roleCapReader — the target-role side of guard #2.
func (f *fakeWorkspaceRepo) GetCapabilities(_ context.Context, roleID uuid.UUID) ([]string, error) {
	return f.roleCaps[roleID], nil
}

func (f *fakeWorkspaceRepo) addMember(userID, orgID uuid.UUID, role *domain.Role, status string) *domain.OrgUser {
	ou := &domain.OrgUser{UserID: userID, OrgID: orgID, RoleID: role.ID, Status: status, Role: role}
	f.orgUsers[wkey(userID, orgID)] = ou
	return ou
}

func (f *fakeWorkspaceRepo) GetOrgUser(_ context.Context, userID, orgID uuid.UUID) (*domain.OrgUser, error) {
	return f.orgUsers[wkey(userID, orgID)], nil
}

func (f *fakeWorkspaceRepo) GetRoleByName(_ context.Context, name string, _ *uuid.UUID) (*domain.Role, error) {
	if r, ok := f.roles[name]; ok {
		return r, nil
	}
	return nil, errors.New("record not found")
}

func (f *fakeWorkspaceRepo) CountOrgUsersByRole(_ context.Context, orgID, roleID uuid.UUID, status string) (int64, error) {
	var n int64
	for _, ou := range f.orgUsers {
		if ou.OrgID == orgID && ou.RoleID == roleID && (status == "" || ou.Status == status) {
			n++
		}
	}
	return n, nil
}

func (f *fakeWorkspaceRepo) UpdateOrgUserRole(_ context.Context, userID, orgID, roleID uuid.UUID) error {
	if ou, ok := f.orgUsers[wkey(userID, orgID)]; ok {
		ou.RoleID = roleID
		ou.Role = f.rolesByID[roleID]
	}
	return nil
}

func (f *fakeWorkspaceRepo) UpdateOrgUserStatus(_ context.Context, userID, orgID uuid.UUID, status string) error {
	if ou, ok := f.orgUsers[wkey(userID, orgID)]; ok {
		ou.Status = status
	}
	return nil
}

func (f *fakeWorkspaceRepo) DeleteOrgUser(_ context.Context, userID, orgID uuid.UUID) error {
	delete(f.orgUsers, wkey(userID, orgID))
	return nil
}

func (f *fakeWorkspaceRepo) GetOrgUserByEmail(_ context.Context, email string, orgID uuid.UUID) (*domain.OrgUser, error) {
	if u, ok := f.usersByEmail[email]; ok {
		return f.orgUsers[wkey(u.ID, orgID)], nil
	}
	return nil, nil
}

func (f *fakeWorkspaceRepo) CreateOrgInvitation(_ context.Context, inv *domain.OrgInvitation) error {
	if inv.ID == uuid.Nil {
		inv.ID = uuid.New()
	}
	if inv.CreatedAt.IsZero() {
		inv.CreatedAt = time.Now()
	}
	f.invites = append(f.invites, inv)
	return nil
}

func (f *fakeWorkspaceRepo) GetOrgInvitationByTokenHash(_ context.Context, tokenHash string) (*domain.OrgInvitation, error) {
	for _, inv := range f.invites {
		if inv.TokenHash == tokenHash {
			return inv, nil
		}
	}
	return nil, nil
}

func (f *fakeWorkspaceRepo) GetOrgInvitationByID(_ context.Context, id, orgID uuid.UUID) (*domain.OrgInvitation, error) {
	for _, inv := range f.invites {
		if inv.ID == id && inv.OrgID == orgID {
			return inv, nil
		}
	}
	return nil, nil
}

func (f *fakeWorkspaceRepo) ListPendingInvitations(_ context.Context, orgID uuid.UUID) ([]domain.OrgInvitation, error) {
	var out []domain.OrgInvitation
	for _, inv := range f.invites {
		if inv.OrgID == orgID && inv.Status == "pending" && inv.RevokedAt == nil && inv.ExpiresAt.After(time.Now()) {
			out = append(out, *inv)
		}
	}
	return out, nil
}

func (f *fakeWorkspaceRepo) ListOpenInvitations(_ context.Context, orgID uuid.UUID) ([]domain.OrgInvitation, error) {
	var out []domain.OrgInvitation
	for _, inv := range f.invites {
		if inv.OrgID == orgID && inv.Status == "pending" && inv.RevokedAt == nil {
			out = append(out, *inv)
		}
	}
	return out, nil
}

func (f *fakeWorkspaceRepo) GetPendingInvitationByEmail(_ context.Context, orgID uuid.UUID, email string) (*domain.OrgInvitation, error) {
	for _, inv := range f.invites {
		if inv.OrgID == orgID && strings.EqualFold(inv.Email, email) && inv.Status == "pending" && inv.RevokedAt == nil {
			return inv, nil
		}
	}
	return nil, nil
}

func (f *fakeWorkspaceRepo) UpdateOrgInvitation(_ context.Context, _ *domain.OrgInvitation) error {
	// invites are stored by pointer, so the usecase's mutations are already live.
	return nil
}

func (f *fakeWorkspaceRepo) GetRoleByID(_ context.Context, id uuid.UUID) (*domain.Role, error) {
	if r, ok := f.rolesByID[id]; ok {
		return r, nil
	}
	return nil, nil
}

func (f *fakeWorkspaceRepo) AcceptInvitation(_ context.Context, inv *domain.OrgInvitation, user *domain.User, createUser bool, newPasswordHash *string) error {
	if createUser {
		f.addUser(user) // assigns ID + registers in users/usersByEmail
	} else if newPasswordHash != nil {
		user.PasswordHash = newPasswordHash
	}
	f.orgUsers[wkey(user.ID, inv.OrgID)] = &domain.OrgUser{
		UserID: user.ID, OrgID: inv.OrgID, RoleID: inv.RoleID, Status: domain.StatusActive,
		Role: f.rolesByID[inv.RoleID],
	}
	inv.Status = "accepted"
	return nil
}

func (f *fakeWorkspaceRepo) TransferOrgOwnership(_ context.Context, orgID, fromUserID, toUserID, ownerRoleID, demoteRoleID uuid.UUID) error {
	from, to := f.orgUsers[wkey(fromUserID, orgID)], f.orgUsers[wkey(toUserID, orgID)]
	if from == nil || to == nil {
		return errors.New("missing org_user")
	}
	from.RoleID, from.Role = demoteRoleID, f.rolesByID[demoteRoleID]
	to.RoleID, to.Role = ownerRoleID, f.rolesByID[ownerRoleID]
	return nil
}

func newWorkspaceUC(repo *fakeWorkspaceRepo, appEnv string, evictor SessionEvictor) domain.WorkspaceUseCase {
	// caps + roleCaps nil → escalation guard #2 no-ops (these tests exercise the
	// owner/transfer guards, not guard #2, which has its own tests below).
	return NewWorkspaceUseCase(repo, &fakeMailer{}, appEnv, "http://localhost:5173", evictor, nil, nil, nil, nil)
}

// newWorkspaceUCWithGuard wires escalation guard #2 (P6): caps checks the caller,
// roleCaps reads the target role's capabilities. fakeWorkspaceRepo satisfies
// roleCapReader via its GetRoleCapabilities-backed GetCapabilities.
func newWorkspaceUCWithGuard(repo *fakeWorkspaceRepo, caps domain.CapabilityChecker) domain.WorkspaceUseCase {
	return NewWorkspaceUseCase(repo, &fakeMailer{}, "test", "http://localhost:5173", &fakeEvictor{}, caps, repo, nil, nil)
}

func assertWorkspaceErr(t *testing.T, err error, want int, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected AppError %d, got nil", ctx, want)
	}
	appErr, ok := err.(*domain.AppError)
	if !ok || appErr.Code != want {
		t.Fatalf("%s: expected AppError %d, got %v", ctx, want, err)
	}
}

// ============================================================
// Owner-promotion escalation guards (P10 P0)
// ============================================================

// members.manage alone must never mint an owner: promoting TO the owner role
// via the role-change endpoint is a straight privilege escalation.
func TestUpdateMemberRole_BlocksOwnerPromotion(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	owner := repo.addRole(domain.RoleOwner, true)
	admin := repo.addRole(domain.RoleAdmin, false)
	repo.addMember(uuid.New(), orgID, owner, domain.StatusActive)
	target := repo.addMember(uuid.New(), orgID, admin, domain.StatusActive)

	uc := newWorkspaceUC(repo, "test", nil)
	err := uc.UpdateMemberRole(context.Background(), orgID, target.UserID, domain.UpdateMemberRoleInput{RoleID: owner.ID})
	assertWorkspaceErr(t, err, 403, "promoting to owner via role change")
	if target.Role.Name != domain.RoleAdmin {
		t.Fatal("target's role must be unchanged after the blocked promotion")
	}
}

func TestInviteMember_BlocksOwnerRole(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	owner := repo.addRole(domain.RoleOwner, true)
	uc := newWorkspaceUC(repo, "test", nil)
	_, _, err := uc.InviteMember(context.Background(), uuid.New(), domain.InviteMemberInput{
		Email: "new@x.com", RoleID: owner.ID,
	})
	assertWorkspaceErr(t, err, 403, "inviting as owner")
	if len(repo.invites) != 0 {
		t.Fatal("no invitation row may be created for a blocked owner invite")
	}
}

// ============================================================
// TransferOwnership — owner-only, transactional demote+promote
// ============================================================

func TestTransferOwnership_OnlyOwnerCanTransfer(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	owner := repo.addRole(domain.RoleOwner, true)
	admin := repo.addRole(domain.RoleAdmin, false)
	repo.addMember(uuid.New(), orgID, owner, domain.StatusActive)
	caller := repo.addMember(uuid.New(), orgID, admin, domain.StatusActive) // admin, NOT owner
	target := repo.addMember(uuid.New(), orgID, admin, domain.StatusActive)

	uc := newWorkspaceUC(repo, "test", nil)
	err := uc.TransferOwnership(context.Background(), orgID, caller.UserID, target.UserID)
	assertWorkspaceErr(t, err, 403, "non-owner transferring ownership")
}

func TestTransferOwnership_DemotesOldOwnerAndPromotesTarget(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	owner := repo.addRole(domain.RoleOwner, true)
	repo.addRole(domain.RoleAdmin, false)
	caller := repo.addMember(uuid.New(), orgID, owner, domain.StatusActive)
	target := repo.addMember(uuid.New(), orgID, repo.roles[domain.RoleAdmin], domain.StatusActive)
	ev := &fakeEvictor{}

	uc := newWorkspaceUC(repo, "test", ev)
	if err := uc.TransferOwnership(context.Background(), orgID, caller.UserID, target.UserID); err != nil {
		t.Fatalf("owner-initiated transfer should succeed: %v", err)
	}
	if !domain.IsOwnerRole(target.Role) {
		t.Fatal("target must hold the owner role after transfer")
	}
	if domain.IsOwnerRole(caller.Role) {
		t.Fatal("previous owner must be DEMOTED — a transfer must never leave two owners")
	}
	if caller.Role.Name != domain.RoleAdmin {
		t.Fatalf("previous owner should land on admin, got %s", caller.Role.Name)
	}
	if len(ev.evicted) != 2 {
		t.Fatalf("both parties' sessions must be evicted, got %d evictions", len(ev.evicted))
	}
}

func TestTransferOwnership_TargetMustBeActiveMember(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	owner := repo.addRole(domain.RoleOwner, true)
	admin := repo.addRole(domain.RoleAdmin, false)
	caller := repo.addMember(uuid.New(), orgID, owner, domain.StatusActive)
	suspended := repo.addMember(uuid.New(), orgID, admin, domain.StatusSuspended)

	uc := newWorkspaceUC(repo, "test", nil)
	if err := uc.TransferOwnership(context.Background(), orgID, caller.UserID, uuid.New()); err != domain.ErrNotMember {
		t.Fatalf("transfer to a non-member: got %v, want ErrNotMember", err)
	}
	err := uc.TransferOwnership(context.Background(), orgID, caller.UserID, suspended.UserID)
	assertWorkspaceErr(t, err, 409, "transfer to a suspended member")
}

// ============================================================
// Session eviction on membership mutations (P10 P0)
// ============================================================

func TestMembershipMutations_EvictSessionCache(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	repo.addRole(domain.RoleOwner, true)
	admin := repo.addRole(domain.RoleAdmin, false)
	viewer := repo.addRole(domain.RoleViewer, false)
	repo.addMember(uuid.New(), orgID, repo.roles[domain.RoleOwner], domain.StatusActive)
	target := repo.addMember(uuid.New(), orgID, admin, domain.StatusActive)

	ev := &fakeEvictor{}
	uc := newWorkspaceUC(repo, "test", ev)
	ctx := context.Background()

	if err := uc.UpdateMemberRole(ctx, orgID, target.UserID, domain.UpdateMemberRoleInput{RoleID: viewer.ID}); err != nil {
		t.Fatalf("role change: %v", err)
	}
	if err := uc.SuspendMember(ctx, orgID, target.UserID); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if err := uc.ReinstateMember(ctx, orgID, target.UserID); err != nil {
		t.Fatalf("reinstate: %v", err)
	}
	if _, err := uc.RemoveMember(ctx, orgID, target.UserID, domain.RemoveMemberInput{Strategy: "unassign"}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if len(ev.evicted) != 4 {
		t.Fatalf("expected 4 session evictions (role change, suspend, reinstate, remove), got %d", len(ev.evicted))
	}
	for i, e := range ev.evicted {
		if e[0] != target.UserID || e[1] != orgID {
			t.Fatalf("eviction %d hit (%s, %s), want the mutated member", i, e[0], e[1])
		}
	}
}

// ============================================================
// Invite debug token allowlist (P10 P1)
// ============================================================

func TestInviteMember_DebugTokenAllowlist(t *testing.T) {
	for _, tc := range []struct {
		appEnv    string
		wantToken bool
	}{
		{"development", true},
		{"test", true},
		{"", false},           // unset env — the prod fail-open this fix closes
		{"prod", false},       // typo'd env must fail closed too
		{"production", false},
	} {
		repo := newFakeWorkspaceRepo()
		viewer := repo.addRole(domain.RoleViewer, false)
		uc := newWorkspaceUC(repo, tc.appEnv, nil)
		_, debug, err := uc.InviteMember(context.Background(), uuid.New(), domain.InviteMemberInput{
			Email: "new@x.com", RoleID: viewer.ID,
		})
		if err != nil {
			t.Fatalf("appEnv=%q: invite failed: %v", tc.appEnv, err)
		}
		if got := debug != nil; got != tc.wantToken {
			t.Errorf("appEnv=%q: debug token returned=%v, want %v", tc.appEnv, got, tc.wantToken)
		}
	}
}

// ============================================================
// Escalation guard #2 (P6): assigning/inviting into a role that can manage
// roles or members requires the CALLER to hold roles.manage.
// ============================================================

// fakeCapChecker fakes the caller-side CapabilityChecker: held is the caller's
// capability set; owner bypasses everything.
type fakeCapChecker struct {
	held    map[string]bool
	isOwner bool
}

func (f *fakeCapChecker) HasCapability(_ context.Context, _ uuid.UUID, capability string) error {
	if f.isOwner || f.held[capability] {
		return nil
	}
	return domain.NewAppError(403, "missing "+capability)
}
func (f *fakeCapChecker) CallerCapabilities(_ context.Context, _ uuid.UUID) []string { return nil }
func (f *fakeCapChecker) CallerObjectAccess(_ context.Context, _ uuid.UUID) map[string]domain.ObjectAccess {
	return nil
}

func TestUpdateMemberRole_EscalationGuardBlocksPrivilegedAssignment(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	repo.addRole(domain.RoleOwner, true)
	viewer := repo.addRoleWithCaps(domain.RoleViewer, nil) // no caps
	target := repo.addMember(uuid.New(), orgID, viewer, domain.StatusActive)
	orgScope := orgID
	// A custom role that can manage members — assigning it is privileged.
	privileged := repo.addRoleWithCaps("HR Lead", &orgScope, domain.CapMembersManage)

	// A caller with members.manage but NOT roles.manage is blocked.
	blocked := newWorkspaceUCWithGuard(repo, &fakeCapChecker{held: map[string]bool{domain.CapMembersManage: true}})
	err := blocked.UpdateMemberRole(context.Background(), orgID, target.UserID, domain.UpdateMemberRoleInput{RoleID: privileged.ID})
	assertWorkspaceErr(t, err, 403, "assigning a privileged role without roles.manage")
	if target.RoleID != viewer.ID {
		t.Fatal("target's role must be unchanged after a blocked escalation")
	}

	// A caller holding roles.manage may assign it.
	allowed := newWorkspaceUCWithGuard(repo, &fakeCapChecker{held: map[string]bool{domain.CapRolesManage: true}})
	if err := allowed.UpdateMemberRole(context.Background(), orgID, target.UserID, domain.UpdateMemberRoleInput{RoleID: privileged.ID}); err != nil {
		t.Fatalf("a roles.manage caller should be able to assign a privileged role: %v", err)
	}
	if target.RoleID != privileged.ID {
		t.Fatal("target should now hold the privileged role")
	}
}

func TestUpdateMemberRole_EscalationGuardAllowsNonPrivileged(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	repo.addRole(domain.RoleOwner, true)
	admin := repo.addRoleWithCaps(domain.RoleAdmin, nil)
	target := repo.addMember(uuid.New(), orgID, admin, domain.StatusActive)
	orgScope := orgID
	plain := repo.addRoleWithCaps("Read Only Plus", &orgScope) // no privileged caps

	// Even a caller holding NEITHER roles.manage nor members.manage can assign a
	// non-privileged role — the guard only fires for roles.manage/members.manage.
	uc := newWorkspaceUCWithGuard(repo, &fakeCapChecker{held: map[string]bool{}})
	if err := uc.UpdateMemberRole(context.Background(), orgID, target.UserID, domain.UpdateMemberRoleInput{RoleID: plain.ID}); err != nil {
		t.Fatalf("assigning a non-privileged role must not trip the escalation guard: %v", err)
	}
	if target.RoleID != plain.ID {
		t.Fatal("target should hold the reassigned non-privileged role")
	}
}

func TestInviteMember_EscalationGuardBlocksPrivilegedInvite(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	orgScope := orgID
	privileged := repo.addRoleWithCaps("Ops Admin", &orgScope, domain.CapRolesManage)

	// Caller can invite, but doesn't hold roles.manage → can't invite into a
	// roles.manage role.
	uc := newWorkspaceUCWithGuard(repo, &fakeCapChecker{held: map[string]bool{domain.CapMembersInvite: true}})
	_, _, err := uc.InviteMember(context.Background(), orgID, domain.InviteMemberInput{Email: "x@y.com", RoleID: privileged.ID})
	assertWorkspaceErr(t, err, 403, "inviting into a roles.manage role without roles.manage")
	if len(repo.invites) != 0 {
		t.Fatal("no invitation may be created for a blocked escalation")
	}
}

func TestUpdateMemberRole_RejectsCrossTenantRole(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	repo.addRole(domain.RoleOwner, true)
	viewer := repo.addRoleWithCaps(domain.RoleViewer, nil)
	target := repo.addMember(uuid.New(), orgID, viewer, domain.StatusActive)
	// A custom role belonging to a DIFFERENT org must not be assignable here.
	otherOrg := uuid.New()
	foreign := repo.addRoleWithCaps("Foreign Role", &otherOrg)

	uc := newWorkspaceUCWithGuard(repo, &fakeCapChecker{held: map[string]bool{domain.CapRolesManage: true}})
	err := uc.UpdateMemberRole(context.Background(), orgID, target.UserID, domain.UpdateMemberRoleInput{RoleID: foreign.ID})
	assertWorkspaceErr(t, err, 400, "assigning a role from another org")
	if target.RoleID != viewer.ID {
		t.Fatal("target's role must be unchanged after a cross-tenant assignment is rejected")
	}
}

// Two-factor (U6.4) — unused by these tests; stubbed to satisfy domain.AuthRepository.
func (f *fakeWorkspaceRepo) SetTOTPSecret(context.Context, uuid.UUID, string) error { return nil }
func (f *fakeWorkspaceRepo) EnableTOTP(context.Context, uuid.UUID, []string) error  { return nil }
func (f *fakeWorkspaceRepo) DisableTOTP(context.Context, uuid.UUID) error           { return nil }
func (f *fakeWorkspaceRepo) ReplaceBackupCodes(context.Context, uuid.UUID, []string) error { return nil }
func (f *fakeWorkspaceRepo) ListUnusedBackupCodes(context.Context, uuid.UUID) ([]domain.TwoFactorBackupCode, error) {
	return nil, nil
}
func (f *fakeWorkspaceRepo) ConsumeBackupCode(context.Context, uuid.UUID) (bool, error) { return false, nil }
func (f *fakeWorkspaceRepo) CountBackupCodesRemaining(context.Context, uuid.UUID) (int, error) { return 0, nil }
func (f *fakeWorkspaceRepo) CreateTwoFactorChallenge(context.Context, *domain.TwoFactorChallenge) error { return nil }
func (f *fakeWorkspaceRepo) GetTwoFactorChallengeByHash(context.Context, string) (*domain.TwoFactorChallenge, error) {
	return nil, nil
}
func (f *fakeWorkspaceRepo) IncrementChallengeAttempts(context.Context, uuid.UUID) error { return nil }
func (f *fakeWorkspaceRepo) ConsumeTwoFactorChallenge(context.Context, uuid.UUID) (bool, error) { return true, nil }
func (f *fakeWorkspaceRepo) DeleteExpiredTwoFactorChallenges(context.Context) (int64, error) { return 0, nil }

func (f *fakeWorkspaceRepo) ClaimChallengeAttempt(context.Context, uuid.UUID, int) (bool, error) { return true, nil }
func (f *fakeWorkspaceRepo) RevokeAllUserAPITokens(context.Context, uuid.UUID) (int64, error)    { return 0, nil }
