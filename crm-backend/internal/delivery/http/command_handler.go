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

// CommandHandler handles POST /api/ai/command (SSE) and POST /api/ai/command-sync (JSON).
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

func (h *CommandHandler) buildCCRequest(c *gin.Context) (uuid.UUID, uuid.UUID, ai.CommandRequest, error) {
	orgID, ok := GetOrgID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, ai.CommandRequest{}, fmt.Errorf("unauthorized: no org_id")
	}
	userID, ok := GetUserID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, ai.CommandRequest{}, fmt.Errorf("unauthorized: no user_id")
	}
	role, _ := GetRole(c)

	var req commandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		return uuid.Nil, uuid.Nil, ai.CommandRequest{}, err
	}

	sessionID := uuid.New()
	if req.SessionID != "" {
		if parsed, err := uuid.Parse(req.SessionID); err == nil {
			sessionID = parsed
		}
	}

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

	return orgID, userID, ccReq, nil
}

func (h *CommandHandler) handleExecuteError(c *gin.Context, err error) {
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
}

// Command handles SSE-streamed AI command execution.
func (h *CommandHandler) Command(c *gin.Context) {
	orgID, userID, ccReq, err := h.buildCCRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	events, err := h.commandCenter.Execute(c.Request.Context(), orgID, userID, ccReq)
	if err != nil {
		h.handleExecuteError(c, err)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()

	for event := range events {
		data, _ := json.Marshal(event)
		sanitized := strings.ReplaceAll(string(data), "\n", "\\n")
		fmt.Fprintf(c.Writer, "data: %s\n\n", sanitized)
		c.Writer.Flush()
	}
}

// CommandSync handles non-streaming AI command execution.
// Returns all events as a JSON array in one response — proxy-safe.
func (h *CommandHandler) CommandSync(c *gin.Context) {
	orgID, userID, ccReq, err := h.buildCCRequest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	eventsCh, err := h.commandCenter.Execute(c.Request.Context(), orgID, userID, ccReq)
	if err != nil {
		h.handleExecuteError(c, err)
		return
	}

	// Collect all events into a slice
	var allEvents []ai.CommandEvent
	for event := range eventsCh {
		allEvents = append(allEvents, event)
	}

	c.JSON(http.StatusOK, gin.H{
		"events": allEvents,
	})
}
