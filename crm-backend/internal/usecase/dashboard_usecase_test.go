package usecase

import (
	"context"
	"net/http"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeWidgetRepo is an in-memory DashboardWidgetRepository.
type fakeWidgetRepo struct {
	widgets []domain.DashboardWidget
}

func (f *fakeWidgetRepo) ListForUser(_ context.Context, _ uuid.UUID, userID uuid.UUID) ([]domain.DashboardWidget, error) {
	var out []domain.DashboardWidget
	for _, w := range f.widgets {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	return out, nil
}

func (f *fakeWidgetRepo) FindByReport(_ context.Context, _ uuid.UUID, userID, reportID uuid.UUID) (*domain.DashboardWidget, error) {
	for i := range f.widgets {
		if f.widgets[i].UserID == userID && f.widgets[i].ReportID == reportID {
			return &f.widgets[i], nil
		}
	}
	return nil, nil
}

func (f *fakeWidgetRepo) Create(_ context.Context, w *domain.DashboardWidget) error {
	if w.ID == uuid.Nil {
		w.ID = uuid.New()
	}
	f.widgets = append(f.widgets, *w)
	return nil
}

func (f *fakeWidgetRepo) UpdateSize(_ context.Context, _ uuid.UUID, userID, id uuid.UUID, size string) (int64, error) {
	for i := range f.widgets {
		if f.widgets[i].ID == id && f.widgets[i].UserID == userID {
			f.widgets[i].Size = size
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeWidgetRepo) Delete(_ context.Context, _ uuid.UUID, userID, id uuid.UUID) (int64, error) {
	for i := range f.widgets {
		if f.widgets[i].ID == id && f.widgets[i].UserID == userID {
			f.widgets = append(f.widgets[:i], f.widgets[i+1:]...)
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeWidgetRepo) Reorder(_ context.Context, _ uuid.UUID, userID uuid.UUID, ids []uuid.UUID) error {
	for pos, id := range ids {
		for i := range f.widgets {
			if f.widgets[i].ID == id && f.widgets[i].UserID == userID {
				f.widgets[i].Position = pos
			}
		}
	}
	return nil
}

func (f *fakeWidgetRepo) NextPosition(_ context.Context, _ uuid.UUID, userID uuid.UUID) (int, error) {
	max := -1
	for _, w := range f.widgets {
		if w.UserID == userID && w.Position > max {
			max = w.Position
		}
	}
	return max + 1, nil
}

// fakeDashReportUC implements just the ResolveAccess the dashboard uses: a
// report is visible to its creator (manage) or org-wide (view), else 404 —
// reading live from the shared reports map so mutations in a test take effect.
type fakeDashReportUC struct {
	domain.ReportUseCase
	repo *fakeReportRepo
}

func (f *fakeDashReportUC) ResolveAccess(_ context.Context, _ uuid.UUID, userID uuid.UUID, id uuid.UUID) (*domain.Report, string, error) {
	rep := f.repo.reports[id]
	if rep == nil {
		return nil, "", domain.ErrReportNotFound
	}
	if rep.CreatedBy != nil && *rep.CreatedBy == userID {
		return rep, domain.ShareLevelManage, nil
	}
	if rep.Visibility == domain.ReportVisibilityOrg {
		return rep, domain.ShareLevelView, nil
	}
	return nil, "", domain.ErrReportNotFound
}

func dashEnv(t *testing.T) (domain.DashboardUseCase, *fakeWidgetRepo, *fakeReportRepo, uuid.UUID) {
	t.Helper()
	widgets := &fakeWidgetRepo{}
	reports := newFakeReportRepo()
	return NewDashboardUseCase(widgets, &fakeDashReportUC{repo: reports}), widgets, reports, uuid.New()
}

func seedReport(reports *fakeReportRepo, creator uuid.UUID, visibility string) *domain.Report {
	rep := &domain.Report{ID: uuid.New(), Name: "R", Visibility: visibility, CreatedBy: &creator, Config: domain.JSON(`{}`)}
	reports.reports[rep.ID] = rep
	return rep
}

func TestDashboard_PinRunsIdempotently(t *testing.T) {
	uc, widgets, reports, orgID := dashEnv(t)
	me := uuid.New()
	rep := seedReport(reports, me, domain.ReportVisibilityPrivate)

	w1, err := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: rep.ID})
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if w1.Size != "half" || w1.Position != 0 {
		t.Errorf("defaults wrong: %+v", w1)
	}
	// Re-pinning the same report returns the existing widget, not a duplicate.
	w2, err := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: rep.ID})
	if err != nil {
		t.Fatalf("re-add failed: %v", err)
	}
	if w2.ID != w1.ID || len(widgets.widgets) != 1 {
		t.Errorf("pin not idempotent: %d widgets", len(widgets.widgets))
	}
}

func TestDashboard_CannotPinInvisibleReport(t *testing.T) {
	uc, _, reports, orgID := dashEnv(t)
	me, stranger := uuid.New(), uuid.New()
	private := seedReport(reports, stranger, domain.ReportVisibilityPrivate)

	if _, err := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: private.ID}); err != domain.ErrReportNotFound {
		t.Errorf("err = %v, want ErrReportNotFound", err)
	}
	// Org-shared reports pin fine.
	shared := seedReport(reports, stranger, domain.ReportVisibilityOrg)
	if _, err := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: shared.ID}); err != nil {
		t.Errorf("shared pin failed: %v", err)
	}
}

