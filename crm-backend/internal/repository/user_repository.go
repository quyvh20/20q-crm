package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) domain.UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) ListByOrgID(ctx context.Context, orgID uuid.UUID) ([]domain.User, error) {
	var users []domain.User
	err := r.db.WithContext(ctx).
		Where("id IN (SELECT user_id FROM org_users WHERE org_id = ? AND status = 'active')", orgID).
		Order("first_name ASC, last_name ASC").
		Find(&users).Error
	return users, err
}
