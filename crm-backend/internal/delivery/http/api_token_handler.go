package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// APITokenHandler serves a user's OWN personal access tokens (U6.5). Every route
// is self-scoped — the user id comes from the session, never from the request — so
// there is no capability gate and no way to address someone else's tokens.
type APITokenHandler struct {
	uc domain.APITokenUseCase
}

func NewAPITokenHandler(uc domain.APITokenUseCase) *APITokenHandler {
	return &APITokenHandler{uc: uc}
}

// List handles GET /api/auth/api-tokens.
func (h *APITokenHandler) List(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, _ := GetUserID(c)
	tokens, err := h.uc.List(c.Request.Context(), orgID, userID)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": tokens, "error": nil})
}

// Create handles POST /api/auth/api-tokens. The response carries the plaintext
// secret — the only time it ever exists outside the caller's machine.
func (h *APITokenHandler) Create(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, _ := GetUserID(c)

	var in domain.CreateAPITokenInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}
	created, err := h.uc.Create(c.Request.Context(), orgID, userID, in)
	if err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": created, "error": nil})
}

// Revoke handles DELETE /api/auth/api-tokens/:id.
func (h *APITokenHandler) Revoke(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, _ := GetUserID(c)
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid token id"))
		return
	}
	if err := h.uc.Revoke(c.Request.Context(), orgID, userID, id); err != nil {
		handleAppError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": "revoked", "error": nil})
}
