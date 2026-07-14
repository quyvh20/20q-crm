package repository

import (
	"context"
	"errors"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// two_factor_repository.go holds the 2FA persistence (U6.4). It lives on
// authRepository so a 2FA check is one dependency, not a new one threaded through
// every auth path.
//
// Every write here is column-scoped or a raw UPDATE with a guard in the WHERE
// clause. That is not style: the security properties depend on it. A struct-based
// Save would drop a nil (GORM's zero-value omission), so "disable 2FA" would
// silently leave the secret in place; and a read-then-write backup-code burn would
// let two concurrent requests spend the same code twice.

// SetTOTPSecret stores an UNCONFIRMED secret (enrollment is not complete until
// EnableTOTP stamps totp_enabled_at).
func (r *authRepository) SetTOTPSecret(ctx context.Context, userID uuid.UUID, encryptedSecret string) error {
	return r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		Update("totp_secret", encryptedSecret).Error
}

// EnableTOTP activates 2FA and installs the backup-code set, atomically. If the
// codes fail to write, 2FA does not come on — enrolling a user with no recovery
// path is how an admin ends up locked out of their own workspace.
func (r *authRepository) EnableTOTP(ctx context.Context, userID uuid.UUID, codeHashes []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&domain.TwoFactorBackupCode{}).Error; err != nil {
			return err
		}
		codes := make([]domain.TwoFactorBackupCode, 0, len(codeHashes))
		for _, h := range codeHashes {
			codes = append(codes, domain.TwoFactorBackupCode{UserID: userID, CodeHash: h})
		}
		if len(codes) > 0 {
			if err := tx.Create(&codes).Error; err != nil {
				return err
			}
		}
		return tx.Model(&domain.User{}).
			Where("id = ?", userID).
			Update("totp_enabled_at", time.Now()).Error
	})
}

// DisableTOTP clears the secret, the enrollment stamp and every backup code.
// Writing NULL through a map (not a struct) so GORM cannot omit it.
func (r *authRepository) DisableTOTP(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&domain.TwoFactorBackupCode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&domain.TwoFactorChallenge{}).Error; err != nil {
			return err
		}
		return tx.Model(&domain.User{}).
			Where("id = ?", userID).
			Updates(map[string]interface{}{
				"totp_secret":     nil,
				"totp_enabled_at": nil,
			}).Error
	})
}

// ReplaceBackupCodes swaps the whole set — regenerating invalidates any unused
// old codes, which is the point.
func (r *authRepository) ReplaceBackupCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ?", userID).Delete(&domain.TwoFactorBackupCode{}).Error; err != nil {
			return err
		}
		codes := make([]domain.TwoFactorBackupCode, 0, len(codeHashes))
		for _, h := range codeHashes {
			codes = append(codes, domain.TwoFactorBackupCode{UserID: userID, CodeHash: h})
		}
		if len(codes) == 0 {
			return nil
		}
		return tx.Create(&codes).Error
	})
}

// ListUnusedBackupCodes returns the live hashes. bcrypt hashes cannot be looked up
// by value, so verification compares against each one; there are at most ten, and
// the caller is attempt-limited.
func (r *authRepository) ListUnusedBackupCodes(ctx context.Context, userID uuid.UUID) ([]domain.TwoFactorBackupCode, error) {
	var out []domain.TwoFactorBackupCode
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND used_at IS NULL", userID).
		Find(&out).Error
	return out, err
}

// ConsumeBackupCode burns one code. The `used_at IS NULL` guard lives in the WHERE
// clause and the result is checked, so this is a compare-and-swap: two concurrent
// requests presenting the same code cannot both win.
func (r *authRepository) ConsumeBackupCode(ctx context.Context, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.TwoFactorBackupCode{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *authRepository) CountBackupCodesRemaining(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&domain.TwoFactorBackupCode{}).
		Where("user_id = ? AND used_at IS NULL", userID).
		Count(&n).Error
	return int(n), err
}

func (r *authRepository) CreateTwoFactorChallenge(ctx context.Context, ch *domain.TwoFactorChallenge) error {
	return r.db.WithContext(ctx).Create(ch).Error
}

func (r *authRepository) GetTwoFactorChallengeByHash(ctx context.Context, tokenHash string) (*domain.TwoFactorChallenge, error) {
	var ch domain.TwoFactorChallenge
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&ch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ch, nil
}

func (r *authRepository) IncrementChallengeAttempts(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.TwoFactorChallenge{}).
		Where("id = ?", id).
		UpdateColumn("attempts", gorm.Expr("attempts + 1")).Error
}

// ClaimChallengeAttempt atomically spends ONE attempt against a live challenge,
// returning false when there is nothing left to spend (used, expired, or the cap is
// reached). The guard is in the WHERE clause, so N concurrent verifies can never
// collectively exceed the cap — a read-then-check would let them all pass the same
// snapshot and turn a 5-try bound into 5-per-request.
func (r *authRepository) ClaimChallengeAttempt(ctx context.Context, id uuid.UUID, maxAttempts int) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.TwoFactorChallenge{}).
		Where("id = ? AND used_at IS NULL AND expires_at > NOW() AND attempts < ?", id, maxAttempts).
		UpdateColumn("attempts", gorm.Expr("attempts + 1"))
	return res.RowsAffected == 1, res.Error
}

// ConsumeTwoFactorChallenge burns the challenge (single use). It reports whether
// THIS call was the one that burned it: two concurrent verifies holding the same
// valid challenge must not both mint a session.
func (r *authRepository) ConsumeTwoFactorChallenge(ctx context.Context, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.TwoFactorChallenge{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	return res.RowsAffected == 1, res.Error
}

// DeleteExpiredTwoFactorChallenges is the sweeper (the half-authenticated rows are
// short-lived and worthless once expired).
func (r *authRepository) DeleteExpiredTwoFactorChallenges(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("expires_at < NOW() - INTERVAL '1 day'").
		Delete(&domain.TwoFactorChallenge{})
	return res.RowsAffected, res.Error
}

// RevokeAllUserAPITokens kills every live personal access token the user holds, in
// every workspace. Called on a password reset / change: a token is a long-lived
// credential minted from the account, so an account that has just been re-secured
// must not leave a set of them quietly working.
func (r *authRepository) RevokeAllUserAPITokens(ctx context.Context, userID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.APIToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now())
	return res.RowsAffected, res.Error
}
