package usecase

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ============================================================
// Fakes
// ============================================================

// fakeAuthRepo is an in-memory domain.AuthRepository sufficient to exercise the
// P1 recovery flows. MarkPasswordResetTokenUsed / MarkEmailVerificationTokenUsed
// reproduce the real "UPDATE … WHERE used_at IS NULL" semantics (return 1 on the
// winning claim, 0 otherwise) so the single-use guarantee is genuinely tested.
type fakeAuthRepo struct {
	users         map[uuid.UUID]*domain.User
	usersByEmail  map[string]*domain.User
	resetTokens   map[uuid.UUID]*domain.PasswordResetToken
	verifyTokens  map[uuid.UUID]*domain.EmailVerificationToken
	refreshTokens map[uuid.UUID]*domain.RefreshToken
	tokenVersions map[uuid.UUID]int
	authEvents    []*domain.AuthEvent
	revokedAll    map[uuid.UUID]int
	// memberships backs the R2 org-selection tests (P3), keyed userID|orgID.
	memberships map[string]*domain.OrgUser
}

func newFakeAuthRepo() *fakeAuthRepo {
	return &fakeAuthRepo{
		users:         map[uuid.UUID]*domain.User{},
		usersByEmail:  map[string]*domain.User{},
		resetTokens:   map[uuid.UUID]*domain.PasswordResetToken{},
		verifyTokens:  map[uuid.UUID]*domain.EmailVerificationToken{},
		refreshTokens: map[uuid.UUID]*domain.RefreshToken{},
		tokenVersions: map[uuid.UUID]int{},
		revokedAll:    map[uuid.UUID]int{},
		memberships:   map[string]*domain.OrgUser{},
	}
}

func (r *fakeAuthRepo) addUser(u *domain.User) *domain.User {
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	r.users[u.ID] = u
	r.usersByEmail[u.Email] = u
	return u
}

// addMembership registers an active/suspended membership for the R2 selection
// tests, with a minimal Org + Role so buildWorkspaces can render it.
func (r *fakeAuthRepo) addMembership(userID, orgID uuid.UUID, role *domain.Role, status string) *domain.OrgUser {
	ou := &domain.OrgUser{
		UserID: userID, OrgID: orgID, Status: status, Role: role,
		Org: &domain.Organization{ID: orgID, Name: "Org-" + orgID.String()[:8], Type: "company"},
	}
	if role != nil {
		ou.RoleID = role.ID
	}
	r.memberships[userID.String()+"|"+orgID.String()] = ou
	return ou
}

// --- methods used by the recovery flows ---

func (r *fakeAuthRepo) GetUserByEmail(_ context.Context, email string) (*domain.User, error) {
	if u, ok := r.usersByEmail[email]; ok {
		return u, nil
	}
	return nil, nil
}

func (r *fakeAuthRepo) GetUserByID(_ context.Context, id uuid.UUID) (*domain.User, error) {
	if u, ok := r.users[id]; ok {
		return u, nil
	}
	return nil, nil
}

func (r *fakeAuthRepo) UpdateUser(_ context.Context, user *domain.User) error {
	r.users[user.ID] = user
	r.usersByEmail[user.Email] = user
	return nil
}

func (r *fakeAuthRepo) UpdateUserProfile(_ context.Context, user *domain.User) error {
	r.users[user.ID] = user
	r.usersByEmail[user.Email] = user
	return nil
}

func (r *fakeAuthRepo) CreatePasswordResetToken(_ context.Context, t *domain.PasswordResetToken) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	r.resetTokens[t.ID] = t
	return nil
}

func (r *fakeAuthRepo) GetPasswordResetTokenByHash(_ context.Context, hash string) (*domain.PasswordResetToken, error) {
	for _, t := range r.resetTokens {
		if t.TokenHash == hash {
			return t, nil
		}
	}
	return nil, nil
}

func (r *fakeAuthRepo) MarkPasswordResetTokenUsed(_ context.Context, id uuid.UUID) (int64, error) {
	t, ok := r.resetTokens[id]
	if !ok || t.UsedAt != nil { // conditional: used_at IS NULL
		return 0, nil
	}
	now := time.Now()
	t.UsedAt = &now
	return 1, nil
}

