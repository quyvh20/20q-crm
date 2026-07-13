package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AuthHandler struct {
	authUC domain.AuthUseCase
	cfg    *config.Config
}

func NewAuthHandler(authUC domain.AuthUseCase, cfg *config.Config) *AuthHandler {
	return &AuthHandler{authUC: authUC, cfg: cfg}
}

const (
	refreshCookieName = "refresh_token"
	csrfCookieName    = "csrf_token"
	// refreshCookieMaxAge mirrors usecase.refreshTokenDuration (7 days). Kept in
	// seconds for http cookies.
	refreshCookieMaxAge = 7 * 24 * 60 * 60
	// oauthStateCookieName binds the OAuth `state` to the browser that started
	// the flow; the callback must present the same value or it is a forged
	// (CSRF'd) login. Short-lived: the Google round-trip takes seconds.
	oauthStateCookieName   = "oauth_state"
	oauthStateCookieMaxAge = 5 * 60
)

// requestIsHTTPS reports whether the request reached us over TLS, accounting for
// a terminating proxy (Railway/Cloudflare) that sets X-Forwarded-Proto.
func requestIsHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// cookiePolicy chooses the SameSite mode + Secure flag for the auth cookies,
// per-request so no env var is required. Any HTTPS request (production, where the
// SPA and API are typically cross-site — Cloudflare Pages + a separate API host)
// gets SameSite=None; Secure, without which the browser won't send the refresh
// cookie on cross-origin requests and login silently loops back to the sign-in
// page. Plain-http local dev gets Lax. An explicit COOKIE_SAMESITE=strict is
// honored (locks the cookie to same-site).
func cookiePolicy(c *gin.Context, cfg *config.Config) (http.SameSite, bool) {
	if strings.EqualFold(cfg.CookieSameSite, "strict") {
		return http.SameSiteStrictMode, cfg.CookieSecure
	}
	if requestIsHTTPS(c) {
		return http.SameSiteNoneMode, true
	}
	if strings.EqualFold(cfg.CookieSameSite, "none") {
		return http.SameSiteNoneMode, true
	}
	return http.SameSiteLaxMode, cfg.CookieSecure
}

func newCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// setAuthCookies writes the httpOnly refresh-token cookie (scoped to /api/auth)
// and the readable csrf_token cookie used by the double-submit check. Called on
// every token-minting response (login, register, refresh, switch, OAuth). P2.
func setAuthCookies(c *gin.Context, cfg *config.Config, refreshToken string) {
	mode, secure := cookiePolicy(c, cfg)
	c.SetSameSite(mode)
	c.SetCookie(refreshCookieName, refreshToken, refreshCookieMaxAge, "/api/auth", cfg.CookieDomain, secure, true)
	c.SetCookie(csrfCookieName, newCSRFToken(), refreshCookieMaxAge, "/", cfg.CookieDomain, secure, false)
}

// clearAuthCookies expires both cookies (logout / failed refresh).
func clearAuthCookies(c *gin.Context, cfg *config.Config) {
	mode, secure := cookiePolicy(c, cfg)
	c.SetSameSite(mode)
	c.SetCookie(refreshCookieName, "", -1, "/api/auth", cfg.CookieDomain, secure, true)
	c.SetCookie(csrfCookieName, "", -1, "/", cfg.CookieDomain, secure, false)
}

// refreshTokenFromRequest resolves the refresh token from the httpOnly cookie
// (normal path) or the JSON body (the one-release localStorage shim / non-browser
// clients). Cookie wins when both are present.
func refreshTokenFromRequest(c *gin.Context, bodyToken string) string {
	if cookie, err := c.Cookie(refreshCookieName); err == nil && cookie != "" {
		return cookie
	}
	return bodyToken
}

func (h *AuthHandler) Register(c *gin.Context) {
	var input domain.RegisterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	resp, err := h.authUC.Register(c.Request.Context(), input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusCreated, domain.Success(resp))
}

