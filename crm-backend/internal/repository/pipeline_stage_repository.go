package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type pipelineStageRepository struct {
	db *gorm.DB
}

func NewPipelineStageRepository(db *gorm.DB) domain.PipelineStageRepository {
	return &pipelineStageRepository{db: db}
}

func (r *pipelineStageRepository) List(ctx context.Context, orgID uuid.UUID) ([]domain.PipelineStage, error) {
	var stages []domain.PipelineStage
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("position ASC").
		Find(&stages).Error
	return stages, err
}

func (r *pipelineStageRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.PipelineStage, error) {
	var stage domain.PipelineStage
	err := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		First(&stage).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &stage, err
}

func (r *pipelineStageRepository) Create(ctx context.Context, s *domain.PipelineStage) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *pipelineStageRepository) Update(ctx context.Context, s *domain.PipelineStage) error {
	return r.db.WithContext(ctx).Save(s).Error
}

func (r *pipelineStageRepository) CountByOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&domain.PipelineStage{}).
		Where("org_id = ?", orgID).
		Count(&count).Error
	return count, err
}
