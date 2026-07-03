package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// seedRefreshToken inserts an unexpired, unrevoked refresh token and returns its
// raw value (the client-side secret whose SHA-256 is stored).
func seedRefreshToken(repo *fakeAuthRepo, userID uuid.UUID) string {
	raw := uuid.NewString()
	id := uuid.New()
	repo.refreshTokens[id] = &domain.RefreshToken{
		ID:        id,
		UserID:    userID,
		TokenHash: hashToken(raw),
		ExpiresAt: time.Now().Add(refreshTokenDuration),
	}
	return raw
}

func decodeTV(t *testing.T, accessToken string) int {
	t.Helper()
	claims := &JWTClaims{}
	_, err := jwt.ParseWithClaims(accessToken, claims, func(*jwt.Token) (interface{}, error) {
		return []byte("test-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	return claims.TokenVersion
}

func hasAuthEvent(repo *fakeAuthRepo, eventType string) bool {
	for _, e := range repo.authEvents {
		if e.EventType == eventType {
			return true
		}
	}
	return false
}

// A normal refresh rotates the token (old one revoked, a different one issued)
// and the new access token carries the user's current token_version.
func TestRefreshToken_RotatesToken(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()})
	raw := seedRefreshToken(repo, u.ID)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	resp, err := uc.RefreshToken(context.Background(), domain.RefreshInput{RefreshToken: raw}, domain.RequestMeta{})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if resp.RefreshToken == "" || resp.RefreshToken == raw {
		t.Error("refresh must issue a new, different refresh token")
	}
	// The presented token is now revoked.
	for _, tok := range repo.refreshTokens {
		if tok.TokenHash == hashToken(raw) && tok.RevokedAt == nil {
			t.Error("the rotated (presented) token must be revoked")
		}
	}
	if got := decodeTV(t, resp.AccessToken); got != u.TokenVersion {
		t.Errorf("access token tv = %d, want %d", got, u.TokenVersion)
	}
}

// Replaying an already-rotated refresh token is treated as theft: every session
// is nuked (refresh revoked + token_version bumped), the user is alerted, a
// security event is written, and the caller gets ErrTokenReuse.
func TestRefreshToken_ReuseDetectionNukesSessions(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()})
	raw := seedRefreshToken(repo, u.ID)
	mail := &fakeMailer{}
	uc := newTestAuthUC(repo, mail, "test")
	ctx := context.Background()

	// First refresh succeeds and rotates `raw` out.
	if _, err := uc.RefreshToken(ctx, domain.RefreshInput{RefreshToken: raw}, domain.RequestMeta{}); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Replaying the now-rotated token trips reuse detection.
	_, err := uc.RefreshToken(ctx, domain.RefreshInput{RefreshToken: raw}, domain.RequestMeta{})
	if err != domain.ErrTokenReuse {
		t.Fatalf("reuse: got %v, want ErrTokenReuse", err)
	}
	if repo.revokedAll[u.ID] == 0 {
		t.Error("reuse must revoke all of the user's refresh tokens")
	}
	if repo.tokenVersions[u.ID] != 1 {
		t.Errorf("reuse must bump token_version once, got %d", repo.tokenVersions[u.ID])
	}
	if len(mail.alerts) != 1 {
		t.Errorf("reuse must send exactly one security alert, got %d", len(mail.alerts))
	}
	if !hasAuthEvent(repo, "token.reuse") {
		t.Error("reuse must write a token.reuse security event")
	}
}

// A refresh token that was deliberately revoked (logout / revoke-device /
// sign-out-everywhere) — i.e. revoked with NO successor in the rotation chain —
// must fail closed with a plain 401 when replayed, WITHOUT nuking the user's other
// sessions or firing the token-theft alarm. This is what keeps P4's revoke-device
// and sign-out-everywhere controls from cascading into a full "your token was
// stolen" logout when the cut-off device later polls with its dead cookie.
func TestRefreshToken_DeliberatelyRevokedDoesNotNuke(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()})
	raw := seedRefreshToken(repo, u.ID)
	// Revoke it directly, as RevokeSession/logout/sign-out-all do — no rotation,
	// so no successor token points back at it.
	for _, tok := range repo.refreshTokens {
		if tok.TokenHash == hashToken(raw) {
			now := time.Now()
			tok.RevokedAt = &now
		}
	}
	mail := &fakeMailer{}
	uc := newTestAuthUC(repo, mail, "test")

	_, err := uc.RefreshToken(context.Background(), domain.RefreshInput{RefreshToken: raw}, domain.RequestMeta{})
	if err != domain.ErrInvalidToken {
		t.Fatalf("deliberately-revoked replay: got %v, want ErrInvalidToken", err)
	}
	if repo.revokedAll[u.ID] != 0 {
		t.Error("deliberate revocation replay must NOT nuke all sessions")
	}
	if repo.tokenVersions[u.ID] != 0 {
		t.Error("deliberate revocation replay must NOT bump token_version")
	}
	if len(mail.alerts) != 0 {
		t.Errorf("deliberate revocation replay must NOT send a theft alert, got %d", len(mail.alerts))
	}
	if hasAuthEvent(repo, "token.reuse") {
		t.Error("deliberate revocation replay must NOT write a token.reuse event")
	}
}

func TestRefreshToken_RejectsUnknownAndExpired(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", OrgID: uuid.New()})
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")
	ctx := context.Background()

	if _, err := uc.RefreshToken(ctx, domain.RefreshInput{RefreshToken: "never-issued"}, domain.RequestMeta{}); err != domain.ErrInvalidToken {
		t.Errorf("unknown token: got %v, want ErrInvalidToken", err)
	}

	// An expired (but unrevoked) token is rejected as expired, not reused.
	raw := uuid.NewString()
	id := uuid.New()
	repo.refreshTokens[id] = &domain.RefreshToken{
		ID:        id,
		UserID:    u.ID,
		TokenHash: hashToken(raw),
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	if _, err := uc.RefreshToken(ctx, domain.RefreshInput{RefreshToken: raw}, domain.RequestMeta{}); err != domain.ErrTokenExpired {
		t.Errorf("expired token: got %v, want ErrTokenExpired", err)
	}
}

// A password reset bumps token_version, which is what makes already-issued access
// tokens die immediately (P2) rather than lingering to their TTL.
func TestResetPassword_BumpsTokenVersion(t *testing.T) {
	repo := newFakeAuthRepo()
	u := repo.addUser(&domain.User{Email: "u@x.com", PasswordHash: ptrStr("oldhash"), OrgID: uuid.New()})
	raw := seedResetToken(repo, u.ID, time.Hour)
	uc := newTestAuthUC(repo, &fakeMailer{}, "test")

	if err := uc.ResetPassword(context.Background(), domain.ResetPasswordInput{Token: raw, Password: "New-Pass1!"}, domain.RequestMeta{}); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if repo.tokenVersions[u.ID] != 1 {
		t.Errorf("reset must bump token_version once, got %d", repo.tokenVersions[u.ID])
	}
}