func (h *AuthHandler) Login(c *gin.Context) {
	var input domain.LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	resp, err := h.authUC.Login(c.Request.Context(), input, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}

	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	// Body is optional — the refresh token normally rides in the httpOnly cookie.
	// A bind error (empty/missing body) is expected on the cookie path, so ignore
	// it and resolve the token from cookie-or-body below.
	var input domain.RefreshInput
	_ = c.ShouldBindJSON(&input)
	input.RefreshToken = refreshTokenFromRequest(c, input.RefreshToken)
	if input.RefreshToken == "" {
		c.JSON(http.StatusUnauthorized, domain.Err("missing refresh token"))
		return
	}

	resp, err := h.authUC.RefreshToken(c.Request.Context(), input, requestMeta(c))
	if err != nil {
		// The requested workspace is gone but the session is still valid: DON'T
		// clear cookies — the SPA retries a plain refresh into its default/first org
		// and routes to the chooser (R2 fail-closed, P3).
		if ouErr, ok := err.(*domain.OrgUnavailableError); ok {
			c.JSON(http.StatusConflict, gin.H{
				"code":       "ORG_UNAVAILABLE",
				"workspaces": ouErr.Workspaces,
				"error":      "You no longer have access to that workspace.",
			})
			return
		}
		// Clear the cookies so a rotated/revoked session doesn't linger and keep
		// bouncing off the server.
		clearAuthCookies(c, h.cfg)
		handleAppError(c, err)
		return
	}

	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var input domain.RefreshInput
	_ = c.ShouldBindJSON(&input)
	token := refreshTokenFromRequest(c, input.RefreshToken)
	if token != "" {
		_ = h.authUC.Logout(c.Request.Context(), token)
	}
	clearAuthCookies(c, h.cfg)
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "logged out successfully"}))
}

// requestMeta pulls transport-level detail for the auth event log.
func requestMeta(c *gin.Context) domain.RequestMeta {
	return domain.RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()}
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var input domain.ForgotPasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	debugToken, err := h.authUC.ForgotPassword(c.Request.Context(), input, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}

	// Always the same message — never reveal whether the email exists.
	resp := gin.H{"message": "If an account exists for that email, a password reset link has been sent."}
	if debugToken != nil {
		resp["debug_token"] = *debugToken
	}
	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var input domain.ResetPasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.authUC.ResetPassword(c.Request.Context(), input, requestMeta(c)); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "Your password has been reset. Please sign in with your new password."}))
}

func (h *AuthHandler) VerifyEmail(c *gin.Context) {
	var input domain.VerifyEmailInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.authUC.VerifyEmail(c.Request.Context(), input); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "Your email address has been verified."}))
}

func (h *AuthHandler) ResendVerification(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	debugToken, err := h.authUC.ResendVerification(c.Request.Context(), userID, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}

	resp := gin.H{"message": "Verification email sent."}
	if debugToken != nil {
		resp["debug_token"] = *debugToken
	}
	c.JSON(http.StatusOK, domain.Success(resp))
}

// ListSessions returns the caller's active devices/sessions (P4). The current
// session is flagged via the refresh cookie presented with the request.
func (h *AuthHandler) ListSessions(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	current, _ := c.Cookie(refreshCookieName)
	sessions, err := h.authUC.ListSessions(c.Request.Context(), userID, current)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(sessions))
}

// RevokeSession revokes one of the caller's own sessions by id (P4).
func (h *AuthHandler) RevokeSession(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid session id"))
		return
	}
	if err := h.authUC.RevokeSession(c.Request.Context(), userID, orgID, sessionID); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "session revoked"}))
}

// SignOutEverywhere kills every other session and re-mints this one (P4), setting
// fresh auth cookies so the current device stays signed in.
func (h *AuthHandler) SignOutEverywhere(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	current := refreshTokenFromRequest(c, "")
	resp, err := h.authUC.SignOutEverywhere(c.Request.Context(), userID, orgID, current)
	if err != nil {
		handleAppError(c, err)
		return
	}
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	user, err := h.authUC.GetMe(c.Request.Context(), userID)
	if err != nil {
		handleAppError(c, err)
		return
	}

	workspaces, _ := h.authUC.ListWorkspaces(c.Request.Context(), userID)

	c.JSON(http.StatusOK, domain.Success(gin.H{
		"user":       user,
		"workspaces": workspaces,
		// Which sign-in methods exist (U2 Connected accounts) — the raw hash and
		// google_id are json:"-", so the SPA needs these computed flags.
		"auth_methods": gin.H{
			"password": user.PasswordHash != nil,
			"google":   user.GoogleID != nil,
		},
	}))
}

// PATCH /api/auth/me — self-serve profile + preferences (U2).
func (h *AuthHandler) UpdateMe(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	var input domain.UpdateProfileInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	user, err := h.authUC.UpdateProfile(c.Request.Context(), userID, input)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"user": user}))
}

// POST /api/auth/change-password — in-app rotation; requires the current
// password, signs out every other device, re-mints this one (U2).
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	var input domain.ChangePasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	resp, err := h.authUC.ChangePassword(c.Request.Context(), userID, orgID, input, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

// POST /api/auth/set-password — OAuth-only accounts add a password (U2).
func (h *AuthHandler) SetPassword(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	orgID, _ := GetOrgID(c)
	var input domain.SetPasswordInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	resp, err := h.authUC.SetPassword(c.Request.Context(), userID, orgID, input, requestMeta(c))
	if err != nil {
		handleAppError(c, err)
		return
	}
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

// POST /api/auth/unlink-google — disconnect Google sign-in (U2; refused while
// no password exists).
func (h *AuthHandler) UnlinkGoogle(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	if err := h.authUC.UnlinkGoogle(c.Request.Context(), userID, requestMeta(c)); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "Google sign-in disconnected"}))
}

