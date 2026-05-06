package http

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"

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

	c.JSON(http.StatusCreated, domain.Success(resp))
}

func (h *AuthHandler) Login(c *gin.Context) {
	var input domain.LoginInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	resp, err := h.authUC.Login(c.Request.Context(), input)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var input domain.RefreshInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	resp, err := h.authUC.RefreshToken(c.Request.Context(), input.RefreshToken)
	if err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(resp))
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var input domain.RefreshInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if err := h.authUC.Logout(c.Request.Context(), input.RefreshToken); err != nil {
		handleAppError(c, err)
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"message": "logged out successfully"}))
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
	redirectURL := fmt.Sprintf("%s/auth/callback?access_token=%s&refresh_token=%s",
		frontendURL, url.QueryEscape(resp.AccessToken), url.QueryEscape(resp.RefreshToken))
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func handleAppError(c *gin.Context, err error) {
	if appErr, ok := err.(*domain.AppError); ok {
		c.JSON(appErr.Code, domain.Err(appErr.Message))
		return
	}
	c.JSON(http.StatusInternalServerError, domain.Err(fmt.Sprintf("internal server error: %v", err)))
}
