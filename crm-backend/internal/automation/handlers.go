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
	engine      *Engine
	repo        *Repository
	db          *gorm.DB
	logger      *slog.Logger
	rateLimiter *tokenBucket       // Rate limiter for webhook inbound
	schemaCache *SchemaCache        // Per-org schema cache (60s TTL)
}

// NewHandler creates a new automation HTTP handler.
func NewHandler(engine *Engine, db *gorm.DB, logger *slog.Logger) *Handler {
	return &Handler{
		engine:      engine,
		repo:        engine.Repo(),
		db:          db,
		logger:      logger,
		rateLimiter: newTokenBucket(),
		schemaCache: NewSchemaCache(60 * time.Second),
	}
}

// InvalidateSchemaCache removes the cached workflow schema for a specific org.
// Call this from delivery handlers when stages, tags, custom fields, or
// custom objects are created/updated/deleted.
func (h *Handler) InvalidateSchemaCache(orgID uuid.UUID) {
	if h.schemaCache != nil {
		h.schemaCache.Invalidate(orgID)
	}
}

// RegisterRoutes registers all automation routes on the gin engine.
func (h *Handler) RegisterRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc, requireRole func(...string) gin.HandlerFunc) {
	workflows := router.Group("/api/workflows")
	workflows.Use(authMiddleware)
	{
		workflows.POST("", requireRole("admin", "manager"), h.CreateWorkflow)
		workflows.GET("", h.ListWorkflows)
		workflows.GET("/schema", h.GetWorkflowSchema)
		workflows.GET("/schema/objects", h.GetSchemaObjects)
		workflows.GET("/schema/objects/:slug/fields", h.GetSchemaObjectFields)
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
		items = append(items, ToWorkflowResponseWithRun(&workflows[i].Workflow, workflows[i].LastRunStatus, workflows[i].LastRunAt))
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

// --- Workflow Schema (for builder field pickers) ---

// GetWorkflowSchema handles GET /api/workflows/schema.
// Returns all available fields, stages, tags, users, and custom objects
// so the frontend builder can render smart pickers instead of raw text inputs.
func (h *Handler) GetWorkflowSchema(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	// Check cache first
	if h.schemaCache != nil {
		if cached := h.schemaCache.Get(orgID); cached != nil {
			c.JSON(http.StatusOK, gin.H{"data": cached})
			return
		}
	}

	ctx := c.Request.Context()

	// 1. Built-in entity fields
	entities := []SchemaEntity{
		{
			Key: "contact", Label: "Contact", Icon: "👤",
			Fields: []SchemaField{
				{Path: "contact.first_name", Label: "First Name", Type: "string"},
				{Path: "contact.last_name", Label: "Last Name", Type: "string"},
				{Path: "contact.email", Label: "Email", Type: "string"},
				{Path: "contact.phone", Label: "Phone", Type: "string"},
				{Path: "contact.owner_id", Label: "Owner", Type: "string", PickerType: "user"},
				{Path: "contact.tags", Label: "Tags", Type: "array", PickerType: "tag"},
				{Path: "contact.company.name", Label: "Company Name", Type: "string"},
				{Path: "contact.created_at", Label: "Created At", Type: "date"},
				{Path: "contact.id", Label: "Contact ID", Type: "string"},
			},
		},
		{
			Key: "deal", Label: "Deal", Icon: "💰",
			Fields: []SchemaField{
				{Path: "deal.title", Label: "Title", Type: "string"},
				{Path: "deal.value", Label: "Value", Type: "number"},
				{Path: "deal.stage", Label: "Stage", Type: "string", PickerType: "stage"},
				{Path: "deal.probability", Label: "Probability (%)", Type: "number"},
				{Path: "deal.is_won", Label: "Is Won", Type: "boolean"},
				{Path: "deal.is_lost", Label: "Is Lost", Type: "boolean"},
				{Path: "deal.owner_id", Label: "Owner", Type: "string", PickerType: "user"},
				{Path: "deal.expected_close_at", Label: "Expected Close", Type: "date"},
				{Path: "deal.closed_at", Label: "Closed At", Type: "date"},
				{Path: "deal.created_at", Label: "Created At", Type: "date"},
				{Path: "deal.id", Label: "Deal ID", Type: "string"},
			},
		},
		{
			Key: "trigger", Label: "Trigger Event", Icon: "⚡",
			Fields: []SchemaField{
				{Path: "trigger.type", Label: "Event Type", Type: "string"},
				{Path: "trigger.from_stage", Label: "Previous Stage", Type: "string", PickerType: "stage"},
				{Path: "trigger.to_stage", Label: "New Stage", Type: "string", PickerType: "stage"},
			},
		},
	}

	// 2. Custom field definitions from org_settings
	type orgSettingsRow struct {
		CustomFieldDefs json.RawMessage `gorm:"column:custom_field_defs"`
	}
	var settings orgSettingsRow
	if err := h.db.WithContext(ctx).Table("org_settings").Where("org_id = ?", orgID).First(&settings).Error; err == nil && len(settings.CustomFieldDefs) > 2 {
		var defs []struct {
			Key        string   `json:"key"`
			Label      string   `json:"label"`
			Type       string   `json:"type"`
			EntityType string   `json:"entity_type"`
			Options    []string `json:"options"`
		}
		if json.Unmarshal(settings.CustomFieldDefs, &defs) == nil {
			for _, d := range defs {
				field := SchemaField{
					Path:  d.EntityType + ".custom_fields." + d.Key,
					Label: d.Label,
					Type:  d.Type,
				}
				if d.Type == "select" {
					field.Options = d.Options
				}
				// Append to the matching entity
				for i := range entities {
					if entities[i].Key == d.EntityType {
						entities[i].Fields = append(entities[i].Fields, field)
					}
				}
			}
		}
	}

	// 3. Custom object definitions
	var customObjects []SchemaEntity
	type customObjRow struct {
		Slug   string          `gorm:"column:slug"`
		Label  string          `gorm:"column:label"`
		Icon   string          `gorm:"column:icon"`
		Fields json.RawMessage `gorm:"column:fields"`
	}
	var objRows []customObjRow
	if err := h.db.WithContext(ctx).Table("custom_object_defs").Where("org_id = ? AND deleted_at IS NULL", orgID).Find(&objRows).Error; err == nil {
		for _, obj := range objRows {
			entity := SchemaEntity{
				Key:   obj.Slug,
				Label: obj.Label,
				Icon:  obj.Icon,
			}
			var fieldDefs []struct {
				Key     string   `json:"key"`
				Label   string   `json:"label"`
				Type    string   `json:"type"`
				Options []string `json:"options"`
			}
			if json.Unmarshal(obj.Fields, &fieldDefs) == nil {
				for _, f := range fieldDefs {
					sf := SchemaField{
						Path:  obj.Slug + "." + f.Key,
						Label: f.Label,
						Type:  f.Type,
					}
					if f.Type == "select" {
						sf.Options = f.Options
					}
					entity.Fields = append(entity.Fields, sf)
				}
			}
			customObjects = append(customObjects, entity)
		}
	}

	// 4. Pipeline stages
	var stages []SchemaStage
	type stageRow struct {
		ID       uuid.UUID `gorm:"column:id"`
		Name     string    `gorm:"column:name"`
		Color    string    `gorm:"column:color"`
		Position int       `gorm:"column:position"`
	}
	var stageRows []stageRow
	if err := h.db.WithContext(ctx).Table("pipeline_stages").Where("org_id = ? AND deleted_at IS NULL", orgID).Order("position ASC").Find(&stageRows).Error; err == nil {
		for _, s := range stageRows {
			stages = append(stages, SchemaStage{ID: s.ID.String(), Name: s.Name, Color: s.Color, Order: s.Position})
		}
	}

	// 5. Tags
	var tags []SchemaTag
	type tagRow struct {
		ID    uuid.UUID `gorm:"column:id"`
		Name  string    `gorm:"column:name"`
		Color string    `gorm:"column:color"`
	}
	var tagRows []tagRow
	if err := h.db.WithContext(ctx).Table("tags").Where("org_id = ? AND deleted_at IS NULL", orgID).Order("name ASC").Find(&tagRows).Error; err == nil {
		for _, t := range tagRows {
			tags = append(tags, SchemaTag{ID: t.ID.String(), Name: t.Name, Color: t.Color})
		}
	}

	// 6. Org members
	var users []SchemaUser
	type userRow struct {
		ID        uuid.UUID `gorm:"column:id"`
		FirstName string    `gorm:"column:first_name"`
		LastName  string    `gorm:"column:last_name"`
		Email     string    `gorm:"column:email"`
	}
	var userRows []userRow
	if err := h.db.WithContext(ctx).Table("users").
		Joins("JOIN org_users ON org_users.user_id = users.id").
		Where("org_users.org_id = ? AND org_users.status = 'active' AND org_users.deleted_at IS NULL", orgID).
		Select("users.id, users.first_name, users.last_name, users.email").
		Order("users.first_name ASC").
		Find(&userRows).Error; err == nil {
		for _, u := range userRows {
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if name == "" {
				name = u.Email
			}
			users = append(users, SchemaUser{ID: u.ID.String(), Name: name, Email: u.Email})
		}
	}

	result := &SchemaResponse{
		Entities:      entities,
		CustomObjects: customObjects,
		Stages:        stages,
		Tags:          tags,
		Users:         users,
	}

	// Store in cache
	if h.schemaCache != nil {
		h.schemaCache.Set(orgID, result)
	}

	c.JSON(http.StatusOK, gin.H{"data": result})
}

// --- New API Contracts ---

// ObjectListItem is a lightweight object entry for the objects list endpoint.
type ObjectListItem struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Icon  string `json:"icon"`
}

// FieldListItem is a field entry for the per-object fields endpoint.
type FieldListItem struct {
	Name           string   `json:"name"`
	Label          string   `json:"label"`
	Type           string   `json:"type"` // text, number, date, boolean, picklist, reference
	PicklistValues []string `json:"picklist_values,omitempty"`
}

// normalizeFieldType maps internal schema types to the public API contract types.
func normalizeFieldType(schemaType, pickerType string) string {
	switch schemaType {
	case "string":
		if pickerType == "user" || pickerType == "stage" {
			return "reference"
		}
		return "text"
	case "number":
		return "number"
	case "boolean":
		return "boolean"
	case "date":
		return "date"
	case "select":
		return "picklist"
	case "array":
		if pickerType == "tag" {
			return "reference"
		}
		return "text"
	default:
		return "text"
	}
}

// GetSchemaObjects handles GET /api/workflows/schema/objects?permission=read.
// Returns a flat list of objects the current user has read permission to.
func (h *Handler) GetSchemaObjects(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	// permission param — currently all authenticated org members have read access.
	// In the future, filter by role-based permissions here.
	_ = c.DefaultQuery("permission", "read")

	ctx := c.Request.Context()
	var objects []ObjectListItem

	// Built-in entities
	objects = append(objects,
		ObjectListItem{Name: "contact", Label: "Contact", Icon: "👤"},
		ObjectListItem{Name: "deal", Label: "Deal", Icon: "💰"},
	)

	// Custom objects from DB
	type customObjRow struct {
		Slug  string `gorm:"column:slug"`
		Label string `gorm:"column:label"`
		Icon  string `gorm:"column:icon"`
	}
	var objRows []customObjRow
	if err := h.db.WithContext(ctx).Table("custom_object_defs").
		Where("org_id = ? AND deleted_at IS NULL", orgID).
		Select("slug, label, icon").
		Find(&objRows).Error; err == nil {
		for _, obj := range objRows {
			icon := obj.Icon
			if icon == "" {
				icon = "📦"
			}
			objects = append(objects, ObjectListItem{Name: obj.Slug, Label: obj.Label, Icon: icon})
		}
	}

	// Webhook (always available)
	objects = append(objects, ObjectListItem{Name: "webhook", Label: "Webhook", Icon: "🔗"})

	c.JSON(http.StatusOK, gin.H{"data": objects})
}

// GetSchemaObjectFields handles GET /api/workflows/schema/objects/:slug/fields?permission=read.
// Returns the fields for a specific object, with type normalization and picklist values.
func (h *Handler) GetSchemaObjectFields(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	slug := c.Param("slug")
	if slug == "" {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_PARAMS", "Object slug is required", nil)
		return
	}

	_ = c.DefaultQuery("permission", "read")
	ctx := c.Request.Context()

	var fields []FieldListItem

	// Check built-in entities first
	builtinFields := map[string][]SchemaField{
		"contact": {
			{Path: "contact.first_name", Label: "First Name", Type: "string"},
			{Path: "contact.last_name", Label: "Last Name", Type: "string"},
			{Path: "contact.email", Label: "Email", Type: "string"},
			{Path: "contact.phone", Label: "Phone", Type: "string"},
			{Path: "contact.owner_id", Label: "Owner", Type: "string", PickerType: "user"},
			{Path: "contact.tags", Label: "Tags", Type: "array", PickerType: "tag"},
			{Path: "contact.company.name", Label: "Company Name", Type: "string"},
			{Path: "contact.created_at", Label: "Created At", Type: "date"},
			{Path: "contact.id", Label: "Contact ID", Type: "string"},
		},
		"deal": {
			{Path: "deal.title", Label: "Title", Type: "string"},
			{Path: "deal.value", Label: "Value", Type: "number"},
			{Path: "deal.stage", Label: "Stage", Type: "string", PickerType: "stage"},
			{Path: "deal.probability", Label: "Probability (%)", Type: "number"},
			{Path: "deal.is_won", Label: "Is Won", Type: "boolean"},
			{Path: "deal.is_lost", Label: "Is Lost", Type: "boolean"},
			{Path: "deal.owner_id", Label: "Owner", Type: "string", PickerType: "user"},
			{Path: "deal.expected_close_at", Label: "Expected Close", Type: "date"},
			{Path: "deal.closed_at", Label: "Closed At", Type: "date"},
			{Path: "deal.created_at", Label: "Created At", Type: "date"},
			{Path: "deal.id", Label: "Deal ID", Type: "string"},
		},
		"trigger": {
			{Path: "trigger.type", Label: "Event Type", Type: "string"},
			{Path: "trigger.from_stage", Label: "Previous Stage", Type: "string", PickerType: "stage"},
			{Path: "trigger.to_stage", Label: "New Stage", Type: "string", PickerType: "stage"},
		},
	}

	if builtinDefs, ok := builtinFields[slug]; ok {
		for _, f := range builtinDefs {
			fields = append(fields, FieldListItem{
				Name:  f.Path,
				Label: f.Label,
				Type:  normalizeFieldType(f.Type, f.PickerType),
			})
		}

		// Append custom field definitions for this entity
		type orgSettingsRow struct {
			CustomFieldDefs json.RawMessage `gorm:"column:custom_field_defs"`
		}
		var settings orgSettingsRow
		if err := h.db.WithContext(ctx).Table("org_settings").Where("org_id = ?", orgID).First(&settings).Error; err == nil && len(settings.CustomFieldDefs) > 2 {
			var defs []struct {
				Key        string   `json:"key"`
				Label      string   `json:"label"`
				Type       string   `json:"type"`
				EntityType string   `json:"entity_type"`
				Options    []string `json:"options"`
			}
			if json.Unmarshal(settings.CustomFieldDefs, &defs) == nil {
				for _, d := range defs {
					if d.EntityType != slug {
						continue
					}
					item := FieldListItem{
						Name:  d.EntityType + ".custom_fields." + d.Key,
						Label: d.Label,
						Type:  normalizeFieldType(d.Type, ""),
					}
					if d.Type == "select" {
						item.PicklistValues = d.Options
					}
					fields = append(fields, item)
				}
			}
		}
	} else {
		// Custom object — look up from custom_object_defs
		type customObjRow struct {
			Slug   string          `gorm:"column:slug"`
			Fields json.RawMessage `gorm:"column:fields"`
		}
		var objRow customObjRow
		err := h.db.WithContext(ctx).Table("custom_object_defs").
			Where("org_id = ? AND slug = ? AND deleted_at IS NULL", orgID, slug).
			First(&objRow).Error
		if err != nil {
			h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "Object not found: "+slug, nil)
			return
		}

		var fieldDefs []struct {
			Key     string   `json:"key"`
			Label   string   `json:"label"`
			Type    string   `json:"type"`
			Options []string `json:"options"`
		}
		if json.Unmarshal(objRow.Fields, &fieldDefs) == nil {
			for _, f := range fieldDefs {
				item := FieldListItem{
					Name:  slug + "." + f.Key,
					Label: f.Label,
					Type:  normalizeFieldType(f.Type, ""),
				}
				if f.Type == "select" {
					item.PicklistValues = f.Options
				}
				fields = append(fields, item)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": fields})
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
