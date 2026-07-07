package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ============================================================
// R2 server-side org selection (P3)
// ============================================================

const testPassword = "Passw0rd!ok"

func pwUser(repo *fakeAuthRepo, email string) *domain.User {
	hash, _ := bcrypt.GenerateFromPassword([]byte(testPassword), bcryptCost)
	s := string(hash)
	return repo.addUser(&domain.User{Email: email, PasswordHash: &s, OrgID: uuid.New()})
}

func aRole() *domain.Role {
	return &domain.Role{ID: uuid.New(), Name: domain.RoleAdmin, DataScope: domain.DataScopeAll}
}

func login(t *testing.T, uc domain.AuthUseCase, email string, orgID *uuid.UUID) *domain.AuthResponse {
	t.Helper()
	resp, err := uc.Login(context.Background(), domain.LoginInput{Email: email, Password: testPassword, OrgID: orgID}, domain.RequestMeta{})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return resp
}

func TestLogin_SingleActiveOrg_NoChooser(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "solo@x.com")
	org := uuid.New()
	repo.addMembership(u.ID, org, aRole(), domain.StatusActive)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp := login(t, uc, "solo@x.com", nil)
	if resp.ActiveOrgID != org {
		t.Fatalf("active_org_id = %v, want %v", resp.ActiveOrgID, org)
	}
	if resp.NeedsChooser {
		t.Error("a single-org user must never see the chooser")
	}
	if len(resp.Workspaces) != 1 || resp.Workspaces[0].MemberCount != 1 {
		t.Fatalf("expected one workspace with member_count 1, got %+v", resp.Workspaces)
	}
}

func TestLogin_MultiOrgNoDefault_NeedsChooser(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "multi@x.com")
	repo.addMembership(u.ID, uuid.New(), aRole(), domain.StatusActive)
	repo.addMembership(u.ID, uuid.New(), aRole(), domain.StatusActive)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp := login(t, uc, "multi@x.com", nil)
	if !resp.NeedsChooser {
		t.Error("a multi-org user with no default must be prompted to choose")
	}
	if resp.ActiveOrgID == uuid.Nil {
		t.Error("the token should still be bound to the deterministic-first org, not nil")
	}
}

func TestLogin_ValidDefault_UsesItNoChooser(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "d@x.com")
	repo.addMembership(u.ID, uuid.New(), aRole(), domain.StatusActive)
	preferred := uuid.New()
	repo.addMembership(u.ID, preferred, aRole(), domain.StatusActive)
	u.DefaultOrgID = &preferred
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp := login(t, uc, "d@x.com", nil)
	if resp.ActiveOrgID != preferred {
		t.Fatalf("a valid default must be honored: active=%v, want %v", resp.ActiveOrgID, preferred)
	}
	if resp.NeedsChooser {
		t.Error("a defaulted user must not see the chooser")
	}
	if resp.DefaultOrgID == nil || *resp.DefaultOrgID != preferred {
		t.Error("response should echo the default org")
	}
}

func TestLogin_InvalidDefault_SelfClears(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "stale@x.com")
	repo.addMembership(u.ID, uuid.New(), aRole(), domain.StatusActive)
	gone := uuid.New() // a default org they are no longer a member of
	u.DefaultOrgID = &gone
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp := login(t, uc, "stale@x.com", nil)
	if resp.DefaultOrgID != nil {
		t.Error("an invalid stored default must be self-cleared and absent from the response")
	}
	if repo.users[u.ID].DefaultOrgID != nil {
		t.Error("the invalid default must be cleared in the datastore too")
	}
}

func TestLogin_ExplicitOrg_HonoredOrRejected(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "x@x.com")
	member := uuid.New()
	repo.addMembership(u.ID, member, aRole(), domain.StatusActive)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	// Valid explicit org is honored.
	resp := login(t, uc, "x@x.com", &member)
	if resp.ActiveOrgID != member {
		t.Fatalf("explicit org should bind the session: got %v", resp.ActiveOrgID)
	}

	// An org the user can't access is a hard 403, never a silent fallback.
	notMine := uuid.New()
	_, err := uc.Login(context.Background(), domain.LoginInput{Email: "x@x.com", Password: testPassword, OrgID: &notMine}, domain.RequestMeta{})
	assertAppCode(t, err, 403)
}

