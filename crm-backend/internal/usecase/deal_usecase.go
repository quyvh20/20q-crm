package usecase

import (
	"context"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type dealUseCase struct {
	dealRepo     domain.DealRepository
	stageRepo    domain.PipelineStageRepository
	activityRepo domain.ActivityRepository
	// pipelines resolves the org's default board for a deal created without a
	// stage (R9.3). A REQUIRED positional parameter rather than a setter: a nil
	// one would leave such deals with pipeline_id NULL, which renders them on no
	// board at all — a silent disappearance, so every construction site has to
	// say out loud what resolves it.
	pipelines domain.PipelineUseCase
}

func NewDealUseCase(dealRepo domain.DealRepository, stageRepo domain.PipelineStageRepository, activityRepo domain.ActivityRepository, pipelines domain.PipelineUseCase) domain.DealUseCase {
	return &dealUseCase{dealRepo: dealRepo, stageRepo: stageRepo, activityRepo: activityRepo, pipelines: pipelines}
}

// resolveStagePipeline is the ONE place that answers "may this deal move to
// this stage, and what board does that put it on" (R9.3).
//
// A cross-pipeline stage move is refused rather than treated as an implicit
// pipeline change: is_won / is_lost / closed_at are pure functions of the
// destination stage, and the "won stage is the last non-lost stage" invariant
// is per-ladder — so jumping ladders has no defined semantics. Moving a deal
// between boards is its own operation (pipeline + stage in one call).
//
// A deal whose pipeline_id is still NULL (created by an older build, or by a
// path the backfill has not reached) ADOPTS the stage's pipeline instead of
// being refused: refusing would make such a deal permanently unmovable.
func (uc *dealUseCase) resolveStagePipeline(deal *domain.Deal, stage *domain.PipelineStage) (*uuid.UUID, error) {
	if stage.PipelineID == nil {
		// Stage predates the backfill; nothing to check against, and adopting a
		// NULL leaves the deal where it was.
		return deal.PipelineID, nil
	}
	if deal.PipelineID != nil && *deal.PipelineID != *stage.PipelineID {
		return nil, domain.ErrStageWrongPipeline
	}
	return stage.PipelineID, nil
}

// defaultPipelineID resolves the org's default board, for a deal being created
// with no stage to derive one from.
func (uc *dealUseCase) defaultPipelineID(ctx context.Context, orgID uuid.UUID) *uuid.UUID {
	if uc.pipelines == nil {
		return nil
	}
	p, err := uc.pipelines.ResolveDefault(ctx, orgID)
	if err != nil || p == nil {
		return nil
	}
	return &p.ID
}

func (uc *dealUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.DealFilter) ([]domain.Deal, string, error) {
	return uc.dealRepo.List(ctx, orgID, f)
}

func (uc *dealUseCase) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Deal, error) {
	deal, err := uc.dealRepo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if deal == nil {
		return nil, domain.ErrDealNotFound
	}
	return deal, nil
}

func (uc *dealUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateDealInput) (*domain.Deal, error) {
	d := &domain.Deal{
		OrgID:       orgID,
		Title:       input.Title,
		ContactID:   input.ContactID,
		CompanyID:   input.CompanyID,
		StageID:     input.StageID,
		Value:       input.Value,
		Probability: input.Probability,
		OwnerUserID: input.OwnerUserID,
	}

	if input.CustomFields != nil {
		d.CustomFields = input.CustomFields
	}

	if input.ExpectedCloseAt != nil {
		t, err := time.Parse(time.RFC3339, *input.ExpectedCloseAt)
		if err == nil {
			d.ExpectedCloseAt = &t
		}
	}

	// R9.3: stamp the board. From the stage when there is one, else the org
	// default — never left NULL, because a deal with no pipeline_id renders on
	// no board and simply looks lost.
	if input.StageID != nil {
		stage, err := uc.stageRepo.GetByID(ctx, orgID, *input.StageID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if stage == nil {
			return nil, domain.ErrStageNotFound
		}
		d.PipelineID = stage.PipelineID
	}
	if d.PipelineID == nil {
		d.PipelineID = uc.defaultPipelineID(ctx, orgID)
	}

	if err := uc.dealRepo.Create(ctx, d); err != nil {
		return nil, domain.ErrInternal
	}

	// Re-fetch with preloads
	return uc.dealRepo.GetByID(ctx, orgID, d.ID)
}

func (uc *dealUseCase) Update(ctx context.Context, orgID, id uuid.UUID, input domain.UpdateDealInput) (*domain.Deal, error) {
	deal, err := uc.dealRepo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if deal == nil {
		return nil, domain.ErrDealNotFound
	}

	if input.Title != nil {
		deal.Title = *input.Title
	}
	if input.ContactID != nil {
		deal.ContactID = input.ContactID
	}
	if input.CompanyID != nil {
		deal.CompanyID = input.CompanyID
	}
	if input.StageID != nil {
		// R9.3: a stage set through the generic Update still has to be a real
		// stage on THIS deal's board. Before this the field was assigned blind —
		// only the FK checked that the id existed at all, so a stage from another
		// org's pipeline was accepted. (The won/lost side-effects deliberately
		// remain ChangeStage's job; this path has never derived them and making
		// it do so would double-fire the stage-change activity.)
		stage, err := uc.stageRepo.GetByID(ctx, orgID, *input.StageID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		if stage == nil {
			return nil, domain.ErrStageNotFound
		}
		pipelineID, err := uc.resolveStagePipeline(deal, stage)
		if err != nil {
			return nil, err
		}
		deal.StageID = input.StageID
		deal.PipelineID = pipelineID
	}
	if input.Value != nil {
		deal.Value = *input.Value
	}
	if input.Probability != nil {
		deal.Probability = *input.Probability
	}
	if input.ClearOwner {
		deal.OwnerUserID = nil
	} else if input.OwnerUserID != nil {
		deal.OwnerUserID = input.OwnerUserID
	}
	if input.ExpectedCloseAt != nil {
		t, err := time.Parse(time.RFC3339, *input.ExpectedCloseAt)
		if err == nil {
			deal.ExpectedCloseAt = &t
		}
	}
	if input.CustomFields != nil {
		// Merge over the stored custom_fields so a partial uniform edit (the edit form
		// PATCHes only changed keys) doesn't blank the custom fields it didn't touch.
		merged, err := mergeJSONBlob(deal.CustomFields, *input.CustomFields)
		if err != nil {
			return nil, domain.NewAppError(400, "invalid custom field data")
		}
		deal.CustomFields = merged
	}

	if err := uc.dealRepo.Update(ctx, deal); err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			return nil, appErr // e.g. view-only share → 403, not a masked 500
		}
		return nil, domain.ErrInternal
	}
	return uc.dealRepo.GetByID(ctx, orgID, deal.ID)
}

