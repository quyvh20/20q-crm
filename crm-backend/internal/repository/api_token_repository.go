package repository

import (
	"context"
	"errors"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type apiTokenRepository struct {
	db *gorm.DB
}

func NewAPITokenRepository(db *gorm.DB) domain.APITokenRepository {
	return &apiTokenRepository{db: db}
}

func (r *apiTokenRepository) Create(ctx context.Context, t *domain.APIToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *apiTokenRepository) ListByUser(ctx context.Context, orgID, userID uuid.UUID) ([]domain.APIToken, error) {
	var out []domain.APIToken
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND user_id = ? AND revoked_at IS NULL", orgID, userID).
		Order("created_at DESC").
		Find(&out).Error
	if out == nil {
		out = []domain.APIToken{}
	}
	return out, err
}

func (r *apiTokenRepository) CountLiveByUser(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&domain.APIToken{}).
		Where("org_id = ? AND user_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > NOW())", orgID, userID).
		Count(&n).Error
	return n, err
}

// GetByHash is the authentication probe. It runs on EVERY request made with a
// personal access token, so it is a single indexed equality lookup — and it is
// deliberately NOT cached: revocation has to be instant, and a cache here would
// mean owning its eviction on every revoke, expiry and offboard. One indexed read
// is cheaper than that bug.
func (r *apiTokenRepository) GetByHash(ctx context.Context, tokenHash string) (*domain.APIToken, error) {
	var t domain.APIToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *apiTokenRepository) Revoke(ctx context.Context, orgID, userID, id uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.APIToken{}).
		Where("id = ? AND org_id = ? AND user_id = ? AND revoked_at IS NULL", id, orgID, userID).
		Update("revoked_at", time.Now())
	return res.RowsAffected, res.Error
}

func (r *apiTokenRepository) RevokeAllForUser(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.APIToken{}).
		Where("org_id = ? AND user_id = ? AND revoked_at IS NULL", orgID, userID).
		Update("revoked_at", time.Now())
	return res.RowsAffected, res.Error
}

// TouchLastUsed records activity at most once a minute per token. The throttle is
// in the WHERE clause, so a token hammered by a script writes one row per minute
// instead of one per request.
func (r *apiTokenRepository) TouchLastUsed(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.APIToken{}).
		Where("id = ? AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')", id).
		UpdateColumn("last_used_at", time.Now()).Error
}

// RevokeAllForUserAllOrgs kills every live token the user holds, in EVERY
// workspace. Used on a password reset / sign-out-everywhere: a compromised account
// must not leave behind a set of long-lived credentials that quietly survive the
// very act of locking the attacker out. Org-agnostic on purpose — a reset does not
// know (or care) which workspaces the tokens belong to.
func (r *apiTokenRepository) RevokeAllForUserAllOrgs(ctx context.Context, userID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.APIToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", time.Now())
	return res.RowsAffected, res.Error
}