func (r *fakeAuthRepo) GetLatestPasswordResetToken(_ context.Context, userID uuid.UUID) (*domain.PasswordResetToken, error) {
	var latest *domain.PasswordResetToken
	for _, t := range r.resetTokens {
		if t.UserID == userID && (latest == nil || t.CreatedAt.After(latest.CreatedAt)) {
			latest = t
		}
	}
	return latest, nil
}

func (r *fakeAuthRepo) VoidActivePasswordResetTokens(_ context.Context, userID uuid.UUID) error {
	now := time.Now()
	for _, t := range r.resetTokens {
		if t.UserID == userID && t.UsedAt == nil {
			t.UsedAt = &now
		}
	}
	return nil
}

func (r *fakeAuthRepo) CountAdminResetTokensSince(_ context.Context, userID uuid.UUID, since time.Time) (int64, error) {
	var n int64
	for _, t := range r.resetTokens {
		if t.UserID == userID && t.InitiatedBy != nil && t.CreatedAt.After(since) {
			n++
		}
	}
	return n, nil
}

func (r *fakeAuthRepo) CreateEmailVerificationToken(_ context.Context, t *domain.EmailVerificationToken) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	r.verifyTokens[t.ID] = t
	return nil
}

func (r *fakeAuthRepo) GetEmailVerificationTokenByHash(_ context.Context, hash string) (*domain.EmailVerificationToken, error) {
	for _, t := range r.verifyTokens {
		if t.TokenHash == hash {
			return t, nil
		}
	}
	return nil, nil
}

func (r *fakeAuthRepo) MarkEmailVerificationTokenUsed(_ context.Context, id uuid.UUID) (int64, error) {
	t, ok := r.verifyTokens[id]
	if !ok || t.UsedAt != nil {
		return 0, nil
	}
	now := time.Now()
	t.UsedAt = &now
	return 1, nil
}

func (r *fakeAuthRepo) GetLatestEmailVerificationToken(_ context.Context, userID uuid.UUID) (*domain.EmailVerificationToken, error) {
	var latest *domain.EmailVerificationToken
	for _, t := range r.verifyTokens {
		if t.UserID == userID && (latest == nil || t.CreatedAt.After(latest.CreatedAt)) {
			latest = t
		}
	}
	return latest, nil
}

func (r *fakeAuthRepo) RevokeAllUserRefreshTokens(_ context.Context, userID uuid.UUID) error {
	r.revokedAll[userID]++
	now := time.Now()
	for _, t := range r.refreshTokens {
		if t.UserID == userID && t.RevokedAt == nil {
			t.RevokedAt = &now
		}
	}
	return nil
}

func (r *fakeAuthRepo) WriteAuthEvent(_ context.Context, e *domain.AuthEvent) error {
	r.authEvents = append(r.authEvents, e)
	return nil
}

func (r *fakeAuthRepo) ListAuthEvents(_ context.Context, orgID uuid.UUID, f domain.AuthEventFilter) ([]domain.AuthEventView, int64, error) {
	var out []domain.AuthEventView
	for _, e := range r.authEvents {
		if e.OrgID == nil || *e.OrgID != orgID {
			continue
		}
		if f.Category != "" && e.Category != f.Category {
			continue
		}
		if f.EventType != "" && e.EventType != f.EventType {
			continue
		}
		if f.ActorID != nil && (e.ActorID == nil || *e.ActorID != *f.ActorID) {
			continue
		}
		out = append(out, domain.AuthEventView{AuthEvent: *e})
	}
	return out, int64(len(out)), nil
}

