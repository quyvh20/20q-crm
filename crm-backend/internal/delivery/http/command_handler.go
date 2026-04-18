package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CommandHandler handles POST /api/ai/command (SSE).
type CommandHandler struct {
	commandCenter *ai.CommandCenter
}

func NewCommandHandler(cc *ai.CommandCenter) *CommandHandler {
	return &CommandHandler{commandCenter: cc}
}

type commandRequest struct {
	SessionID     string                  `json:"session_id"`
	Message       string                  `json:"message" binding:"required"`
	History       []ai.HistoryMessage     `json:"history,omitempty"`
	Workspaces    []ai.WorkspaceInfo      `json:"workspaces,omitempty"`
	Context       *struct {
		Page     string `json:"page"`
		EntityID string `json:"entity_id"`
	} `json:"context,omitempty"`
	Confirmed     bool            `json:"confirmed,omitempty"`
	ConfirmedTool string          `json:"confirmed_tool,omitempty"`
	ConfirmedArgs json.RawMessage `json:"confirmed_args,omitempty"`
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
	role, _ := GetRole(c)

	var req commandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	// Parse or generate session ID
	sessionID := uuid.New()
	if req.SessionID != "" {
		if parsed, err := uuid.Parse(req.SessionID); err == nil {
			sessionID = parsed
		}
	}

	// Trim history to last 10 turns server-side as well
	history := req.History
	if len(history) > 10 {
		history = history[len(history)-10:]
	}

	ccReq := ai.CommandRequest{
		SessionID:     sessionID,
		UserMessage:   req.Message,
		History:       history,
		UserRole:      role,
		Workspaces:    req.Workspaces,
		Confirmed:     req.Confirmed,
		ConfirmedTool: req.ConfirmedTool,
		ConfirmedArgs: req.ConfirmedArgs,
	}

	events, err := h.commandCenter.Execute(c.Request.Context(), orgID, userID, ccReq)
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
		// Sanitize data to avoid double-newlines breaking the SSE frame
		sanitized := strings.ReplaceAll(string(data), "\n", "\\n")
		fmt.Fprintf(c.Writer, "data: %s\n\n", sanitized)
		flush()
	}
}
