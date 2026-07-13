package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// seedInvite inserts a pending invitation whose raw token is returned for the
// accept call. Mirrors the real InviteMember write (hashed token at rest).
func seedInvite(repo *fakeWorkspaceRepo, orgID uuid.UUID, email string, roleID uuid.UUID, rawToken string) *domain.OrgInvitation {
	inv := &domain.OrgInvitation{
		ID:        uuid.New(),
		Email:     email,
		OrgID:     orgID,
		RoleID:    roleID,
		TokenHash: hashInviteToken(rawToken),
		ExpiresAt: time.Now().Add(inviteTokenDuration),
		Status:    "pending",
		CreatedAt: time.Now(),
	}
	repo.invites = append(repo.invites, inv)
	return inv
}

// ============================================================
// 256-bit invite tokens (P2)
// ============================================================

func TestInviteMember_Uses256BitToken(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	viewer := repo.addRole(domain.RoleViewer, false)
	uc := newWorkspaceUC(repo, "test", nil)

	_, debug, err := uc.InviteMember(context.Background(), uuid.New(), domain.InviteMemberInput{
		Email: "new@x.com", RoleID: viewer.ID,
	})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if debug == nil {
		t.Fatal("test env should surface the debug token")
	}
	// 32 bytes hex-encoded = 64 chars, vs uuid.New().String()'s 36. This is the
	// concrete jump from ~122 bits to 256.
	if len(*debug) != 64 {
		t.Errorf("expected a 64-char (256-bit hex) invite token, got %d chars", len(*debug))
	}
}

// ============================================================
// AcceptInvite — transactional, set-password, UPSERT (P2)
// ============================================================

func TestAcceptInvite_CreatesInviteeWithPassword(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	raw := uuid.NewString()
	seedInvite(repo, orgID, "invitee@x.com", viewer.ID, raw)

	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{
		Token: raw, Password: "Sup3r-Secret!", FirstName: "In", LastName: "Vitee",
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	u := repo.usersByEmail["invitee@x.com"]
	if u == nil {
		t.Fatal("invitee user should have been created")
	}
	if u.PasswordHash == nil {
		t.Fatal("invitee must no longer be created PASSWORDLESS when they set a password")
	}
	if bcrypt.CompareHashAndPassword([]byte(*u.PasswordHash), []byte("Sup3r-Secret!")) != nil {
		t.Error("stored hash does not verify against the chosen password")
	}
	ou := repo.orgUsers[wkey(u.ID, orgID)]
	if ou == nil || ou.Status != domain.StatusActive {
		t.Fatal("invitee should be an active member after accept")
	}
}

func TestAcceptInvite_RejectsWeakPasswordWithoutWriting(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	raw := uuid.NewString()
	seedInvite(repo, orgID, "invitee@x.com", viewer.ID, raw)

	uc := newWorkspaceUC(repo, "test", nil)
	_, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{Token: raw, Password: "short"})
	assertWorkspaceErr(t, err, 400, "weak password on accept")
	if repo.usersByEmail["invitee@x.com"] != nil {
		t.Error("a rejected weak password must not leave a half-created account")
	}
}

func TestAcceptInvite_ExistingUserPasswordNotOverwritten(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	existing := repo.addUser(&domain.User{Email: "member@x.com", PasswordHash: ptrStr("original-hash"), OrgID: uuid.New()})
	raw := uuid.NewString()
	seedInvite(repo, orgID, "member@x.com", viewer.ID, raw)

	uc := newWorkspaceUC(repo, "test", nil)
	// An attacker-supplied password on the accept form must NOT replace the
	// existing account's password — that would be account takeover.
	_, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{Token: raw, Password: "Attacker-Set1!"})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if existing.PasswordHash == nil || *existing.PasswordHash != "original-hash" {
		t.Fatal("existing account's password must be left untouched by invite-accept")
	}
	if ou := repo.orgUsers[wkey(existing.ID, orgID)]; ou == nil || ou.Status != domain.StatusActive {
		t.Fatal("existing user should be (re)granted active membership")
	}
}

