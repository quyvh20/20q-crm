package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WorkspaceHandler struct {
	workspaceUC domain.WorkspaceUseCase
}

func NewWorkspaceHandler(workspaceUC domain.WorkspaceUseCase) *WorkspaceHandler {
	return &WorkspaceHandler{workspaceUC: workspaceUC}
}

func (h *WorkspaceHandler) ListMembers(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	members, err := h.workspaceUC.ListMembers(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(members))
}

func (h *WorkspaceHandler) InviteMember(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.InviteMemberInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	member, err := h.workspaceUC.InviteMember(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusCreated, domain.Success(member))
}

func (h *WorkspaceHandler) UpdateMemberRole(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	targetUserID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid user id"))
		return
	}

	var input domain.UpdateMemberRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.workspaceUC.UpdateMemberRole(c.Request.Context(), orgID, targetUserID, input); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "role updated"}))
}

func (h *WorkspaceHandler) RemoveMember(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	targetUserID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid user id"))
		return
	}

	if err := h.workspaceUC.RemoveMember(c.Request.Context(), orgID, targetUserID); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "member removed"}))
}
