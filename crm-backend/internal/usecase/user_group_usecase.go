package usecase

import (
	"context"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// userGroupUseCase is the admin surface for user groups. Mutations are gated at
// the route by groups.manage; listing is open to any member (the report share
// picker needs it). Every method is org-scoped.
type userGroupUseCase struct {
	repo domain.UserGroupRepository
}

func NewUserGroupUseCase(repo domain.UserGroupRepository) domain.UserGroupUseCase {
	return &userGroupUseCase{repo: repo}
}

func (uc *userGroupUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.UserGroupView, error) {
	return uc.repo.List(ctx, orgID)
}

func (uc *userGroupUseCase) Create(ctx context.Context, orgID, actorID uuid.UUID, in domain.UserGroupInput) (*domain.UserGroup, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, domain.NewAppError(400, "group name is required")
	}
	g := &domain.UserGroup{OrgID: orgID, Name: name, Description: in.Description, CreatedBy: &actorID}
	if err := uc.repo.Create(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (uc *userGroupUseCase) Update(ctx context.Context, orgID, id uuid.UUID, in domain.UserGroupInput) (*domain.UserGroup, error) {
	g, err := uc.repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, domain.NewAppError(404, "group not found")
	}
	g.Name = strings.TrimSpace(in.Name)
	g.Description = in.Description
	if err := uc.repo.Update(ctx, g); err != nil {
		return nil, err
	}
	return g, nil
}

func (uc *userGroupUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.repo.SoftDelete(ctx, orgID, id)
}

func (uc *userGroupUseCase) AddMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	if err := uc.assertGroup(ctx, orgID, groupID); err != nil {
		return err
	}
	return uc.repo.AddMember(ctx, orgID, groupID, userID)
}

func (uc *userGroupUseCase) RemoveMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	if err := uc.assertGroup(ctx, orgID, groupID); err != nil {
		return err
	}
	return uc.repo.RemoveMember(ctx, orgID, groupID, userID)
}

func (uc *userGroupUseCase) assertGroup(ctx context.Context, orgID, groupID uuid.UUID) error {
	g, err := uc.repo.GetByID(ctx, orgID, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return domain.NewAppError(404, "group not found")
	}
	return nil
}
