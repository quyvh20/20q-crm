package http

import (
	"net/http"
	"strconv"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ChatSessionHandler handles admin-only chat log endpoints.
type ChatSessionHandler struct {
	repo domain.ChatSessionRepository
}

func NewChatSessionHandler(repo domain.ChatSessionRepository) *ChatSessionHandler {
	return &ChatSessionHandler{repo: repo}
}

// ListSessions — GET /api/ai/sessions
// Admin/owner only. Returns paginated list of all sessions in the org.
func (h *ChatSessionHandler) ListSessions(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := (page - 1) * limit

	sessions, total, err := h.repo.ListSessions(c.Request.Context(), orgID, domain.ChatSessionFilter{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  sessions,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

// GetSessionMessages — GET /api/ai/sessions/:id/messages
// Admin/owner only. Returns full transcript.
func (h *ChatSessionHandler) GetSessionMessages(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid session id"))
		return
	}

	messages, err := h.repo.ListMessages(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": messages})
}

// DeleteSession — DELETE /api/ai/sessions/:id
// Admin/owner only. Hard deletes a session and all its messages.
func (h *ChatSessionHandler) DeleteSession(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid session id"))
		return
	}

	if err := h.repo.DeleteSession(c.Request.Context(), orgID, sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session deleted"})
}

// EndSession — POST /api/ai/sessions/:id/end
// Any authenticated user. Marks the session as ended (New Chat button).
func (h *ChatSessionHandler) EndSession(c *gin.Context) {
	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid session id"))
		return
	}

	if err := h.repo.EndSession(c.Request.Context(), sessionID); err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "session ended"})
}
