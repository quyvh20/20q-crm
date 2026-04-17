package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"crm-backend/internal/worker"
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
	queue     *worker.AIJobQueue
}

func NewAIHandler(gateway *ai.AIGateway, budget *ai.BudgetGuard, embedSvc *ai.EmbeddingService, kbBuilder *ai.KnowledgeBuilder, queue *worker.AIJobQueue, contactUC ...domain.ContactUseCase) *AIHandler {
	h := &AIHandler{gateway: gateway, budget: budget, embedSvc: embedSvc, kbBuilder: kbBuilder, queue: queue}
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
	sortOpt := c.DefaultQuery("sort", "cost")

	usages, err := h.budget.GetTopUsages(c.Request.Context(), orgID, 10, sortOpt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(usages))
}

func (h *AIHandler) GetUsageStats(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	stats, err := h.budget.GetUsageStats(c.Request.Context(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	c.JSON(http.StatusOK, domain.Success(stats))
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

func (h *AIHandler) Embed(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	var req embedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	text := req.Text
	if len(text) > 20000 {
		text = text[:20000]
	}

	vec, err := h.embedSvc.EmbedText(c.Request.Context(), text)
	if err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err(err.Error()))
		return
	}

	_ = orgID

	c.JSON(http.StatusOK, domain.Success(vec))
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

// ============================================================
// Async / Job Queue Endpoints
// ============================================================

func (h *AIHandler) GetJobStatus(c *gin.Context) {
	_, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, domain.Err("missing job id"))
		return
	}

	job, err := h.queue.GetStatus(c.Request.Context(), jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, domain.Err("job not found"))
		return
	}

	c.JSON(http.StatusOK, domain.Success(job))
}

func (h *AIHandler) ScoreDeal(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}

	userID, _ := GetUserID(c)
	dealIDStr := c.Param("id")
	dealID, err := uuid.Parse(dealIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, domain.Err("invalid deal id"))
		return
	}

	// Instantly bypass worker if cached
	cacheKey := fmt.Sprintf("deal_score:%s", dealID.String())
	if cachedData, err := h.queue.GetRedis().Get(c.Request.Context(), cacheKey).Result(); err == nil && cachedData != "" {
		var result map[string]interface{}
		if json.Unmarshal([]byte(cachedData), &result) == nil {
			c.JSON(http.StatusOK, domain.Success(map[string]interface{}{
				"status": "completed",
				"result": result,
			}))
			return
		}
	}

	// Enqueue job map
	payloadBytes, _ := json.Marshal(map[string]interface{}{"deal_id": dealID})
	job := &worker.AIJob{
		JobID:    uuid.New(),
		OrgID:    orgID,
		UserID:   userID,
		TaskType: string(ai.TaskDealScore),
		Payload:  payloadBytes,
	}

	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err("failed to enqueue job: "+err.Error()))
		return
	}

	c.JSON(http.StatusAccepted, domain.Success(map[string]string{
		"status": "processing",
		"job_id": job.JobID.String(),
	}))
}

type summarizeRequest struct {
	Transcript string      `json:"transcript" binding:"required"`
	DealID     *uuid.UUID  `json:"deal_id,omitempty"`
	ContactID  *uuid.UUID  `json:"contact_id,omitempty"`
}

func (h *AIHandler) SummarizeMeeting(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, _ := GetUserID(c)

	var req summarizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	payloadBytes, _ := json.Marshal(req)
	job := &worker.AIJob{
		JobID:    uuid.New(),
		OrgID:    orgID,
		UserID:   userID,
		TaskType: string(ai.TaskMeetingSummary),
		Payload:  payloadBytes,
	}

	if err := h.queue.Enqueue(c.Request.Context(), job); err != nil {
		c.JSON(http.StatusInternalServerError, domain.Err("failed to enqueue job: "+err.Error()))
		return
	}

	c.JSON(http.StatusAccepted, domain.Success(map[string]string{
		"status": "processing",
		"job_id": job.JobID.String(),
	}))
}

// ============================================================
// Sync Endpoints
// ============================================================

type emailComposeRequest struct {
	Instruction string     `json:"instruction" binding:"required"`
	Tone        string     `json:"tone" binding:"required"`
	ContactID   *uuid.UUID `json:"contact_id,omitempty"`
	DealID      *uuid.UUID `json:"deal_id,omitempty"`
}

func (h *AIHandler) ComposeEmail(c *gin.Context) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	userID, _ := GetUserID(c)

	var req emailComposeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, domain.Err(err.Error()))
		return
	}

	prompt := fmt.Sprintf(`Write an email. Tone: %s. Instruction: %s
Ensure the email has absolutely no markdown wrapping blocks (like '''email). Just output the raw text of the email cleanly.`, req.Tone, req.Instruction)

	msgs := []ai.Message{{Role: "user", Content: prompt}}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}

	// Pseudo streaming: We just do synchronous fetch, then stream back chunks.
	resp, err := h.gateway.Complete(c.Request.Context(), orgID, userID, ai.TaskEmailCompose, msgs)
	if err != nil {
		c.Writer.Write([]byte(fmt.Sprintf("event: error\ndata: {\"code\": \"error\", \"message\": \"%s\"}\n\n", err.Error())))
		return
	}

	// Stream chunks pseudo-live to user
	chunkSize := 15
	for i := 0; i < len(resp.Content); i += chunkSize {
		end := i + chunkSize
		if end > len(resp.Content) {
			end = len(resp.Content)
		}

		encodedChunk, _ := json.Marshal(resp.Content[i:end])
		fmt.Fprintf(c.Writer, "data: %s\n\n", encodedChunk)
		if f, ok := c.Writer.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(20 * time.Millisecond) // Give browser time to paint
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
}
