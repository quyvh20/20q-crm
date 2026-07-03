package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// seedLabeledRefreshToken inserts an active refresh token for a user with a known
// raw value (so tests can compute its hash) and device label, returning its id.
func seedLabeledRefreshToken(r *fakeAuthRepo, userID uuid.UUID, raw, label string) uuid.UUID {
	id := uuid.New()
	lbl := label
	r.refreshTokens[id] = &domain.RefreshToken{
		ID:          id,
		UserID:      userID,
		TokenHash:   hashToken(raw),
		DeviceLabel: &lbl,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		CreatedAt:   time.Now(),
	}
	return id
}

func TestListSessions_MarksCurrent(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "a@x.com"})
	seedLabeledRefreshToken(repo, u.ID, "raw-current", "Chrome on macOS")
	seedLabeledRefreshToken(repo, u.ID, "raw-other", "Firefox on Windows")
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	sessions, err := uc.ListSessions(context.Background(), u.ID, "raw-current")
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(sessions))
	}
	current := 0
	for _, s := range sessions {
		if s.Current {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("want exactly 1 current session, got %d", current)
	}
}

func TestRevokeSession_OwnerOnly(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "a@x.com"})
	other := repo.addUser(&domain.User{Email: "b@x.com"})
	sid := seedLabeledRefreshToken(repo, u.ID, "raw1", "Chrome")
	otherSid := seedLabeledRefreshToken(repo, other.ID, "raw2", "Safari")
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")
	org := uuid.New()

	// Another user's session cannot be revoked (scoped to owner) → error, stays active.
	if err := uc.RevokeSession(context.Background(), u.ID, org, otherSid); err == nil {
		t.Fatal("expected error revoking another user's session")
	}
	if repo.refreshTokens[otherSid].RevokedAt != nil {
		t.Fatal("another user's session must not be revoked")
	}

	// Own session revokes, bumps token_version (so the revoked device's access
	// token dies now, not in ≤2h), and is audited.
	beforeTV := repo.users[u.ID].TokenVersion
	if err := uc.RevokeSession(context.Background(), u.ID, org, sid); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if repo.refreshTokens[sid].RevokedAt == nil {
		t.Fatal("own session should be revoked")
	}
	if repo.users[u.ID].TokenVersion != beforeTV+1 {
		t.Fatalf("RevokeSession should bump token_version %d->%d to kill the revoked device's access token", beforeTV, repo.users[u.ID].TokenVersion)
	}
	if !hasAuthEvent(repo, "session.revoked") {
		t.Fatal("expected session.revoked audit event")
	}
}

func TestSignOutEverywhere_RevokesAllBumpsVersionKeepsCurrent(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "a@x.com"})
	seedLabeledRefreshToken(repo, u.ID, "raw-current", "Chrome")
	seedLabeledRefreshToken(repo, u.ID, "raw-other", "Firefox")
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")
	org := uuid.New()

	before := repo.users[u.ID].TokenVersion
	resp, err := uc.SignOutEverywhere(context.Background(), u.ID, org, "raw-current")
	if err != nil {
		t.Fatalf("SignOutEverywhere: %v", err)
	}
	if resp.AccessToken == "" || resp.RefreshToken == "" {
		t.Fatal("expected fresh access + refresh tokens for the current device")
	}
	if repo.users[u.ID].TokenVersion != before+1 {
		t.Fatalf("token_version should bump by 1, got %d→%d", before, repo.users[u.ID].TokenVersion)
	}
	// Exactly one active session remains: the freshly minted one.
	active, _ := repo.ListActiveRefreshTokens(context.Background(), u.ID)
	if len(active) != 1 {
		t.Fatalf("want 1 active session after sign-out-all, got %d", len(active))
	}
	if hashToken(resp.RefreshToken) != active[0].TokenHash {
		t.Fatal("the remaining session should be the newly minted one")
	}
	if !hasAuthEvent(repo, "session.signed_out_others") {
		t.Fatal("expected session.signed_out_others audit event")
	}
}

