package usecase

import (
	"context"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// reportShareUseCase manages a report's share list. It reuses the report
// usecase's ResolveAccess both to authorize (must see the report to list; must
// 'manage' it to add/remove) and to keep the visibility rule in one place.
type reportShareUseCase struct {
	reports domain.ReportUseCase
	shares  domain.ReportShareRepository
}

func NewReportShareUseCase(reports domain.ReportUseCase, shares domain.ReportShareRepository) domain.ReportShareUseCase {
	return &reportShareUseCase{reports: reports, shares: shares}
}

func (uc *reportShareUseCase) List(ctx context.Context, orgID, userID, reportID uuid.UUID) ([]domain.ReportShareView, error) {
	if _, _, err := uc.reports.ResolveAccess(ctx, orgID, userID, reportID); err != nil {
		return nil, err
	}
	return uc.shares.ListByReport(ctx, orgID, reportID)
}

func (uc *reportShareUseCase) Add(ctx context.Context, orgID, userID, reportID uuid.UUID, in domain.AddReportShareInput) error {
	if err := uc.requireManage(ctx, orgID, userID, reportID); err != nil {
		return err
	}
	switch in.TargetType {
	case domain.ShareTargetUser, domain.ShareTargetRole, domain.ShareTargetGroup:
	default:
		return domain.NewAppError(http.StatusBadRequest, "target_type must be 'user', 'role', or 'group'")
	}
	if !domain.IsStorableShareLevel(in.Level) {
		return domain.NewAppError(http.StatusBadRequest, "level must be 'view', 'comment', or 'edit'")
	}
	return uc.shares.Create(ctx, &domain.ReportShare{
		OrgID: orgID, ReportID: reportID,
		TargetType: in.TargetType, TargetID: in.TargetID, Level: in.Level,
		CreatedBy: &userID,
	})
}

func (uc *reportShareUseCase) Remove(ctx context.Context, orgID, userID, reportID, shareID uuid.UUID) error {
	if err := uc.requireManage(ctx, orgID, userID, reportID); err != nil {
		return err
	}
	n, err := uc.shares.Delete(ctx, orgID, reportID, shareID)
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewAppError(http.StatusNotFound, "share not found")
	}
	return nil
}

func (uc *reportShareUseCase) requireManage(ctx context.Context, orgID, userID, reportID uuid.UUID) error {
	_, level, err := uc.reports.ResolveAccess(ctx, orgID, userID, reportID)
	if err != nil {
		return err
	}
	if level != domain.ShareLevelManage {
		return domain.ErrForbidden
	}
	return nil
}
