package http

import (
	"encoding/json"
	"errors"
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
		var budgetErr ai.ErrBudgetExceeded
		var timeoutErr ai.ErrAITimeout
		var planErr ai.ErrFeatureNotInPlan

		if errors.As(err, &budgetErr) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": budgetErr.Error(), "code": "budget_exceeded",
				"reset_at": budgetErr.ResetAt,
			})
			return
		}
		if errors.As(err, &timeoutErr) {
			c.Header("Retry-After", fmt.Sprintf("%d", timeoutErr.After))
			c.JSON(http.StatusServiceUnavailable, domain.Err(timeoutErr.Error()))
			return
		}
		if errors.As(err, &planErr) {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error": planErr.Error(), "code": "feature_not_in_plan",
				"requires_plan": planErr.RequiresPlan,
			})
			return
		}

		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}
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
