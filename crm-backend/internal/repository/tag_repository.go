package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type tagRepository struct {
	db *gorm.DB
}

func NewTagRepository(db *gorm.DB) domain.TagRepository {
	return &tagRepository{db: db}
}

func (r *tagRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.Tag, error) {
	var tags []domain.Tag
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("name ASC").
		Find(&tags).Error
	return tags, err
}

func (r *tagRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Tag, error) {
	var tag domain.Tag
	err := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		First(&tag).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &tag, err
}

func (r *tagRepository) Create(ctx context.Context, t *domain.Tag) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *tagRepository) Update(ctx context.Context, t *domain.Tag) error {
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *tagRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&domain.Tag{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
