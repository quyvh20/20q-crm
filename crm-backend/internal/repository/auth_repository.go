package repository

import (
	"context"
	"errors"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type authRepository struct {
	db *gorm.DB
}

func NewAuthRepository(db *gorm.DB) domain.AuthRepository {
	return &authRepository{db: db}
}

func (r *authRepository) CreateOrganization(ctx context.Context, org *domain.Organization) error {
	return r.db.WithContext(ctx).Create(org).Error
}

func (r *authRepository) CreateUser(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Create(user).Error
}

func (r *authRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) GetUserByGoogleID(ctx context.Context, googleID string) (*domain.User, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("google_id = ?", googleID).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *authRepository) UpdateUser(ctx context.Context, user *domain.User) error {
	return r.db.WithContext(ctx).Save(user).Error
}

func (r *authRepository) CreateRefreshToken(ctx context.Context, token *domain.RefreshToken) error {
	return r.db.WithContext(ctx).Create(token).Error
}

func (r *authRepository) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ? AND revoked_at IS NULL AND expires_at > ?", tokenHash, time.Now()).
		First(&token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

// GetRefreshTokenByHashAny returns the row for a hash regardless of revoked/expiry
// state (nil when the hash was never issued). Refresh uses this to tell a genuinely
// unknown token from an already-rotated one — the latter is a reuse/theft signal.
func (r *authRepository) GetRefreshTokenByHashAny(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	var token domain.RefreshToken
	err := r.db.WithContext(ctx).
		Where("token_hash = ?", tokenHash).
		First(&token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &token, nil
}

func (r *authRepository) IncrementUserTokenVersion(ctx context.Context, userID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		UpdateColumn("token_version", gorm.Expr("token_version + 1")).Error
}

func (r *authRepository) GetUserTokenVersion(ctx context.Context, userID uuid.UUID) (int, error) {
	var tv int
	err := r.db.WithContext(ctx).
		Model(&domain.User{}).
		Where("id = ?", userID).
		Pluck("token_version", &tv).Error
	return tv, err
}

// RefreshTokenHasSuccessor reports whether any refresh token was rotated from the
// given one, i.e. this token was superseded in a refresh chain (theft signal when
// replayed) rather than deliberately revoked (logout/revoke-device). P4.
func (r *authRepository) RefreshTokenHasSuccessor(ctx context.Context, tokenID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("rotated_from = ?", tokenID).
		Count(&count).Error
	return count > 0, err
}

func (r *authRepository) RevokeRefreshToken(ctx context.Context, tokenID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("id = ?", tokenID).
		Update("revoked_at", now).Error
}

func (r *authRepository) RevokeAllUserRefreshTokens(ctx context.Context, userID uuid.UUID) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
}

func (r *authRepository) CreateOrgUser(ctx context.Context, ou *domain.OrgUser) error {
	return r.db.WithContext(ctx).Create(ou).Error
}

func (r *authRepository) GetOrgUser(ctx context.Context, userID, orgID uuid.UUID) (*domain.OrgUser, error) {
	var ou domain.OrgUser
	err := r.db.WithContext(ctx).
		Preload("Role").
		Where("user_id = ? AND org_id = ?", userID, orgID).
		First(&ou).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ou, nil
}

func (r *authRepository) ListOrgsByUserID(ctx context.Context, userID uuid.UUID) ([]domain.OrgUser, error) {
	var orgUsers []domain.OrgUser
	err := r.db.WithContext(ctx).
		Preload("Org").
		Preload("Role").
		Where("user_id = ? AND status = 'active'", userID).
		Find(&orgUsers).Error
	return orgUsers, err
}

func (r *authRepository) ListMembersByOrgID(ctx context.Context, orgID uuid.UUID) ([]domain.OrgUser, error) {
	var orgUsers []domain.OrgUser
	err := r.db.WithContext(ctx).
		Preload("User").
		Preload("Role").
		Where("org_id = ?", orgID).
		Order("joined_at ASC").
		Find(&orgUsers).Error
	return orgUsers, err
}

func (r *authRepository) UpdateOrgUserRole(ctx context.Context, userID, orgID, roleID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Update("role_id", roleID).Error
}

func (r *authRepository) UpdateOrgUserStatus(ctx context.Context, userID, orgID uuid.UUID, status string) error {
	return r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Update("status", status).Error
}

func (r *authRepository) DeleteOrgUser(ctx context.Context, userID, orgID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Delete(&domain.OrgUser{}).Error // GORM will soft-delete if DeletedAt exists
}

func (r *authRepository) GetOrgUserByEmail(ctx context.Context, email string, orgID uuid.UUID) (*domain.OrgUser, error) {
	var user domain.User
	err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.GetOrgUser(ctx, user.ID, orgID)
}

func (r *authRepository) CountOrgUsersByRole(ctx context.Context, orgID, roleID uuid.UUID, status string) (int64, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&domain.OrgUser{}).Where("org_id = ? AND role_id = ?", orgID, roleID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	err := query.Count(&count).Error
	return count, err
}

func (r *authRepository) GetRoleByName(ctx context.Context, name string, orgID *uuid.UUID) (*domain.Role, error) {
	var role domain.Role
	query := r.db.WithContext(ctx).Where("name = ?", name)
	if orgID == nil {
		query = query.Where("org_id IS NULL AND is_system = true")
	} else {
		query = query.Where("org_id = ? OR (org_id IS NULL AND is_system = true)", orgID)
	}
	err := query.Preload("Permissions").First(&role).Error
	return &role, err
}

func (r *authRepository) GetRoleByID(ctx context.Context, id uuid.UUID) (*domain.Role, error) {
	var role domain.Role
	err := r.db.WithContext(ctx).Preload("Permissions").Where("id = ?", id).First(&role).Error
	return &role, err
}

func (r *authRepository) CreateOrgInvitation(ctx context.Context, inv *domain.OrgInvitation) error {
	return r.db.WithContext(ctx).Create(inv).Error
}

func (r *authRepository) GetOrgInvitationByTokenHash(ctx context.Context, tokenHash string) (*domain.OrgInvitation, error) {
	var inv domain.OrgInvitation
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&inv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &inv, err
}

func (r *authRepository) UpdateOrgInvitation(ctx context.Context, inv *domain.OrgInvitation) error {
	return r.db.WithContext(ctx).Save(inv).Error
}

// --- Account recovery (P1) ---

func (r *authRepository) CreatePasswordResetToken(ctx context.Context, t *domain.PasswordResetToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *authRepository) GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (*domain.PasswordResetToken, error) {
	var t domain.PasswordResetToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) MarkPasswordResetTokenUsed(ctx context.Context, id uuid.UUID) (int64, error) {
	// Conditional on used_at IS NULL so exactly one caller can claim the token
	// even under concurrent requests — the atomic single-use gate.
	res := r.db.WithContext(ctx).
		Model(&domain.PasswordResetToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	return res.RowsAffected, res.Error
}

func (r *authRepository) CreateEmailVerificationToken(ctx context.Context, t *domain.EmailVerificationToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *authRepository) GetEmailVerificationTokenByHash(ctx context.Context, tokenHash string) (*domain.EmailVerificationToken, error) {
	var t domain.EmailVerificationToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) MarkEmailVerificationTokenUsed(ctx context.Context, id uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.EmailVerificationToken{}).
		Where("id = ? AND used_at IS NULL", id).
		Update("used_at", time.Now())
	return res.RowsAffected, res.Error
}

func (r *authRepository) GetLatestEmailVerificationToken(ctx context.Context, userID uuid.UUID) (*domain.EmailVerificationToken, error) {
	var t domain.EmailVerificationToken
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *authRepository) WriteAuthEvent(ctx context.Context, e *domain.AuthEvent) error {
	return r.db.WithContext(ctx).Create(e).Error
}

// --- Admin audit query + session management (P4) ---

// ListAuthEvents returns a filtered, paginated page of the org's audit log
// (newest first) with each actor's name/email resolved via a LEFT JOIN, plus the
// total matching count. Filter columns are table-qualified because the join adds a
// `users` table that also has a `created_at` column.
func (r *authRepository) ListAuthEvents(ctx context.Context, orgID uuid.UUID, f domain.AuthEventFilter) ([]domain.AuthEventView, int64, error) {
	apply := func(q *gorm.DB) *gorm.DB {
		q = q.Where("auth_events.org_id = ?", orgID)
		if f.Category != "" {
			q = q.Where("auth_events.category = ?", f.Category)
		}
		if f.EventType != "" {
			q = q.Where("auth_events.event_type = ?", f.EventType)
		}
		if f.ActorID != nil {
			q = q.Where("auth_events.actor_id = ?", *f.ActorID)
		}
		if f.From != nil {
			q = q.Where("auth_events.created_at >= ?", *f.From)
		}
		if f.To != nil {
			q = q.Where("auth_events.created_at <= ?", *f.To)
		}
		return q
	}

	var total int64
	if err := apply(r.db.WithContext(ctx).Model(&domain.AuthEvent{})).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	var rows []domain.AuthEventView
	q := apply(r.db.WithContext(ctx).Table("auth_events")).
		Select("auth_events.*, u.full_name AS actor_name, u.email AS actor_email").
		Joins("LEFT JOIN users u ON u.id = auth_events.actor_id").
		Order("auth_events.created_at DESC").
		Limit(limit).Offset(offset)
	if err := q.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *authRepository) ListActiveRefreshTokens(ctx context.Context, userID uuid.UUID) ([]domain.RefreshToken, error) {
	var tokens []domain.RefreshToken
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, time.Now()).
		Order("last_used_at DESC NULLS LAST, created_at DESC").
		Find(&tokens).Error
	return tokens, err
}

// RevokeRefreshTokenForUser revokes one refresh token scoped to its owner and
// returns rows affected, so a caller can never revoke another user's session and
// can distinguish "revoked" (1) from "not found / already revoked" (0).
func (r *authRepository) RevokeRefreshTokenForUser(ctx context.Context, id, userID uuid.UUID) (int64, error) {
	now := time.Now()
	res := r.db.WithContext(ctx).
		Model(&domain.RefreshToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Update("revoked_at", now)
	return res.RowsAffected, res.Error
}
