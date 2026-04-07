package http

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

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

// POST /api/auth/register
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

// POST /api/auth/login
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

// POST /api/auth/refresh
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

// POST /api/auth/logout
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

// GET /api/auth/me
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

	c.JSON(http.StatusOK, domain.Success(user))
}

// GET /api/auth/google → redirect to consent screen
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

// GET /api/auth/google/callback
func (h *AuthHandler) GoogleCallback(c *gin.Context) {
	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, domain.Err("missing authorization code"))
		return
	}

	resp, err := h.authUC.GoogleLogin(c.Request.Context(), code)
	if err != nil {
		handleAppError(c, err)
		return
	}

	// Redirect to frontend with tokens
	frontendURL := h.cfg.FrontendURL
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}

	redirectURL := fmt.Sprintf("%s/auth/callback?access_token=%s&refresh_token=%s",
		frontendURL, resp.AccessToken, resp.RefreshToken)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

// ============================================================
// Error Helper
// ============================================================

func handleAppError(c *gin.Context, err error) {
	if appErr, ok := err.(*domain.AppError); ok {
		c.JSON(appErr.Code, domain.Err(appErr.Message))
		return
	}
	c.JSON(http.StatusInternalServerError, domain.Err("internal server error"))
}
