package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type fakeGroupUC struct {
	groups   map[uuid.UUID]*domain.UserGroup
	added    []string // "groupID/userID"
	removed  []string
	createErr error
}

func (f *fakeGroupUC) List(context.Context, uuid.UUID) ([]domain.UserGroupView, error) {
	out := []domain.UserGroupView{}
	for _, g := range f.groups {
		out = append(out, domain.UserGroupView{ID: g.ID, Name: g.Name})
	}
	return out, nil
}
func (f *fakeGroupUC) Create(_ context.Context, orgID, actorID uuid.UUID, in domain.UserGroupInput) (*domain.UserGroup, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	g := &domain.UserGroup{ID: uuid.New(), OrgID: orgID, Name: in.Name}
	f.groups[g.ID] = g
	return g, nil
}
func (f *fakeGroupUC) Update(_ context.Context, _ uuid.UUID, id uuid.UUID, in domain.UserGroupInput) (*domain.UserGroup, error) {
	g, ok := f.groups[id]
	if !ok {
		return nil, domain.NewAppError(404, "group not found")
	}
	g.Name = in.Name
	return g, nil
}
func (f *fakeGroupUC) Delete(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeGroupUC) AddMember(_ context.Context, _ uuid.UUID, groupID, userID uuid.UUID) error {
	if _, ok := f.groups[groupID]; !ok {
		return domain.NewAppError(404, "group not found")
	}
	f.added = append(f.added, groupID.String()+"/"+userID.String())
	return nil
}
func (f *fakeGroupUC) RemoveMember(_ context.Context, _ uuid.UUID, groupID, userID uuid.UUID) error {
	f.removed = append(f.removed, groupID.String()+"/"+userID.String())
	return nil
}

func mountGroupRoutes(h *UserGroupHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	orgID, userID := uuid.New(), uuid.New()
	r.Use(func(c *gin.Context) { c.Set("org_id", orgID); c.Set("user_id", userID) })
	g := r.Group("/api/groups")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.PATCH("/:id", h.Update)
	g.DELETE("/:id", h.Delete)
	g.POST("/:id/members", h.AddMember)
	g.DELETE("/:id/members/:userId", h.RemoveMember)
	return r
}

func TestGroupRoutes_CreateAndAddMember(t *testing.T) {
	uc := &fakeGroupUC{groups: map[uuid.UUID]*domain.UserGroup{}}
	router := mountGroupRoutes(NewUserGroupHandler(uc))

	// create
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(`{"name":"West Region"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", w.Code, w.Body.String())
	}
	var gid uuid.UUID
	for id := range uc.groups {
		gid = id
	}

	// add member
	uid := uuid.New()
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/groups/"+gid.String()+"/members", strings.NewReader(`{"user_id":"`+uid.String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("add member status = %d, body %s", w.Code, w.Body.String())
	}
	if len(uc.added) != 1 || uc.added[0] != gid.String()+"/"+uid.String() {
		t.Errorf("member not added: %v", uc.added)
	}
}

func TestGroupRoutes_AddMemberToMissingGroup404(t *testing.T) {
	uc := &fakeGroupUC{groups: map[uuid.UUID]*domain.UserGroup{}}
	router := mountGroupRoutes(NewUserGroupHandler(uc))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/groups/"+uuid.New().String()+"/members", strings.NewReader(`{"user_id":"`+uuid.New().String()+`"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestGroupRoutes_BadIdIs400(t *testing.T) {
	uc := &fakeGroupUC{groups: map[uuid.UUID]*domain.UserGroup{}}
	router := mountGroupRoutes(NewUserGroupHandler(uc))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/groups/not-a-uuid", strings.NewReader(`{"name":"x"}`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
