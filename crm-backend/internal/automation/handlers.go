package automation

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"crm-backend/internal/domain"

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
	rateLimiter *tokenBucket // Rate limiter for webhook inbound
	schemaCache *SchemaCache // Per-org schema cache (60s TTL)
	// capChecker resolves the caller's workflows.run_any capability for Run Now /
	// Retry (P3). Nil in unit tests, where authorizeRunNow's legacy role check is
	// used; set in prod via SetCapabilityChecker.
	capChecker domain.CapabilityChecker
}

// SetCapabilityChecker wires the P3 capability engine so Run Now / Retry authorize
// on the workflows.run_any capability rather than hardcoded role names. Optional:
// when unset, authorization falls back to the legacy owner/admin/manager check.
func (h *Handler) SetCapabilityChecker(c domain.CapabilityChecker) { h.capChecker = c }

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
func (h *Handler) RegisterRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc, requireCap func(string) gin.HandlerFunc) {
	workflows := router.Group("/api/workflows")
	workflows.Use(authMiddleware)
	{
		workflows.POST("", requireCap(domain.CapWorkflowsManage), h.CreateWorkflow)
		workflows.GET("", h.ListWorkflows)
		workflows.GET("/schema", h.GetWorkflowSchema)
		workflows.GET("/schema/objects", h.GetSchemaObjects)
		workflows.GET("/schema/objects/:slug/fields", h.GetSchemaObjectFields)
		workflows.GET("/:id", h.GetWorkflow)
		workflows.PUT("/:id", requireCap(domain.CapWorkflowsManage), h.UpdateWorkflow)
		workflows.DELETE("/:id", requireCap(domain.CapWorkflowsManage), h.DeleteWorkflow)
		workflows.POST("/:id/toggle", requireCap(domain.CapWorkflowsManage), h.ToggleWorkflow)
		workflows.POST("/:id/test-run", requireCap(domain.CapWorkflowsManage), h.TestRun)
		// Run Now intentionally has NO requireRole guard: authorization is enforced inside
		// h.RunNow (owner/admin/manager may run any workflow; any other caller may run only
		// a workflow they created — the creator allowance). A static route guard cannot
		// express the creator check because it needs the loaded workflow's CreatedBy.
		workflows.POST("/:id/run", h.RunNow)
		workflows.GET("/:id/runs", h.ListRuns)
		workflows.GET("/runs/:runId", h.GetRunDetail)
		// Retry a failed run (P21): re-queues it to resume from the failed step. Like Run
		// Now, it carries NO requireRole guard — authorization (owner/admin/manager, or the
		// workflow's creator) is enforced inside h.RetryRun, which needs the workflow's
		// CreatedBy.
		workflows.POST("/runs/:runId/retry", h.RetryRun)
	}

	// Webhook setup (P17): per-org inbound token + signing-secret management.
	// Admin/manager only — these expose/rotate the org's signing credential.
	webhooks := router.Group("/api/webhooks")
	webhooks.Use(authMiddleware)
	{
		webhooks.GET("/token", requireCap(domain.CapWorkflowsManage), h.GetWebhookToken)
		webhooks.POST("/reveal-secret", requireCap(domain.CapWorkflowsManage), h.RevealWebhookSecret)
		webhooks.POST("/regenerate-secret", requireCap(domain.CapWorkflowsManage), h.RegenerateWebhookSecret)
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
	result := ValidateWorkflowPayload(req.Trigger, req.Conditions, req.Actions, req.Steps)
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
		Steps:       req.Steps,
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
	q := c.Query("q")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	workflows, total, err := h.repo.ListWorkflows(c.Request.Context(), orgID, activeOnly, q, page, size)
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
	if req.Steps != nil {
		wf.Steps = req.Steps
	}

	// Re-validate
	result := ValidateWorkflowPayload(wf.Trigger, wf.Conditions, wf.Actions, wf.Steps)
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

// RunNow handles POST /api/workflows/:id/run — a real, single-workflow execution
// against a sample contact or deal. Unlike TestRun (a dry run with no side effects and
// no Workflow_Run), RunNow drives the Automation_Engine to create and execute a real run
// for ONLY the workflow identified by :id, with all side effects.
//
// Validation follows a fixed rejection order, returning early on the first failure so a
// request is rejected at the most specific applicable stage and no run is created for a
// rejected request (Req 1.3, 2.x, 3.1, 4.4, 6.7, 7.5). The compatibility check runs
// before entity loading and run creation so an incompatible request can never produce a
// created-then-failed run (Req 4.4).
//
// Authorization is enforced here rather than via route middleware: owner/admin/manager may
// run any workflow in the org, while any other caller may run ONLY a workflow they created
// (creator allowance, see authorizeRunNow). The check runs right after the workflow is
// loaded — it needs the workflow's CreatedBy — and before any side effect, so an
// unauthorized request yields 403 and never creates a run.
func (h *Handler) RunNow(c *gin.Context) {
	// Auth / org context — getContext writes the 401 itself (Req 1.3). userID identifies
	// the caller for the creator allowance below.
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	// Path :id must be a valid UUID (Req 2.6).
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid workflow ID", nil)
		return
	}

	// Bind body and classify it into exactly one entity target (Req 2.1–2.5).
	var req RunNowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body", nil)
		return
	}

	kind, entityID, err := classifyRunNowRequest(req)
	if err != nil {
		if errors.Is(err, ErrRunNowInvalidUUID) {
			h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", err.Error(), nil)
		} else {
			h.errorResponse(c, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		}
		return
	}

	ctx := c.Request.Context()

	// Workflow must exist within the caller's org (Req 1.5, 3.1).
	wf, err := h.repo.GetWorkflowByID(ctx, orgID, id)
	if err != nil || wf == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "workflow not found", nil)
		return
	}

	// Authorization (creator allowance): owner/admin/manager may run any workflow in the
	// org; any other caller may run only a workflow they created. Enforced after the load
	// (needs wf.CreatedBy) and before any side effect, so an unauthorized request never
	// creates a run.
	roleVal, _ := c.Get("role")
	role, _ := roleVal.(string)
	if !h.authorizeRunNowCtx(c, role, userID, wf.CreatedBy) {
		h.errorResponse(c, http.StatusForbidden, "FORBIDDEN",
			"you do not have permission to run this workflow", nil)
		return
	}

	// Resolve the workflow's trigger type from wf.Trigger (a datatypes.JSON holding a
	// TriggerSpec) the same way the scheduler/validator/engine do.
	var triggerSpec TriggerSpec
	_ = json.Unmarshal(wf.Trigger, &triggerSpec)
	triggerType := triggerSpec.Type

	// Compatibility check BEFORE loading the entity or creating any run (Req 4.1–4.4).
	expectedKind := entityKindForTrigger(triggerType)
	if expectedKind == "" || expectedKind != kind {
		h.errorResponse(c, http.StatusBadRequest, "INCOMPATIBLE_ENTITY",
			fmt.Sprintf("workflow trigger type %q is not compatible with the selected %s entity", triggerType, kind),
			nil)
		return
	}

	// Load the entity scoped to org. A nil map means not found (Req 3.2–3.4); a returned
	// error is an internal failure.
	var entity map[string]any
	switch kind {
	case "contact":
		entity, err = h.loadContactForRun(ctx, orgID, entityID)
	case "deal":
		entity, err = h.loadDealForRun(ctx, orgID, entityID)
	}
	if err != nil {
		h.logger.Error("run now: failed to load entity", "error", err, "kind", kind, "entity_id", entityID.String())
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load entity", nil)
		return
	}
	if entity == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("%s not found", kind), nil)
		return
	}

	// Build the Trigger_Context mirroring a real event payload (source = run_now,
	// no _internal_update marker) — Req 5.1–5.6.
	payload := buildRunNowTriggerContext(kind, triggerType, entity)

	// Targeted real execution. A synchronous failure yields a 500 with no run id
	// (Req 6.7, 7.5).
	runID, err := h.engine.RunWorkflowNow(ctx, orgID, wf, triggerType, payload)
	if err != nil {
		h.logger.Error("run now: failed to initiate run", "error", err, "workflow_id", wf.ID.String())
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to initiate run", nil)
		return
	}

	// Success: 201 with the created run's id and status (Req 7.2, 7.3).
	c.JSON(http.StatusCreated, gin.H{
		"data": RunNowResponse{
			ID:     runID,
			Status: StatusPending,
		},
	})
}

