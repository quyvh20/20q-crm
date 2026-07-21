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

	// ?scope=teammates narrows the list to the people the caller shares a team with
	// — the assignee set a 'team'-scoped role may pick from (U6.1). Any member may
	// ask; the full roster is already readable on this route.
	var (
		members []domain.MemberInfo
		err     error
	)
	if c.Query("scope") == "teammates" {
		userID, _ := GetUserID(c)
		members, err = h.workspaceUC.ListTeammates(c.Request.Context(), orgID, userID)
	} else {
		members, err = h.workspaceUC.ListMembers(c.Request.Context(), orgID)
	}
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

// ListMyInvitations handles GET /api/auth/me/invitations — the pending invitations
// addressed to the authenticated user's OWN account email, across workspaces, for
// the post-OAuth / zero-workspace "you've been invited to X" consent surface (U4
// item 6). Runs under AuthMiddlewareOptionalOrg so a zero-membership caller reaches it.
func (h *WorkspaceHandler) ListMyInvitations(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	invites, err := h.workspaceUC.ListMyInvitations(c.Request.Context(), userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(invites))
}

// AcceptMyInvitation handles POST /api/auth/me/invitations/:id/accept — the caller
// accepts one of their OWN pending invitations (authorized by the email match, not
// id possession) and is signed straight into the joined workspace (U4 item 6). Runs
// under AuthMiddlewareOptionalOrg so a zero-membership caller reaches it.
func (h *WorkspaceHandler) AcceptMyInvitation(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	invID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid invitation id"))
		return
	}
	orgID, err := h.workspaceUC.AcceptMyInvitation(c.Request.Context(), userID, invID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	// Mint a session scoped to the newly-joined workspace so the SPA lands the user
	// in it (the membership now exists, so IssueSessionForUser succeeds).
	resp, err := h.authUC.IssueSessionForUser(c.Request.Context(), userID, orgID, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
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

	result, err := h.workspaceUC.RemoveMember(c.Request.Context(), orgID, targetUserID, input)
	if err != nil {
		// The target still owns records and no strategy was chosen: answer with a
		// machine-readable code + real counts so the SPA opens the reassignment
		// dialog off the CODE, never a message substring (U0.2).
		var reassign *domain.ReassignmentRequiredError
		if errors.As(err, &reassign) {
			// routing_sources existed on the error struct but rode ONLY inside the
			// free-text message, which this handler's own contract forbids the SPA
			// from parsing — so the dialog could never show it. It gets its own key.
			routing := reassign.RoutingSources
			if routing == nil {
				routing = []string{}
			}
			c.JSON(http.StatusConflict, gin.H{
				"code":  domain.CodeReassignmentRequired,
				"error": reassign.Error(),
				// custom = the custom-object records they own (U6.3). Dropping it here
				// would under-report the impact in the very dialog where the admin decides.
				"owned":           gin.H{"contacts": reassign.Contacts, "deals": reassign.Deals, "custom": reassign.Custom},
				"routing_sources": routing,
			})
			return
		}
		handleAppError(c, err)
		return
	}

	// Removal succeeded. The routing disclosure ships on THIS path too, and that is
	// the point: a member who owns no records never triggers the 409 at all — and a
	// recently-added SDR who is on the rotation but has not closed anything yet is
	// the single commonest offboarding there is. They were being removed in total
	// silence while their lead sources went ownerless.
	c.JSON(http.StatusOK, domain.Success(gin.H{
		"message":                 "member removed",
		"routing_sources_cleared": result.RoutingSourcesCleared,
	}))
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

// GetMemberDetail handles GET /api/workspaces/members/:user_id — the member
// drawer payload (identity + groups + owned counts + sessions) (U4).
func (h *WorkspaceHandler) GetMemberDetail(c *gin.Context) {
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
	detail, err := h.workspaceUC.GetMemberDetail(c.Request.Context(), orgID, targetUserID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(detail))
}

// ForceSignOutMember handles DELETE /api/workspaces/members/:user_id/sessions —
// the admin "sign this person out everywhere" action (U4).
func (h *WorkspaceHandler) ForceSignOutMember(c *gin.Context) {
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
	if err := h.workspaceUC.ForceSignOutMember(c.Request.Context(), orgID, callerID, targetUserID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "member signed out of all sessions"}))
}

// GetCurrentWorkspace handles GET /api/workspaces/current — the Workspace General
// page payload (U4). Any member may read it.
func (h *WorkspaceHandler) GetCurrentWorkspace(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	callerID, _ := GetUserID(c)
	detail, err := h.workspaceUC.GetCurrentWorkspace(c.Request.Context(), orgID, callerID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(detail))
}

// UpdateWorkspace handles PATCH /api/workspaces/current — rename + defaults (U4).
// org.settings-gated at the route.
func (h *WorkspaceHandler) UpdateWorkspace(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var input domain.UpdateWorkspaceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	if err := h.workspaceUC.UpdateWorkspace(c.Request.Context(), orgID, input); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "workspace updated"}))
}

// DeleteWorkspace handles DELETE /api/workspaces/current — soft-delete the whole
// workspace (U4). Owner-only, verified in the usecase.
func (h *WorkspaceHandler) DeleteWorkspace(c *gin.Context) {
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
	if err := h.workspaceUC.DeleteWorkspace(c.Request.Context(), orgID, callerID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "workspace deleted"}))
}

// LeaveWorkspace handles POST /api/workspaces/leave — the caller leaves the
// current workspace (U4). Any member; the sole owner is refused.
func (h *WorkspaceHandler) LeaveWorkspace(c *gin.Context) {
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
	if err := h.workspaceUC.LeaveWorkspace(c.Request.Context(), orgID, callerID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "left workspace"}))
}

// CreateWorkspace handles POST /api/workspaces — an already-signed-in user
// creates a NEW workspace they own and is switched into it (U4). Mints the
// session + sets the auth cookies like Register.
func (h *WorkspaceHandler) CreateWorkspace(c *gin.Context) {
	callerID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var input domain.CreateWorkspaceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	resp, err := h.authUC.CreateWorkspace(c.Request.Context(), callerID, input, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusCreated, domain.Success(resp))
}
