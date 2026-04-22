package automation

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Handler provides HTTP handlers for the workflow automation API.
type Handler struct {
	engine *Engine
	repo   *Repository
	db     *gorm.DB
	logger *slog.Logger
	// Rate limiter for webhook inbound
	rateLimiter *tokenBucket
}

// NewHandler creates a new automation HTTP handler.
func NewHandler(engine *Engine, db *gorm.DB, logger *slog.Logger) *Handler {
	return &Handler{
		engine:      engine,
		repo:        engine.Repo(),
		db:          db,
		logger:      logger,
		rateLimiter: newTokenBucket(),
	}
}

// RegisterRoutes registers all automation routes on the gin engine.
func (h *Handler) RegisterRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc, requireRole func(...string) gin.HandlerFunc) {
	workflows := router.Group("/api/workflows")
	workflows.Use(authMiddleware)
	{
		workflows.POST("", requireRole("admin", "manager"), h.CreateWorkflow)
		workflows.GET("", h.ListWorkflows)
		workflows.GET("/:id", h.GetWorkflow)
		workflows.PUT("/:id", requireRole("admin", "manager"), h.UpdateWorkflow)
		workflows.DELETE("/:id", requireRole("admin", "manager"), h.DeleteWorkflow)
		workflows.POST("/:id/toggle", requireRole("admin", "manager"), h.ToggleWorkflow)
		workflows.POST("/:id/test-run", requireRole("admin", "manager"), h.TestRun)
		workflows.GET("/:id/runs", h.ListRuns)
		workflows.GET("/runs/:runId", h.GetRunDetail)
	}

	// Public webhook inbound
	router.POST("/api/webhooks/inbound/:org_token", h.WebhookInbound)
}

// --- Workflow CRUD ---

// CreateWorkflow handles POST /api/workflows
func (h *Handler) CreateWorkflow(c *gin.Context) {
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	var req CreateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error(), nil)
		return
	}

	// Validate JSON payloads
	result := ValidateWorkflowPayload(req.Trigger, req.Conditions, req.Actions)
	if !result.Valid {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", "workflow payload validation failed", result.Errors)
		return
	}

	wf := &Workflow{
		OrgID:       orgID,
		Name:        req.Name,
		Description: req.Description,
		Trigger:     req.Trigger,
		Conditions:  req.Conditions,
		Actions:     req.Actions,
		CreatedBy:   userID,
	}

	if err := h.repo.CreateWorkflow(c.Request.Context(), wf); err != nil {
		h.logger.Error("create workflow failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create workflow", nil)
		return
	}

	resp := ToWorkflowResponse(wf)
	c.JSON(http.StatusCreated, gin.H{"data": resp})
}

// ListWorkflows handles GET /api/workflows
func (h *Handler) ListWorkflows(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	activeOnly := c.Query("active") == "true"
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	workflows, total, err := h.repo.ListWorkflows(c.Request.Context(), orgID, activeOnly, page, size)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list workflows", nil)
		return
	}

	var items []WorkflowResponse
	for i := range workflows {
		items = append(items, ToWorkflowResponse(&workflows[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"data": WorkflowListResponse{
			Workflows: items,
			Total:     total,
			Page:      page,
			Size:      size,
		},
	})
}

// GetWorkflow handles GET /api/workflows/:id
func (h *Handler) GetWorkflow(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	wf, err := h.repo.GetWorkflowByID(c.Request.Context(), orgID, id)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get workflow", nil)
		return
	}
	if wf == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "workflow not found", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": ToWorkflowResponse(wf)})
}

// UpdateWorkflow handles PUT /api/workflows/:id
func (h *Handler) UpdateWorkflow(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	wf, err := h.repo.GetWorkflowByID(c.Request.Context(), orgID, id)
	if err != nil || wf == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "workflow not found", nil)
		return
	}

	var req UpdateWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error(), nil)
		return
	}

	if req.Name != nil {
		wf.Name = *req.Name
	}
	if req.Description != nil {
		wf.Description = *req.Description
	}
	if req.Trigger != nil {
		wf.Trigger = req.Trigger
	}
	if req.Conditions != nil {
		wf.Conditions = req.Conditions
	}
	if req.Actions != nil {
		wf.Actions = req.Actions
	}

	// Re-validate
	result := ValidateWorkflowPayload(wf.Trigger, wf.Conditions, wf.Actions)
	if !result.Valid {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", "workflow payload validation failed", result.Errors)
		return
	}

	if err := h.repo.UpdateWorkflow(c.Request.Context(), wf); err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to update workflow", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": ToWorkflowResponse(wf)})
}

// DeleteWorkflow handles DELETE /api/workflows/:id
func (h *Handler) DeleteWorkflow(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	if err := h.repo.SoftDeleteWorkflow(c.Request.Context(), orgID, id); err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete workflow", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": nil})
}

