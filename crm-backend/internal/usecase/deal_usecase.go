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
}

func NewDealUseCase(dealRepo domain.DealRepository, stageRepo domain.PipelineStageRepository, activityRepo domain.ActivityRepository) domain.DealUseCase {
	return &dealUseCase{dealRepo: dealRepo, stageRepo: stageRepo, activityRepo: activityRepo}
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

	if input.ExpectedCloseAt != nil {
		t, err := time.Parse(time.RFC3339, *input.ExpectedCloseAt)
		if err == nil {
			d.ExpectedCloseAt = &t
		}
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
		deal.StageID = input.StageID
	}
	if input.Value != nil {
		deal.Value = *input.Value
	}
	if input.Probability != nil {
		deal.Probability = *input.Probability
	}
	if input.OwnerUserID != nil {
		deal.OwnerUserID = input.OwnerUserID
	}
	if input.ExpectedCloseAt != nil {
		t, err := time.Parse(time.RFC3339, *input.ExpectedCloseAt)
		if err == nil {
			deal.ExpectedCloseAt = &t
		}
	}

	if err := uc.dealRepo.Update(ctx, deal); err != nil {
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

func (uc *dealUseCase) ChangeStage(ctx context.Context, orgID, dealID uuid.UUID, input domain.UpdateDealStageInput) (*domain.Deal, error) {
	deal, err := uc.dealRepo.GetByID(ctx, orgID, dealID)
	if err != nil || deal == nil {
		return nil, domain.ErrDealNotFound
	}

	stage, err := uc.stageRepo.GetByID(ctx, orgID, input.StageID)
	if err != nil || stage == nil {
		return nil, domain.ErrStageNotFound
	}

	oldStageID := deal.StageID
	deal.StageID = &stage.ID

	activityType := "stage_change"
	activityTitle := fmt.Sprintf("Stage changed to %s", stage.Name)

	if stage.IsWon {
		deal.IsWon = true
		now := time.Now()
		deal.ClosedAt = &now
		activityType = "won"
		activityTitle = "Deal won!"
	} else if stage.IsLost {
		deal.IsLost = true
		now := time.Now()
		deal.ClosedAt = &now
		if input.LostReason != nil {
			// Store lost reason (we don't have a field, so skip or add later)
		}
		activityType = "lost"
		activityTitle = "Deal lost"
	}

	if err := uc.dealRepo.Update(ctx, deal); err != nil {
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
