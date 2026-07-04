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

type fakeReportCommentUC struct {
	listResult []domain.ReportCommentView
	listErr    error
	added      *domain.AddReportCommentInput
	addErr     error
	deletedID  uuid.UUID
	deleteErr  error
}

func (f *fakeReportCommentUC) List(_ context.Context, _, _, _ uuid.UUID) ([]domain.ReportCommentView, error) {
	return f.listResult, f.listErr
}

func (f *fakeReportCommentUC) Add(_ context.Context, _, userID, _ uuid.UUID, in domain.AddReportCommentInput) (*domain.ReportComment, error) {
	if f.addErr != nil {
		return nil, f.addErr
	}
	f.added = &in
	return &domain.ReportComment{ID: uuid.New(), AuthorID: &userID, Body: in.Body}, nil
}

func (f *fakeReportCommentUC) Delete(_ context.Context, _, _, _, commentID uuid.UUID) error {
	f.deletedID = commentID
	return f.deleteErr
}

// mountReportCommentRoutes mirrors router.go's /reports group shape around the
// comment routes, so this test fails if they develop a gin wildcard conflict
// with the sibling /:id and /:id/shares routes.
func mountReportCommentRoutes(h *ReportCommentHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	orgID, userID := uuid.New(), uuid.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
	})
	stub := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": "sibling", "error": nil}) }
	grp := r.Group("/api/reports")
	grp.GET("/:id", stub)
	grp.GET("/:id/run", stub)
	grp.GET("/:id/shares", stub)
	grp.DELETE("/:id/shares/:shareId", stub)
	grp.GET("/:id/comments", h.List)
	grp.POST("/:id/comments", h.Add)
	grp.DELETE("/:id/comments/:commentId", h.Remove)
	return r
}

func TestReportCommentRoutes_CommentsAndIDCoexist(t *testing.T) {
	uc := &fakeReportCommentUC{listResult: []domain.ReportCommentView{
		{ID: uuid.New(), AuthorName: "Alice", Body: "looks great", CanDelete: true},
	}}
	router := mountReportCommentRoutes(NewReportCommentHandler(uc))
	id := uuid.New()

	// GET /:id still dispatches to the sibling, not the comment list.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/"+id.String(), nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sibling") {
		t.Fatalf("GET /:id = %d body %s", w.Code, w.Body.String())
	}

	// GET /:id/comments dispatches to List.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/"+id.String()+"/comments", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []domain.ReportCommentView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].Body != "looks great" {
		t.Errorf("unexpected list: %+v", resp.Data)
	}
}

func TestReportCommentRoutes_AddReturns201(t *testing.T) {
	uc := &fakeReportCommentUC{}
	router := mountReportCommentRoutes(NewReportCommentHandler(uc))
	id := uuid.New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/reports/"+id.String()+"/comments", strings.NewReader(`{"body":"nice report"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add status = %d body %s", w.Code, w.Body.String())
	}
	if uc.added == nil || uc.added.Body != "nice report" {
		t.Errorf("Add not dispatched with body: %+v", uc.added)
	}
}

func TestReportCommentRoutes_AddEmptyBodyIs400(t *testing.T) {
	uc := &fakeReportCommentUC{}
	router := mountReportCommentRoutes(NewReportCommentHandler(uc))
	id := uuid.New()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/reports/"+id.String()+"/comments", strings.NewReader(`{"body":""}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty body status = %d, want 400 (binding)", w.Code)
	}
}

func TestReportCommentRoutes_DeleteDispatches(t *testing.T) {
	uc := &fakeReportCommentUC{}
	router := mountReportCommentRoutes(NewReportCommentHandler(uc))
	id, commentID := uuid.New(), uuid.New()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/reports/"+id.String()+"/comments/"+commentID.String(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d body %s", w.Code, w.Body.String())
	}
	if uc.deletedID != commentID {
		t.Errorf("delete id = %v, want %v", uc.deletedID, commentID)
	}

	// A malformed comment id is a 400, not a route miss.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/reports/"+id.String()+"/comments/not-a-uuid", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad comment id status = %d, want 400", w.Code)
	}
}

func TestReportCommentRoutes_NotFoundPropagates(t *testing.T) {
	uc := &fakeReportCommentUC{listErr: domain.ErrReportNotFound}
	router := mountReportCommentRoutes(NewReportCommentHandler(uc))
	id := uuid.New()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/"+id.String()+"/comments", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (report usecase 404 propagates)", w.Code)
	}
}

func TestReportCommentRoutes_BadReportIDIs400(t *testing.T) {
	uc := &fakeReportCommentUC{}
	router := mountReportCommentRoutes(NewReportCommentHandler(uc))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/reports/not-a-uuid/comments", nil))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