func (r *fakeAuthRepo) ListActiveRefreshTokens(_ context.Context, userID uuid.UUID) ([]domain.RefreshToken, error) {
	var out []domain.RefreshToken
	for _, t := range r.refreshTokens {
		if t.UserID == userID && t.RevokedAt == nil && t.ExpiresAt.After(time.Now()) {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (r *fakeAuthRepo) RevokeRefreshTokenForUser(_ context.Context, id, userID uuid.UUID) (int64, error) {
	t, ok := r.refreshTokens[id]
	if !ok || t.UserID != userID || t.RevokedAt != nil {
		return 0, nil
	}
	now := time.Now()
	t.RevokedAt = &now
	return 1, nil
}

// --- unused stubs to satisfy domain.AuthRepository ---

func (r *fakeAuthRepo) CreateOrganization(context.Context, *domain.Organization) error { return nil }
func (r *fakeAuthRepo) GetOrganizationByID(context.Context, uuid.UUID) (*domain.Organization, error) {
	return nil, nil
}
func (r *fakeAuthRepo) CreateUser(context.Context, *domain.User) error                 { return nil }
func (r *fakeAuthRepo) GetUserByGoogleID(context.Context, string) (*domain.User, error) {
	return nil, nil
}
func (r *fakeAuthRepo) CreateRefreshToken(_ context.Context, t *domain.RefreshToken) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	r.refreshTokens[t.ID] = t
	return nil
}
func (r *fakeAuthRepo) GetRefreshTokenByHash(_ context.Context, hash string) (*domain.RefreshToken, error) {
	for _, t := range r.refreshTokens {
		if t.TokenHash == hash && t.RevokedAt == nil && time.Now().Before(t.ExpiresAt) {
			return t, nil
		}
	}
	return nil, nil
}
func (r *fakeAuthRepo) GetRefreshTokenByHashAny(_ context.Context, hash string) (*domain.RefreshToken, error) {
	for _, t := range r.refreshTokens {
		if t.TokenHash == hash {
			return t, nil
		}
	}
	return nil, nil
}
func (r *fakeAuthRepo) RefreshTokenHasSuccessor(_ context.Context, id uuid.UUID) (bool, error) {
	for _, t := range r.refreshTokens {
		if t.RotatedFrom != nil && *t.RotatedFrom == id {
			return true, nil
		}
	}
	return false, nil
}
func (r *fakeAuthRepo) RevokeRefreshToken(_ context.Context, id uuid.UUID) error {
	if t, ok := r.refreshTokens[id]; ok && t.RevokedAt == nil {
		now := time.Now()
		t.RevokedAt = &now
	}
	return nil
}
func (r *fakeAuthRepo) IncrementUserTokenVersion(_ context.Context, userID uuid.UUID) error {
	r.tokenVersions[userID]++
	if u, ok := r.users[userID]; ok {
		u.TokenVersion++
	}
	return nil
}
func (r *fakeAuthRepo) GetUserTokenVersion(_ context.Context, userID uuid.UUID) (int, error) {
	return r.tokenVersions[userID], nil
}
func (r *fakeAuthRepo) CreateOrgUser(context.Context, *domain.OrgUser) error { return nil }
func (r *fakeAuthRepo) GetOrgUser(_ context.Context, userID, orgID uuid.UUID) (*domain.OrgUser, error) {
	return r.memberships[userID.String()+"|"+orgID.String()], nil
}

// ListOrgsByUserID mirrors the real repo: ACTIVE memberships only, deterministic
// order (joined_at is zero here so fall back to a stable org_id sort).
func (r *fakeAuthRepo) ListOrgsByUserID(_ context.Context, userID uuid.UUID) ([]domain.OrgUser, error) {
	var out []domain.OrgUser
	for _, ou := range r.memberships {
		if ou.UserID == userID && ou.Status == domain.StatusActive {
			out = append(out, *ou)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OrgID.String() < out[j].OrgID.String() })
	return out, nil
}

func (r *fakeAuthRepo) ListAllOrgMembershipsByUserID(_ context.Context, userID uuid.UUID) ([]domain.OrgUser, error) {
	var out []domain.OrgUser
	for _, ou := range r.memberships {
		if ou.UserID == userID {
			out = append(out, *ou)
		}
	}
	// Active first, then by org id — mirrors the repo's display ordering.
	sort.Slice(out, func(i, j int) bool {
		ai, aj := out[i].Status == domain.StatusActive, out[j].Status == domain.StatusActive
		if ai != aj {
			return ai
		}
		return out[i].OrgID.String() < out[j].OrgID.String()
	})
	return out, nil
}

func (r *fakeAuthRepo) SetUserDefaultOrg(_ context.Context, userID uuid.UUID, orgID *uuid.UUID) error {
	if u, ok := r.users[userID]; ok {
		u.DefaultOrgID = orgID
	}
	return nil
}

func (r *fakeAuthRepo) CountActiveMembersByOrgs(_ context.Context, orgIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	want := map[uuid.UUID]bool{}
	for _, id := range orgIDs {
		want[id] = true
	}
	out := map[uuid.UUID]int{}
	for _, ou := range r.memberships {
		if want[ou.OrgID] && ou.Status == domain.StatusActive {
			out[ou.OrgID]++
		}
	}
	return out, nil
}
func (r *fakeAuthRepo) ListMembersByOrgID(context.Context, uuid.UUID) ([]domain.OrgUser, error) {
	return nil, nil
}
func (r *fakeAuthRepo) UpdateOrgUserRole(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (r *fakeAuthRepo) UpdateOrgUserStatus(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (r *fakeAuthRepo) DeleteOrgUser(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (r *fakeAuthRepo) GetOrgUserByEmail(context.Context, string, uuid.UUID) (*domain.OrgUser, error) {
	return nil, nil
}
func (r *fakeAuthRepo) CountOrgUsersByRole(context.Context, uuid.UUID, uuid.UUID, string) (int64, error) {
	return 0, nil
}
func (r *fakeAuthRepo) GetRoleByName(context.Context, string, *uuid.UUID) (*domain.Role, error) {
	return nil, nil
}
func (r *fakeAuthRepo) GetRoleByID(context.Context, uuid.UUID) (*domain.Role, error) { return nil, nil }
func (r *fakeAuthRepo) CreateOrgInvitation(context.Context, *domain.OrgInvitation) error {
	return nil
}
func (r *fakeAuthRepo) GetOrgInvitationByTokenHash(context.Context, string) (*domain.OrgInvitation, error) {
	return nil, nil
}
func (r *fakeAuthRepo) GetOrgInvitationByID(context.Context, uuid.UUID, uuid.UUID) (*domain.OrgInvitation, error) {
	return nil, nil
}
func (r *fakeAuthRepo) ListPendingInvitations(context.Context, uuid.UUID) ([]domain.OrgInvitation, error) {
	return nil, nil
}
func (r *fakeAuthRepo) AcceptInvitation(context.Context, *domain.OrgInvitation, *domain.User, bool, *string) error {
	return nil
}
func (r *fakeAuthRepo) UpdateOrgInvitation(context.Context, *domain.OrgInvitation) error { return nil }
func (r *fakeAuthRepo) TransferOrgOwnership(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

// fakeMailer records recipients per email type. SendPasswordReset AND
// SendSecurityAlert are invoked from detached goroutines (ForgotPassword /
// ResetPassword side effects), so the counters are mutex-guarded and async
// assertions go through waitForAlerts.
type fakeMailer struct {
	mu      sync.Mutex
	resets  []string
	verifs  []string
	alerts  []string
	invites []string
}

func (m *fakeMailer) SendInvite(_ context.Context, to, _, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invites = append(m.invites, to)
	return nil
}
func (m *fakeMailer) SendPasswordReset(_ context.Context, to, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets = append(m.resets, to)
	return nil
}
func (m *fakeMailer) SendVerification(_ context.Context, to, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifs = append(m.verifs, to)
	return nil
}
func (m *fakeMailer) SendSecurityAlert(_ context.Context, to, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, to)
	return nil
}

func (m *fakeMailer) alertCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.alerts)
}

func (m *fakeMailer) verifCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.verifs)
}

func (m *fakeMailer) resetVerifs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.verifs = nil
}

