package http

import (
	"net/http"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// two_factor_handler.go serves the 2FA endpoints (U6.4). The enrollment routes sit
// behind AuthMiddleware (you must be signed in to add a factor to your own
// account); /2fa/verify is PUBLIC by necessity — it is what turns a login challenge
// into a session, so the caller has no session yet.

// twoFactorChallengeCookie carries the login challenge for the Google redirect
// flow, where there is no JSON response to put it in. Short-lived, httpOnly, and
// scoped to the auth path.
const twoFactorChallengeCookie = "2fa_challenge"

func setTwoFactorChallengeCookie(c *gin.Context, cfg *config.Config, token string) {
	mode, secure := cookiePolicy(c, cfg)
	c.SetSameSite(mode)
	c.SetCookie(twoFactorChallengeCookie, token, int(domain.TwoFactorChallengeTTL.Seconds()), "/api/auth", cfg.CookieDomain, secure, true)
}

func clearTwoFactorChallengeCookie(c *gin.Context, cfg *config.Config) {
	mode, secure := cookiePolicy(c, cfg)
	c.SetSameSite(mode)
	c.SetCookie(twoFactorChallengeCookie, "", -1, "/api/auth", cfg.CookieDomain, secure, true)
}

// StartTwoFactorSetup handles POST /api/auth/2fa/setup — generate the secret + QR.
func (h *AuthHandler) StartTwoFactorSetup(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	setup, err := h.authUC.StartTwoFactorSetup(c.Request.Context(), userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(setup))
}

// EnableTwoFactor handles POST /api/auth/2fa/enable — confirm the code, return the
// backup codes ONCE.
func (h *AuthHandler) EnableTwoFactor(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	var input domain.TwoFactorEnableInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	res, err := h.authUC.EnableTwoFactor(c.Request.Context(), userID, orgID, input.Code, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(res))
}

// DisableTwoFactor handles POST /api/auth/2fa/disable.
func (h *AuthHandler) DisableTwoFactor(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	var input domain.TwoFactorDisableInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	if err := h.authUC.DisableTwoFactor(c.Request.Context(), userID, orgID, input.Code, requestMeta(c)); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success("two-factor authentication turned off"))
}

// RegenerateBackupCodes handles POST /api/auth/2fa/backup-codes.
func (h *AuthHandler) RegenerateBackupCodes(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	var input domain.TwoFactorDisableInput // same shape: a current code
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	res, err := h.authUC.RegenerateBackupCodes(c.Request.Context(), userID, orgID, input.Code, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(res))
}

// GetTwoFactorStatus handles GET /api/auth/2fa — what the Security page renders.
func (h *AuthHandler) GetTwoFactorStatus(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	status, err := h.authUC.GetTwoFactorStatus(c.Request.Context(), userID, orgID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(status))
}

// VerifyTwoFactor handles POST /api/auth/2fa/verify — exchange a login challenge
// for a real session. PUBLIC (there is no session yet) and rate-limited at the
// route; the per-challenge attempt counter in the usecase is the backstop that
// holds even when the IP limiter can't (it fails open with no Redis).
func (h *AuthHandler) VerifyTwoFactor(c *gin.Context) {
	var input domain.TwoFactorVerifyInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	// The Google flow carries the challenge in an httpOnly cookie instead of the body.
	token := input.ChallengeToken
	if token == "" {
		if ck, err := c.Cookie(twoFactorChallengeCookie); err == nil {
			token = ck
		}
	}

	resp, err := h.authUC.VerifyTwoFactor(c.Request.Context(), token, input.Code, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}

	clearTwoFactorChallengeCookie(c, h.cfg)
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

// ResetMemberTwoFactor handles DELETE /api/workspaces/members/:user_id/two-factor
// — the admin break-glass for someone who lost their device AND their backup
// codes. members.manage-gated at the route; audited as an admin action.
func (h *AuthHandler) ResetMemberTwoFactor(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	actorID, _ := GetUserID(c)
	targetID, err := uuid.Parse(c.Param("user_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid user id"))
		return
	}
	if err := h.authUC.ResetMemberTwoFactor(c.Request.Context(), orgID, actorID, targetID, requestMeta(c)); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success("two-factor authentication reset"))
}
