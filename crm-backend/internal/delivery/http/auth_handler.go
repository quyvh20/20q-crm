package http

import (
	"crypto/rand"
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
)

// sameSiteMode maps the configured policy to the http enum. Defaults to Lax.
func sameSiteMode(s string) http.SameSite {
	switch strings.ToLower(s) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
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
	mode := sameSiteMode(cfg.CookieSameSite)
	secure := cfg.CookieSecure
	// SameSite=None is invalid without Secure — browsers drop the cookie — so
	// force Secure in that case rather than silently failing.
	if mode == http.SameSiteNoneMode {
		secure = true
	}
	c.SetSameSite(mode)
	c.SetCookie(refreshCookieName, refreshToken, refreshCookieMaxAge, "/api/auth", cfg.CookieDomain, secure, true)
	c.SetCookie(csrfCookieName, newCSRFToken(), refreshCookieMaxAge, "/", cfg.CookieDomain, secure, false)
}

// clearAuthCookies expires both cookies (logout / failed refresh).
func clearAuthCookies(c *gin.Context, cfg *config.Config) {
	mode := sameSiteMode(cfg.CookieSameSite)
	secure := cfg.CookieSecure
	if mode == http.SameSiteNoneMode {
		secure = true
	}
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
	}))
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

	resp, err := h.authUC.SwitchWorkspace(c.Request.Context(), userID, input)
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

func (h *AuthHandler) GoogleLogin(c *gin.Context) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	state := hex.EncodeToString(b)

	url := h.authUC.GetGoogleAuthURL(state)
	if url == "" {
		c.JSON(http.StatusServiceUnavailable, domain.Err("Google OAuth not configured"))
		return
	}

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
	// access token travels in the URL, for the SPA to pick up into memory. (P2)
	setAuthCookies(c, h.cfg, resp.RefreshToken)
	redirectURL := fmt.Sprintf("%s/auth/callback?access_token=%s",
		frontendURL, url.QueryEscape(resp.AccessToken))
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
