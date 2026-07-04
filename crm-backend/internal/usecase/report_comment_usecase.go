package usecase

import (
	"context"
	"net/http"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// reportCommentUseCase serves a report's comment thread. It reuses the report
// usecase's ResolveAccess so the visibility/level rule lives in ONE place:
// reading needs any grant, posting needs 'comment', deleting needs to be the
// author or 'manage'.
type reportCommentUseCase struct {
	reports  domain.ReportUseCase
	comments domain.ReportCommentRepository
}

func NewReportCommentUseCase(reports domain.ReportUseCase, comments domain.ReportCommentRepository) domain.ReportCommentUseCase {
	return &reportCommentUseCase{reports: reports, comments: comments}
}

func (uc *reportCommentUseCase) List(ctx context.Context, orgID, userID, reportID uuid.UUID) ([]domain.ReportCommentView, error) {
	// Any level ≥ view can read the thread; no access 404s (propagated).
	_, level, err := uc.reports.ResolveAccess(ctx, orgID, userID, reportID)
	if err != nil {
		return nil, err
	}
	views, err := uc.comments.ListByReport(ctx, orgID, reportID)
	if err != nil {
		return nil, err
	}
	manager := level == domain.ShareLevelManage
	for i := range views {
		views[i].CanDelete = manager || (views[i].AuthorID != nil && *views[i].AuthorID == userID)
	}
	return views, nil
}

func (uc *reportCommentUseCase) Add(ctx context.Context, orgID, userID, reportID uuid.UUID, in domain.AddReportCommentInput) (*domain.ReportComment, error) {
	_, level, err := uc.reports.ResolveAccess(ctx, orgID, userID, reportID)
	if err != nil {
		return nil, err
	}
	if !domain.ShareLevelAtLeast(level, domain.ShareLevelComment) {
		return nil, domain.ErrForbidden
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		return nil, domain.NewAppError(http.StatusBadRequest, "comment body is required")
	}
	c := &domain.ReportComment{
		OrgID: orgID, ReportID: reportID, AuthorID: &userID, Body: body,
	}
	if err := uc.comments.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (uc *reportCommentUseCase) Delete(ctx context.Context, orgID, userID, reportID, commentID uuid.UUID) error {
	c, err := uc.comments.GetByID(ctx, orgID, commentID)
	if err != nil {
		return err
	}
	if c == nil || c.ReportID != reportID {
		return domain.NewAppError(http.StatusNotFound, "comment not found")
	}
	// ResolveAccess both authorizes (a caller with no grant on the report gets
	// the report's 404, not a comment 404) and yields the level for the manager
	// check.
	_, level, err := uc.reports.ResolveAccess(ctx, orgID, userID, reportID)
	if err != nil {
		return err
	}
	isAuthor := c.AuthorID != nil && *c.AuthorID == userID
	if !isAuthor && level != domain.ShareLevelManage {
		return domain.ErrForbidden
	}
	n, err := uc.comments.SoftDelete(ctx, orgID, commentID)
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewAppError(http.StatusNotFound, "comment not found")
	}
	return nil
}
