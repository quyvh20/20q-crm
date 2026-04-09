package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type companyUseCase struct {
	repo domain.CompanyRepository
}

func NewCompanyUseCase(repo domain.CompanyRepository) domain.CompanyUseCase {
	return &companyUseCase{repo: repo}
}

func (uc *companyUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.CompanyFilter) ([]domain.Company, string, error) {
	return uc.repo.List(ctx, orgID, f)
}

func (uc *companyUseCase) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Company, error) {
	company, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if company == nil {
		return nil, domain.NewAppError(404, "company not found")
	}
	return company, nil
}

func (uc *companyUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateCompanyInput) (*domain.Company, error) {
	c := &domain.Company{
		OrgID:        orgID,
		Name:         input.Name,
		Industry:     input.Industry,
		Website:      input.Website,
		CustomFields: input.CustomFields,
	}
	if err := uc.repo.Create(ctx, c); err != nil {
		return nil, domain.ErrInternal
	}
	return c, nil
}

func (uc *companyUseCase) Update(ctx context.Context, orgID, id uuid.UUID, input domain.UpdateCompanyInput) (*domain.Company, error) {
	company, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if company == nil {
		return nil, domain.NewAppError(404, "company not found")
	}

	if input.Name != nil {
		company.Name = *input.Name
	}
	if input.Industry != nil {
		company.Industry = input.Industry
	}
	if input.Website != nil {
		company.Website = input.Website
	}
	if input.CustomFields != nil {
		company.CustomFields = *input.CustomFields
	}

	if err := uc.repo.Update(ctx, company); err != nil {
		return nil, domain.ErrInternal
	}
	return company, nil
}

func (uc *companyUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	if err := uc.repo.SoftDelete(ctx, orgID, id); err != nil {
		return domain.NewAppError(404, "company not found")
	}
	return nil
}

func (uc *companyUseCase) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	return uc.repo.Count(ctx, orgID)
}
