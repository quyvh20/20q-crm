package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---- fake usecase ---------------------------------------------------------

type fakeReportUC struct {
	domain.ReportUseCase
	reports    map[uuid.UUID]*domain.Report
	runResult  *domain.ReportResult
	runErr     error
	previewed  *domain.ReportConfig
	lastSlug   string
	fieldsList []domain.ReportFieldDescriptor
}

func (f *fakeReportUC) List(context.Context, uuid.UUID, uuid.UUID) ([]domain.Report, error) {
	var out []domain.Report
	for _, r := range f.reports {
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeReportUC) Get(_ context.Context, _ uuid.UUID, _ uuid.UUID, id uuid.UUID) (*domain.Report, error) {
	if r, ok := f.reports[id]; ok {
		return r, nil
	}
	return nil, domain.ErrReportNotFound
}

func (f *fakeReportUC) Run(_ context.Context, _ uuid.UUID, _ uuid.UUID, id uuid.UUID) (*domain.ReportResult, error) {
	if f.runErr != nil {
		return nil, f.runErr
	}
	if _, ok := f.reports[id]; !ok {
		return nil, domain.ErrReportNotFound
	}
	return f.runResult, nil
}

func (f *fakeReportUC) Preview(_ context.Context, _ uuid.UUID, slug string, cfg domain.ReportConfig) (*domain.ReportResult, error) {
	f.lastSlug = slug
	f.previewed = &cfg
	return f.runResult, nil
}

func (f *fakeReportUC) ListFields(_ context.Context, _ uuid.UUID, slug string) ([]domain.ReportFieldDescriptor, error) {
	f.lastSlug = slug
	return f.fieldsList, nil
}

// mountReportRoutes mirrors router.go's /reports group exactly, so this test
// fails if the route shape develops a gin conflict (e.g. /preview vs /:id).
func mountReportRoutes(h *ReportHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	orgID, userID := uuid.New(), uuid.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
	})
	grp := r.Group("/api/reports")
	grp.GET("", h.List)
	grp.POST("", h.Create)
	grp.POST("/preview", h.Preview)
	grp.GET("/objects/:slug/fields", h.ListFields)
	grp.GET("/:id", h.Get)
	grp.PATCH("/:id", h.Update)
	grp.DELETE("/:id", h.Delete)
	grp.GET("/:id/run", h.Run)
	grp.GET("/:id/export.csv", h.ExportCSV)
	return r
}

func groupsResult() *domain.ReportResult {
	return &domain.ReportResult{
		Kind: domain.ReportResultGroups,
		Groups: []domain.ReportGroup{
			{Key: "a", Label: "Negotiation", Value: 12500, Count: 5},
			{Key: "b", Label: "=HYPERLINK(\"evil\")", Value: 1, Count: 1},
		},
		RowCount: 6,
	}
}

func TestReportRoutes_PreviewAndIDCoexist(t *testing.T) {
	uc := &fakeReportUC{reports: map[uuid.UUID]*domain.Report{}, runResult: groupsResult()}
	router := mountReportRoutes(NewReportHandler(uc))

	// Static /preview dispatches to Preview, not Get(:id = "preview").
	body := `{"object_slug":"deal","config":{"chart":"bar","group_by":{"field":"stage"}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/reports/preview", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body: %s", w.Code, w.Body.String())
	}
	if uc.lastSlug != "deal" || uc.previewed == nil || uc.previewed.Chart != "bar" {
		t.Errorf("preview not dispatched correctly: slug=%q cfg=%+v", uc.lastSlug, uc.previewed)
	}

	// The fields catalog route dispatches too.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/objects/property/fields", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("fields status = %d", w.Code)
	}
	if uc.lastSlug != "property" {
		t.Errorf("fields slug = %q", uc.lastSlug)
	}

	// And a real id still routes to Get.
	id := uuid.New()
	uc.reports[id] = &domain.Report{ID: id, Name: "Pipeline"}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/"+id.String(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d", w.Code)
	}
}

func TestReportRoutes_BadIDIs400(t *testing.T) {
	uc := &fakeReportUC{reports: map[uuid.UUID]*domain.Report{}}
	router := mountReportRoutes(NewReportHandler(uc))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/not-a-uuid/run", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestReportRun_ReturnsResultEnvelope(t *testing.T) {
	id := uuid.New()
	uc := &fakeReportUC{
		reports:   map[uuid.UUID]*domain.Report{id: {ID: id, Name: "Pipeline"}},
		runResult: groupsResult(),
	}
	router := mountReportRoutes(NewReportHandler(uc))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/"+id.String()+"/run", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Data domain.ReportResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if resp.Data.Kind != domain.ReportResultGroups || len(resp.Data.Groups) != 2 {
		t.Errorf("unexpected result: %+v", resp.Data)
	}
}

func TestReportExportCSV_FormulaInjectionNeutralized(t *testing.T) {
	id := uuid.New()
	uc := &fakeReportUC{
		reports:   map[uuid.UUID]*domain.Report{id: {ID: id, Name: "Pipeline by Stage!"}},
		runResult: groupsResult(),
	}
	router := mountReportRoutes(NewReportHandler(uc))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/"+id.String()+"/export.csv", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="pipeline-by-stage.csv"` {
		t.Errorf("disposition = %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "label,value,count") {
		t.Errorf("missing header: %s", body)
	}
	if !strings.Contains(body, "Negotiation,12500,5") {
		t.Errorf("missing group row: %s", body)
	}
	// The hostile label must be neutralized with a leading quote.
	if !strings.Contains(body, `'=HYPERLINK`) {
		t.Errorf("formula injection not neutralized: %s", body)
	}
}

func TestReportRoutes_UnauthorizedWithoutContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New() // no org/user middleware
	h := NewReportHandler(&fakeReportUC{})
	r.GET("/api/reports", h.List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
