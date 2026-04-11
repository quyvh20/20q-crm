package http

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"

	"crm-backend/internal/ai"

	"github.com/gin-gonic/gin"
	"crm-backend/internal/domain"
)

// AIHandler handles /api/ai routes.
type AIHandler struct {
	gateway  *ai.AIGateway
	budget   *ai.BudgetGuard
	embedSvc *ai.EmbeddingService
}

func NewAIHandler(gateway *ai.AIGateway, budget *ai.BudgetGuard, embedSvc *ai.EmbeddingService) *AIHandler {
	return &AIHandler{gateway: gateway, budget: budget, embedSvc: embedSvc}
}

// ============================================================
// GET /api/ai/usage
// ============================================================

type aiUsageResponse struct {
	Used    int    `json:"used_tokens"`
	Limit   int    `json:"limit_tokens"`
	ResetAt string `json:"reset_at"`
	Percent int    `json:"percent"`
}

func (h *AIHandler) GetUsage(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	used, limit, resetAt := h.budget.GetUsage(c.Request.Context(), orgID)
	pct := 0
	if limit > 0 {
		pct = used * 100 / limit
	}

	c.JSON(http.StatusOK, domain.Success(aiUsageResponse{
		Used:    used,
		Limit:   limit,
		ResetAt: resetAt.Format("2006-01-02"),
		Percent: pct,
	}))
}

// ============================================================
// POST /api/ai/chat  — SSE streaming response
// ============================================================

type chatRequest struct {
	Message   string  `json:"message" binding:"required"`
	ContextID *string `json:"context_id,omitempty"`
}

func (h *AIHandler) Chat(c *gin.Context) {
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

	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	messages := []ai.Message{
		{Role: "system", Content: "You are a helpful CRM assistant. Be concise and professional."},
		{Role: "user", Content: req.Message},
	}

	result, err := h.gateway.Complete(c.Request.Context(), orgID, userID, ai.TaskAssistantChat, messages)
	if err != nil {
		var budgetErr ai.ErrBudgetExceeded
		var planErr ai.ErrFeatureNotInPlan
		switch {
		case errors.As(err, &budgetErr):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": budgetErr.Error(), "code": "budget_exceeded", "reset_at": budgetErr.ResetAt})
		case errors.As(err, &planErr):
			c.JSON(http.StatusPaymentRequired, gin.H{"error": planErr.Error(), "code": "feature_not_in_plan", "requires_plan": planErr.RequiresPlan})
		default:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ai_unavailable"})
		}
		return
	}

	// Stream response as SSE
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")

	flusher, canFlush := c.Writer.(http.Flusher)

	// Simulate token streaming by writing chunks of the response
	chunkSize := 10
	content := result.Content
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		chunk := content[i:end]
		fmt.Fprintf(c.Writer, "data: %s\n\n", chunk)
		if canFlush {
			flusher.Flush()
		}
	}

	// Send done event
	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
}

// ============================================================
// POST /api/ai/embed
// ============================================================

type embedRequest struct {
	Text string `json:"text" binding:"required"`
}

type embedResponse struct {
	Vector     []float32 `json:"vector"`
	Dimensions int       `json:"dimensions"`
	Model      string    `json:"model"`
}

func (h *AIHandler) Embed(c *gin.Context) {
	_, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var req embedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	if h.embedSvc == nil {
		c.JSON(http.StatusServiceUnavailable, domain.Err("embedding service not configured"))
		return
	}

	vec, err := h.embedSvc.EmbedText(c.Request.Context(), req.Text)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, domain.Err("embedding failed: "+err.Error()))
		return
	}

	if len(vec) != 768 {
		c.JSON(http.StatusInternalServerError, domain.Err(
			fmt.Sprintf("unexpected embedding dimensions: got %d, want 768", len(vec)),
		))
		return
	}

	c.JSON(http.StatusOK, domain.Success(embedResponse{
		Vector:     vec,
		Dimensions: len(vec),
		Model:      "@cf/google/embeddinggemma-300m",
	}))
}

// ============================================================
// Suppress unused import warning
// ============================================================
var (
	_ = bufio.NewScanner
	_ = io.EOF
)
