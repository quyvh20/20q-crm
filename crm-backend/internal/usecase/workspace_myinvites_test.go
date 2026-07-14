package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// U4 item 6 — the "you've been invited" consent surface (ListMyInvitations +
// AcceptMyInvitation): a logged-in user seeing/accepting the invitations addressed
// to their OWN account email, authorized by the email match rather than the invite
// token. Distinct from the token-based AcceptInvite (public link) and the admin
// outgoing-invites panel.

func verifiedNow() *time.Time { t := time.Now(); return &t }

// addValidInvite seeds a live (pending, unexpired) invite to a live workspace.
func addValidInvite(repo *fakeWorkspaceRepo, orgID, roleID uuid.UUID, email string) *domain.OrgInvitation {
	inv := &domain.OrgInvitation{
		ID: uuid.New(), Email: email, OrgID: orgID, RoleID: roleID,
		Status: "pending", ExpiresAt: time.Now().Add(inviteTokenDuration), CreatedAt: time.Now(),
	}
	repo.invites = append(repo.invites, inv)
	return inv
}

func TestListMyInvitations_ReturnsOnlyMineToLiveWorkspaces(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), EmailVerifiedAt: verifiedNow()})
	rep := repo.addRole(domain.RoleViewer, false)

	orgA, orgB, orgDead := uuid.New(), uuid.New(), uuid.New()
	repo.addOrg(orgA, "Acme")
	repo.addOrg(orgB, "Beta")
	// orgDead is intentionally NOT added → models a soft-deleted workspace.
	addValidInvite(repo, orgA, rep.ID, "bob@acme.com")
	addValidInvite(repo, orgB, rep.ID, "bob@acme.com")
	addValidInvite(repo, orgDead, rep.ID, "bob@acme.com") // to a deleted workspace — excluded
	addValidInvite(repo, orgA, rep.ID, "someone-else@x.com")
	// An expired invite to a live workspace — excluded.
	expired := addValidInvite(repo, orgB, rep.ID, "bob@acme.com")
	expired.ExpiresAt = time.Now().Add(-time.Hour)

	uc := newWorkspaceUC(repo, "test", nil)
	list, err := uc.ListMyInvitations(context.Background(), me.ID)
	if err != nil {
		t.Fatalf("ListMyInvitations: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected exactly my 2 live invites (Acme, Beta), got %d: %+v", len(list), list)
	}
	names := map[string]bool{}
	for _, inv := range list {
		names[inv.OrgName] = true
		if inv.RoleName != domain.RoleViewer {
			t.Errorf("expected role name resolved, got %q", inv.RoleName)
		}
	}
	if !names["Acme"] || !names["Beta"] {
		t.Errorf("expected Acme + Beta, got %+v", names)
	}
}

func TestAcceptMyInvitation_JoinsAndMarksAccepted(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), EmailVerifiedAt: verifiedNow()})
	role := repo.addRole(domain.RoleViewer, false)
	orgID := uuid.New()
	repo.addOrg(orgID, "Acme")
	inv := addValidInvite(repo, orgID, role.ID, "bob@acme.com")

	uc := newWorkspaceUC(repo, "test", nil)
	joined, err := uc.AcceptMyInvitation(context.Background(), me.ID, inv.ID)
	if err != nil {
		t.Fatalf("AcceptMyInvitation: %v", err)
	}
	if joined != orgID {
		t.Fatalf("expected to join %s, got %s", orgID, joined)
	}
	if ou := repo.orgUsers[wkey(me.ID, orgID)]; ou == nil || ou.Status != domain.StatusActive || ou.RoleID != role.ID {
		t.Fatalf("expected an active membership with the invited role, got %+v", ou)
	}
	if inv.Status != "accepted" {
		t.Errorf("the invitation should be marked accepted, got %q", inv.Status)
	}
}

