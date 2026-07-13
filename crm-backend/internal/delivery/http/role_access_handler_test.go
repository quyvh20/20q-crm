package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ---- fakes for the three narrow ports ------------------------------------------

type accessFakeRoles struct {
	detail *domain.RoleDetail
	err    error
}

func (f *accessFakeRoles) GetDetail(context.Context, uuid.UUID, uuid.UUID) (*domain.RoleDetail, error) {
	return f.detail, f.err
}

type accessFakeReader struct {
	objects []domain.RoleObjectAccess
	err     error
}

func (f *accessFakeReader) EffectiveAccess(context.Context, uuid.UUID, uuid.UUID) ([]domain.RoleObjectAccess, error) {
	return f.objects, f.err
}

type accessFakeLayouts struct {
	layouts []domain.RoleLayoutAssignment
	err     error
}

func (f *accessFakeLayouts) ListOrgRoleLayoutAssignments(context.Context, uuid.UUID, uuid.UUID) ([]domain.RoleLayoutAssignment, error) {
	return f.layouts, f.err
}

func serveRoleAccess(t *testing.T, h *RoleAccessHandler, id string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("org_id", uuid.New())
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	h.Get(c)
	return w
}

// roleAccessResponse mirrors the endpoint's {role, objects, layouts} payload.
type roleAccessResponse struct {
	Data struct {
		Role    *domain.RoleDetail          `json:"role"`
		Objects []domain.RoleObjectAccess   `json:"objects"`
		Layouts []domain.RoleLayoutAssignment `json:"layouts"`
	} `json:"data"`
}

// The endpoint merges the three sources into one payload so the role detail
// page renders in a single round-trip.
func TestRoleAccessGet_MergesRoleObjectsLayouts(t *testing.T) {
	roleID := uuid.New()
	layoutID := uuid.New()
	h := NewRoleAccessHandler(
		&accessFakeRoles{detail: &domain.RoleDetail{
			ID: roleID, Name: "Support Agent", DataScope: domain.DataScopeOwn,
			Capabilities: []string{domain.CapAuditView},
		}},
		&accessFakeReader{objects: []domain.RoleObjectAccess{{
			Slug: "deal", Label: "Deal", IsSystem: true,
			ObjectAccess:     domain.ObjectAccess{Read: true},
			RestrictedFields: []domain.RoleRestrictedField{{Key: "value", Label: "Amount", Level: "hidden"}},
		}}},
		&accessFakeLayouts{layouts: []domain.RoleLayoutAssignment{{
			ObjectSlug: "deal", LayoutID: layoutID, LayoutName: "Sales view",
		}}},
	)

	w := serveRoleAccess(t, h, roleID.String())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp roleAccessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response JSON: %v", err)
	}
	if resp.Data.Role == nil || resp.Data.Role.Name != "Support Agent" {
		t.Errorf("role missing or wrong: %+v", resp.Data.Role)
	}
	if len(resp.Data.Objects) != 1 || resp.Data.Objects[0].Slug != "deal" || !resp.Data.Objects[0].Read {
		t.Errorf("objects wrong: %+v", resp.Data.Objects)
	}
	if len(resp.Data.Objects[0].RestrictedFields) != 1 || resp.Data.Objects[0].RestrictedFields[0].Level != "hidden" {
		t.Errorf("restricted fields wrong: %+v", resp.Data.Objects[0].RestrictedFields)
	}
	if len(resp.Data.Layouts) != 1 || resp.Data.Layouts[0].LayoutID != layoutID {
		t.Errorf("layouts wrong: %+v", resp.Data.Layouts)
	}
}

func TestRoleAccessGet_InvalidUUIDIs400(t *testing.T) {
	h := NewRoleAccessHandler(&accessFakeRoles{}, &accessFakeReader{}, &accessFakeLayouts{})
	w := serveRoleAccess(t, h, "not-a-uuid")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// A role the org can't see propagates the usecase's 404 through handleAppError.
func TestRoleAccessGet_RoleNotFoundPropagates(t *testing.T) {
	h := NewRoleAccessHandler(
		&accessFakeRoles{err: domain.NewAppError(http.StatusNotFound, "role not found")},
		&accessFakeReader{}, &accessFakeLayouts{},
	)
	w := serveRoleAccess(t, h, uuid.New().String())
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// Nil slices from the ports serialize as [] (never null), so the SPA can map
// over the payload without guards.
func TestRoleAccessGet_NilSlicesNormalizeToEmpty(t *testing.T) {
	h := NewRoleAccessHandler(
		&accessFakeRoles{detail: &domain.RoleDetail{ID: uuid.New(), Name: "viewer"}},
		&accessFakeReader{objects: nil}, &accessFakeLayouts{layouts: nil},
	)
	w := serveRoleAccess(t, h, uuid.New().String())
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp roleAccessResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response JSON: %v", err)
	}
	// json.Unmarshal leaves a slice nil for JSON null but allocates for [].
	if resp.Data.Objects == nil {
		t.Error("objects must serialize as [], got null")
	}
	if resp.Data.Layouts == nil {
		t.Error("layouts must serialize as [], got null")
	}
}
