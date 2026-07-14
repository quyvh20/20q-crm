package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/google/uuid"
)

// These tests cover U4 item 6 — the Google-first invitee fix — via resolveGoogleUser,
// the account-provisioning core GoogleLogin calls after fetching the Google profile
// (extracted so it is testable without OAuth network I/O). The headline behavior: a
// brand-new, Google-VERIFIED email that has an open invitation is created with NO
// personal org and NO membership (the zero-membership landing where the SPA offers
// the invite for explicit consent) rather than silently auto-joined OR forked into a
// junk "<name>'s Workspace".

// fakeStageRepo is a no-op PipelineStageRepository whose CountByOrg returns a
// positive count so seedDefaultStages short-circuits without a DB.
type fakeStageRepo struct{}

func (fakeStageRepo) List(context.Context, uuid.UUID) ([]domain.PipelineStage, error) {
	return nil, nil
}
func (fakeStageRepo) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.PipelineStage, error) {
	return nil, nil
}
func (fakeStageRepo) Create(context.Context, *domain.PipelineStage) error  { return nil }
func (fakeStageRepo) Update(context.Context, *domain.PipelineStage) error  { return nil }
func (fakeStageRepo) Delete(context.Context, uuid.UUID, uuid.UUID) error   { return nil }
func (fakeStageRepo) CountByOrg(context.Context, uuid.UUID) (int64, error) { return 1, nil }

// fakeGoogleRepo layers real invite + account-provisioning behavior onto the shared
// fakeAuthRepo so resolveGoogleUser can be tested end-to-end without network I/O.
type fakeGoogleRepo struct {
	*fakeAuthRepo
	validInvites []domain.OrgInvitation // returned by ListValidInvitationsByEmail
	inviteErr    error                  // when set, ListValidInvitationsByEmail fails
	googleUsers  map[string]*domain.User
	ownerRole    *domain.Role
	orgsCreated  int
	membersMade  int
}

func newFakeGoogleRepo() *fakeGoogleRepo {
	owner := &domain.Role{ID: uuid.New(), Name: domain.RoleOwner, IsSystem: true, IsOwner: true, DataScope: domain.DataScopeAll}
	return &fakeGoogleRepo{
		fakeAuthRepo: newFakeAuthRepo(),
		googleUsers:  map[string]*domain.User{},
		ownerRole:    owner,
	}
}

func (f *fakeGoogleRepo) GetUserByGoogleID(_ context.Context, googleID string) (*domain.User, error) {
	return f.googleUsers[googleID], nil
}

func (f *fakeGoogleRepo) ListValidInvitationsByEmail(_ context.Context, _ string) ([]domain.OrgInvitation, error) {
	if f.inviteErr != nil {
		return nil, f.inviteErr
	}
	return f.validInvites, nil
}

func (f *fakeGoogleRepo) CreateOrganization(_ context.Context, org *domain.Organization) error {
	if org.ID == uuid.Nil {
		org.ID = uuid.New()
	}
	f.orgsCreated++
	return nil
}

func (f *fakeGoogleRepo) CreateUser(_ context.Context, u *domain.User) error {
	f.addUser(u) // assigns ID + registers in users/usersByEmail
	return nil
}

func (f *fakeGoogleRepo) CreateOrgUser(_ context.Context, ou *domain.OrgUser) error {
	f.membersMade++
	role := f.ownerRole
	if ou.RoleID != f.ownerRole.ID {
		role = &domain.Role{ID: ou.RoleID}
	}
	f.addMembership(ou.UserID, ou.OrgID, role, ou.Status)
	return nil
}

func (f *fakeGoogleRepo) GetRoleByName(_ context.Context, name string, _ *uuid.UUID) (*domain.Role, error) {
	if name == domain.RoleOwner {
		return f.ownerRole, nil
	}
	return nil, errors.New("role not found")
}

func newGoogleAuthUC(repo domain.AuthRepository) *authUseCase {
	cfg := &config.Config{FrontendURL: "http://localhost:5173", JWTSecret: "test-secret"}
	return NewAuthUseCase(repo, fakeStageRepo{}, cfg, &fakeMailer{}, "test", nil).(*authUseCase)
}

func validInvite(email string, orgID uuid.UUID) domain.OrgInvitation {
	return domain.OrgInvitation{
		ID: uuid.New(), Email: email, OrgID: orgID, RoleID: uuid.New(),
		Status: "pending", ExpiresAt: time.Now().Add(inviteTokenDuration), CreatedAt: time.Now(),
	}
}

// Headline: a brand-new, Google-verified email WITH an open invite is created with NO
// personal org and NO membership — the zero-membership landing for consent. Crucially
// the invite is NOT auto-accepted (no silent capture).
func TestResolveGoogleUser_InvitedUser_ZeroMembershipNoJunkOrg(t *testing.T) {
	repo := newFakeGoogleRepo()
	orgID := uuid.New()
	repo.validInvites = []domain.OrgInvitation{validInvite("bob@acme.com", orgID)}
	uc := newGoogleAuthUC(repo)

	g := domain.GoogleUserInfo{ID: "g-123", Email: "bob@acme.com", VerifiedEmail: true, GivenName: "Bob", FamilyName: "Jones"}
	user, err := uc.resolveGoogleUser(context.Background(), g)
	if err != nil {
		t.Fatalf("resolveGoogleUser: %v", err)
	}
	if repo.orgsCreated != 0 {
		t.Fatalf("an invited Google user must NOT fork a personal org; created %d", repo.orgsCreated)
	}
	if repo.membersMade != 0 {
		t.Fatalf("an invited Google user must NOT be auto-joined (no silent capture); memberships created %d", repo.membersMade)
	}
	if user == nil || user.OrgID != uuid.Nil {
		t.Fatalf("the invitee should have NO home org (uuid.Nil) until they consent, got %+v", user)
	}
	if user.GoogleID == nil || *user.GoogleID != "g-123" {
		t.Error("the Google account should be linked to the new user")
	}
	if user.EmailVerifiedAt == nil {
		t.Error("a Google-verified invitee should be email-verified")
	}
	// The invite is untouched — accepted later via explicit consent, not here.
	if repo.validInvites[0].Status != "pending" {
		t.Errorf("the invitation must remain pending (not auto-accepted), got %q", repo.validInvites[0].Status)
	}
}

