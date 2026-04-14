package http

import (
	"net/http"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
)

// KnowledgeHandler handles /api/knowledge-base routes.
type KnowledgeHandler struct {
	uc domain.KnowledgeBaseUseCase
}

func NewKnowledgeHandler(uc domain.KnowledgeBaseUseCase) *KnowledgeHandler {
	return &KnowledgeHandler{uc: uc}
}

// GET /api/knowledge-base
func (h *KnowledgeHandler) ListSections(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	entries, err := h.uc.ListSections(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(entries))
}

// GET /api/knowledge-base/ai-prompt
func (h *KnowledgeHandler) GetAIPrompt(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	prompt, err := h.uc.GetAIPrompt(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(gin.H{"prompt": prompt}))
}

// GET /api/knowledge-base/:section
func (h *KnowledgeHandler) GetSection(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	section := c.Param("section")
	entry, err := h.uc.GetSection(c.Request.Context(), orgID, section)
	if err != nil {
		c.JSON(http.StatusNotFound, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(entry))
}

// PUT /api/knowledge-base/:section
func (h *KnowledgeHandler) UpsertSection(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, ok := GetUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	section := c.Param("section")

	var input domain.UpsertKBInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	entry, err := h.uc.UpsertSection(c.Request.Context(), orgID, userID, section, input)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(entry))
}
