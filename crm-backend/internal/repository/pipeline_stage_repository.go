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

// List returns an org's stages, optionally scoped to one pipeline (R9.3).
//
// ORDER BY pipeline_id, position — not position alone. Position was never
// unique and is only dense WITHIN a ladder, so once several pipelines share the
// table an org-wide list ordered by position alone interleaves boards
// (pipeline A position 1, pipeline B position 1, A 2, …). Ordering by pipeline
// first keeps each board's ladder contiguous for callers that read all of them.
func (r *pipelineStageRepository) List(ctx context.Context, orgID uuid.UUID, pipelineID *uuid.UUID) ([]domain.PipelineStage, error) {
	q := r.db.WithContext(ctx).Where("org_id = ?", orgID)
	if pipelineID != nil {
		q = q.Where("pipeline_id = ?", *pipelineID)
	}
	var stages []domain.PipelineStage
	err := q.Order("pipeline_id ASC NULLS FIRST, position ASC").Find(&stages).Error
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

// Update writes the stage. Save() writes EVERY column, including pipeline_id —
// which is exactly why the usecase must load the stage first and mutate it,
// never hand this a partially-built struct: a zero PipelineID would blank the
// stage off its board and it would silently vanish from every scoped list.
func (r *pipelineStageRepository) Update(ctx context.Context, s *domain.PipelineStage) error {
	return r.db.WithContext(ctx).Save(s).Error
}

func (r *pipelineStageRepository) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&domain.PipelineStage{}).Error
}

func (r *pipelineStageRepository) CountByOrg(ctx context.Context, orgID uuid.UUID, pipelineID *uuid.UUID) (int64, error) {
	q := r.db.WithContext(ctx).Model(&domain.PipelineStage{}).Where("org_id = ?", orgID)
	if pipelineID != nil {
		q = q.Where("pipeline_id = ?", *pipelineID)
	}
	var count int64
	err := q.Count(&count).Error
	return count, err
}