// Regression guard: a brand-new Google user with NO invite still forks a personal
// workspace and owns it.
func TestResolveGoogleUser_NoInvite_ForksPersonalOrg(t *testing.T) {
	repo := newFakeGoogleRepo()
	uc := newGoogleAuthUC(repo)

	g := domain.GoogleUserInfo{ID: "g-new", Email: "solo@founder.com", VerifiedEmail: true, GivenName: "Solo"}
	user, err := uc.resolveGoogleUser(context.Background(), g)
	if err != nil {
		t.Fatalf("resolveGoogleUser: %v", err)
	}
	if repo.orgsCreated != 1 {
		t.Fatalf("a brand-new Google user with no invite should fork exactly one personal org, got %d", repo.orgsCreated)
	}
	ou, _ := repo.GetOrgUser(context.Background(), user.ID, user.OrgID)
	if ou == nil || ou.RoleID != repo.ownerRole.ID {
		t.Fatalf("a solo Google sign-up should own their personal org, got %+v", ou)
	}
}

// Security gate: an UNVERIFIED Google email never triggers the invite path (the
// invite→email trust assumes the address is proven) — it forks a personal org.
func TestResolveGoogleUser_UnverifiedEmail_IgnoresInviteForksOrg(t *testing.T) {
	repo := newFakeGoogleRepo()
	repo.validInvites = []domain.OrgInvitation{validInvite("bob@acme.com", uuid.New())}
	uc := newGoogleAuthUC(repo)

	g := domain.GoogleUserInfo{ID: "g-unv", Email: "bob@acme.com", VerifiedEmail: false, GivenName: "Bob"}
	user, err := uc.resolveGoogleUser(context.Background(), g)
	if err != nil {
		t.Fatalf("resolveGoogleUser: %v", err)
	}
	if repo.orgsCreated != 1 || user.OrgID == uuid.Nil {
		t.Fatalf("an UNVERIFIED Google email must fork a personal org, not enter the invite path (orgsCreated=%d)", repo.orgsCreated)
	}
}

// Fail-closed: a DB error on the invite lookup must NOT silently fork a junk org and
// strand the invite — it surfaces as an error so the login can be retried.
func TestResolveGoogleUser_InviteLookupError_FailsClosed(t *testing.T) {
	repo := newFakeGoogleRepo()
	repo.inviteErr = errors.New("db blip")
	uc := newGoogleAuthUC(repo)

	g := domain.GoogleUserInfo{ID: "g-err", Email: "bob@acme.com", VerifiedEmail: true, GivenName: "Bob"}
	_, err := uc.resolveGoogleUser(context.Background(), g)
	if err == nil {
		t.Fatal("a DB error on the invite lookup must surface, not fall through to forking a junk org")
	}
	if repo.orgsCreated != 0 {
		t.Fatalf("no org should be forked when the invite lookup errored, got %d", repo.orgsCreated)
	}
}

// An EXISTING local account signing in with Google links Google and never forks an
// org; its pending invite is NOT auto-accepted (it stays clickable via consent).
func TestResolveGoogleUser_ExistingAccount_LinksNoOrgNoAutoAccept(t *testing.T) {
	repo := newFakeGoogleRepo()
	existing := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), PasswordHash: ptrStr("hash")})
	repo.validInvites = []domain.OrgInvitation{validInvite("bob@acme.com", uuid.New())}
	uc := newGoogleAuthUC(repo)

	g := domain.GoogleUserInfo{ID: "g-existing", Email: "bob@acme.com", VerifiedEmail: true, GivenName: "Bob"}
	user, err := uc.resolveGoogleUser(context.Background(), g)
	if err != nil {
		t.Fatalf("resolveGoogleUser: %v", err)
	}
	if user.ID != existing.ID {
		t.Fatal("should return the existing account, not a new one")
	}
	if repo.orgsCreated != 0 || repo.membersMade != 0 {
		t.Fatal("linking Google to an existing account must not fork an org or mint membership")
	}
	if user.GoogleID == nil || *user.GoogleID != "g-existing" {
		t.Error("Google should be linked to the existing account")
	}
}

// A returning Google user (already linked by google_id) is returned as-is: no email
// lookup, no invite check, no org creation.
func TestResolveGoogleUser_AlreadyLinked_ReturnsExisting(t *testing.T) {
	repo := newFakeGoogleRepo()
	linked := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New()})
	repo.googleUsers["g-linked"] = linked
	repo.validInvites = []domain.OrgInvitation{validInvite("bob@acme.com", uuid.New())}
	uc := newGoogleAuthUC(repo)

	g := domain.GoogleUserInfo{ID: "g-linked", Email: "bob@acme.com", VerifiedEmail: true, GivenName: "Bob"}
	user, err := uc.resolveGoogleUser(context.Background(), g)
	if err != nil {
		t.Fatalf("resolveGoogleUser: %v", err)
	}
	if user.ID != linked.ID {
		t.Fatal("should return the already-linked account")
	}
	if repo.orgsCreated != 0 {
		t.Fatal("no org should be created for a returning Google user")
	}
}
