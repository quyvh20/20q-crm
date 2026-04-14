package http

import (
	"encoding/json"
	"fmt"
	"net/http"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
)

// CommandHandler handles POST /api/ai/command (SSE).
type CommandHandler struct {
	commandCenter *ai.CommandCenter
}

func NewCommandHandler(cc *ai.CommandCenter) *CommandHandler {
	return &CommandHandler{commandCenter: cc}
}

type commandRequest struct {
	Message string `json:"message" binding:"required"`
	Context *struct {
		Page     string `json:"page"`
		EntityID string `json:"entity_id"`
	} `json:"context,omitempty"`
}

// Command handles SSE-streamed AI command execution.
func (h *CommandHandler) Command(c *gin.Context) {
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

	var req commandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	events, err := h.commandCenter.Execute(c.Request.Context(), orgID, userID, req.Message)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")
	c.Status(http.StatusOK)

	flusher, canFlush := c.Writer.(http.Flusher)
	flush := func() {
		if canFlush {
			flusher.Flush()
		}
	}

	for event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flush()
	}
}
