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
	"github.com/jackc/pgx/v5/pgconn"
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
	// Retry. Required as of P8 (NewHandler panics on nil): the legacy
	// owner/admin/manager name fallback was deleted, so Run Now / Retry authorize
	// purely on the capability (plus the creator allowance).
	capChecker domain.CapabilityChecker
	// authz stamps the P5a audit trail for the inbound-webhook contact upsert, which
	// runs as the system actor (P8). nil (unit tests) simply skips the audit.
	authz domain.RecordAuthorizer
	// draftAI backs the AI copilot's NL→draft endpoint (A7). nil disables it (the
	// endpoint returns 503). Set via SetDraftAI from main.go.
	draftAI draftAICaller
	// appEnv gates the WEBHOOK_SKIP_SIGNATURE escape hatch (L7.3). The ZERO VALUE
	// disables it, which is the whole point: an unset field means production, so a
	// handler built without SetAppEnv can never be talked out of verifying a
	// signature. Set via SetAppEnv from main.go.
	appEnv string
}

// SetAppEnv tells the handler which environment it is running in, using the same
// exact-allowlist convention as usecase.debugTokensEnabled. Anything other than
// "development" or "test" is production.
func (h *Handler) SetAppEnv(env string) { h.appEnv = env }

// skipSignatureAllowed reports whether the inbound webhook may accept an unsigned
// body.
//
// WEBHOOK_SKIP_SIGNATURE was a bare os.Getenv read with no environment gate: setting
// it anywhere turned off HMAC verification for EVERY org's public endpoint at once,
// with no log line, no UI indication, and no record of the variable's existence —
// it is in no config struct, no BindEnv, and neither .env.example. A production
// deployment that inherited it from a dev shell would have handed anyone who could
// read an org token (it travels in the URL, and the URL is written to the access log)
// the ability to create contacts and fire workflows in that workspace.
//
// The gate is an exact allowlist rather than `!= "production"` for the reason P10 P1
// records: APP_ENV is unset on prod today, so a negative match fails OPEN — which is
// exactly how account-takeover debug tokens once shipped.
func (h *Handler) skipSignatureAllowed() bool {
	if h.appEnv != "development" && h.appEnv != "test" {
		return false
	}
	return os.Getenv("WEBHOOK_SKIP_SIGNATURE") == "true"
}