func TestDashboard_ListDropsUnsharedAndDeletedReports(t *testing.T) {
	uc, _, reports, orgID := dashEnv(t)
	me, other := uuid.New(), uuid.New()
	mine := seedReport(reports, me, domain.ReportVisibilityPrivate)
	shared := seedReport(reports, other, domain.ReportVisibilityOrg)

	for _, rep := range []*domain.Report{mine, shared} {
		if _, err := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: rep.ID}); err != nil {
			t.Fatalf("add failed: %v", err)
		}
	}

	// The other user's report gets unshared after pinning.
	shared.Visibility = domain.ReportVisibilityPrivate

	views, err := uc.ListWidgets(context.Background(), orgID, me)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(views) != 1 || views[0].ReportID != mine.ID {
		t.Errorf("unshared report should drop off the dashboard: %+v", views)
	}
	if views[0].Report == nil || views[0].Report.ID != mine.ID {
		t.Error("widget view must embed its report")
	}
}

func TestDashboard_SizeValidationAndOwnership(t *testing.T) {
	uc, _, reports, orgID := dashEnv(t)
	me, other := uuid.New(), uuid.New()
	rep := seedReport(reports, me, domain.ReportVisibilityOrg)
	w, _ := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: rep.ID})

	if err := uc.UpdateWidget(context.Background(), orgID, me, w.ID, domain.UpdateWidgetInput{Size: "huge"}); err == nil {
		t.Error("invalid size accepted")
	}
	if err := uc.UpdateWidget(context.Background(), orgID, me, w.ID, domain.UpdateWidgetInput{Size: "full"}); err != nil {
		t.Errorf("resize failed: %v", err)
	}
	// Another user cannot touch my widget (scoped update finds nothing → 404).
	err := uc.UpdateWidget(context.Background(), orgID, other, w.ID, domain.UpdateWidgetInput{Size: "half"})
	appErr, ok := err.(*domain.AppError)
	if !ok || appErr.Code != http.StatusNotFound {
		t.Errorf("cross-user update = %v, want 404", err)
	}
	if err := uc.RemoveWidget(context.Background(), orgID, other, w.ID); err == nil {
		t.Error("cross-user delete should fail")
	}
}

func TestDashboard_Reorder(t *testing.T) {
	uc, widgets, reports, orgID := dashEnv(t)
	me := uuid.New()
	r1 := seedReport(reports, me, domain.ReportVisibilityPrivate)
	r2 := seedReport(reports, me, domain.ReportVisibilityPrivate)
	w1, _ := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: r1.ID})
	w2, _ := uc.AddWidget(context.Background(), orgID, me, domain.AddWidgetInput{ReportID: r2.ID})

	if err := uc.Reorder(context.Background(), orgID, me, domain.ReorderWidgetsInput{WidgetIDs: []uuid.UUID{w2.ID, w1.ID}}); err != nil {
		t.Fatalf("reorder failed: %v", err)
	}
	byID := map[uuid.UUID]int{}
	for _, w := range widgets.widgets {
		byID[w.ID] = w.Position
	}
	if byID[w2.ID] != 0 || byID[w1.ID] != 1 {
		t.Errorf("positions wrong: %v", byID)
	}
}
