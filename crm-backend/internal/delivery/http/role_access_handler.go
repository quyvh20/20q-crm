package http

import (
	"context"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RoleAccessHandler serves GET /api/roles/:id/access — the role detail page's
// single merged "effective access" payload (U3): the role's identity +
// capabilities, its OLS/FLS access over every registry object, and the detail
// layouts it is routed to. Three sources, one response, so the page renders in
// one round-trip and the shapes can't drift apart.
//
// It deliberately takes three NARROW ports (satisfied by the role usecase, the
// permission usecase, and the layout repository) rather than the broad domain
// interfaces, so its unit tests wire tiny fakes.
type RoleAccessHandler struct {
	roles   roleDetailGetter
	access  effectiveAccessReader
	layouts layoutAssignmentLister
}

// roleDetailGetter is the identity slice of RoleUseCase this handler needs.
type roleDetailGetter interface {
	GetDetail(ctx context.Context, orgID, id uuid.UUID) (*domain.RoleDetail, error)
}

// effectiveAccessReader is the OLS+FLS slice of PermissionUseCase it needs.
type effectiveAccessReader interface {
	EffectiveAccess(ctx context.Context, orgID, roleID uuid.UUID) ([]domain.RoleObjectAccess, error)
}

// layoutAssignmentLister is the layout slice of ObjectLayoutRepository it needs.
type layoutAssignmentLister interface {
	ListOrgRoleLayoutAssignments(ctx context.Context, orgID, roleID uuid.UUID) ([]domain.RoleLayoutAssignment, error)
}

func NewRoleAccessHandler(roles roleDetailGetter, access effectiveAccessReader, layouts layoutAssignmentLister) *RoleAccessHandler {
	return &RoleAccessHandler{roles: roles, access: access, layouts: layouts}
}

// Get handles GET /api/roles/:id/access (roles.manage-gated in the router).
func (h *RoleAccessHandler) Get(c *gin.Context) {
	orgID := c.MustGet("org_id").(uuid.UUID)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"data": nil, "error": "invalid role id"})
		return
	}
	ctx := c.Request.Context()

	role, err := h.roles.GetDetail(ctx, orgID, id)
	if err != nil {
		handleAppError(c, err) // 404 when the role isn't visible to the org
		return
	}
	objects, err := h.access.EffectiveAccess(ctx, orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	if objects == nil {
		objects = []domain.RoleObjectAccess{}
	}
	layouts, err := h.layouts.ListOrgRoleLayoutAssignments(ctx, orgID, id)
	if err != nil {
		handleAppError(c, err)
		return
	}
	if layouts == nil {
		layouts = []domain.RoleLayoutAssignment{}
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"role":    role,
		"objects": objects,
		"layouts": layouts,
	}, "error": nil})
}
