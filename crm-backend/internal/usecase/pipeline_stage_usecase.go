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

func (uc *pipelineStageUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	stage, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if stage == nil {
		return domain.ErrStageNotFound
	}
	return uc.repo.Delete(ctx, orgID, id)
}

var defaultStages = []struct {
	Name  string
	Color string
	IsWon bool
	Pos   int
}{
	{"Lead In", "#6366F1", false, 0},
	{"Qualified", "#3B82F6", false, 1},
	{"Proposal", "#F59E0B", false, 2},
	{"Negotiation", "#EF4444", false, 3},
	{"Closed Won", "#10B981", true, 4},
}

func (uc *pipelineStageUseCase) SeedDefaults(ctx context.Context, orgID uuid.UUID) ([]domain.PipelineStage, error) {
	count, err := uc.repo.CountByOrg(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if count > 0 {
		// Already seeded; just return existing stages
		return uc.repo.List(ctx, orgID)
	}
	var created []domain.PipelineStage
	for _, s := range defaultStages {
		stage := &domain.PipelineStage{
			OrgID:    orgID,
			Name:     s.Name,
			Color:    s.Color,
			Position: s.Pos,
			IsWon:    s.IsWon,
		}
		if err := uc.repo.Create(ctx, stage); err != nil {
			return nil, domain.ErrInternal
		}
		created = append(created, *stage)
	}
	return created, nil
}
