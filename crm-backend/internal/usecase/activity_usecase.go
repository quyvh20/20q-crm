package usecase

import (
	"context"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type activityUseCase struct {
	repo domain.ActivityRepository
}

func NewActivityUseCase(repo domain.ActivityRepository) domain.ActivityUseCase {
	return &activityUseCase{repo: repo}
}

func (uc *activityUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.ActivityFilter) ([]domain.Activity, error) {
	return uc.repo.List(ctx, orgID, f)
}

func (uc *activityUseCase) Create(ctx context.Context, orgID uuid.UUID, userID uuid.UUID, input domain.CreateActivityInput) (*domain.Activity, error) {
	a := &domain.Activity{
		OrgID:           orgID,
		Type:            input.Type,
		DealID:          input.DealID,
		ContactID:       input.ContactID,
		UserID:          &userID,
		Title:           &input.Title,
		Body:            input.Body,
		DurationMinutes: input.DurationMinutes,
		OccurredAt:      time.Now(),
	}

	if input.OccurredAt != nil {
		t, err := time.Parse(time.RFC3339, *input.OccurredAt)
		if err == nil {
			a.OccurredAt = t
		}
	}

	if err := uc.repo.Create(ctx, a); err != nil {
		return nil, domain.ErrInternal
	}
	return a, nil
}