// seedSessionFull inserts an active refresh token carrying a full User-Agent and
// IP so the new-device matcher (which compares both) can be exercised.
func seedSessionFull(r *fakeAuthRepo, userID uuid.UUID, ua, ip string) {
	id := uuid.New()
	u, i := ua, ip
	lbl := deviceLabelFromUA(ua)
	r.refreshTokens[id] = &domain.RefreshToken{
		ID:          id,
		UserID:      userID,
		TokenHash:   hashToken(uuid.NewString()),
		DeviceLabel: &lbl,
		UserAgent:   &u,
		IP:          &i,
		ExpiresAt:   time.Now().Add(24 * time.Hour),
		CreatedAt:   time.Now(),
	}
}

// TestMaybeAlertNewDevice_ANDMatch pins the fixed suppression logic: a sign-in is
// treated as recognized (no alert) only when BOTH the exact device (full UA) AND
// the IP match an existing session. A new device on a shared IP, or a known
// device on a new network, must still alert — the OR-match regression silenced
// exactly those account-takeover cases.
func TestMaybeAlertNewDevice_ANDMatch(t *testing.T) {
	const uaChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0 Safari/537.36"
	const uaFirefox = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Firefox/121.0"
	const ip1 = "1.1.1.1"
	const ip2 = "2.2.2.2"

	cases := []struct {
		name             string
		loginUA, loginIP string
		wantAlert        bool
	}{
		{"known device on known network → no alert", uaChrome, ip1, false},
		{"known device on new network → alert", uaChrome, ip2, true},
		{"new device on shared IP → alert", uaFirefox, ip1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newFakeAuthRepo()
			u := repo.addUser(&domain.User{Email: "a@x.com", OrgID: uuid.New()})
			seedSessionFull(repo, u.ID, uaChrome, ip1)
			uc := newTestAuthUC(repo, &fakeMailer{}, "test").(*authUseCase)
			org := uuid.New()

			uc.maybeAlertNewDevice(context.Background(), repo.users[u.ID], org,
				domain.RequestMeta{UserAgent: tc.loginUA, IP: tc.loginIP})

			got := hasAuthEvent(repo, "login.new_device")
			if got != tc.wantAlert {
				t.Fatalf("alert=%v, want %v", got, tc.wantAlert)
			}
			if tc.wantAlert {
				// The event must be attributed to the ACTIVE org (not the user's
				// legacy home org), so it lands in the workspace being signed into.
				var ev *domain.AuthEvent
				for _, e := range repo.authEvents {
					if e.EventType == "login.new_device" {
						ev = e
						break
					}
				}
				if ev == nil || ev.OrgID == nil || *ev.OrgID != org {
					t.Fatalf("new_device event should be attributed to active org %v, got %+v", org, ev)
				}
			}
		})
	}
}

// TestMaybeAlertNewDevice_FirstDeviceNoAlert: with no existing sessions the login
// itself is the signal — no alert.
func TestMaybeAlertNewDevice_FirstDeviceNoAlert(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "a@x.com", OrgID: uuid.New()})
	uc := newTestAuthUC(repo, &fakeMailer{}, "test").(*authUseCase)
	uc.maybeAlertNewDevice(context.Background(), repo.users[u.ID], uuid.New(),
		domain.RequestMeta{UserAgent: "UA", IP: "1.1.1.1"})
	if hasAuthEvent(repo, "login.new_device") {
		t.Fatal("first device (no existing sessions) must not alert")
	}
}

// TestRoleChange_EmitsAuditEvent proves the admin-audit writer is wired into the
// role usecase: creating a role records a role.created event (P4).
func TestRoleChange_EmitsAuditEvent(t *testing.T) {
	roleRepo := newFakeRoleRepo()
	orgID := uuid.New()
	viewer := roleRepo.add(&domain.Role{ID: uuid.New(), Name: domain.RoleViewer, IsSystem: true, DataScope: domain.DataScopeAll})
	audit := newFakeAuthRepo()
	uc := NewRoleUseCase(roleRepo, &fakeInvalidator{}, audit)

	_, err := uc.Create(context.Background(), orgID, domain.CreateRoleInput{
		Name:        "Support Agent",
		CloneFromID: &viewer.ID,
		DataScope:   domain.DataScopeOwn,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !hasAuthEvent(audit, "role.created") {
		t.Fatal("expected role.created audit event in the admin log")
	}
}
