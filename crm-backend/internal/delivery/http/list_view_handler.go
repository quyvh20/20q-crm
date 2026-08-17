package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListViewHandler serves saved list views over the uniform record list:
//
//	GET    /api/registry/objects/:slug/views → List (own + org-shared)
//	POST   /api/registry/objects/:slug/views → Create
//	PUT    /api/registry/views/:id           → Update (owner only)
//	DELETE /api/registry/views/:id           → Delete (owner only)
//
// Update/Delete address a view by id alone (no :slug) because the object slug
// is a stored property of the view — trusting a slug from the URL would let a
// caller re-authorize against a different object than the one the view reads.
type ListViewHandler struct {
	uc domain.ListViewUseCase
}

// NewListViewHandler builds the handler.
func NewListViewHandler(uc domain.ListViewUseCase) *ListViewHandler { return &ListViewHandler{uc: uc} }

// RegisterRoutes mounts the view routes under the protected stack (the same
// AuthMiddlewareWithAPITokens + workspace-2FA pair the /api/registry record
// routes run behind). No route-level OLS middleware, matching GET /records:
// the usecase authorizes ActionRead on the object itself, which is the only
// way the id-addressed routes CAN be gated (the slug isn't in their URL).
//
// Registered as its own group (the SegmentHandler pattern) rather than inside
// registerObjectRegistryRoutes: `views` is a new static segment under both
// /registry/objects/:slug and /registry, so the gin tree cannot conflict.
func (h *ListViewHandler) RegisterRoutes(router *gin.Engine, protected []gin.HandlerFunc) {
	g := router.Group("/api/registry/objects/:slug/views")
	g.Use(protected...)
	g.GET("", h.List)
	g.POST("", h.Create)

	v := router.Group("/api/registry/views")
	v.Use(protected...)
	v.PUT("/:id", h.Update)
	v.DELETE("/:id", h.Delete)
}

func (h *ListViewHandler) callerIDs(c *gin.Context) (orgID, userID uuid.UUID, ok bool) {
	orgID, okOrg := GetOrgID(c)
	userID, okUser := GetUserID(c)
	if !okOrg || !okUser {
		c.JSON(http.StatusUnauthorized, gin.H{"data": nil, "error": "unauthorized"})
		return uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, true
}

func viewIDParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid view id"})
		return uuid.Nil, false
	}
	return id, true
}

// List handles GET /api/registry/objects/:slug/views
func (h *ListViewHandler) List(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	views, err := h.uc.List(c.Request.Context(), orgID, userID, c.Param("slug"))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": views, "error": nil})
}

// Create handles POST /api/registry/objects/:slug/views
func (h *ListViewHandler) Create(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	var input domain.ListViewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	view, err := h.uc.Create(c.Request.Context(), orgID, userID, c.Param("slug"), input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": view, "error": nil})
}

// Update handles PUT /api/registry/views/:id
func (h *ListViewHandler) Update(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := viewIDParam(c)
	if !ok {
		return
	}
	var input domain.ListViewInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	view, err := h.uc.Update(c.Request.Context(), orgID, userID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": view, "error": nil})
}

// Delete handles DELETE /api/registry/views/:id
func (h *ListViewHandler) Delete(c *gin.Context) {
	orgID, userID, ok := h.callerIDs(c)
	if !ok {
		return
	}
	id, ok := viewIDParam(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, userID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}