// Authorization is the email match: you cannot accept an invitation addressed to a
// DIFFERENT email even if you know its id.
func TestAcceptMyInvitation_RejectsInviteForAnotherEmail(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), EmailVerifiedAt: verifiedNow()})
	role := repo.addRole(domain.RoleViewer, false)
	orgID := uuid.New()
	repo.addOrg(orgID, "Acme")
	inv := addValidInvite(repo, orgID, role.ID, "victim@acme.com") // NOT bob

	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptMyInvitation(context.Background(), me.ID, inv.ID)
	assertWorkspaceErr(t, err, 403, "accepting an invite addressed to another email")
	if repo.orgUsers[wkey(me.ID, orgID)] != nil {
		t.Error("no membership should be created when the email doesn't match")
	}
	if inv.Status != "pending" {
		t.Error("the other person's invite must stay pending")
	}
}

func TestAcceptMyInvitation_RejectsExpired(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), EmailVerifiedAt: verifiedNow()})
	role := repo.addRole(domain.RoleViewer, false)
	orgID := uuid.New()
	repo.addOrg(orgID, "Acme")
	inv := addValidInvite(repo, orgID, role.ID, "bob@acme.com")
	inv.ExpiresAt = time.Now().Add(-time.Hour)

	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptMyInvitation(context.Background(), me.ID, inv.ID)
	assertWorkspaceErr(t, err, 400, "accepting an expired invite")
}

// A pending invite to a workspace that was deleted (org not in the store) can't be
// accepted — it would strand the user in a phantom workspace.
func TestAcceptMyInvitation_RejectsDeletedWorkspace(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), EmailVerifiedAt: verifiedNow()})
	role := repo.addRole(domain.RoleViewer, false)
	orgID := uuid.New() // deliberately NOT addOrg'd → deleted/gone
	inv := addValidInvite(repo, orgID, role.ID, "bob@acme.com")

	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptMyInvitation(context.Background(), me.ID, inv.ID)
	assertWorkspaceErr(t, err, 400, "accepting an invite to a deleted workspace")
}

func TestAcceptMyInvitation_UnknownID404(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New(), EmailVerifiedAt: verifiedNow()})
	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptMyInvitation(context.Background(), me.ID, uuid.New())
	assertWorkspaceErr(t, err, 404, "accepting a non-existent invitation")
}

// Security gate (review HIGH): consent-by-email requires a VERIFIED account email —
// an unverified account (which anyone can register for a not-yet-taken address
// without inbox control) must not be able to enumerate or accept invites here. It
// falls back to the emailed link, whose single-use token proves inbox control.
func TestListMyInvitations_UnverifiedAccountSeesNothing(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New()}) // EmailVerifiedAt nil
	role := repo.addRole(domain.RoleViewer, false)
	orgID := uuid.New()
	repo.addOrg(orgID, "Acme")
	addValidInvite(repo, orgID, role.ID, "bob@acme.com")

	uc := newWorkspaceUC(repo, "test", nil)
	list, err := uc.ListMyInvitations(context.Background(), me.ID)
	if err != nil {
		t.Fatalf("ListMyInvitations: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("an unverified account must see no consent invites, got %d", len(list))
	}
}

func TestAcceptMyInvitation_RejectsUnverifiedAccount(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	me := repo.addUser(&domain.User{Email: "bob@acme.com", OrgID: uuid.New()}) // EmailVerifiedAt nil
	role := repo.addRole(domain.RoleViewer, false)
	orgID := uuid.New()
	repo.addOrg(orgID, "Acme")
	inv := addValidInvite(repo, orgID, role.ID, "bob@acme.com")

	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptMyInvitation(context.Background(), me.ID, inv.ID)
	assertWorkspaceErr(t, err, 403, "unverified account accepting via the consent surface")
	if repo.orgUsers[wkey(me.ID, orgID)] != nil {
		t.Error("no membership should be minted for an unverified account")
	}
	if inv.Status != "pending" {
		t.Error("the invite must stay pending (usable via the emailed link)")
	}
}
