package usecase

import (
	"context"
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
	users        map[uuid.UUID]*domain.User
	usersByEmail map[string]*domain.User
	resetTokens  map[uuid.UUID]*domain.PasswordResetToken
	verifyTokens map[uuid.UUID]*domain.EmailVerificationToken
	authEvents   []*domain.AuthEvent
	revokedAll   map[uuid.UUID]int
}

func newFakeAuthRepo() *fakeAuthRepo {
	return &fakeAuthRepo{
		users:        map[uuid.UUID]*domain.User{},
		usersByEmail: map[string]*domain.User{},
		resetTokens:  map[uuid.UUID]*domain.PasswordResetToken{},
		verifyTokens: map[uuid.UUID]*domain.EmailVerificationToken{},
		revokedAll:   map[uuid.UUID]int{},
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
	return nil
}

func (r *fakeAuthRepo) WriteAuthEvent(_ context.Context, e *domain.AuthEvent) error {
	r.authEvents = append(r.authEvents, e)
	return nil
}

// --- unused stubs to satisfy domain.AuthRepository ---

func (r *fakeAuthRepo) CreateOrganization(context.Context, *domain.Organization) error { return nil }
func (r *fakeAuthRepo) CreateUser(context.Context, *domain.User) error                 { return nil }
func (r *fakeAuthRepo) GetUserByGoogleID(context.Context, string) (*domain.User, error) {
	return nil, nil
}
func (r *fakeAuthRepo) CreateRefreshToken(context.Context, *domain.RefreshToken) error { return nil }
func (r *fakeAuthRepo) GetRefreshTokenByHash(context.Context, string) (*domain.RefreshToken, error) {
	return nil, nil
}
func (r *fakeAuthRepo) RevokeRefreshToken(context.Context, uuid.UUID) error   { return nil }
func (r *fakeAuthRepo) CreateOrgUser(context.Context, *domain.OrgUser) error  { return nil }
func (r *fakeAuthRepo) GetOrgUser(context.Context, uuid.UUID, uuid.UUID) (*domain.OrgUser, error) {
	return nil, nil
}
func (r *fakeAuthRepo) ListOrgsByUserID(context.Context, uuid.UUID) ([]domain.OrgUser, error) {
	return nil, nil
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
func (r *fakeAuthRepo) UpdateOrgInvitation(context.Context, *domain.OrgInvitation) error { return nil }

// fakeMailer records recipients per email type. SendPasswordReset is invoked from
// ForgotPassword's goroutine, so tests never assert on it (avoids a race);
// SendSecurityAlert / SendVerification are called synchronously and are asserted.
type fakeMailer struct {
	resets  []string
	verifs  []string
	alerts  []string
}

func (m *fakeMailer) SendInvite(context.Context, string, string, string) error { return nil }
func (m *fakeMailer) SendPasswordReset(_ context.Context, to, _ string) error {
	m.resets = append(m.resets, to)
	return nil
}
func (m *fakeMailer) SendVerification(_ context.Context, to, _ string) error {
	m.verifs = append(m.verifs, to)
	return nil
}
func (m *fakeMailer) SendSecurityAlert(_ context.Context, to, _, _ string) error {
	m.alerts = append(m.alerts, to)
	return nil
}

func newTestAuthUC(repo *fakeAuthRepo, mail *fakeMailer, appEnv string) domain.AuthUseCase {
	cfg := &config.Config{FrontendURL: "http://localhost:5173", JWTSecret: "test-secret"}
	return NewAuthUseCase(repo, nil, cfg, mail, appEnv)
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
	// Security alert sent.
	if len(mail.alerts) != 1 {
		t.Errorf("expected 1 security alert, got %d", len(mail.alerts))
	}
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
	if len(mail.verifs) != 1 {
		t.Errorf("expected 1 verification email, got %d", len(mail.verifs))
	}

	// Immediate second resend hits the cooldown.
	if _, err := uc.ResendVerification(ctx, u.ID, domain.RequestMeta{}); err != domain.ErrResendTooSoon {
		t.Errorf("second resend: got %v, want ErrResendTooSoon", err)
	}

	// An already-verified user is a no-op success (no new token, no email).
	now := time.Now()
	verified := repo.addUser(&domain.User{Email: "v@x.com", OrgID: uuid.New(), EmailVerifiedAt: &now})
	mail.verifs = nil
	if _, err := uc.ResendVerification(ctx, verified.ID, domain.RequestMeta{}); err != nil {
		t.Fatalf("resend for verified user should be nil: %v", err)
	}
	if len(mail.verifs) != 0 {
		t.Error("verified user must not receive a verification email")
	}
}
