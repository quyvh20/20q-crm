package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AIHandler handles /api/ai routes.
type AIHandler struct {
	gateway   *ai.AIGateway
	budget    *ai.BudgetGuard
	embedSvc  *ai.EmbeddingService
	contactUC domain.ContactUseCase
	kbBuilder *ai.KnowledgeBuilder
}

func NewAIHandler(gateway *ai.AIGateway, budget *ai.BudgetGuard, embedSvc *ai.EmbeddingService, kbBuilder *ai.KnowledgeBuilder, contactUC ...domain.ContactUseCase) *AIHandler {
	h := &AIHandler{gateway: gateway, budget: budget, embedSvc: embedSvc, kbBuilder: kbBuilder}
	if len(contactUC) > 0 {
		h.contactUC = contactUC[0]
	}
	return h
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

func (h *AIHandler) GetTopUsage(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	usages, err := h.budget.GetTopUsages(c.Request.Context(), orgID, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(usages))
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
		{Role: "system", Content: h.buildSystemPrompt(c.Request.Context(), req.ContextID, orgID)},
		{Role: "user", Content: req.Message},
	}

	flusher, canFlush := c.Writer.(http.Flusher)

	// writeSSEHeaders is called by StreamChat the moment the upstream
	// HTTP connection is established — BEFORE any body bytes are written.
	// This way, if the upstream times out, no headers have been committed
	// and we can still return a proper HTTP 503.
	headerWritten := false
	writeSSEHeaders := func() {
		if headerWritten {
			return
		}
		headerWritten = true
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Header("Transfer-Encoding", "chunked")
	}
	flush := func() {
		if canFlush {
			flusher.Flush()
		}
	}

	// ── Real streaming via CF Workers AI ──────────────────────────────────
	err := h.gateway.StreamChat(
		c.Request.Context(), orgID, userID, ai.TaskAssistantChat,
		messages, c.Writer, writeSSEHeaders, flush,
	)
	if err != nil {
		var timeoutErr ai.ErrAITimeout
		var budgetErr ai.ErrBudgetExceeded
		var planErr ai.ErrFeatureNotInPlan
		switch {
		case errors.As(err, &timeoutErr):
			if !headerWritten {
				// Headers not committed — return proper HTTP 503
				c.Header("Retry-After", fmt.Sprintf("%d", timeoutErr.After))
				c.JSON(http.StatusServiceUnavailable, domain.Err(timeoutErr.Error()))
			} else {
				// Headers already sent — emit SSE error event
				fmt.Fprintf(c.Writer, "event: error\ndata: {\"code\":\"timeout\",\"retry_after\":%d}\n\n", timeoutErr.After)
				flush()
			}
		case errors.As(err, &budgetErr):
			if !headerWritten {
				c.JSON(http.StatusTooManyRequests, gin.H{
					"error": budgetErr.Error(), "code": "budget_exceeded",
					"reset_at": budgetErr.ResetAt,
				})
			} else {
				fmt.Fprintf(c.Writer, "event: error\ndata: {\"code\":\"budget_exceeded\",\"reset_at\":\"%s\"}\n\n", budgetErr.ResetAt)
				flush()
			}
		case errors.As(err, &planErr):
			if !headerWritten {
				c.JSON(http.StatusPaymentRequired, gin.H{
					"error": planErr.Error(), "code": "feature_not_in_plan",
					"requires_plan": planErr.RequiresPlan,
				})
			} else {
				fmt.Fprintf(c.Writer, "event: error\ndata: {\"code\":\"feature_not_in_plan\",\"requires_plan\":\"%s\"}\n\n", planErr.RequiresPlan)
				flush()
			}
		default:
			// Generic fallback: call non-streaming Complete
			result, ferr := h.gateway.Complete(c.Request.Context(), orgID, userID, ai.TaskAssistantChat, messages)
			if ferr != nil {
				var fTimeoutErr ai.ErrAITimeout
				if errors.As(ferr, &fTimeoutErr) && !headerWritten {
					c.Header("Retry-After", fmt.Sprintf("%d", fTimeoutErr.After))
					c.JSON(http.StatusServiceUnavailable, domain.Err(ferr.Error()))
					return
				}
				if !headerWritten {
					c.JSON(http.StatusServiceUnavailable, domain.Err("ai_unavailable"))
				} else {
					fmt.Fprintf(c.Writer, "event: error\ndata: {\"code\":\"ai_unavailable\"}\n\n")
					flush()
				}
				return
			}
			// Stream the complete response in chunks
			writeSSEHeaders()
			chunkSize := 10
			content := result.Content
			for i := 0; i < len(content); i += chunkSize {
				end := i + chunkSize
				if end > len(content) {
					end = len(content)
				}
				fmt.Fprintf(c.Writer, "data: %s\n\n", content[i:end])
				flush()
			}
			fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
			flush()
		}
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
// buildSystemPrompt — injects contact context when context_id is provided
// ============================================================

func (h *AIHandler) buildSystemPrompt(ctx context.Context, contextID *string, orgID uuid.UUID) string {
	// Use the compiled KB prompt as base if available, fall back to generic
	base := "You are a helpful CRM assistant. Be concise and professional."
	if h.kbBuilder != nil {
		if kbPrompt, err := h.kbBuilder.BuildSystemPrompt(ctx, orgID); err == nil && kbPrompt != "" {
			base = kbPrompt
		}
	}

	if contextID == nil || *contextID == "" || h.contactUC == nil {
		slog.Info("ai_chat_context", "status", "no_context_id", "has_uc", h.contactUC != nil)
		return base
	}

	slog.Info("ai_chat_context", "status", "resolving", "context_id", *contextID, "org_id", orgID.String())

	contactID, err := uuid.Parse(*contextID)
	if err != nil {
		slog.Info("ai_chat_context", "status", "invalid_uuid", "context_id", *contextID)
		return base // not a UUID — silently fall back
	}

	contact, err := h.contactUC.GetByID(ctx, orgID, contactID)
	if err != nil || contact == nil {
		slog.Info("ai_chat_context", "status", "contact_not_found", "contact_id", contactID.String(), "err", err)
		return base // contact not found or access denied — fall back
	}

	slog.Info("ai_chat_context", "status", "found",
		"contact_id", contact.ID.String(),
		"name", contact.FirstName+" "+contact.LastName,
	)

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n--- CONTACT CONTEXT ---\n")
	b.WriteString(fmt.Sprintf("Name: %s %s\n", contact.FirstName, contact.LastName))

	if contact.Email != nil {
		b.WriteString(fmt.Sprintf("Email: %s\n", *contact.Email))
	}
	if contact.Phone != nil {
		b.WriteString(fmt.Sprintf("Phone: %s\n", *contact.Phone))
	}
	if contact.Company != nil {
		b.WriteString(fmt.Sprintf("Company: %s\n", contact.Company.Name))
	}
	if contact.Owner != nil {
		b.WriteString(fmt.Sprintf("Account Owner: %s %s\n", contact.Owner.FirstName, contact.Owner.LastName))
	}
	if len(contact.Tags) > 0 {
		tagNames := make([]string, len(contact.Tags))
		for i, t := range contact.Tags {
			tagNames[i] = t.Name
		}
		b.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(tagNames, ", ")))
	}
	if len(contact.CustomFields) > 0 {
		b.WriteString(fmt.Sprintf("Custom Fields: %s\n", string(contact.CustomFields)))
	}
	b.WriteString(fmt.Sprintf("Contact Since: %s\n", contact.CreatedAt.Format("2006-01-02")))
	b.WriteString("--- END CONTEXT ---\n\n")
	b.WriteString("Use the above contact data when answering questions. Reference the contact by their full name.")

	return b.String()
}