// waitForAlerts polls until the async security-alert goroutine has delivered
// (or the deadline passes) — the alert is fire-and-forget by design.
func waitForAlerts(t *testing.T, m *fakeMailer, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.alertCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("expected %d security alert(s), got %d after waiting", want, m.alertCount())
}

func newTestAuthUC(repo *fakeAuthRepo, mail *fakeMailer, appEnv string) domain.AuthUseCase {
	cfg := &config.Config{FrontendURL: "http://localhost:5173", JWTSecret: "test-secret"}
	// nil Redis: the per-email throttle and session-cache eviction no-op, which is
	// what we want for pure usecase unit tests.
	return NewAuthUseCase(repo, nil, cfg, mail, appEnv, nil)
}

func ptrStr(s string) *string { return &s }

// ============================================================
// ForgotPassword — no account enumeration
// ============================================================

func TestForgotPassword_NoEnumerationInProduction(t *testing.T) {
	repo := newFakeAuthRepo()
	repo.addUser(&domain.User{Email: "real@x.com", PasswordHash: ptrStr("old"), OrgID: uuid.New()})
	uc := newTestAuthUC(repo, &fakeMailer{}, "production")
	ctx := context.Background()

	knownDebug, knownErr := uc.ForgotPassword(ctx, domain.ForgotPasswordInput{Email: "real@x.com"}, domain.RequestMeta{})
	unknownDebug, unknownErr := uc.ForgotPassword(ctx, domain.ForgotPasswordInput{Email: "nobody@x.com"}, domain.RequestMeta{})

	if knownErr != nil || unknownErr != nil {
		t.Fatalf("forgot must not error on lookup: known=%v unknown=%v", knownErr, unknownErr)
	}
	// In production both branches must return identical (nil) debug tokens — no
	// existence signal in the response.
	if knownDebug != nil || unknownDebug != nil {
		t.Errorf("production must not leak a debug token: known=%v unknown=%v", knownDebug, unknownDebug)
	}
	// A reset token is created only for the real account.
	if len(repo.resetTokens) != 1 {
		t.Errorf("expected exactly 1 reset token (real user only), got %d", len(repo.resetTokens))
	}
}

