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

// Options handles GET /api/roles/options — the minimal role list any member may
// read to populate role pickers (member/invite dropdowns, the report Share
// dialog). Unlike List it carries no capabilities, so it needs no roles.manage
// gate (P6).
func (h *RoleHandler) Options(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	roles, err := h.uc.Options(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": roles, "error": nil})
}

// Catalog handles GET /api/roles/catalog — the static capability metadata
// (labels, descriptions, groups, sensitive flags) any member may read to render
// the roles UI (P6). The vocabulary is compile-time, so this is served straight
// from the domain catalog.
func (h *RoleHandler) Catalog(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"capabilities": domain.CapabilityCatalog,
		"groups":       domain.CapabilityGroups,
	}, "error": nil})
}

// Duplicate handles POST /api/roles/:id/duplicate — clone a role into a new
// custom role, optionally reassigning the source's members onto the copy (P6).
func (h *RoleHandler) Duplicate(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	var input domain.DuplicateRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": err.Error()})
		return
	}
	role, err := h.uc.Duplicate(c.Request.Context(), orgID, id, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": role, "error": nil})
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

// Delete handles DELETE /api/roles/:id. When members still hold the role, the
// caller passes ?reassign_to=<role_id> to move them onto another role in the same
// transaction (P6 delete-with-reassign); omitting it while members remain is a 409
// that drives the "N people have this role — move them to:" picker.
func (h *RoleHandler) Delete(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	var reassignTo *uuid.UUID
	if raw := c.Query("reassign_to"); raw != "" {
		target, err := uuid.Parse(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid reassign_to role id"})
			return
		}
		reassignTo = &target
	}
	if err := h.uc.Delete(c.Request.Context(), orgID, id, reassignTo); err != nil {
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
