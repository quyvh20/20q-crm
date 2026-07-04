package usecase

import (
	"context"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// dashboardUseCase manages the caller's own dashboard (P9 Phase B). Widgets are
// pure layout; report visibility is resolved through the report usecase on every
// read, so a report that was unshared (or deleted) after being pinned silently
// drops off, and a report shared with the caller (directly / by role / by group)
// can be pinned and stays visible. Widget DATA is fetched by the frontend via
// the normal run endpoint, where OLS/FLS/data scope apply.
type dashboardUseCase struct {
	widgets domain.DashboardWidgetRepository
	reports domain.ReportUseCase
}

func NewDashboardUseCase(widgets domain.DashboardWidgetRepository, reports domain.ReportUseCase) domain.DashboardUseCase {
	return &dashboardUseCase{widgets: widgets, reports: reports}
}

var errWidgetNotFound = domain.NewAppError(http.StatusNotFound, "widget not found")

func (uc *dashboardUseCase) ListWidgets(ctx context.Context, orgID, userID uuid.UUID) ([]domain.DashboardWidgetView, error) {
	widgets, err := uc.widgets.ListForUser(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DashboardWidgetView, 0, len(widgets))
	for _, w := range widgets {
		// ResolveAccess 404s when the report is gone or no longer visible to the
		// caller — drop those widgets rather than leaking a stub.
		rep, _, err := uc.reports.ResolveAccess(ctx, orgID, userID, w.ReportID)
		if err != nil {
			continue
		}
		out = append(out, domain.DashboardWidgetView{DashboardWidget: w, Report: rep})
	}
	return out, nil
}

func (uc *dashboardUseCase) AddWidget(ctx context.Context, orgID, userID uuid.UUID, in domain.AddWidgetInput) (*domain.DashboardWidget, error) {
	size := in.Size
	switch size {
	case "":
		size = "half"
	case "half", "full":
	default:
		return nil, domain.NewAppError(http.StatusBadRequest, "size must be 'half' or 'full'")
	}

	// Pin only reports the caller can actually see.
	if _, _, err := uc.reports.ResolveAccess(ctx, orgID, userID, in.ReportID); err != nil {
		return nil, err
	}

	// Idempotent: re-pinning returns the existing widget.
	if existing, err := uc.widgets.FindByReport(ctx, orgID, userID, in.ReportID); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, nil
	}

	pos, err := uc.widgets.NextPosition(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	w := &domain.DashboardWidget{OrgID: orgID, UserID: userID, ReportID: in.ReportID, Position: pos, Size: size}
	if err := uc.widgets.Create(ctx, w); err != nil {
		return nil, err
	}
	return w, nil
}

func (uc *dashboardUseCase) UpdateWidget(ctx context.Context, orgID, userID, id uuid.UUID, in domain.UpdateWidgetInput) error {
	if in.Size != "half" && in.Size != "full" {
		return domain.NewAppError(http.StatusBadRequest, "size must be 'half' or 'full'")
	}
	n, err := uc.widgets.UpdateSize(ctx, orgID, userID, id, in.Size)
	if err != nil {
		return err
	}
	if n == 0 {
		return errWidgetNotFound
	}
	return nil
}

func (uc *dashboardUseCase) RemoveWidget(ctx context.Context, orgID, userID, id uuid.UUID) error {
	n, err := uc.widgets.Delete(ctx, orgID, userID, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return errWidgetNotFound
	}
	return nil
}

func (uc *dashboardUseCase) Reorder(ctx context.Context, orgID, userID uuid.UUID, in domain.ReorderWidgetsInput) error {
	if len(in.WidgetIDs) == 0 {
		return nil
	}
	return uc.widgets.Reorder(ctx, orgID, userID, in.WidgetIDs)
}