func TestForgotPassword_NonProdReturnsDebugToken(t *testing.T) {
	repo := newFakeAuthRepo()
	repo.addUser(&domain.User{Email: "real@x.com", PasswordHash: ptrStr("old"), OrgID: uuid.New()})
	uc := newTestAuthUC(repo, &fakeMailer{}, "development")

	debug, err := uc.ForgotPassword(context.Background(), domain.ForgotPasswordInput{Email: "real@x.com"}, domain.RequestMeta{})
	if err != nil {
		t.Fatalf("forgot err: %v", err)
	}
	if debug == nil || *debug == "" {
		t.Error("non-prod should return a debug token for a known email")
	}
}

// TestForgotPassword_DebugTokenFailsClosed is the regression test for the P10
// P1 takeover hole: the old `appEnv != "production"` gate returned the raw
// reset token whenever APP_ENV was unset or typo'd — which it was on prod. Only
// the explicit development/test allowlist may enable debug tokens.
func TestForgotPassword_DebugTokenFailsClosed(t *testing.T) {
	for _, appEnv := range []string{"", "prod", "Production", "staging"} {
		repo := newFakeAuthRepo()
		repo.addUser(&domain.User{Email: "real@x.com", PasswordHash: ptrStr("old"), OrgID: uuid.New()})
		uc := newTestAuthUC(repo, &fakeMailer{}, appEnv)

		debug, err := uc.ForgotPassword(context.Background(), domain.ForgotPasswordInput{Email: "real@x.com"}, domain.RequestMeta{})
		if err != nil {
			t.Fatalf("appEnv=%q: forgot err: %v", appEnv, err)
		}
		if debug != nil {
			t.Errorf("appEnv=%q must NOT return a debug token — that is an account-takeover primitive", appEnv)
		}
	}
}

// TestForgotPassword_PerEmailCooldown: a second request within the cooldown
// silently succeeds (enumeration-safe) but mints no new token — the guard
// against reset-email bombing.
func TestForgotPassword_PerEmailCooldown(t *testing.T) {
	repo := newFakeAuthRepo()
	repo.addUser(&domain.User{Email: "real@x.com", PasswordHash: ptrStr("old"), OrgID: uuid.New()})
	uc := newTestAuthUC(repo, &fakeMailer{}, "development")
	ctx := context.Background()

	first, err := uc.ForgotPassword(ctx, domain.ForgotPasswordInput{Email: "real@x.com"}, domain.RequestMeta{})
	if err != nil || first == nil {
		t.Fatalf("first request should mint a token: token=%v err=%v", first, err)
	}
	second, err := uc.ForgotPassword(ctx, domain.ForgotPasswordInput{Email: "real@x.com"}, domain.RequestMeta{})
	if err != nil {
		t.Fatalf("throttled request must still report success (no enumeration signal): %v", err)
	}
	if second != nil {
		t.Error("throttled request must not mint or return a token")
	}
	if len(repo.resetTokens) != 1 {
		t.Errorf("expected exactly 1 reset token after the throttled repeat, got %d", len(repo.resetTokens))
	}
}

