package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RoleHandler serves custom-role management (P3): list roles with their
// capabilities + data_scope, create (optionally cloning), rename/rescope, delete,
// and read/set a role's capability set. All routes are roles.manage-gated in the
// router; the usecase enforces the system-role / owner guardrails.
type RoleHandler struct {
	uc domain.RoleUseCase
}

func NewRoleHandler(uc domain.RoleUseCase) *RoleHandler {
	return &RoleHandler{uc: uc}
}

// List handles GET /api/roles.
func (h *RoleHandler) List(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	roles, err := h.uc.List(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": roles, "error": nil})
}

// Create handles POST /api/roles (create or clone-from).
func (h *RoleHandler) Create(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	var input domain.CreateRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	role, err := h.uc.Create(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": role, "error": nil})
}

// Update handles PATCH /api/roles/:id (rename / rescope a custom role).
func (h *RoleHandler) Update(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	var input domain.UpdateRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	if err := h.uc.Update(c.Request.Context(), orgID, id, input); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "saved", "error": nil})
}

// Delete handles DELETE /api/roles/:id.
func (h *RoleHandler) Delete(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "deleted", "error": nil})
}

// GetCapabilities handles GET /api/roles/:id/capabilities.
func (h *RoleHandler) GetCapabilities(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	caps, err := h.uc.GetCapabilities(c.Request.Context(), orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"capabilities": caps, "available": domain.AllCapabilities}, "error": nil})
}

// SetCapabilities handles PUT /api/roles/:id/capabilities.
func (h *RoleHandler) SetCapabilities(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	var input domain.SetCapabilitiesInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	if err := h.uc.SetCapabilities(c.Request.Context(), orgID, id, input); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "saved", "error": nil})
}
