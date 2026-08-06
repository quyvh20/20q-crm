package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// pipelineUseCase owns pipeline CRUD and the ONE definition of "which board do
// I use when nobody said" (R9.3).
//
// It deliberately does NOT depend on the stage usecase — the dependency runs
// the other way (pipelineStageUseCase holds a PipelineUseCase to resolve the
// default). Seeding a new pipeline's stages therefore goes through the stage
// REPOSITORY directly, which is also what keeps the two constructors free of a
// cycle.
type pipelineUseCase struct {
	repo   domain.PipelineRepository
	stages domain.PipelineStageRepository
}

func NewPipelineUseCase(repo domain.PipelineRepository, stages domain.PipelineStageRepository) domain.PipelineUseCase {
	return &pipelineUseCase{repo: repo, stages: stages}
}

func (uc *pipelineUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.Pipeline, error) {
	// EnsureDefault first so an org that predates R9.3 (or that the boot
	// backfill missed) always has at least one board to show, rather than an
	// empty switcher the user cannot recover from through the UI.
	if _, err := uc.repo.EnsureDefault(ctx, orgID); err != nil {
		return nil, domain.ErrInternal
	}
	out, err := uc.repo.List(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	return out, nil
}

func (uc *pipelineUseCase) ResolveDefault(ctx context.Context, orgID uuid.UUID) (*domain.Pipeline, error) {
	p, err := uc.repo.EnsureDefault(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if p == nil {
		return nil, domain.ErrPipelineNotFound
	}
	return p, nil
}

func (uc *pipelineUseCase) Create(ctx context.Context, orgID uuid.UUID, in domain.CreatePipelineInput) (*domain.Pipeline, error) {
	// Guarantee the org has a default BEFORE adding a second board — otherwise
	// the first pipeline an org ever creates through the UI is a non-default
	// one and nothing is the fallback.
	if _, err := uc.repo.EnsureDefault(ctx, orgID); err != nil {
		return nil, domain.ErrInternal
	}

	pos := 0
	if in.Position != nil {
		pos = *in.Position
	} else {
		existing, err := uc.repo.List(ctx, orgID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		for _, e := range existing {
			if e.Position >= pos {
				pos = e.Position + 1
			}
		}
	}

	p := &domain.Pipeline{ID: uuid.New(), OrgID: orgID, Name: in.Name, Position: pos}
	if err := uc.repo.Create(ctx, p); err != nil {
		return nil, domain.ErrInternal
	}

	if in.SeedDefaultStages {
		// Straight to the repository, not through pipelineStageUseCase.SeedDefaults
		// — that would be a constructor cycle. The ladder is the same list.
		for _, s := range defaultStages {
			stage := &domain.PipelineStage{
				OrgID:      orgID,
				PipelineID: &p.ID,
				Name:       s.Name,
				Color:      s.Color,
				Position:   s.Pos,
				IsWon:      s.IsWon,
				IsLost:     s.IsLost,
			}
			if err := uc.stages.Create(ctx, stage); err != nil {
				return nil, domain.ErrInternal
			}
		}
	}
	return p, nil
}

func (uc *pipelineUseCase) Update(ctx context.Context, orgID, id uuid.UUID, in domain.UpdatePipelineInput) (*domain.Pipeline, error) {
	p, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if p == nil {
		return nil, domain.ErrPipelineNotFound
	}

	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.Position != nil {
		p.Position = *in.Position
	}
	// is_default is promoted through SetDefault (one transaction, demote then
	// promote) rather than written on this struct: Save() here would insert a
	// second default and trip uq_pipelines_org_default. Demotion to nothing is
	// not offered — an org must always have exactly one default, so the only
	// way to demote a pipeline is to promote another.
	promote := in.IsDefault != nil && *in.IsDefault && !p.IsDefault
	if err := uc.repo.Update(ctx, p); err != nil {
		return nil, domain.ErrInternal
	}
	if promote {
		if err := uc.repo.SetDefault(ctx, orgID, id); err != nil {
			return nil, err
		}
		p.IsDefault = true
	}
	return p, nil
}

func (uc *pipelineUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	p, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if p == nil {
		return domain.ErrPipelineNotFound
	}
	if p.IsDefault {
		return domain.ErrPipelineIsDefault
	}
	// Refuse rather than cascade. Stage deletion already demonstrates what
	// cascading would cost: it is a soft delete, so deals.stage_id's
	// ON DELETE SET NULL never fires and deals orphan at a tombstoned id. Doing
	// that at pipeline scale strands a whole board's worth of deals somewhere
	// no view lists them. Moving the deals first is an explicit user action.
	n, err := uc.repo.CountOpenDeals(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if n > 0 {
		return domain.ErrPipelineHasDeals
	}
	return uc.repo.SoftDelete(ctx, orgID, id)
}
