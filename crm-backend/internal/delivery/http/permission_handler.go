package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PermissionHandler serves the admin Object-Level Security grid (role × object)
// and the per-record audit trail (P5a). Enforcement itself lives in
// RecordService (the chokepoint); this handler only configures and inspects it.
// Mounted under /api/registry so it sits alongside the rest of the object surface
// and is promoted to /api in P7.
type PermissionHandler struct {
	uc domain.PermissionUseCase
}

func NewPermissionHandler(uc domain.PermissionUseCase) *PermissionHandler {
	return &PermissionHandler{uc: uc}
}

// GetMyCapabilities handles GET /api/auth/capabilities — the caller's effective
// system capabilities for the active org, so the SPA can render permission-aware
// UI (e.g. show the Roles/Permissions admin tab only to a caller with
// roles.manage). The server still enforces every action independently.
func (h *PermissionHandler) GetMyCapabilities(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	caps := h.uc.CallerCapabilities(c.Request.Context(), orgID)
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"capabilities": caps}, "error": nil})
}

// GetGrid handles GET /api/registry/permissions — the full role × object matrix.
func (h *PermissionHandler) GetGrid(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	grid, err := h.uc.GetGrid(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": grid, "error": nil})
}

// SetPermission handles PUT /api/registry/permissions — upsert one cell.
func (h *PermissionHandler) SetPermission(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	var input domain.SetPermissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	if err := h.uc.SetPermission(c.Request.Context(), orgID, input); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "saved", "error": nil})
}

// GetFieldGrid handles GET /api/registry/objects/:slug/field-permissions — the
// field × role level matrix for one object (Field-Level Security, P5b).
func (h *PermissionHandler) GetFieldGrid(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	grid, err := h.uc.GetFieldGrid(c.Request.Context(), orgID, slug)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": grid, "error": nil})
}

// SetFieldPermission handles PUT /api/registry/objects/:slug/field-permissions —
// set one (role, field) level. The path slug is authoritative over any object_slug
// in the body.
func (h *PermissionHandler) SetFieldPermission(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	var input domain.SetFieldPermissionInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	input.ObjectSlug = c.Param("slug")
	if err := h.uc.SetFieldPermission(c.Request.Context(), orgID, input); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "saved", "error": nil})
}

// ListAudit handles GET /api/registry/objects/:slug/records/:id/audit — the
// per-record change history.
func (h *PermissionHandler) ListAudit(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	slug := c.Param("slug")
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid record id"})
		return
	}
	entries, err := h.uc.ListRecordAudit(c.Request.Context(), orgID, slug, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": entries, "error": nil})
}