func TestAcceptInvite_ReinstatesRemovedMember(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	u := repo.addUser(&domain.User{Email: "back@x.com", PasswordHash: ptrStr("h"), OrgID: uuid.New()})
	// A stale suspended row from a prior stint — accept must UPSERT it to active.
	repo.orgUsers[wkey(u.ID, orgID)] = &domain.OrgUser{UserID: u.ID, OrgID: orgID, RoleID: viewer.ID, Status: domain.StatusSuspended, Role: viewer}
	raw := uuid.NewString()
	seedInvite(repo, orgID, "back@x.com", viewer.ID, raw)

	uc := newWorkspaceUC(repo, "test", nil)
	if _, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{Token: raw}); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if ou := repo.orgUsers[wkey(u.ID, orgID)]; ou == nil || ou.Status != domain.StatusActive {
		t.Fatal("re-accepting an invite must reinstate the member to active")
	}
}

func TestAcceptInvite_RejectsRevokedAndExpired(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	uc := newWorkspaceUC(repo, "test", nil)

	revokedRaw := uuid.NewString()
	revoked := seedInvite(repo, orgID, "a@x.com", viewer.ID, revokedRaw)
	now := time.Now()
	revoked.Status = "revoked"
	revoked.RevokedAt = &now

	expiredRaw := uuid.NewString()
	expired := seedInvite(repo, orgID, "b@x.com", viewer.ID, expiredRaw)
	expired.ExpiresAt = time.Now().Add(-time.Minute)

	if _, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{Token: revokedRaw}); err == nil {
		t.Error("a revoked invite must not be acceptable")
	}
	if _, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{Token: expiredRaw}); err == nil {
		t.Error("an expired invite must not be acceptable")
	}
}

// ============================================================
// Invite resend / revoke (P2)
// ============================================================

func TestResendInvitation_MintsFreshTokenAndStamps(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	oldRaw := uuid.NewString()
	inv := seedInvite(repo, orgID, "p@x.com", viewer.ID, oldRaw)
	oldHash := inv.TokenHash

	uc := newWorkspaceUC(repo, "test", nil)
	debug, err := uc.ResendInvitation(context.Background(), orgID, inv.ID)
	if err != nil {
		t.Fatalf("resend: %v", err)
	}
	if debug == nil || len(*debug) != 64 {
		t.Fatalf("resend should mint a fresh 256-bit token, got %v", debug)
	}
	if inv.TokenHash == oldHash {
		t.Error("resend must rotate the token hash (old link dies)")
	}
	if inv.ResentAt == nil {
		t.Error("resend must stamp resent_at")
	}
	// The OLD token must no longer resolve.
	if got, _ := repo.GetOrgInvitationByTokenHash(context.Background(), oldHash); got != nil {
		t.Error("old invite token must be invalidated by the resend")
	}
}

