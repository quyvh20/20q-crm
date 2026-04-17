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

	member, debugToken, err := h.workspaceUC.InviteMember(c.Request.Context(), orgID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	response := gin.H{"member": member}
	if debugToken != nil {
		response["debug_token"] = *debugToken
	}

	c.JSON(http.StatusCreated, domain.Success(response))
}

func (h *WorkspaceHandler) AcceptInvite(c *gin.Context) {
	var input struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.workspaceUC.AcceptInvite(c.Request.Context(), input.Token); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "invitation accepted"}))
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

	var input domain.RemoveMemberInput
	// It's a DELETE request so the body might be optional if no reassignment logic applies,
	// but we should attempt to bind it.
	if err := c.ShouldBindJSON(&input); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.workspaceUC.RemoveMember(c.Request.Context(), orgID, targetUserID, input); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "member removed"}))
}

func (h *WorkspaceHandler) SuspendMember(c *gin.Context) {
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

	if err := h.workspaceUC.SuspendMember(c.Request.Context(), orgID, targetUserID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "member suspended"}))
}

func (h *WorkspaceHandler) ReinstateMember(c *gin.Context) {
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

	if err := h.workspaceUC.ReinstateMember(c.Request.Context(), orgID, targetUserID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "member reinstated"}))
}

func (h *WorkspaceHandler) TransferOwnership(c *gin.Context) {
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

	if err := h.workspaceUC.TransferOwnership(c.Request.Context(), orgID, targetUserID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "ownership transferred"}))
}