// ToggleWorkflow handles POST /api/workflows/:id/toggle
func (h *Handler) ToggleWorkflow(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	wf, err := h.repo.ToggleWorkflow(c.Request.Context(), orgID, id)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to toggle workflow", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": ToWorkflowResponse(wf)})
}

// TestRun handles POST /api/workflows/:id/test-run
func (h *Handler) TestRun(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	wf, err := h.repo.GetWorkflowByID(c.Request.Context(), orgID, id)
	if err != nil || wf == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "workflow not found", nil)
		return
	}

	var req TestRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", err.Error(), nil)
		return
	}

	// Build eval context from request
	evalCtx := EvalContext{Actions: make(map[string]any)}
	if contact, ok := req.Context["contact"].(map[string]any); ok {
		evalCtx.Contact = contact
	}
	if deal, ok := req.Context["deal"].(map[string]any); ok {
		evalCtx.Deal = deal
	}
	if trigger, ok := req.Context["trigger"].(map[string]any); ok {
		evalCtx.Trigger = trigger
	}

	// Evaluate conditions
	conditionResult := true
	if wf.Conditions != nil && len(wf.Conditions) > 0 {
		var conditions ConditionGroup
		if err := json.Unmarshal(wf.Conditions, &conditions); err == nil {
			conditionResult = EvaluateConditions(conditions, evalCtx)
		}
	}

	// Resolve action params (no side effects)
	var actions []ActionSpec
	json.Unmarshal(wf.Actions, &actions)

	var testActions []TestRunAction
	for _, action := range actions {
		resolved := make(map[string]any)
		for k, v := range action.Params {
			if str, ok := v.(string); ok {
				resolved[k] = InterpolateTemplate(str, evalCtx)
			} else {
				resolved[k] = v
			}
		}
		testActions = append(testActions, TestRunAction{
			ID:             action.ID,
			Type:           action.Type,
			ResolvedParams: resolved,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data": TestRunResponse{
			ConditionResult: conditionResult,
			Actions:         testActions,
		},
	})
}

// --- Run history ---