func (uc *dealUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	if err := uc.dealRepo.SoftDelete(ctx, orgID, id); err != nil {
		return domain.ErrDealNotFound
	}
	return nil
}

// ============================================================
// ChangeStage — core business logic with auto-activity creation
// ============================================================

// KEEP IN SYNC: the stage side-effects below (is_won/is_lost/closed_at + the
// "stage_change" activity) are mirrored by the workflow automation path,
// automation.UpdateRecordExecutor.handleDealStageChange, which applies them via
// raw SQL inside its own transaction (it cannot reuse this method without breaking
// that transaction boundary — see the note there). If you change the side-effects
// here, update handleDealStageChange too.
//
// is_won/is_lost/closed_at are a pure function of the destination stage: a won/lost
// stage sets them, and ANY open stage clears them — so reopening a closed deal does
// not leave stale flags (which would hide it from is_won=false/is_lost=false reports
// like Forecast).
func (uc *dealUseCase) ChangeStage(ctx context.Context, orgID, dealID uuid.UUID, input domain.UpdateDealStageInput) (*domain.Deal, error) {
	deal, err := uc.dealRepo.GetByID(ctx, orgID, dealID)
	if err != nil || deal == nil {
		return nil, domain.ErrDealNotFound
	}

	stage, err := uc.stageRepo.GetByID(ctx, orgID, input.StageID)
	if err != nil || stage == nil {
		return nil, domain.ErrStageNotFound
	}

	// R9.3: refuse a move onto another board's ladder. Mirrored EXACTLY by
	// automation.handleDealStageChange — the two are contractual twins, so a
	// check added here without the twin leaves the automation path open.
	pipelineID, err := uc.resolveStagePipeline(deal, stage)
	if err != nil {
		return nil, err
	}

	oldStageID := deal.StageID
	deal.StageID = &stage.ID
	deal.PipelineID = pipelineID

	activityType := "stage_change"
	activityTitle := fmt.Sprintf("Stage changed to %s", stage.Name)

	if stage.IsWon {
		deal.IsWon = true
		deal.IsLost = false
		now := time.Now()
		deal.ClosedAt = &now
		activityTitle = "Deal won! 🏆"
	} else if stage.IsLost {
		deal.IsWon = false
		deal.IsLost = true
		now := time.Now()
		deal.ClosedAt = &now
		if input.LostReason != nil {
			activityTitle = fmt.Sprintf("Deal lost: %s", *input.LostReason)
		} else {
			activityTitle = "Deal lost"
		}
	} else {
		// Open stage: clear any prior terminal state so the flags always reflect the
		// current stage (reopening a previously won/lost deal).
		deal.IsWon = false
		deal.IsLost = false
		deal.ClosedAt = nil
	}

	if err := uc.dealRepo.Update(ctx, deal); err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			return nil, appErr // e.g. view-only share → 403, not a masked 500
		}
		return nil, domain.ErrInternal
	}

	// Auto-create activity
	_ = oldStageID // used to track change
	activity := &domain.Activity{
		OrgID:      orgID,
		Type:       activityType,
		DealID:     &dealID,
		Title:      &activityTitle,
		OccurredAt: time.Now(),
	}
	_ = uc.activityRepo.Create(ctx, activity)

	return uc.dealRepo.GetByID(ctx, orgID, dealID)
}

func (uc *dealUseCase) Forecast(ctx context.Context, orgID uuid.UUID) ([]domain.ForecastRow, error) {
	return uc.dealRepo.Forecast(ctx, orgID)
}

func (uc *dealUseCase) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	return uc.dealRepo.Count(ctx, orgID)
}
