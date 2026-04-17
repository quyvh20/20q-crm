package usecase

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type workspaceUseCase struct {
	authRepo domain.AuthRepository
}

func NewWorkspaceUseCase(authRepo domain.AuthRepository) domain.WorkspaceUseCase {
	return &workspaceUseCase{authRepo: authRepo}
}

func (uc *workspaceUseCase) ListMembers(ctx context.Context, orgID uuid.UUID) ([]domain.MemberInfo, error) {
	orgUsers, err := uc.authRepo.ListMembersByOrgID(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}

	members := make([]domain.MemberInfo, 0, len(orgUsers))
	for _, ou := range orgUsers {
		m := domain.MemberInfo{
			UserID: ou.UserID,
			Role:   ou.Role,
			Status: ou.Status,
		}
		if ou.User != nil {
			m.Email = ou.User.Email
			m.FirstName = ou.User.FirstName
			m.LastName = ou.User.LastName
			m.FullName = ou.User.FullName
			m.AvatarURL = ou.User.AvatarURL
		}
		members = append(members, m)
	}
	return members, nil
}

func (uc *workspaceUseCase) InviteMember(ctx context.Context, orgID uuid.UUID, input domain.InviteMemberInput) (*domain.MemberInfo, error) {
	validRoles := map[string]bool{
		"admin": true, "manager": true, "sales": true, "viewer": true,
	}
	if !validRoles[input.Role] {
		return nil, domain.NewAppError(400, "invalid role")
	}

	existing, err := uc.authRepo.GetOrgUserByEmail(ctx, input.Email, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if existing != nil {
		return nil, domain.ErrAlreadyMember
	}

	user, err := uc.authRepo.GetUserByEmail(ctx, input.Email)
	if err != nil {
		return nil, domain.ErrInternal
	}

	if user == nil {
		user = &domain.User{
			OrgID:     orgID,
			Email:     input.Email,
			FirstName: input.Email,
			FullName:  input.Email,
			Role:      "viewer",
		}
		if err := uc.authRepo.CreateUser(ctx, user); err != nil {
			return nil, domain.ErrInternal
		}
	}

	ou := &domain.OrgUser{
		UserID: user.ID,
		OrgID:  orgID,
		Role:   input.Role,
		Status: "active",
	}
	if err := uc.authRepo.CreateOrgUser(ctx, ou); err != nil {
		return nil, domain.ErrInternal
	}

	return &domain.MemberInfo{
		UserID:    user.ID,
		Email:     user.Email,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		FullName:  user.FullName,
		AvatarURL: user.AvatarURL,
		Role:      input.Role,
		Status:    "active",
	}, nil
}

func (uc *workspaceUseCase) UpdateMemberRole(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID, input domain.UpdateMemberRoleInput) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil {
		return domain.ErrNotMember
	}
	if ou.Role == "super_admin" {
		return domain.ErrCannotRemoveSuperAdmin
	}

	validRoles := map[string]bool{
		"admin": true, "manager": true, "sales": true, "viewer": true,
	}
	if !validRoles[input.Role] {
		return domain.NewAppError(400, "invalid role")
	}

	return uc.authRepo.UpdateOrgUserRole(ctx, targetUserID, orgID, input.Role)
}

func (uc *workspaceUseCase) RemoveMember(ctx context.Context, orgID uuid.UUID, targetUserID uuid.UUID) error {
	ou, err := uc.authRepo.GetOrgUser(ctx, targetUserID, orgID)
	if err != nil {
		return domain.ErrInternal
	}
	if ou == nil {
		return domain.ErrNotMember
	}
	if ou.Role == "super_admin" {
		return domain.ErrCannotRemoveSuperAdmin
	}
	return uc.authRepo.DeleteOrgUser(ctx, targetUserID, orgID)
}
