package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type tagUseCase struct {
	repo domain.TagRepository
}

func NewTagUseCase(repo domain.TagRepository) domain.TagUseCase {
	return &tagUseCase{repo: repo}
}

func (uc *tagUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.Tag, error) {
	return uc.repo.List(ctx, orgID)
}

func (uc *tagUseCase) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Tag, error) {
	tag, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if tag == nil {
		return nil, domain.NewAppError(404, "tag not found")
	}
	return tag, nil
}

func (uc *tagUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateTagInput) (*domain.Tag, error) {
	tag := &domain.Tag{
		OrgID: orgID,
		Name:  input.Name,
		Color: input.Color,
	}
	if err := uc.repo.Create(ctx, tag); err != nil {
		return nil, domain.ErrInternal
	}
	return tag, nil
}

func (uc *tagUseCase) Update(ctx context.Context, orgID, id uuid.UUID, input domain.UpdateTagInput) (*domain.Tag, error) {
	tag, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if tag == nil {
		return nil, domain.NewAppError(404, "tag not found")
	}

	if input.Name != nil {
		tag.Name = *input.Name
	}
	if input.Color != nil {
		tag.Color = *input.Color
	}

	if err := uc.repo.Update(ctx, tag); err != nil {
		return nil, domain.ErrInternal
	}
	return tag, nil
}

func (uc *tagUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	if err := uc.repo.Delete(ctx, orgID, id); err != nil {
		return domain.NewAppError(404, "tag not found")
	}
	return nil
}
