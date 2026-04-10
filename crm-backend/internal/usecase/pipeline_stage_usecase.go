package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type pipelineStageUseCase struct {
	repo domain.PipelineStageRepository
}

func NewPipelineStageUseCase(repo domain.PipelineStageRepository) domain.PipelineStageUseCase {
	return &pipelineStageUseCase{repo: repo}
}

func (uc *pipelineStageUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.PipelineStage, error) {
	return uc.repo.List(ctx, orgID)
}

func (uc *pipelineStageUseCase) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.PipelineStage, error) {
	stage, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if stage == nil {
		return nil, domain.ErrStageNotFound
	}
	return stage, nil
}

func (uc *pipelineStageUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateStageInput) (*domain.PipelineStage, error) {
	s := &domain.PipelineStage{
		OrgID:    orgID,
		Name:     input.Name,
		Position: input.Position,
		Color:    input.Color,
		IsWon:    input.IsWon,
		IsLost:   input.IsLost,
	}
	if s.Color == "" {
		s.Color = "#3B82F6"
	}
	if err := uc.repo.Create(ctx, s); err != nil {
		return nil, domain.ErrInternal
	}
	return s, nil
}

func (uc *pipelineStageUseCase) Update(ctx context.Context, orgID, id uuid.UUID, input domain.UpdateStageInput) (*domain.PipelineStage, error) {
	stage, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if stage == nil {
		return nil, domain.ErrStageNotFound
	}

	if input.Name != nil {
		stage.Name = *input.Name
	}
	if input.Position != nil {
		stage.Position = *input.Position
	}
	if input.Color != nil {
		stage.Color = *input.Color
	}
	if input.IsWon != nil {
		stage.IsWon = *input.IsWon
	}
	if input.IsLost != nil {
		stage.IsLost = *input.IsLost
	}

	if err := uc.repo.Update(ctx, stage); err != nil {
		return nil, domain.ErrInternal
	}
	return stage, nil
}
