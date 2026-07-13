package http

import (
	"errors"
	"net/http"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WorkspaceHandler struct {
	workspaceUC domain.WorkspaceUseCase
	// authUC + cfg back invite-accept auto-login (U4): after a successful accept
	// the handler mints a session for the invitee and sets the auth cookies, so
	// they land in the app signed in instead of being bounced to /login.
	authUC domain.AuthUseCase
	cfg    *config.Config
}

func NewWorkspaceHandler(workspaceUC domain.WorkspaceUseCase, authUC domain.AuthUseCase, cfg *config.Config) *WorkspaceHandler {
	return &WorkspaceHandler{workspaceUC: workspaceUC, authUC: authUC, cfg: cfg}
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

// GetInvitationPreview handles GET /api/auth/invitations/:token — the public
// accept-page metadata (org/role/email + validity + whether the account exists)
// so the invitee sees "Join Acme as Sales Rep" before committing (U4). The raw
// token is the credential; a bad token yields a clean Status "invalid", not a 404.
func (h *WorkspaceHandler) GetInvitationPreview(c *gin.Context) {
	preview, err := h.workspaceUC.GetInvitationPreview(c.Request.Context(), c.Param("token"))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(preview))
}

func (h *WorkspaceHandler) AcceptInvite(c *gin.Context) {
	var input domain.AcceptInviteInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	result, err := h.workspaceUC.AcceptInvite(c.Request.Context(), input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	// Auto-login (U4): mint a session ONLY for a brand-new account (result.AutoLogin)
	// so they land in the app signed in. An EXISTING account is never auto-logged-in
	// from an invite link — it adds the workspace and signs in normally, so a leaked
	// link can't silently take over a whole multi-workspace account. If the mint
	// fails (e.g. a suspended membership), fall back to the plain "accepted"
	// response so the accept itself still succeeds and they can sign in manually.
	if result.AutoLogin {
		if resp, sErr := h.authUC.IssueSessionForUser(c.Request.Context(), result.UserID, result.OrgID, requestMeta(c)); sErr == nil {
			setAuthCookies(c, h.cfg, resp.RefreshToken)
			c.JSON(http.StatusOK, domain.Success(resp))
			return
		}
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
		// The target still owns records and no strategy was chosen: answer with a
		// machine-readable code + real counts so the SPA opens the reassignment
		// dialog off the CODE, never a message substring (U0.2).
		var reassign *domain.ReassignmentRequiredError
		if errors.As(err, &reassign) {
			c.JSON(http.StatusConflict, gin.H{
				"code":  domain.CodeReassignmentRequired,
				"error": reassign.Error(),
				"owned": gin.H{"contacts": reassign.Contacts, "deals": reassign.Deals},
			})
			return
		}
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