func (h *AuthHandler) SwitchWorkspace(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var input domain.SwitchWorkspaceInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	// Thread device meta + the presented refresh token so SwitchWorkspace can stamp
	// the new session's device row and revoke the old credential (switch hygiene, P3).
	resp, err := h.authUC.SwitchWorkspace(c.Request.Context(), userID, input, requestMeta(c), refreshTokenFromRequest(c, ""))
	if err != nil {
		handleAppError(c, err)
		return
	}

	setAuthCookies(c, h.cfg, resp.RefreshToken)
	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) ListWorkspaces(c *gin.Context) {
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	workspaces, err := h.authUC.ListWorkspaces(c.Request.Context(), userID)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(workspaces))
}

// oauthStateCookiePolicy is cookiePolicy with Strict downgraded to Lax: the
// state cookie must survive the top-level cross-site redirect BACK from Google,
// which SameSite=Strict blocks (Lax sends cookies on top-level GET navigations).
func oauthStateCookiePolicy(c *gin.Context, cfg *config.Config) (http.SameSite, bool) {
	mode, secure := cookiePolicy(c, cfg)
	if mode == http.SameSiteStrictMode {
		mode = http.SameSiteLaxMode
	}
	return mode, secure
}

func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	url := h.authUC.GetGoogleAuthURL(state)
	if url == "" {
		c.JSON(http.StatusServiceUnavailable, domain.Err("Google OAuth not configured"))
		return
	}

	// Persist the state so GoogleCallback can validate it — a state that is
	// generated but never checked is no CSRF protection at all (P10 P1).
	mode, secure := oauthStateCookiePolicy(c, h.cfg)
	c.SetSameSite(mode)
	c.SetCookie(oauthStateCookieName, state, oauthStateCookieMaxAge, "/api/auth", h.cfg.CookieDomain, secure, true)

	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (h *AuthHandler) GoogleCallback(c *gin.Context) {
	frontendURL := h.cfg.FrontendURL
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}

	// Google sends ?error=access_denied when user denies consent
	if errMsg := c.Query("error"); errMsg != "" {
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("%s/login?error=%s", frontendURL, errMsg))
		return
	}

	// Validate the OAuth state against the cookie set in GoogleLogin. The cookie
	// is single-use (cleared here regardless of outcome). Without this check an
	// attacker can silently log the victim into an attacker-controlled session
	// (login CSRF) by sending them a crafted callback URL.
	stateCookie, cookieErr := c.Cookie(oauthStateCookieName)
	mode, secure := oauthStateCookiePolicy(c, h.cfg)
	c.SetSameSite(mode)
	c.SetCookie(oauthStateCookieName, "", -1, "/api/auth", h.cfg.CookieDomain, secure, true)
	state := c.Query("state")
	if cookieErr != nil || stateCookie == "" || state == "" ||
		subtle.ConstantTimeCompare([]byte(stateCookie), []byte(state)) != 1 {
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("%s/login?error=invalid_oauth_state", frontendURL))
		return
	}

	code := c.Query("code")
	if code == "" {
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("%s/login?error=missing_code", frontendURL))
		return
	}

	resp, err := h.authUC.GoogleLogin(c.Request.Context(), code)
	if err != nil {
		log.Printf("[GoogleCallback] GoogleLogin error: %v", err)
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("%s/login?error=google_login_failed&detail=%s", frontendURL, url.QueryEscape(err.Error())))
		return
	}

	log.Printf("[GoogleCallback] Success for user %s, redirecting to %s", resp.User.Email, frontendURL)
	// Set the refresh token as an httpOnly cookie server-side rather than leaking
	// it in the redirect URL (which lands in history/logs). Only the short-lived
	// access token travels back — in the URL FRAGMENT (never sent to servers, kept
	// out of access logs / the Referer header), not the query string (P3). The
	// needs_chooser flag rides in the query so the callback page can route a
	// multi-org user to the chooser after applying the same server-side selection.
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	redirectURL := fmt.Sprintf("%s/auth/callback?needs_chooser=%t#access_token=%s",
		frontendURL, resp.NeedsChooser, url.QueryEscape(resp.AccessToken))
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func handleAppError(c *gin.Context, err error) {
	if appErr, ok := err.(*domain.AppError); ok {
		if appErr.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(appErr.RetryAfter))
		}
		c.JSON(appErr.Code, domain.Err(appErr.Message))
		return
	}
	c.JSON(http.StatusInternalServerError, domain.Err(fmt.Sprintf("internal server error: %v", err)))
}