// ListRuns handles GET /api/workflows/:id/runs
func (h *Handler) ListRuns(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	runs, total, err := h.repo.ListRunsByWorkflow(c.Request.Context(), id, page, size)
	if err != nil {
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list runs", nil)
		return
	}

	var items []WorkflowRunResponse
	for i := range runs {
		items = append(items, ToRunResponse(&runs[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"runs":  items,
			"total": total,
			"page":  page,
			"size":  size,
		},
	})
}

// GetRunDetail handles GET /api/workflows/runs/:runId
func (h *Handler) GetRunDetail(c *gin.Context) {
	runID, err := uuid.Parse(c.Param("runId"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid run ID", nil)
		return
	}

	run, err := h.repo.GetRunByID(c.Request.Context(), runID)
	if err != nil || run == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "run not found", nil)
		return
	}

	logs, _ := h.repo.GetActionLogsByRunID(c.Request.Context(), runID)
	var logResponses []ActionLogResponse
	for i := range logs {
		logResponses = append(logResponses, ToActionLogResponse(&logs[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"data": RunDetailResponse{
			Run:        ToRunResponse(run),
			ActionLogs: logResponses,
		},
	})
}

// --- Webhook Inbound (§10) ---

// WebhookInbound handles POST /api/webhooks/inbound/:org_token
func (h *Handler) WebhookInbound(c *gin.Context) {
	tokenStr := c.Param("org_token")

	token, err := h.repo.GetOrgToken(c.Request.Context(), tokenStr)
	if err != nil || token == nil {
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error: ErrorBody{Code: "NOT_FOUND", Message: "invalid token"},
		})
		return
	}

	// Rate limit: 100 req/min per token
	if !h.rateLimiter.allow(tokenStr) {
		c.JSON(http.StatusTooManyRequests, ErrorResponse{
			Error: ErrorBody{Code: "RATE_LIMITED", Message: "too many requests"},
		})
		return
	}

	// Read body
	bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20)) // 1MB limit
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorBody{Code: "BAD_REQUEST", Message: "failed to read request body"},
		})
		return
	}

	// Signature verification (skip in dev if env flag set)
	skipSig := os.Getenv("WEBHOOK_SKIP_SIGNATURE") == "true"
	if !skipSig {
		sigHeader := c.GetHeader("X-Signature")
		if sigHeader == "" {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorBody{Code: "UNAUTHORIZED", Message: "missing X-Signature header"},
			})
			return
		}

		expectedSig := "sha256=" + computeHMAC(bodyBytes, token.Secret)
		if !hmac.Equal([]byte(sigHeader), []byte(expectedSig)) {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				Error: ErrorBody{Code: "UNAUTHORIZED", Message: "invalid signature"},
			})
			return
		}
	}

	// Parse body
	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorBody{Code: "BAD_REQUEST", Message: "invalid JSON body"},
		})
		return
	}

	// Extract email (required)
	email, ok := payload["email"].(string)
	if !ok || email == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: ErrorBody{Code: "BAD_REQUEST", Message: "email field is required"},
		})
		return
	}

	// Field mapping (v1: hardcoded convention)
	contactData := map[string]any{
		"email": email,
	}
	directFields := []string{"first_name", "last_name", "phone", "company"}
	customFields := make(map[string]any)

	for key, val := range payload {
		found := false
		for _, df := range directFields {
			if key == df {
				contactData[key] = val
				found = true
				break
			}
		}
		if !found && key != "email" {
			customFields[key] = val
		}
	}
	if len(customFields) > 0 {
		contactData["custom_fields"] = customFields
	}

	// Upsert contact by email
	var contactID uuid.UUID
	var eventType string

	var existingContact struct {
		ID uuid.UUID `gorm:"column:id"`
	}
	err = h.db.Raw("SELECT id FROM contacts WHERE org_id = ? AND email = ? AND deleted_at IS NULL LIMIT 1",
		token.OrgID, email).Scan(&existingContact).Error

	if err == nil && existingContact.ID != uuid.Nil {
		// Update existing
		contactID = existingContact.ID
		updates := make(map[string]any)
		if fn, ok := contactData["first_name"]; ok {
			updates["first_name"] = fn
		}
		if ln, ok := contactData["last_name"]; ok {
			updates["last_name"] = ln
		}
		if ph, ok := contactData["phone"]; ok {
			updates["phone"] = ph
		}
		if cf, ok := contactData["custom_fields"]; ok {
			cfJSON, _ := json.Marshal(cf)
			updates["custom_fields"] = datatypes.JSON(cfJSON)
		}
		if len(updates) > 0 {
			h.db.Table("contacts").Where("id = ?", contactID).Updates(updates)
		}
		eventType = TriggerContactUpdated
	} else {
		// Create new
		contactID = uuid.New()
		firstName := ""
		if fn, ok := contactData["first_name"].(string); ok {
			firstName = fn
		}
		lastName := ""
		if ln, ok := contactData["last_name"].(string); ok {
			lastName = ln
		}

		cfJSON, _ := json.Marshal(customFields)

		h.db.Exec(
			`INSERT INTO contacts (id, org_id, first_name, last_name, email, phone, custom_fields, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			contactID, token.OrgID, firstName, lastName, email,
			contactData["phone"], datatypes.JSON(cfJSON),
		)
		eventType = TriggerContactCreated
	}

	// Trigger event asynchronously
	triggerPayload := map[string]any{
		"entity_id": contactID.String(),
		"contact": map[string]any{
			"id":    contactID.String(),
			"email": email,
		},
		"trigger": map[string]any{
			"type":   eventType,
			"source": "webhook_inbound",
		},
	}
	// Merge contact data into the trigger payload
	for k, v := range contactData {
		triggerPayload["contact"].(map[string]any)[k] = v
	}

	h.engine.TriggerEvent(c.Request.Context(), token.OrgID, eventType, triggerPayload)

	c.JSON(http.StatusOK, WebhookInboundResponse{
		Status:    "accepted",
		ContactID: contactID.String(),
	})
}

// --- Helpers ---

func (h *Handler) getContext(c *gin.Context) (uuid.UUID, uuid.UUID) {
	orgIDVal, exists := c.Get("org_id")
	if !exists {
		h.errorResponse(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing org context", nil)
		return uuid.Nil, uuid.Nil
	}
	orgID, ok := orgIDVal.(uuid.UUID)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid org context", nil)
		return uuid.Nil, uuid.Nil
	}

	userIDVal, _ := c.Get("user_id")
	userID, _ := userIDVal.(uuid.UUID)

	return orgID, userID
}

func (h *Handler) errorResponse(c *gin.Context, status int, code, message string, details []ValidationError) {
	c.JSON(status, ErrorResponse{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func computeHMAC(message []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// GenerateToken generates a random token string.
func GenerateToken(length int) string {
	b := make([]byte, length/2)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --- In-memory rate limiter (token bucket) ---

type tokenBucket struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

func newTokenBucket() *tokenBucket {
	return &tokenBucket{
		buckets: make(map[string]*bucket),
	}
}

func (tb *tokenBucket) allow(key string) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	b, ok := tb.buckets[key]
	if !ok || time.Since(b.lastReset) > time.Minute {
		tb.buckets[key] = &bucket{tokens: 99, lastReset: time.Now()}
		return true
	}

	if b.tokens <= 0 {
		return false
	}

	b.tokens--
	return true
}

// suppress unused import warnings
var _ = strings.Contains
var _ = fmt.Sprintf
