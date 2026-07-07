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
	var input domain.AcceptInviteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.workspaceUC.AcceptInvite(c.Request.Context(), input); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "invitation accepted"}))
}

// ListInvitations returns the org's pending invitations for the members panel (P2).
func (h *WorkspaceHandler) ListInvitations(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	invites, err := h.workspaceUC.ListInvitations(c.Request.Context(), orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(invites))
}

// ResendInvitation re-mints and re-sends a pending invitation (P2).
func (h *WorkspaceHandler) ResendInvitation(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	invID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid invitation id"))
		return
	}
	debugToken, err := h.workspaceUC.ResendInvitation(c.Request.Context(), orgID, invID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	resp := gin.H{"message": "invitation resent"}
	if debugToken != nil {
		resp["debug_token"] = *debugToken
	}
	c.JSON(http.StatusOK, domain.Success(resp))
}

// RevokeInvitation kills a pending invitation (P2).
func (h *WorkspaceHandler) RevokeInvitation(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	invID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid invitation id"))
		return
	}
	if err := h.workspaceUC.RevokeInvitation(c.Request.Context(), orgID, invID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "invitation revoked"}))
}

// SendMemberResetLink emails a member a password-reset link on the admin's behalf
// (P2). The admin never sees or sets the password.
func (h *WorkspaceHandler) SendMemberResetLink(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	callerID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	targetUserID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid user id"))
		return
	}
	if err := h.workspaceUC.SendMemberResetLink(c.Request.Context(), orgID, callerID, targetUserID, requestMeta(c)); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "We've emailed this member a password reset link."}))
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
	callerID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	targetUserID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid user id"))
		return
	}

	if err := h.workspaceUC.TransferOwnership(c.Request.Context(), orgID, callerID, targetUserID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "ownership transferred"}))
}