// --- Run history ---

// ListRuns handles GET /api/workflows/:id/runs.
//
// The workflow is resolved within the caller's org BEFORE its runs are listed: a workflow
// that does not exist in the caller's org yields 404, so another org's run history (and the
// PII its trigger context can carry) cannot be enumerated by guessing a workflow id.
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

	// Org scoping: the workflow must belong to the caller's org. A foreign-org (or unknown)
	// workflow is reported as not found so its runs cannot be enumerated cross-org.
	wf, err := h.repo.GetWorkflowByID(c.Request.Context(), orgID, id)
	if err != nil {
		h.logger.Error("list runs: failed to load workflow", "error", err, "workflow_id", id.String())
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load workflow", nil)
		return
	}
	if wf == nil {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "workflow not found", nil)
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

// GetRunDetail handles GET /api/workflows/runs/:runId.
//
// The run is scoped to the caller's org: a run that belongs to another org is reported as
// not found so its existence is not leaked. This matters because the detail (the run's
// trigger context and the action logs' resolved input/output) can contain PII. Mirrors the
// org-scoping in RetryRun.
func (h *Handler) GetRunDetail(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	runID, err := uuid.Parse(c.Param("runId"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid run ID", nil)
		return
	}

	run, err := h.repo.GetRunByID(c.Request.Context(), runID)
	if err != nil {
		h.logger.Error("get run detail: failed to load run", "error", err, "run_id", runID.String())
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load run", nil)
		return
	}
	// Org scoping: a foreign-org run (or a non-existent one) must be indistinguishable.
	if run == nil || run.OrgID != orgID {
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

// RetryRun handles POST /api/workflows/runs/:runId/retry — re-queues a FAILED run so it
// resumes from the step that failed, preserving the work already completed (P21). It does
// NOT restart the run from the beginning: the engine skips steps whose action logs are
// already successful, so prior side effects are not repeated.
//
// Only a failed run may be retried — a completed/skipped run has nothing to resume, and a
// pending/running run is already in flight; any other state is rejected 409 Conflict (the
// request is well-formed, but the run's state conflicts with the operation). The run must
// belong to the caller's org; a run from another org is reported as not found so its
// existence is not leaked.
//
// Authorization mirrors Run Now (so the route carries NO requireRole guard): owner/admin/
// manager may retry any run in the org, and any other caller may retry only a run whose
// workflow they created. The decision needs the workflow's CreatedBy, so it is enforced
// here after the workflow is loaded, before any state change. A successful retry is written
// to the structured log as an audit event (actor + timestamp).
func (h *Handler) RetryRun(c *gin.Context) {
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	runID, err := uuid.Parse(c.Param("runId"))
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", "invalid run ID", nil)
		return
	}

	ctx := c.Request.Context()

	run, err := h.repo.GetRunByID(ctx, runID)
	if err != nil {
		h.logger.Error("retry run: failed to load run", "error", err, "run_id", runID.String())
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load run", nil)
		return
	}
	// Org scoping: a foreign-org run (or a non-existent one) must be indistinguishable.
	if run == nil || run.OrgID != orgID {
		h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "run not found", nil)
		return
	}

	// Authorization (creator allowance): owner/admin/manager may retry any run; any other
	// caller may retry only a run whose workflow they created. Load the workflow for its
	// CreatedBy — a soft-deleted/absent workflow yields uuid.Nil, so only the privileged
	// roles can retry an orphaned run.
	var createdBy uuid.UUID
	if wf, werr := h.repo.GetWorkflowByID(ctx, orgID, run.WorkflowID); werr == nil && wf != nil {
		createdBy = wf.CreatedBy
	}
	roleVal, _ := c.Get("role")
	role, _ := roleVal.(string)
	if !h.authorizeRunNowCtx(c, role, userID, createdBy) {
		h.errorResponse(c, http.StatusForbidden, "FORBIDDEN",
			"you do not have permission to retry this run", nil)
		return
	}

	// Only failed runs are retryable. 409 Conflict (not 400): the request is well-formed,
	// but the run's state conflicts with the operation. Surface the current status so the
	// client can explain why the action is unavailable.
	if run.Status != StatusFailed {
		h.errorResponse(c, http.StatusConflict, "INVALID_STATE",
			fmt.Sprintf("only failed runs can be retried (current status: %s)", run.Status), nil)
		return
	}

	if err := h.engine.RetryRun(ctx, run.ID); err != nil {
		if errors.Is(err, ErrRunNotRetryable) {
			// Lost the race: the run left the failed state between our read and the locked
			// reset — also a state conflict, so 409.
			h.errorResponse(c, http.StatusConflict, "INVALID_STATE",
				"run is no longer in a retryable state", nil)
			return
		}
		h.logger.Error("retry run: failed to re-queue", "error", err, "run_id", run.ID.String())
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to retry run", nil)
		return
	}

	// Audit event: who re-queued which run, and when. Emitted to the structured log (the
	// app has no dedicated audit store); the explicit event tag keeps it filterable.
	h.logger.Info("audit: workflow run retried",
		"event", "workflow_run.retried",
		"actor_user_id", userID.String(),
		"org_id", orgID.String(),
		"workflow_id", run.WorkflowID.String(),
		"run_id", run.ID.String(),
		"at", time.Now().UTC().Format(time.RFC3339),
	)

	// The run resumes under its existing id (it is re-queued, not cloned); report the new
	// pending status. Reuses the {id,status} envelope shared with Run Now.
	c.JSON(http.StatusOK, gin.H{
		"data": RunNowResponse{
			ID:     run.ID,
			Status: StatusPending,
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

	// Dispatch on context.Background(), NOT c.Request.Context(): TriggerEvent is
	// fire-and-forget (engine.go runs triggerEventInternal in a goroutine), and gin
	// cancels the request context the instant this handler returns its 200. Using the
	// request context would cancel the goroutine's GetActiveWorkflowsByTrigger/CreateRun
	// queries mid-flight, so the contact would be upserted but no workflow run would ever
	// be created. The regular contact handler dispatches on context.Background() for the
	// same reason.
	h.engine.TriggerEvent(context.Background(), token.OrgID, eventType, triggerPayload)

	c.JSON(http.StatusOK, WebhookInboundResponse{
		Status:    "accepted",
		ContactID: contactID.String(),
	})
}

// GetWebhookToken handles GET /api/webhooks/token.
//
// It returns the current org's inbound-webhook token, a MASKED secret (last 4
// chars only), and the absolute URL external systems POST to — so the builder's
// webhook trigger can show real setup instructions (P17). The full secret is
// never returned here; it is shown exactly once by RegenerateWebhookSecret. The
// token (with a secret) is created on first call so the URL is always available.
// Admin/manager only.
func (h *Handler) GetWebhookToken(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	token, err := h.getOrCreateOrgToken(c.Request.Context(), orgID)
	if err != nil {
		h.logger.Error("get/create org webhook token failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load webhook token", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": WebhookTokenResponse{
			Token:        token.Token,
			SecretMasked: maskSecret(token.Secret),
			URL:          inboundWebhookURL(requestScheme(c), c.Request.Host, token.Token),
		},
	})
}

// RevealWebhookSecret handles POST /api/webhooks/reveal-secret.
//
// It returns the org's CURRENT signing secret in full, for on-demand reveal/copy
// in the setup UI — the listing GET only ever returns the masked form. It does not
// rotate; the secret is unchanged. POST (not GET) keeps the secret out of URLs and
// proxy/browser caches and marks it as an explicit, auditable retrieval.
// Admin/manager only.
func (h *Handler) RevealWebhookSecret(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	token, err := h.getOrCreateOrgToken(c.Request.Context(), orgID)
	if err != nil {
		h.logger.Error("get/create org webhook token failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load webhook secret", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": WebhookSecretRevealResponse{Secret: token.Secret},
	})
}

// RegenerateWebhookSecret handles POST /api/webhooks/regenerate-secret.
//
// It rotates the org's signing secret and returns the new secret in FULL — this
// is the only time the full secret is exposed, so the caller must capture it
// immediately (subsequent GETs return only the masked form). Rotating invalidates
// the previous secret: inbound requests signed with the old secret stop verifying.
// The token (and therefore the inbound URL) is left unchanged. Admin/manager only.
func (h *Handler) RegenerateWebhookSecret(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}

	token, err := h.getOrCreateOrgToken(c.Request.Context(), orgID)
	if err != nil {
		h.logger.Error("get/create org webhook token failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load webhook token", nil)
		return
	}

	newSecret := GenerateToken(64)
	if err := h.repo.UpdateOrgSecret(c.Request.Context(), orgID, newSecret); err != nil {
		h.logger.Error("rotate org webhook secret failed", "error", err)
		h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to rotate webhook secret", nil)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": WebhookSecretResponse{
			Token:  token.Token,
			Secret: newSecret, // full — shown exactly once
			URL:    inboundWebhookURL(requestScheme(c), c.Request.Host, token.Token),
		},
	})
}

// getOrCreateOrgToken returns the org's webhook token row, creating one (with a
// random token + secret) if none exists yet. Safe against a concurrent first
// create: on a duplicate-insert it re-reads and returns the winning row.
func (h *Handler) getOrCreateOrgToken(ctx context.Context, orgID uuid.UUID) (*WorkflowOrgToken, error) {
	token, err := h.repo.GetOrgTokenByOrgID(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if token != nil {
		return token, nil
	}

	token = &WorkflowOrgToken{
		OrgID:  orgID,
		Token:  GenerateToken(32),
		Secret: GenerateToken(64),
	}
	if err := h.repo.CreateOrgToken(ctx, token); err != nil {
		// A concurrent caller may have created the row (OrgID is the primary key)
		// between our read and write — re-read and prefer the persisted winner.
		existing, refErr := h.repo.GetOrgTokenByOrgID(ctx, orgID)
		if refErr != nil {
			return nil, refErr
		}
		if existing == nil {
			return nil, err
		}
		return existing, nil
	}
	return token, nil
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

// schemaCustomField is one admin-defined field on a system object, used to build
// the workflow builder's custom-field pickers.
type schemaCustomField struct {
	Slug    string
	Key     string
	Label   string
	Type    string
	Options []string
}

// systemCustomFieldDefs returns the admin-defined (is_system=false) fields of the
// org's system objects from object_fields. After the P7 cutover this is the source
// of truth for the workflow schema's custom-field picker — the legacy
// org_settings.custom_field_defs blob is no longer written. Reads directly via the
// shared *gorm.DB (the automation package must not depend on internal/usecase),
// mirroring how the rest of GetWorkflowSchema loads stages/tags/users.
func (h *Handler) systemCustomFieldDefs(ctx context.Context, orgID uuid.UUID) []schemaCustomField {
	type row struct {
		Slug    string          `gorm:"column:slug"`
		Key     string          `gorm:"column:key"`
		Label   string          `gorm:"column:label"`
		Type    string          `gorm:"column:type"`
		Options json.RawMessage `gorm:"column:options"`
	}
	var rows []row
	if err := h.db.WithContext(ctx).
		Table("object_fields AS f").
		Select("d.slug AS slug, f.key AS key, f.label AS label, f.type AS type, f.options AS options").
		Joins("JOIN object_defs d ON d.id = f.object_def_id AND d.is_system = true AND d.deleted_at IS NULL").
		Where("f.org_id = ? AND f.is_system = ? AND f.deleted_at IS NULL", orgID, false).
		Order("d.slug ASC, f.position ASC").
		Scan(&rows).Error; err != nil {
		return nil
	}
	out := make([]schemaCustomField, 0, len(rows))
	for _, r := range rows {
		var opts []string
		if len(r.Options) > 0 {
			_ = json.Unmarshal(r.Options, &opts)
		}
		out = append(out, schemaCustomField{Slug: r.Slug, Key: r.Key, Label: r.Label, Type: r.Type, Options: opts})
	}
	return out
}

// customObjSchema is one custom object plus its fields, for the workflow builder.
type customObjSchema struct {
	Slug   string
	Label  string
	Icon   string
	Fields []schemaCustomField
}

// customObjectSchemas returns the org's custom objects and their fields from the
// registry (object_defs is_system=false + object_fields). After the P7 convergence
// custom objects live here, not in custom_object_defs.
func (h *Handler) customObjectSchemas(ctx context.Context, orgID uuid.UUID) []customObjSchema {
	type defRow struct {
		ID    uuid.UUID `gorm:"column:id"`
		Slug  string    `gorm:"column:slug"`
		Label string    `gorm:"column:label"`
		Icon  string    `gorm:"column:icon"`
	}
	var defs []defRow
	if err := h.db.WithContext(ctx).
		Table("object_defs").
		Select("id, slug, label, icon").
		Where("org_id = ? AND is_system = ? AND deleted_at IS NULL", orgID, false).
		Order("created_at ASC").
		Scan(&defs).Error; err != nil {
		return nil
	}
	out := make([]customObjSchema, 0, len(defs))
	for _, d := range defs {
		type fRow struct {
			Key     string          `gorm:"column:key"`
			Label   string          `gorm:"column:label"`
			Type    string          `gorm:"column:type"`
			Options json.RawMessage `gorm:"column:options"`
		}
		var frows []fRow
		if err := h.db.WithContext(ctx).
			Table("object_fields").
			Select("key, label, type, options").
			Where("object_def_id = ? AND is_system = ? AND deleted_at IS NULL", d.ID, false).
			Order("position ASC").
			Scan(&frows).Error; err != nil {
			continue
		}
		cs := customObjSchema{Slug: d.Slug, Label: d.Label, Icon: d.Icon}
		for _, fr := range frows {
			var opts []string
			if len(fr.Options) > 0 {
				_ = json.Unmarshal(fr.Options, &opts)
			}
			cs.Fields = append(cs.Fields, schemaCustomField{Slug: d.Slug, Key: fr.Key, Label: fr.Label, Type: fr.Type, Options: opts})
		}
		out = append(out, cs)
	}
	return out
}

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

	// 2. Custom field definitions (object_fields; P7 — was the org_settings blob)
	for _, d := range h.systemCustomFieldDefs(ctx, orgID) {
		field := SchemaField{
			Path:  d.Slug + ".custom_fields." + d.Key,
			Label: d.Label,
			Type:  d.Type,
		}
		if d.Type == "select" {
			field.Options = d.Options
		}
		// Append to the matching entity
		for i := range entities {
			if entities[i].Key == d.Slug {
				entities[i].Fields = append(entities[i].Fields, field)
			}
		}
	}

	// 3. Custom object definitions (registry; P7 — was custom_object_defs)
	var customObjects []SchemaEntity
	for _, obj := range h.customObjectSchemas(ctx, orgID) {
		entity := SchemaEntity{Key: obj.Slug, Label: obj.Label, Icon: obj.Icon}
		for _, f := range obj.Fields {
			sf := SchemaField{Path: obj.Slug + "." + f.Key, Label: f.Label, Type: f.Type}
			if f.Type == "select" {
				sf.Options = f.Options
			}
			entity.Fields = append(entity.Fields, sf)
		}
		customObjects = append(customObjects, entity)
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

	// Custom objects from the registry (object_defs; P7)
	for _, obj := range h.customObjectSchemas(ctx, orgID) {
		icon := obj.Icon
		if icon == "" {
			icon = "📦"
		}
		objects = append(objects, ObjectListItem{Name: obj.Slug, Label: obj.Label, Icon: icon})
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

		// Append custom field definitions for this entity (object_fields; P7)
		for _, d := range h.systemCustomFieldDefs(ctx, orgID) {
			if d.Slug != slug {
				continue
			}
			item := FieldListItem{
				Name:  d.Slug + ".custom_fields." + d.Key,
				Label: d.Label,
				Type:  normalizeFieldType(d.Type, ""),
			}
			if d.Type == "select" {
				item.PicklistValues = d.Options
			}
			fields = append(fields, item)
		}
	} else {
		// Custom object — look up from the registry (object_defs/object_fields; P7)
		found := false
		for _, obj := range h.customObjectSchemas(ctx, orgID) {
			if obj.Slug != slug {
				continue
			}
			found = true
			for _, f := range obj.Fields {
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
		if !found {
			h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", "Object not found: "+slug, nil)
			return
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

// inboundWebhookURL assembles the absolute URL external systems POST inbound
// webhooks to. It mirrors the route registered at
// POST /api/webhooks/inbound/:org_token.
func inboundWebhookURL(scheme, host, token string) string {
	return fmt.Sprintf("%s://%s/api/webhooks/inbound/%s", scheme, host, token)
}

// maskSecret returns a display-safe form of a signing secret, revealing only the
// last 4 characters (e.g. "••••••••••••3f2a"). The bullet run is fixed-width so
// the true secret length is not leaked. Used by GET /api/webhooks/token; the full
// secret is only ever returned by the regenerate endpoint.
func maskSecret(secret string) string {
	const visible = 4
	if len(secret) <= visible {
		return strings.Repeat("•", len(secret))
	}
	return strings.Repeat("•", 12) + secret[len(secret)-visible:]
}

// requestScheme returns the best-effort external scheme (http/https) for a
// request, honoring X-Forwarded-Proto set by an upstream proxy/load balancer and
// otherwise falling back to the connection's TLS state.
func requestScheme(c *gin.Context) string {
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		// May be a comma-separated list (proxy chain); the first is the client-facing one.
		if i := strings.IndexByte(proto, ','); i >= 0 {
			proto = proto[:i]
		}
		return strings.TrimSpace(proto)
	}
	if c.Request.TLS != nil {
		return "https"
	}
	return "http"
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
