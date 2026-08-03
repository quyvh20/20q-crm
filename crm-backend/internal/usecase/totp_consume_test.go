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

// These drive consumeSecondFactor itself, which the arithmetic tests in
// totp_replay_test.go do not reach. Without them, deleting the step claim from
// consumeSecondFactor leaves the whole suite green — verified: that mutation was
// how the gap was found.

// totpConsumeRepo is a minimal AuthRepository: only the three methods
// consumeSecondFactor actually calls need to behave.
type totpConsumeRepo struct {
	domain.AuthRepository // embedded: everything unused panics rather than lying

	claimOK     bool
	claimCalls  int
	claimedStep int64

	backupCodes  []domain.TwoFactorBackupCode
	consumedCode uuid.UUID
}

func (r *totpConsumeRepo) ConsumeTOTPStep(_ context.Context, _ uuid.UUID, step int64) (bool, error) {
	r.claimCalls++
	r.claimedStep = step
	return r.claimOK, nil
}

func (r *totpConsumeRepo) ListUnusedBackupCodes(_ context.Context, _ uuid.UUID) ([]domain.TwoFactorBackupCode, error) {
	return r.backupCodes, nil
}

func (r *totpConsumeRepo) ConsumeBackupCode(_ context.Context, id uuid.UUID) (bool, error) {
	r.consumedCode = id
	return true, nil
}

func totpUser(t *testing.T, secret string) *domain.User {
	t.Helper()
	// Stored encrypted, exactly as the real column holds it.
	enc, err := encryptTOTPSecret(secret, "", "test-jwt-secret")
	if err != nil {
		t.Fatalf("encryptTOTPSecret: %v", err)
	}
	return &domain.User{ID: uuid.New(), TotpSecret: &enc}
}

func totpUC(repo domain.AuthRepository) *authUseCase {
	return &authUseCase{authRepo: repo, cfg: &config.Config{JWTSecret: "test-jwt-secret"}}
}

// The control working: a correct code whose step is unspent is accepted, and the
// step it claims is the one the code belongs to.
func TestConsumeSecondFactor_AcceptsAnUnspentCode(t *testing.T) {
	secret := testSecret(t)
	repo := &totpConsumeRepo{claimOK: true}
	code := codeAt(t, secret, time.Now())

	ok, err := totpUC(repo).consumeSecondFactor(context.Background(), totpUser(t, secret), code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("a correct, unspent code must be accepted")
	}
	if repo.claimCalls != 1 {
		t.Fatalf("the step must be claimed exactly once, got %d calls", repo.claimCalls)
	}
	if want := time.Now().Unix() / totpPeriod; repo.claimedStep != want {
		t.Fatalf("claimed step %d, want %d", repo.claimedStep, want)
	}
}

// THE REPLAY. The code is arithmetically perfect; the claim fails because the step
// was already spent. This is the test that fails if the guard is removed.
func TestConsumeSecondFactor_RejectsAnAlreadySpentCode(t *testing.T) {
	secret := testSecret(t)
	repo := &totpConsumeRepo{claimOK: false} // the CAS refuses: step already spent
	code := codeAt(t, secret, time.Now())

	ok, err := totpUC(repo).consumeSecondFactor(context.Background(), totpUser(t, secret), code)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("a replayed code was accepted — the step claim is not being enforced")
	}
	if repo.claimCalls != 1 {
		t.Fatalf("the claim must be attempted, got %d calls", repo.claimCalls)
	}
}

// A spent TOTP code must not block a legitimate backup code presented afterwards:
// consumeSecondFactor falls through rather than returning early.
func TestConsumeSecondFactor_SpentCodeStillFallsThroughToBackupCodes(t *testing.T) {
	backup := "ABCDE-FGHJK"
	hash, err := bcrypt.GenerateFromPassword([]byte(normalizeBackupCode(backup)), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	id := uuid.New()
	repo := &totpConsumeRepo{
		claimOK:     false,
		backupCodes: []domain.TwoFactorBackupCode{{ID: id, CodeHash: string(hash)}},
	}

	ok, err := totpUC(repo).consumeSecondFactor(context.Background(), totpUser(t, testSecret(t)), backup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("a valid backup code must still work")
	}
	if repo.consumedCode != id {
		t.Fatal("the backup code must be burned")
	}
}

// A backup-code login must not touch the TOTP step, or it could be used to reset
// the guard and re-enable a replay.
func TestConsumeSecondFactor_BackupCodeDoesNotClaimAStep(t *testing.T) {
	backup := "ABCDE-FGHJK"
	hash, _ := bcrypt.GenerateFromPassword([]byte(normalizeBackupCode(backup)), bcrypt.MinCost)
	repo := &totpConsumeRepo{
		claimOK:     true,
		backupCodes: []domain.TwoFactorBackupCode{{ID: uuid.New(), CodeHash: string(hash)}},
	}

	if _, err := totpUC(repo).consumeSecondFactor(context.Background(), totpUser(t, testSecret(t)), backup); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.claimCalls != 0 {
		t.Fatalf("a backup code must not claim a TOTP step, got %d calls", repo.claimCalls)
	}
}

// A wrong code must never reach the claim — otherwise guessing would burn steps
// and lock the real user out.
func TestConsumeSecondFactor_WrongCodeNeverClaims(t *testing.T) {
	repo := &totpConsumeRepo{claimOK: true}

	ok, err := totpUC(repo).consumeSecondFactor(context.Background(), totpUser(t, testSecret(t)), "000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("a wrong code was accepted")
	}
	if repo.claimCalls != 0 {
		t.Fatalf("a wrong code must not claim a step, got %d calls", repo.claimCalls)
	}
}
