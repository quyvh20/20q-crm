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
		Where("user_id = ? AND status = 'active'", userID).
		Find(&orgUsers).Error
	return orgUsers, err
}

func (r *authRepository) ListMembersByOrgID(ctx context.Context, orgID uuid.UUID) ([]domain.OrgUser, error) {
	var orgUsers []domain.OrgUser
	err := r.db.WithContext(ctx).
		Preload("User").
		Where("org_id = ?", orgID).
		Order("joined_at ASC").
		Find(&orgUsers).Error
	return orgUsers, err
}

func (r *authRepository) UpdateOrgUserRole(ctx context.Context, userID, orgID uuid.UUID, role string) error {
	return r.db.WithContext(ctx).
		Model(&domain.OrgUser{}).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Update("role", role).Error
}

func (r *authRepository) DeleteOrgUser(ctx context.Context, userID, orgID uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND org_id = ?", userID, orgID).
		Delete(&domain.OrgUser{}).Error
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