// TestForgotPassword_VoidsPriorTokens: a fresh request invalidates outstanding
// unused tokens, so re-requesting narrows rather than widens the window of
// concurrently valid reset links.
func TestForgotPassword_VoidsPriorTokens(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "real@x.com", PasswordHash: ptrStr("old"), OrgID: uuid.New()})
	// Seed an OLD outstanding token (past the cooldown) directly.
	oldRaw := seedResetToken(repo, u.ID, time.Hour)
	for _, tok := range repo.resetTokens {
		tok.CreatedAt = time.Now().Add(-2 * time.Minute)
	}
	uc := newTestAuthUC(repo, &fakeMailer{}, "development")
	ctx := context.Background()

	fresh, err := uc.ForgotPassword(ctx, domain.ForgotPasswordInput{Email: "real@x.com"}, domain.RequestMeta{})
	if err != nil || fresh == nil {
		t.Fatalf("request should mint a token: token=%v err=%v", fresh, err)
	}

	// The old token must be dead; the fresh one must work.
	if err := uc.ResetPassword(ctx, domain.ResetPasswordInput{Token: oldRaw, Password: "New-Pass1!"}, domain.RequestMeta{}); err != domain.ErrInvalidResetToken {
		t.Errorf("prior token must be voided by the new request: got %v, want ErrInvalidResetToken", err)
	}
	if err := uc.ResetPassword(ctx, domain.ResetPasswordInput{Token: *fresh, Password: "New-Pass1!"}, domain.RequestMeta{}); err != nil {
		t.Errorf("the freshly minted token must be usable: %v", err)
	}
}

// ============================================================
// ResetPassword — set password, single-use, expiry, revoke sessions
// ============================================================

// seedResetToken inserts an unexpired, unused reset token whose raw value is
// returned for use in the request.
func seedResetToken(repo *fakeAuthRepo, userID uuid.UUID, ttl time.Duration) string {
	raw := uuid.NewString()
	id := uuid.New()
	repo.resetTokens[id] = &domain.PasswordResetToken{
		ID:        id,
		UserID:    userID,
		TokenHash: hashToken(raw),
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}
	return raw
}

func TestResetPassword_SucceedsAndKillsSessions(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", PasswordHash: ptrStr("oldhash"), OrgID: uuid.New()})
	raw := seedResetToken(repo, u.ID, time.Hour)
	mail := &fakeMailer{}
	uc := newTestAuthUC(repo, mail, "test")

	err := uc.ResetPassword(context.Background(), domain.ResetPasswordInput{Token: raw, Password: "New-Pass1!"}, domain.RequestMeta{})
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	// New password committed and hashed.
	if u.PasswordHash == nil || *u.PasswordHash == "oldhash" {
		t.Fatal("password hash was not updated")
	}
	if bcrypt.CompareHashAndPassword([]byte(*u.PasswordHash), []byte("New-Pass1!")) != nil {
		t.Error("stored hash does not verify against the new password")
	}
	// Sessions revoked exactly once.
	if repo.revokedAll[u.ID] != 1 {
		t.Errorf("expected all refresh tokens revoked once, got %d", repo.revokedAll[u.ID])
	}
	// Security alert sent (async — the alert is detached from the request).
	waitForAlerts(t, mail, 1)
	// Reset also verifies the email (control of inbox proven).
	if u.EmailVerifiedAt == nil {
		t.Error("expected email_verified_at to be set after reset")
	}
}

func TestResetPassword_TokenIsSingleUse(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", PasswordHash: ptrStr("oldhash"), OrgID: uuid.New()})
	raw := seedResetToken(repo, u.ID, time.Hour)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")
	ctx := context.Background()

	if err := uc.ResetPassword(ctx, domain.ResetPasswordInput{Token: raw, Password: "New-Pass1!"}, domain.RequestMeta{}); err != nil {
		t.Fatalf("first reset should succeed: %v", err)
	}
	// Replay the same token — must be rejected.
	err := uc.ResetPassword(ctx, domain.ResetPasswordInput{Token: raw, Password: "Other-Pass2!"}, domain.RequestMeta{})
	if err != domain.ErrInvalidResetToken {
		t.Errorf("replayed reset token: got %v, want ErrInvalidResetToken", err)
	}
}

