package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type pipelineStageUseCase struct {
	repo      domain.PipelineStageRepository
	pipelines domain.PipelineUseCase
}

func NewPipelineStageUseCase(repo domain.PipelineStageRepository, pipelines domain.PipelineUseCase) domain.PipelineStageUseCase {
	return &pipelineStageUseCase{repo: repo, pipelines: pipelines}
}

func (uc *pipelineStageUseCase) List(ctx context.Context, orgID uuid.UUID, pipelineID *uuid.UUID) ([]domain.PipelineStage, error) {
	return uc.repo.List(ctx, orgID, pipelineID)
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
	// A stage must land on a board. An omitted pipeline_id means the org
	// default rather than NULL: a NULL-pipeline stage is invisible to every
	// scoped board query, so it would look like the create silently failed.
	pipelineID := input.PipelineID
	if pipelineID == nil {
		p, err := uc.pipelines.ResolveDefault(ctx, orgID)
		if err != nil {
			return nil, err
		}
		pipelineID = &p.ID
	} else {
		// Confirm the caller's pipeline is theirs — otherwise a stage could be
		// planted on another org's board.
		p, err := uc.pipelines.List(ctx, orgID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		found := false
		for i := range p {
			if p[i].ID == *pipelineID {
				found = true
				break
			}
		}
		if !found {
			return nil, domain.ErrPipelineNotFound
		}
	}
	s := &domain.PipelineStage{
		OrgID:      orgID,
		PipelineID: pipelineID,
		Name:       input.Name,
		Position:   input.Position,
		Color:      input.Color,
		IsWon:      input.IsWon,
		IsLost:     input.IsLost,
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

// defaultStages is THE default ladder — the single copy.
//
// There used to be two divergent ones (this list and authUseCase's
// defaultPipelineStages), and they had already drifted: this one carried no
// IsLost field at all, so the registration path could produce a lost stage and
// the seed-defaults button could not. Two more copies exist as name-only lists
// that must stay byte-identical — systemTemplateUseCase's seededStageNames and
// the frontend's SEEDED_STAGE_NAMES — because "is this pipeline untouched?" is
// answered by comparing against them.
var defaultStages = []struct {
	Name   string
	Color  string
	IsWon  bool
	IsLost bool
	Pos    int
}{
	{"Lead In", "#6366F1", false, false, 0},
	{"Qualified", "#3B82F6", false, false, 1},
	{"Proposal", "#F59E0B", false, false, 2},
	{"Negotiation", "#EF4444", false, false, 3},
	{"Closed Won", "#10B981", true, false, 4},
}

// SeedDefaults fills ONE pipeline with the default ladder.
//
// The emptiness check is per-PIPELINE, not per-org, and that is the whole point
// of the pipelineID argument: gating on an org-wide count means a newly created
// second pipeline can never be seeded — the org already "has stages", so this
// would silently hand back the FIRST pipeline's ladder and the new board would
// render empty forever.
func (uc *pipelineStageUseCase) SeedDefaults(ctx context.Context, orgID uuid.UUID, pipelineID *uuid.UUID) ([]domain.PipelineStage, error) {
	if pipelineID == nil {
		p, err := uc.pipelines.ResolveDefault(ctx, orgID)
		if err != nil {
			return nil, err
		}
		pipelineID = &p.ID
	}
	count, err := uc.repo.CountByOrg(ctx, orgID, pipelineID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if count > 0 {
		// Already seeded; hand back what this pipeline has.
		return uc.repo.List(ctx, orgID, pipelineID)
	}
	var created []domain.PipelineStage
	for _, s := range defaultStages {
		stage := &domain.PipelineStage{
			OrgID:      orgID,
			PipelineID: pipelineID,
			Name:       s.Name,
			Color:      s.Color,
			Position:   s.Pos,
			IsWon:      s.IsWon,
			IsLost:     s.IsLost,
		}
		if err := uc.repo.Create(ctx, stage); err != nil {
			return nil, domain.ErrInternal
		}
		created = append(created, *stage)
	}
	return created, nil
}