// NewHandler creates a new automation HTTP handler. capChecker is required: Run Now
// and Retry authorize on workflows.run_any (there is no role-name fallback as of
// P8), so a nil checker is a wiring bug and panics rather than silently failing
// open. authz stamps the webhook-inbound audit trail (may be nil in unit tests).
func NewHandler(engine *Engine, db *gorm.DB, logger *slog.Logger, capChecker domain.CapabilityChecker, authz domain.RecordAuthorizer) *Handler {
	if capChecker == nil {
		panic("automation.NewHandler: capChecker is required (Run Now / Retry authorize on workflows.run_any; the role-name fallback was removed in P8)")
	}
	return &Handler{
		engine:      engine,
		repo:        engine.Repo(),
		db:          db,
		logger:      logger,
		rateLimiter: newTokenBucket(),
		schemaCache: NewSchemaCache(60 * time.Second),
		capChecker:  capChecker,
		authz:       authz,
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

		// AI copilot (A7): natural-language → workflow draft. Manage-gated (drafting
		// is authoring). Static "ai" segment coexists with "/:id" like "/schema".
		// Never saves — the client applies the returned draft through the same zod
		// validation as a manual edit.
		workflows.POST("/ai/draft", requireCap(domain.CapWorkflowsManage), h.DraftWorkflow)
		// Lightweight probe of the AI path (gateway + CF creds + model) via one tiny
		// model call — hit this to confirm the copilot works end-to-end after config.
		workflows.GET("/ai/health", requireCap(domain.CapWorkflowsManage), h.DraftHealth)

		// Email templates library (A5). All manage-gated. Registered before the
		// "/:id" param routes' subtree is unaffected — gin allows the static
		// "email-templates" segment alongside "/:id" (same as "/schema", "/runs").
		workflows.GET("/email-templates", requireCap(domain.CapWorkflowsManage), h.ListEmailTemplates)
		workflows.POST("/email-templates", requireCap(domain.CapWorkflowsManage), h.CreateEmailTemplate)
		workflows.GET("/email-templates/:id", requireCap(domain.CapWorkflowsManage), h.GetEmailTemplate)
		workflows.PUT("/email-templates/:id", requireCap(domain.CapWorkflowsManage), h.UpdateEmailTemplate)
		workflows.DELETE("/email-templates/:id", requireCap(domain.CapWorkflowsManage), h.DeleteEmailTemplate)
		workflows.POST("/email-templates/:id/test-send", requireCap(domain.CapWorkflowsManage), h.TestSendEmailTemplate)
		// Run Now intentionally has NO route-level capability guard: authorization is
		// enforced inside h.RunNow (a caller with the workflows.run_any capability may
		// run any workflow; any other caller may run only a workflow they created — the
		// creator allowance). A static route guard cannot express the creator check
		// because it needs the loaded workflow's CreatedBy.
		workflows.POST("/:id/run", h.RunNow)
		workflows.GET("/:id/runs", h.ListRuns)
		workflows.GET("/runs/:runId", h.GetRunDetail)
		// Retry a failed run (P21): re-queues it to resume from the failed step. Like Run
		// Now, it carries no route-level capability guard — authorization (the
		// workflows.run_any capability, or the workflow's creator) is enforced inside
		// h.RetryRun, which needs the workflow's CreatedBy.
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

// hasSteps reports whether a steps JSON payload holds a non-empty tree.
func hasSteps(steps datatypes.JSON) bool {
	return len(steps) > 0 && string(steps) != "null" && string(steps) != "[]"
}

// deriveActionsFromSteps re-derives the deprecated flat actions column from
// the canonical steps tree so legacy consumers (TestRun, the flat execution
// path) keep working until the column's scheduled removal (A8, 2026-09-01).
func deriveActionsFromSteps(stepsJSON datatypes.JSON) (datatypes.JSON, error) {
	var steps []StepSpec
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return nil, err
	}
	flat := FlattenStepsToActions(steps)
	if flat == nil {
		flat = []ActionSpec{}
	}
	out, err := json.Marshal(flat)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(out), nil
}

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

	// Steps are the canonical format: when present, the deprecated flat actions
	// column is derived server-side and any client-sent actions are ignored.
	// Actions-only bodies stay accepted until the column's removal (A8).
	if hasSteps(req.Steps) {
		derived, err := deriveActionsFromSteps(req.Steps)
		if err != nil {
			h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid steps JSON: "+err.Error(), nil)
			return
		}
		req.Actions = derived
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

	h.armWorkflowTimers(c.Request.Context(), wf)

	resp := ToWorkflowResponse(wf)
	c.JSON(http.StatusCreated, gin.H{"data": resp})
}

// armWorkflowTimers reconciles a workflow's time-based-trigger timers after a
// create/update/toggle (A4). A bad cron only fails arming, never the save — the
// workflow persists and simply won't fire until the schedule is corrected (the FE
// validates the cron; this is defense in depth).
func (h *Handler) armWorkflowTimers(ctx context.Context, wf *Workflow) {
	if err := h.repo.ArmScheduleTimer(ctx, wf, time.Now()); err != nil {
		h.logger.Warn("automation: arm schedule timer failed", "error", err, "workflow_id", wf.ID.String())
	}
	// date_field timers are materialized per-record on writes (not armed here), but a
	// deactivate or a change of trigger away from date_field must drop the workflow's
	// pending date_field timers so they don't fire against the old config.
	if !isActiveDateFieldWorkflow(wf) {
		if err := h.repo.CancelWorkflowTimers(ctx, wf.ID, TimerKindDateField); err != nil {
			h.logger.Warn("automation: cancel date_field timers failed", "error", err, "workflow_id", wf.ID.String())
		}
	}
}

// isActiveDateFieldWorkflow reports whether wf is an active workflow with a
// date_field trigger.
func isActiveDateFieldWorkflow(wf *Workflow) bool {
	if !wf.IsActive {
		return false
	}
	var trig TriggerSpec
	if err := json.Unmarshal(wf.Trigger, &trig); err != nil {
		return false
	}
	return trig.Type == TriggerDateField
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

	// Steps are canonical: re-derive the deprecated flat actions from the
	// effective steps tree, overriding any client-sent actions (A1).
	if hasSteps(wf.Steps) {
		derived, err := deriveActionsFromSteps(wf.Steps)
		if err != nil {
			h.errorResponse(c, http.StatusBadRequest, "VALIDATION_FAILED", "invalid steps JSON: "+err.Error(), nil)
			return
		}
		wf.Actions = derived
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

	h.armWorkflowTimers(c.Request.Context(), wf)

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

	// Cancel any pending time-based timers so the scanner doesn't churn on a
	// deleted workflow (it already guards firing on the workflow being active).
	_ = h.repo.CancelWorkflowTimers(c.Request.Context(), id, TimerKindSchedule)
	_ = h.repo.CancelWorkflowTimers(c.Request.Context(), id, TimerKindDateField)

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

	// Arm (now-active) or cancel (now-inactive) the workflow's schedule timer.
	h.armWorkflowTimers(c.Request.Context(), wf)

	c.JSON(http.StatusOK, gin.H{"data": ToWorkflowResponse(wf)})
}

// TestRun handles POST /api/workflows/:id/test-run
func (h *Handler) TestRun(c *gin.Context) {
	orgID, userID := h.getContext(c)
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

	ctx := c.Request.Context()

	// Resolve the trigger type (for sample-entity compatibility + context shape).
	var triggerSpec TriggerSpec
	_ = json.Unmarshal(wf.Trigger, &triggerSpec)
	triggerType := triggerSpec.Type

	// Build the dry-run trigger context. Preferred: a sample entity the server loads
	// (realistic data, mirrors Run Now). Fallback: a raw context map (tests/advanced).
	var payload map[string]any
	if req.ContactID != "" || req.DealID != "" {
		// The sample path loads a real org record and echoes its interpolated fields
		// back in resolved_params, so gate it like Run Now (creator or run_any) — a
		// bare workflows.manage holder can't dry-run another user's workflow against
		// an arbitrary record to read its fields. The raw-context path needs no such
		// gate: the caller supplies the data, so there's no server-side record read.
		if !h.authorizeRunNowCtx(c, userID, wf.CreatedBy) {
			h.errorResponse(c, http.StatusForbidden, "FORBIDDEN",
				"you do not have permission to test this workflow against a sample record", nil)
			return
		}
		kind, entityID, cerr := classifyRunNowRequest(RunNowRequest{ContactID: req.ContactID, DealID: req.DealID})
		if cerr != nil {
			if errors.Is(cerr, ErrRunNowInvalidUUID) {
				h.errorResponse(c, http.StatusBadRequest, "INVALID_ID", cerr.Error(), nil)
			} else {
				h.errorResponse(c, http.StatusBadRequest, "INVALID_REQUEST", cerr.Error(), nil)
			}
			return
		}
		expectedKind := entityKindForTrigger(triggerType)
		if expectedKind == "" || expectedKind != kind {
			h.errorResponse(c, http.StatusBadRequest, "INCOMPATIBLE_ENTITY",
				fmt.Sprintf("workflow trigger type %q is not compatible with the selected %s entity", triggerType, kind), nil)
			return
		}
		var entity map[string]any
		switch kind {
		case "contact":
			entity, err = h.loadContactForRun(ctx, orgID, entityID)
		case "deal":
			entity, err = h.loadDealForRun(ctx, orgID, entityID)
		}
		if err != nil {
			h.logger.Error("test run: failed to load entity", "error", err, "kind", kind, "entity_id", entityID.String())
			h.errorResponse(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load entity", nil)
			return
		}
		if entity == nil {
			h.errorResponse(c, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("%s not found", kind), nil)
			return
		}
		payload = buildRunNowTriggerContext(kind, triggerType, entity)
	} else {
		payload = req.Context
		if payload == nil {
			payload = map[string]any{}
		}
	}

	// Side-effect-free steps-tree walk (per-step run/skip + interpolation previews).
	c.JSON(http.StatusOK, gin.H{"data": h.engine.DryRun(orgID, wf, payload)})
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
// Authorization is enforced here rather than via route middleware: a caller with the
// workflows.run_any capability may run any workflow in the org, while any other caller may
// run ONLY a workflow they created (creator allowance, see authorizeRunNowCtx). The check
// runs right after the workflow is loaded — it needs the workflow's CreatedBy — and before
// any side effect, so an unauthorized request yields 403 and never creates a run.
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

	// Authorization: the creator may run their own workflow; any other caller must
	// hold workflows.run_any (P8 — no role-name fallback). Enforced after the load
	// (needs wf.CreatedBy) and before any side effect, so an unauthorized request
	// never creates a run.
	if !h.authorizeRunNowCtx(c, userID, wf.CreatedBy) {
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
// Authorization mirrors Run Now (so the route carries no route-level capability guard): a
// caller with the workflows.run_any capability may retry any run in the org, and any other
// caller may retry only a run whose workflow they created. The decision needs the workflow's
// CreatedBy, so it is enforced here after the workflow is loaded, before any state change. A
// successful retry is written to the structured log as an audit event (actor + timestamp).
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

	// Authorization (creator allowance): a caller with the workflows.run_any capability may
	// retry any run; any other caller may retry only a run whose workflow they created. Load
	// the workflow for its CreatedBy — a soft-deleted/absent workflow yields uuid.Nil, so only
	// a workflows.run_any holder can retry an orphaned run.
	var createdBy uuid.UUID
	if wf, werr := h.repo.GetWorkflowByID(ctx, orgID, run.WorkflowID); werr == nil && wf != nil {
		createdBy = wf.CreatedBy
	}
	if !h.authorizeRunNowCtx(c, userID, createdBy) {
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

	// Signature verification. The skip is honoured only in a dev/test environment —
	// see skipSignatureAllowed for why the env var alone is not enough.
	if !h.skipSignatureAllowed() {
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

	// Upsert contact by email.
	contactID, eventType, err := h.upsertWebhookContact(token.OrgID, email, contactData, customFields)
	if err != nil {
		// Answering 500 here is a deliberate change from the shipped behaviour, which
		// discarded every write error and still returned 200 with a freshly-minted
		// uuid. That 200 was not merely uninformative: it fired a contact_created
		// workflow against a contact id that does not exist, so runs referenced a
		// phantom record and the sender had no way to learn the lead was gone. A 5xx
		// is also the only answer a retrying integrator can act on.
		if h.logger != nil {
			h.logger.Error("inbound webhook contact upsert failed",
				"org_id", token.OrgID.String(), "error", err)
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: ErrorBody{Code: "INTERNAL_ERROR", Message: "failed to record the lead"},
		})
		return
	}

	// Audit the webhook-driven contact write as the SYSTEM actor (P8 webhook-run
	// actor). An inbound webhook is org-token-authenticated with no human creator,
	// so the audit row carries a nil actor (rendered as "System") plus the source,
	// keeping this automation write attributable in the per-record history like a
	// UI/API/workflow write. Synchronous + best-effort; a nil authz (tests) skips it.
	if h.authz != nil {
		action := domain.ActionCreate
		if eventType == TriggerContactUpdated {
			action = domain.ActionEdit
		}
		// context.Background(), NOT c.Request.Context(): the audit is a durable
		// side-effect that must complete even if the external webhook client
		// disconnects (which cancels the request context). The contact upsert above
		// already runs on h.db for the same reason.
		h.authz.Audit(context.Background(), domain.AuditEntry{
			OrgID:      token.OrgID,
			ActorID:    uuid.Nil, // system actor — inbound webhook, no human author
			ObjectSlug: "contact",
			RecordID:   contactID,
			Action:     action,
			Changes:    map[string]interface{}{"source": "webhook_inbound", "email": email},
		})
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

// contactEmailUniqueIndex is the partial unique index on contacts(org_id, email).
// Matched by NAME rather than by bare SQLSTATE 23505, for the reason its twin in
// internal/usecase/contact_usecase.go gives: contacts may grow another unique index,
// and a constraint-blind check would read that unrelated conflict as an email
// duplicate and update the WRONG row.
const contactEmailUniqueIndex = "idx_contacts_org_email"

func isContactEmailConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == contactEmailUniqueIndex
}

// upsertWebhookContact writes an inbound legacy-webhook lead and reports which
// lifecycle event it is (contact_created / contact_updated).
//
// It deliberately keeps the legacy FIELD SEMANTICS byte-for-byte — the same four
// direct fields, the same "every other key is a custom field", the same
// case-sensitive email match — because the trigger payload built from them is the
// interface every existing workflow is written against. What it fixes is the three
// ways the shipped version lost data while reporting success:
//
//  1. Every write error was discarded. A duplicate-email race, an over-length name
//     or a wrong-typed value all returned 200 with a contact id that was never
//     inserted. The race in particular is the shape a busy integrator hits.
//  2. custom_fields was REPLACED wholesale with only the current payload's unknown
//     keys, so a delivery carrying one custom field destroyed every other custom
//     field on that contact — including ones a human had typed.
//  3. A failed SELECT fell through to the INSERT branch, so a transient database
//     blip created a duplicate contact instead of updating the existing one.
func (h *Handler) upsertWebhookContact(orgID uuid.UUID, email string, contactData, customFields map[string]any) (uuid.UUID, string, error) {
	// context.Background(), matching the audit and trigger dispatch below: a client
	// that hangs up must not leave the lead half-written.
	ctx := context.Background()

	existingID, err := h.findContactByEmail(ctx, orgID, email)
	if err != nil {
		return uuid.Nil, "", err
	}
	if existingID == uuid.Nil {
		id := uuid.New()
		cfJSON, _ := json.Marshal(customFields)
		insertErr := h.db.WithContext(ctx).Exec(
			`INSERT INTO contacts (id, org_id, first_name, last_name, email, phone, custom_fields, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
			id, orgID, stringField(contactData, "first_name"), stringField(contactData, "last_name"), email,
			phoneValue(contactData), datatypes.JSON(cfJSON),
		).Error
		if insertErr == nil {
			return id, TriggerContactCreated, nil
		}
		if !isContactEmailConflict(insertErr) {
			return uuid.Nil, "", insertErr
		}
		// Two first-time deliveries for the same address raced and the other one won.
		// Re-read and fall through to the update: exactly one contact, and the loser
		// reports contact_updated, which is what actually happened. Re-matched ONCE —
		// the winner's row cannot disappear underneath us, and an unbounded loop here
		// would be a way to spin on a genuinely broken index.
		existingID, err = h.findContactByEmail(ctx, orgID, email)
		if err != nil {
			return uuid.Nil, "", err
		}
		if existingID == uuid.Nil {
			return uuid.Nil, "", insertErr
		}
	}

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
	if len(customFields) > 0 {
		cfJSON, _ := json.Marshal(customFields)
		// MERGE, never replace. One statement rather than a read-modify-write, so
		// concurrent deliveries for the same contact cannot lose each other's keys:
		// jsonb `||` is a shallow merge with the right-hand side winning, which is
		// precisely the legacy per-key intent minus the collateral damage.
		updates["custom_fields"] = gorm.Expr("COALESCE(contacts.custom_fields, '{}'::jsonb) || ?::jsonb", datatypes.JSON(cfJSON))
	}
	if len(updates) > 0 {
		// Set explicitly: Table()+map supplies no model schema, so GORM does not
		// track timestamps here and the row's updated_at would go stale on every
		// inbound update — invisible until someone sorts a list by it.
		updates["updated_at"] = gorm.Expr("NOW()")
		if uerr := h.db.WithContext(ctx).Table("contacts").Where("id = ?", existingID).Updates(updates).Error; uerr != nil {
			return uuid.Nil, "", uerr
		}
	}
	return existingID, TriggerContactUpdated, nil
}

// findContactByEmail returns the live contact id for an address, or uuid.Nil when
// there is none. A query FAILURE is returned as an error and never conflated with
// "no such contact" — that conflation is what made a database blip create a
// duplicate contact instead of updating the existing one.
func (h *Handler) findContactByEmail(ctx context.Context, orgID uuid.UUID, email string) (uuid.UUID, error) {
	var row struct {
		ID uuid.UUID `gorm:"column:id"`
	}
	res := h.db.WithContext(ctx).Raw(
		"SELECT id FROM contacts WHERE org_id = ? AND email = ? AND deleted_at IS NULL LIMIT 1",
		orgID, email).Scan(&row)
	if res.Error != nil {
		return uuid.Nil, res.Error
	}
	return row.ID, nil
}

// stringField reproduces the legacy type assertion exactly: a non-string value
// becomes "". Deliberately NOT widened to coerce numbers — unlike the phone case
// below there is no lost lead to recover here, so changing what lands in the column
// would be a product change wearing a bug fix's clothes.
func stringField(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// phoneValue renders a phone for the varchar column.
//
// The shipped code passed the raw JSON value straight into the INSERT, so a payload
// with a NUMERIC phone (a real shape — plenty of senders emit 5551234567 unquoted)
// failed the insert, and because the error was discarded the caller got a 200 and
// the lead vanished. Coercing scalars costs nothing and cannot regress stored data,
// since the alternative outcome was no row at all. The trigger payload keeps the
// caller's raw value, so no workflow sees a different phone than it does today.
func phoneValue(m map[string]any) any {
	switch v := m["phone"].(type) {
	case nil:
		return nil
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case json.Number:
		return v.String()
	default:
		// An object or an array is not a phone number by any reading; storing NULL
		// keeps the rest of the lead rather than failing the whole delivery over it.
		return nil
	}
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

// builtinObjectLabels/Icons describe the closed set of system objects the builder
// offers as first-class trigger sources and field owners. Company was previously
// absent (making it second-class); it's now included.
var builtinObjectMeta = []struct {
	Slug, Label, Icon string
}{
	{"contact", "Contact", "👤"},
	{"deal", "Deal", "💰"},
	{"company", "Company", "🏢"},
}

// builtinObjectFieldDefs is the single source of truth for the native fields of
// each system object, keyed by slug. Paths mirror the emitted event payload keys
// (contactToMap/dealToMap/companyAutomationMap) so {{slug.field}} resolves — most
// notably the owner column is owner_user_id (not owner_id). Admin-defined custom
// fields are layered on top from object_fields (systemCustomFieldDefs).
func builtinObjectFieldDefs(slug string) []SchemaField {
	switch slug {
	case "contact":
		return []SchemaField{
			{Path: "contact.first_name", Label: "First Name", Type: "string"},
			{Path: "contact.last_name", Label: "Last Name", Type: "string"},
			{Path: "contact.email", Label: "Email", Type: "string"},
			{Path: "contact.phone", Label: "Phone", Type: "string"},
			{Path: "contact.owner_user_id", Label: "Owner", Type: "string", PickerType: "user"},
			{Path: "contact.company_id", Label: "Company", Type: "string"},
			{Path: "contact.tags", Label: "Tags", Type: "array", PickerType: "tag"},
			{Path: "contact.id", Label: "Contact ID", Type: "string"},
		}
	case "deal":
		return []SchemaField{
			{Path: "deal.title", Label: "Title", Type: "string"},
			{Path: "deal.value", Label: "Value", Type: "number"},
			{Path: "deal.stage_id", Label: "Stage", Type: "string", PickerType: "stage"},
			{Path: "deal.probability", Label: "Probability (%)", Type: "number"},
			{Path: "deal.is_won", Label: "Is Won", Type: "boolean"},
			{Path: "deal.is_lost", Label: "Is Lost", Type: "boolean"},
			{Path: "deal.owner_user_id", Label: "Owner", Type: "string", PickerType: "user"},
			{Path: "deal.contact_id", Label: "Contact", Type: "string"},
			{Path: "deal.company_id", Label: "Company", Type: "string"},
			{Path: "deal.expected_close_at", Label: "Expected Close", Type: "date"},
			{Path: "deal.closed_at", Label: "Closed At", Type: "date"},
			{Path: "deal.id", Label: "Deal ID", Type: "string"},
		}
	case "company":
		return []SchemaField{
			{Path: "company.name", Label: "Name", Type: "string"},
			{Path: "company.industry", Label: "Industry", Type: "string"},
			{Path: "company.website", Label: "Website", Type: "string"},
			{Path: "company.id", Label: "Company ID", Type: "string"},
		}
	case "trigger":
		return []SchemaField{
			{Path: "trigger.type", Label: "Event Type", Type: "string"},
			{Path: "trigger.from_stage", Label: "Previous Stage", Type: "string", PickerType: "stage"},
			{Path: "trigger.to_stage", Label: "New Stage", Type: "string", PickerType: "stage"},
		}
	default:
		return nil
	}
}

// builtinSchemaEntities builds the system-object entities (contact/deal/company)
// plus the synthetic trigger pseudo-entity for the workflow schema response.
func builtinSchemaEntities() []SchemaEntity {
	entities := make([]SchemaEntity, 0, len(builtinObjectMeta)+1)
	for _, m := range builtinObjectMeta {
		entities = append(entities, SchemaEntity{
			Key: m.Slug, Label: m.Label, Icon: m.Icon,
			Fields: builtinObjectFieldDefs(m.Slug),
		})
	}
	entities = append(entities, SchemaEntity{
		Key: "trigger", Label: "Trigger Event", Icon: "⚡",
		Fields: builtinObjectFieldDefs("trigger"),
	})
	return entities
}

// GetWorkflowSchema handles GET /api/workflows/schema.
// Returns all available fields, stages, tags, users, and custom objects
// so the frontend builder can render smart pickers instead of raw text inputs.
func (h *Handler) GetWorkflowSchema(c *gin.Context) {
	orgID, _ := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.buildSchema(c.Request.Context(), orgID)})
}

// buildSchema assembles the org's full workflow schema (entities + custom objects
// + stages + tags + users), served by GetWorkflowSchema and reused by the AI
// copilot's get_workflow_schema tool (A7). Cached per-org (60s TTL).
func (h *Handler) buildSchema(ctx context.Context, orgID uuid.UUID) *SchemaResponse {
	// Check cache first
	if h.schemaCache != nil {
		if cached := h.schemaCache.Get(orgID); cached != nil {
			return cached
		}
	}

	// 1. Built-in entity fields. Paths must match the emitted event payload keys
	// (contactToMap/dealToMap/companyAutomationMap) so templates/conditions resolve
	// — e.g. the owner column is owner_user_id, not owner_id.
	entities := builtinSchemaEntities()

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

	return result
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

	// Built-in system objects (contact/deal/company — company was previously absent)
	for _, m := range builtinObjectMeta {
		objects = append(objects, ObjectListItem{Name: m.Slug, Label: m.Label, Icon: m.Icon})
	}

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

	// Built-in system objects share their field definitions with GetWorkflowSchema
	// (single source of truth), so the picker and the token list can never drift.
	if builtinDefs := builtinObjectFieldDefs(slug); builtinDefs != nil {
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