func TestResetPassword_RejectsExpiredToken(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", PasswordHash: ptrStr("oldhash"), OrgID: uuid.New()})
	raw := seedResetToken(repo, u.ID, -time.Minute) // already expired
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	err := uc.ResetPassword(context.Background(), domain.ResetPasswordInput{Token: raw, Password: "New-Pass1!"}, domain.RequestMeta{})
	if err != domain.ErrInvalidResetToken {
		t.Errorf("expired reset token: got %v, want ErrInvalidResetToken", err)
	}
	if *u.PasswordHash != "oldhash" {
		t.Error("expired token must not change the password")
	}
}

func TestResetPassword_RejectsWeakPasswordWithoutConsumingToken(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", PasswordHash: ptrStr("oldhash"), OrgID: uuid.New()})
	raw := seedResetToken(repo, u.ID, time.Hour)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	err := uc.ResetPassword(context.Background(), domain.ResetPasswordInput{Token: raw, Password: "short"}, domain.RequestMeta{})
	if err == nil {
		t.Fatal("weak password should be rejected")
	}
	if *u.PasswordHash != "oldhash" {
		t.Error("weak password must not change the stored hash")
	}
	// The token must remain usable after a rejected weak-password attempt.
	for _, tok := range repo.resetTokens {
		if tok.UsedAt != nil {
			t.Error("policy rejection must not consume the reset token")
		}
	}
}

// ============================================================
// VerifyEmail — verify, single-use, expiry
// ============================================================

func seedVerifyToken(repo *fakeAuthRepo, userID uuid.UUID, ttl time.Duration) string {
	raw := uuid.NewString()
	id := uuid.New()
	repo.verifyTokens[id] = &domain.EmailVerificationToken{
		ID:        id,
		UserID:    userID,
		TokenHash: hashToken(raw),
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}
	return raw
}

func TestVerifyEmail_SucceedsThenSingleUse(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()})
	raw := seedVerifyToken(repo, u.ID, 24*time.Hour)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")
	ctx := context.Background()

	if err := uc.VerifyEmail(ctx, domain.VerifyEmailInput{Token: raw}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if u.EmailVerifiedAt == nil {
		t.Error("expected email_verified_at set after verify")
	}
	// Replay must be rejected (single-use).
	if err := uc.VerifyEmail(ctx, domain.VerifyEmailInput{Token: raw}); err != domain.ErrInvalidVerifyToken {
		t.Errorf("replayed verify token: got %v, want ErrInvalidVerifyToken", err)
	}
}

func TestVerifyEmail_RejectsExpiredToken(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()})
	raw := seedVerifyToken(repo, u.ID, -time.Minute)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	if err := uc.VerifyEmail(context.Background(), domain.VerifyEmailInput{Token: raw}); err != domain.ErrInvalidVerifyToken {
		t.Errorf("expired verify token: got %v, want ErrInvalidVerifyToken", err)
	}
	if u.EmailVerifiedAt != nil {
		t.Error("expired token must not verify the email")
	}
}

// ============================================================
// ResendVerification — cooldown + idempotency
// ============================================================

func TestResendVerification_CooldownAndAlreadyVerified(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()}) // unverified
	mail := &fakeMailer{}
	uc := newTestAuthUC(repo, mail, "development")
	ctx := context.Background()

	debug, err := uc.ResendVerification(ctx, u.ID, domain.RequestMeta{})
	if err != nil {
		t.Fatalf("first resend: %v", err)
	}
	if debug == nil {
		t.Error("non-prod resend should return a debug token")
	}
	if mail.verifCount() != 1 {
		t.Errorf("expected 1 verification email, got %d", mail.verifCount())
	}

	// Immediate second resend hits the cooldown.
	if _, err := uc.ResendVerification(ctx, u.ID, domain.RequestMeta{}); err != domain.ErrResendTooSoon {
		t.Errorf("second resend: got %v, want ErrResendTooSoon", err)
	}

	// An already-verified user is a no-op success (no new token, no email).
	now := time.Now()
	verified := repo.addUser(&domain.User{Email: "v@x.com", OrgID: uuid.New(), EmailVerifiedAt: &now})
	mail.resetVerifs()
	if _, err := uc.ResendVerification(ctx, verified.ID, domain.RequestMeta{}); err != nil {
		t.Fatalf("resend for verified user should be nil: %v", err)
	}
	if mail.verifCount() != 0 {
		t.Error("verified user must not receive a verification email")
	}
}
