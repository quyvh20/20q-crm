package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ObjectLayoutHandler serves the P8 per-role detail-layout admin CRUD.
//
// Routes (all admin-only, registered in registerObjectRegistryRoutes):
//
//	GET    /api/registry/objects/:slug/layouts           → ListLayouts
//	POST   /api/registry/objects/:slug/layouts           → CreateLayout
//	PATCH  /api/registry/objects/:slug/layouts/:id       → UpdateLayout
//	DELETE /api/registry/objects/:slug/layouts/:id       → DeleteLayout
//	PUT    /api/registry/objects/:slug/layouts/:id/roles → SetLayoutRoles
//
// The caller's effective layout is NOT served from here — it is folded into
// GET /api/registry/objects/:slug/schema by ObjectRegistryHandler.GetSchema, so
// the frontend needs no extra call to render the detail page with sections.
type ObjectLayoutHandler struct {
	uc domain.ObjectLayoutUseCase
}

func NewObjectLayoutHandler(uc domain.ObjectLayoutUseCase) *ObjectLayoutHandler {
	return &ObjectLayoutHandler{uc: uc}
}

// ListLayouts handles GET /api/registry/objects/:slug/layouts
func (h *ObjectLayoutHandler) ListLayouts(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	layouts, err := h.uc.ListLayouts(c.Request.Context(), orgID, slug)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": layouts, "error": nil})
}

// CreateLayout handles POST /api/registry/objects/:slug/layouts
func (h *ObjectLayoutHandler) CreateLayout(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	var in domain.CreateLayoutInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	layout, err := h.uc.CreateLayout(c.Request.Context(), orgID, slug, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": layout, "error": nil})
}

// UpdateLayout handles PATCH /api/registry/objects/:slug/layouts/:id
func (h *ObjectLayoutHandler) UpdateLayout(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid layout id"})
		return
	}
	var in domain.UpdateLayoutInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	layout, err := h.uc.UpdateLayout(c.Request.Context(), orgID, slug, id, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": layout, "error": nil})
}

// DeleteLayout handles DELETE /api/registry/objects/:slug/layouts/:id
func (h *ObjectLayoutHandler) DeleteLayout(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid layout id"})
		return
	}
	if err := h.uc.DeleteLayout(c.Request.Context(), orgID, slug, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": nil, "error": nil})
}

// SetLayoutRoles handles PUT /api/registry/objects/:slug/layouts/:id/roles.
// The body is { "role_ids": ["uuid", ...] }. Sending an empty array clears all
// role assignments; the layout remains but is no longer role-targeted.
func (h *ObjectLayoutHandler) SetLayoutRoles(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid layout id"})
		return
	}
	var body struct {
		RoleIDs []uuid.UUID `json:"role_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.uc.SetLayoutRoles(c.Request.Context(), orgID, slug, id, body.RoleIDs); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": nil, "error": nil})
}