func TestLogin_ZeroActiveMemberships_DeadEnd(t *testing.T) {
	repo := newFakeAuthRepo()
	pwUser(repo, "orphan@x.com")
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp := login(t, uc, "orphan@x.com", nil)
	if resp.ActiveOrgID != uuid.Nil {
		t.Errorf("a user with no active workspace must get active_org_id = nil, got %v", resp.ActiveOrgID)
	}
	if len(resp.Workspaces) != 0 {
		t.Errorf("zero-membership user should have no workspaces, got %d", len(resp.Workspaces))
	}
	if resp.NeedsChooser {
		t.Error("no workspaces means the dead-end page, not the chooser")
	}
}

func TestRefresh_ExplicitOrgUnavailable_409_KeepsToken(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "r@x.com")
	repo.addMembership(u.ID, uuid.New(), aRole(), domain.StatusActive)
	// Seed a live refresh token.
	rawRefresh := uuid.NewString()
	rtID := uuid.New()
	repo.refreshTokens[rtID] = &domain.RefreshToken{
		ID: rtID, UserID: u.ID, TokenHash: hashToken(rawRefresh),
		ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now(),
	}
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	notMine := uuid.New()
	_, err := uc.RefreshToken(context.Background(), domain.RefreshInput{RefreshToken: rawRefresh, OrgID: &notMine}, domain.RequestMeta{})
	ouErr, ok := err.(*domain.OrgUnavailableError)
	if !ok {
		t.Fatalf("refresh into an unavailable org must return OrgUnavailableError, got %T %v", err, err)
	}
	if len(ouErr.Workspaces) != 1 {
		t.Errorf("409 should carry the caller's workspaces, got %d", len(ouErr.Workspaces))
	}
	// Crucially, the presented refresh token must survive so the SPA can retry a
	// plain refresh and route to the chooser.
	if repo.refreshTokens[rtID].RevokedAt != nil {
		t.Fatal("a 409 ORG_UNAVAILABLE must NOT revoke the presented refresh token")
	}
}

func TestSwitchWorkspace_SetDefaultAndRevokePresented(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "s@x.com")
	target := uuid.New()
	repo.addMembership(u.ID, target, aRole(), domain.StatusActive)
	rawRefresh := uuid.NewString()
	rtID := uuid.New()
	repo.refreshTokens[rtID] = &domain.RefreshToken{
		ID: rtID, UserID: u.ID, TokenHash: hashToken(rawRefresh),
		ExpiresAt: time.Now().Add(24 * time.Hour), CreatedAt: time.Now(),
	}
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp, err := uc.SwitchWorkspace(context.Background(), u.ID,
		domain.SwitchWorkspaceInput{OrgID: target, SetDefault: true},
		domain.RequestMeta{IP: "9.9.9.9", UserAgent: "UA"}, rawRefresh)
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if resp.ActiveOrgID != target {
		t.Fatalf("switch should bind to the target org, got %v", resp.ActiveOrgID)
	}
	if repo.users[u.ID].DefaultOrgID == nil || *repo.users[u.ID].DefaultOrgID != target {
		t.Error("set_default must persist the target as the user's default")
	}
	// Switch hygiene: the presented refresh token is revoked (not orphaned).
	if repo.refreshTokens[rtID].RevokedAt == nil {
		t.Error("switch must revoke the presented refresh token")
	}
}

func TestSwitchWorkspace_NonActiveMemberRejected(t *testing.T) {
	repo := newFakeAuthRepo()
	u := pwUser(repo, "s2@x.com")
	suspended := uuid.New()
	repo.addMembership(u.ID, suspended, aRole(), domain.StatusSuspended)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	_, err := uc.SwitchWorkspace(context.Background(), u.ID,
		domain.SwitchWorkspaceInput{OrgID: suspended}, domain.RequestMeta{}, "")
	if err != domain.ErrNotMember {
		t.Fatalf("switching into a non-active membership: got %v, want ErrNotMember", err)
	}
}