func TestRevokeInvitation_BlocksSubsequentAccept(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	raw := uuid.NewString()
	inv := seedInvite(repo, orgID, "p@x.com", viewer.ID, raw)

	uc := newWorkspaceUC(repo, "test", nil)
	if err := uc.RevokeInvitation(context.Background(), orgID, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if inv.Status != "revoked" || inv.RevokedAt == nil {
		t.Fatal("revoke must flip status to revoked and stamp revoked_at")
	}
	if _, err := uc.AcceptInvite(context.Background(), domain.AcceptInviteInput{Token: raw}); err == nil {
		t.Error("a revoked invitation must not be acceptable")
	}
}

func TestListInvitations_OnlyPending(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	seedInvite(repo, orgID, "pending@x.com", viewer.ID, uuid.NewString())
	revoked := seedInvite(repo, orgID, "revoked@x.com", viewer.ID, uuid.NewString())
	now := time.Now()
	revoked.Status, revoked.RevokedAt = "revoked", &now

	uc := newWorkspaceUC(repo, "test", nil)
	list, err := uc.ListInvitations(context.Background(), orgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Email != "pending@x.com" {
		t.Fatalf("only the pending invite should be listed, got %+v", list)
	}
	if list[0].Role != domain.RoleViewer {
		t.Errorf("invitation should resolve its role name, got %q", list[0].Role)
	}
}

// TestInviteMember_DedupesExistingInvite re-invites the same email and asserts
// the open invite is re-minted in place (U4) rather than stacked as a second row.
func TestInviteMember_DedupesExistingInvite(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	manager := repo.addRole(domain.RoleManager, false)
	uc := newWorkspaceUC(repo, "test", nil)

	if _, _, err := uc.InviteMember(context.Background(), orgID, domain.InviteMemberInput{Email: "dup@x.com", RoleID: viewer.ID}); err != nil {
		t.Fatalf("first invite: %v", err)
	}
	firstHash := repo.invites[0].TokenHash
	// Re-invite the same email with a different role.
	if _, _, err := uc.InviteMember(context.Background(), orgID, domain.InviteMemberInput{Email: "dup@x.com", RoleID: manager.ID}); err != nil {
		t.Fatalf("second invite: %v", err)
	}
	if len(repo.invites) != 1 {
		t.Fatalf("re-invite must not stack a second row, got %d", len(repo.invites))
	}
	if repo.invites[0].TokenHash == firstHash {
		t.Error("re-invite must re-mint a fresh token on the existing row")
	}
	if repo.invites[0].RoleID != manager.ID {
		t.Error("re-invite must update the role on the existing row")
	}
	if repo.invites[0].ResentAt == nil {
		t.Error("re-invite must stamp resent_at")
	}
}

// TestListInvitations_IncludesExpiredWithStatus asserts an expired-but-open
// invite is surfaced with a computed "expired" status (U4) instead of vanishing.
func TestListInvitations_IncludesExpiredWithStatus(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	exp := seedInvite(repo, orgID, "expired@x.com", viewer.ID, uuid.NewString())
	exp.ExpiresAt = time.Now().Add(-time.Hour)

	uc := newWorkspaceUC(repo, "test", nil)
	list, err := uc.ListInvitations(context.Background(), orgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Status != "expired" {
		t.Fatalf("expired invite should list with status 'expired', got %+v", list)
	}
}

// TestGetInvitationPreview_Statuses covers the accept-page metadata (U4): a bad
// token is a clean "invalid", validity is distinguished, and HasAccount flips
// when the email already exists.
func TestGetInvitationPreview_Statuses(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	uc := newWorkspaceUC(repo, "test", nil)

	// Unknown token → invalid, never an error.
	if p, err := uc.GetInvitationPreview(context.Background(), "no-such-token"); err != nil || p.Status != "invalid" {
		t.Fatalf("unknown token: status=%q err=%v", p.Status, err)
	}

	// Valid pending invite for a brand-new email.
	raw := uuid.NewString()
	seedInvite(repo, orgID, "preview@x.com", viewer.ID, raw)
	p, err := uc.GetInvitationPreview(context.Background(), raw)
	if err != nil {
		t.Fatalf("valid preview: %v", err)
	}
	if p.Status != "valid" || p.RoleName != domain.RoleViewer || p.Email != "preview@x.com" {
		t.Fatalf("valid preview mismatch: %+v", p)
	}
	if p.HasAccount {
		t.Error("HasAccount should be false for a brand-new invitee email")
	}

	// Same email now has an account → HasAccount true.
	repo.addUser(&domain.User{Email: "preview@x.com", OrgID: uuid.New()})
	if p, _ := uc.GetInvitationPreview(context.Background(), raw); !p.HasAccount {
		t.Error("HasAccount should be true once the email has an account")
	}

	// Expired.
	expRaw := uuid.NewString()
	exp := seedInvite(repo, orgID, "exp@x.com", viewer.ID, expRaw)
	exp.ExpiresAt = time.Now().Add(-time.Minute)
	if p, _ := uc.GetInvitationPreview(context.Background(), expRaw); p.Status != "expired" {
		t.Errorf("expired preview: got %q", p.Status)
	}

	// Revoked.
	revRaw := uuid.NewString()
	rev := seedInvite(repo, orgID, "rev@x.com", viewer.ID, revRaw)
	now := time.Now()
	rev.Status, rev.RevokedAt = "revoked", &now
	if p, _ := uc.GetInvitationPreview(context.Background(), revRaw); p.Status != "revoked" {
		t.Errorf("revoked preview: got %q", p.Status)
	}
}

// ============================================================
// Admin "send reset link" — membership, cooldown, daily cap (P2)
// ============================================================

func addResetLinkTarget(repo *fakeWorkspaceRepo, orgID uuid.UUID, role *domain.Role) *domain.User {
	u := repo.addUser(&domain.User{Email: "target@x.com", PasswordHash: ptrStr("h"), OrgID: uuid.New()})
	repo.addMember(u.ID, orgID, role, domain.StatusActive)
	return u
}

func TestSendMemberResetLink_RejectsNonMember(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	uc := newWorkspaceUC(repo, "test", nil)
	// target is not a member of orgID
	stranger := repo.addUser(&domain.User{Email: "stranger@x.com", OrgID: uuid.New()})
	if err := uc.SendMemberResetLink(context.Background(), orgID, uuid.New(), stranger.ID, domain.RequestMeta{}); err != domain.ErrNotMember {
		t.Fatalf("non-member target: got %v, want ErrNotMember", err)
	}
	if len(repo.resetTokens) != 0 {
		t.Error("no reset token may be minted for a non-member")
	}
}

func TestSendMemberResetLink_CreatesInitiatedTokenAndAudits(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	target := addResetLinkTarget(repo, orgID, viewer)
	caller := uuid.New()

	uc := newWorkspaceUC(repo, "test", nil)
	if err := uc.SendMemberResetLink(context.Background(), orgID, caller, target.ID, domain.RequestMeta{}); err != nil {
		t.Fatalf("send reset link: %v", err)
	}
	if len(repo.resetTokens) != 1 {
		t.Fatalf("expected exactly one reset token, got %d", len(repo.resetTokens))
	}
	for _, tok := range repo.resetTokens {
		if tok.InitiatedBy == nil || *tok.InitiatedBy != caller {
			t.Error("admin-sent link must stamp initiated_by with the acting admin")
		}
	}
	// Audit: the sending-org admin event + a user-level event exist.
	var sawAdmin, sawUserLevel bool
	for _, e := range repo.authEvents {
		if e.EventType == "password.reset_link_sent_by_admin" {
			sawAdmin = true
		}
		if e.EventType == "password.reset_link_received" {
			sawUserLevel = true
		}
	}
	if !sawAdmin || !sawUserLevel {
		t.Errorf("expected both audit events, admin=%v userLevel=%v", sawAdmin, sawUserLevel)
	}
}

func TestSendMemberResetLink_DailyCap(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	target := addResetLinkTarget(repo, orgID, viewer)

	// Pre-seed the target at the cap with admin-initiated tokens from "today",
	// each past the per-email cooldown so only the daily cap can trip.
	for i := 0; i < adminResetLinkDailyCap; i++ {
		admin := uuid.New()
		repo.resetTokens[uuid.New()] = &domain.PasswordResetToken{
			ID: uuid.New(), UserID: target.ID, InitiatedBy: &admin,
			CreatedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(time.Hour),
		}
	}

	uc := newWorkspaceUC(repo, "test", nil)
	err := uc.SendMemberResetLink(context.Background(), orgID, uuid.New(), target.ID, domain.RequestMeta{})
	assertWorkspaceErr(t, err, 429, "daily cap exceeded")
}

func TestSendMemberResetLink_Cooldown(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	orgID := uuid.New()
	viewer := repo.addRole(domain.RoleViewer, false)
	target := addResetLinkTarget(repo, orgID, viewer)

	uc := newWorkspaceUC(repo, "test", nil)
	if err := uc.SendMemberResetLink(context.Background(), orgID, uuid.New(), target.ID, domain.RequestMeta{}); err != nil {
		t.Fatalf("first send: %v", err)
	}
	// A second send immediately after hits the shared per-email cooldown.
	err := uc.SendMemberResetLink(context.Background(), orgID, uuid.New(), target.ID, domain.RequestMeta{})
	assertWorkspaceErr(t, err, 429, "cooldown")
}
