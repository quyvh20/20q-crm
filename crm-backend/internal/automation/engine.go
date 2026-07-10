package automation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ErrRunNotRetryable is returned by RetryRun when the target run is no longer in the
// failed state by the time the atomic reset runs (e.g. it was already retried, or never
// failed). Handlers map it to a 409 Conflict.
var ErrRunNotRetryable = errors.New("automation: run is not in a retryable (failed) state")

// Engine is the core automation engine that manages workers and dispatches actions.
type Engine struct {
	db        *gorm.DB
	repo      *Repository
	logger    *slog.Logger
	jobs      chan WorkflowRunJob
	workers   int
	wg        sync.WaitGroup
	ctx       context.Context
	cancel    context.CancelFunc
	executors map[string]ActionExecutor
	scheduler *Scheduler
	// authz is the OLS/FLS + audit chokepoint the record-writing executors enforce
	// through, running as the workflow author (P8, §3.5). Same value REST/AI use
	// (PermissionUseCase). nil disables enforcement (unit tests) — then writes run
	// unrestricted, matching pre-P8 behavior.
	authz domain.RecordAuthorizer
	// callerResolver resolves the workflow author's identity so actions run as the
	// creator. nil (unit tests) ⇒ no caller attached ⇒ legacy trusted behavior.
	callerResolver CallerResolver
	// PostActionLogHook is called inside commitActionAndRun after both DB writes
	// (UpdateActionLogTx + UpdateRunTx) but before tx.Commit(). Exported so tests
	// can inject a panic to simulate a crash and verify that uncommitted writes
	// are rolled back atomically. Must be nil in production.
	PostActionLogHook func()
}

// WorkflowRunJob is pushed to the jobs channel to signal a run needs processing.
type WorkflowRunJob struct {
	RunID uuid.UUID
}

// NewEngine creates a new automation engine.
func NewEngine(db *gorm.DB, logger *slog.Logger, opts ...EngineOption) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		db:        db,
		repo:      NewRepository(db),
		logger:    logger,
		jobs:      make(chan WorkflowRunJob, 100),
		workers:   5,
		ctx:       ctx,
		cancel:    cancel,
		executors: make(map[string]ActionExecutor),
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// EngineOption configures the engine.
type EngineOption func(*Engine)

// WithWorkers sets the number of worker goroutines.
func WithWorkers(n int) EngineOption {
	return func(e *Engine) {
		if n > 0 {
			e.workers = n
		}
	}
}

// WithEmailExecutor registers an email executor. e.db is already set (NewEngine
// assigns it before applying options) so the executor can load library email
// templates for the send_email template_id path (A5).
func WithEmailExecutor(apiKey, fromEmail string) EngineOption {
	return func(e *Engine) {
		e.executors[ActionSendEmail] = NewEmailExecutor(e.db, apiKey, fromEmail)
	}
}

// WithNotificationExecutor registers the notify_user executor (A6). The creator
// is the platform NotificationUseCase, which inserts the inbox row and pushes it
// over the recipient's per-user SSE channel. e.db is already set (NewEngine
// assigns it before options) so the executor can enforce the per-run cap.
func WithNotificationExecutor(nc NotificationCreator) EngineOption {
	return func(e *Engine) {
		e.executors[ActionNotifyUser] = NewNotifyUserExecutor(e.db, nc)
	}
}

// WithCreateRecordExecutor registers the create_record executor (A6). The creator
// is the platform RecordService, so the create runs through the same uniform
// validation + OLS/FLS (as the workflow author) + audit + event path as REST/AI.
func WithCreateRecordExecutor(rc RecordCreator) EngineOption {
	return func(e *Engine) {
		e.executors[ActionCreateRecord] = NewCreateRecordExecutor(rc)
	}
}

// WithRecordActions registers the find_records + enroll_records executors (A6).
// Both read through the platform RecordService (OLS/FLS as the workflow author);
// enroll also creates runs in a target workflow, for which it uses the engine
// itself (which satisfies Enroller). Called at NewEngine time, so e is available
// to hand to the enroll executor.
func WithRecordActions(lister RecordLister) EngineOption {
	return func(e *Engine) {
		e.executors[ActionFindRecords] = NewFindRecordsExecutor(lister)
		e.executors[ActionEnrollRecords] = NewEnrollRecordsExecutor(e, lister)
	}
}

// WithAIGenerator registers the ai_generate executor (A7). The generator is an
// adapter over the AI gateway (bounded by TaskWorkflowAI's budget), so a workflow
// step can produce free-form text for later steps to consume.
func WithAIGenerator(gen AITextGenerator) EngineOption {
	return func(e *Engine) {
		e.executors[ActionAIGenerate] = NewAIGenerateExecutor(gen)
	}
}

// LoadWorkflow fetches a workflow by id within an org — the enroll_records target
// lookup. Part of the Enroller port *Engine satisfies.
func (e *Engine) LoadWorkflow(ctx context.Context, orgID, wfID uuid.UUID) (*Workflow, error) {
	return e.repo.GetWorkflowByID(ctx, orgID, wfID)
}

// EnrollRun creates and dispatches one run for target with the supplied trigger
// context + idempotency key, reusing the run-creation/dispatch machinery. Returns
// inserted=false when the idempotency key already exists (a duplicate enroll of the
// same source-run+record is an idempotent no-op). Part of the Enroller port.
func (e *Engine) EnrollRun(ctx context.Context, orgID uuid.UUID, target *Workflow, triggerCtx map[string]any, idempotencyKey string) (bool, error) {
	tc, err := json.Marshal(triggerCtx)
	if err != nil {
		return false, fmt.Errorf("marshal enroll trigger context: %w", err)
	}
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      target.ID,
		WorkflowVersion: target.Version,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(tc),
		IdempotencyKey:  idempotencyKey,
	}
	inserted, err := e.repo.CreateRun(ctx, run)
	if err != nil {
		return false, fmt.Errorf("create enrolled run: %w", err)
	}
	if inserted {
		select {
		case e.jobs <- WorkflowRunJob{RunID: run.ID}:
		default:
			e.logger.Warn("automation: jobs channel full, enrolled run will be picked up by scheduler",
				"run_id", run.ID.String(),
			)
		}
	}
	return inserted, nil
}

// SendTestEmail sends a one-off email through the registered email executor,
// bypassing a workflow run. Used by the email-template test-send endpoint (A5):
// the caller renders subject/body against a sample record, then delivers it.
// Returns an error when no email executor is configured.
func (e *Engine) SendTestEmail(ctx context.Context, to, subject, bodyHTML string) error {
	ex, ok := e.executors[ActionSendEmail].(*EmailExecutor)
	if !ok || ex == nil {
		return fmt.Errorf("email executor is not configured")
	}
	_, err := ex.sendEmail(ctx, "test-send", to, subject, bodyHTML, "", nil)
	return err
}

// WithAuthorizer wires the OLS/FLS + audit chokepoint (PermissionUseCase) so the
// record-writing executors enforce and audit as the workflow author (P8). Without
// it the executors write unrestricted (pre-P8 behavior), so prod MUST set it.
func WithAuthorizer(authz domain.RecordAuthorizer) EngineOption {
	return func(e *Engine) { e.authz = authz }
}

// WithCallerResolver wires the run-as-creator identity resolver (P8). Without it
// the engine attaches no caller and actions keep the legacy trusted behavior.
func WithCallerResolver(r CallerResolver) EngineOption {
	return func(e *Engine) { e.callerResolver = r }
}

// RegisterExecutor registers an action executor for a given action type.
func (e *Engine) RegisterExecutor(actionType string, executor ActionExecutor) {
	e.executors[actionType] = executor
}

// Start launches the worker pool, scheduler, and runs crash recovery.
func (e *Engine) Start() {
	e.logger.Info("automation engine starting", "workers", e.workers)

	// Register default executors if not already registered
	if _, ok := e.executors[ActionCreateTask]; !ok {
		e.executors[ActionCreateTask] = NewTaskExecutor(e.db, e.authz)
	}
	if _, ok := e.executors[ActionAssignUser]; !ok {
		e.executors[ActionAssignUser] = NewAssignUserExecutor(e.db, e.authz)
	}
	if _, ok := e.executors[ActionSendWebhook]; !ok {
		e.executors[ActionSendWebhook] = NewWebhookExecutor()
	}
	if _, ok := e.executors[ActionDelay]; !ok {
		e.executors[ActionDelay] = NewDelayExecutor()
	}
	if _, ok := e.executors[ActionUpdateRecord]; !ok {
		executor := NewUpdateRecordExecutor(e.db, e.authz)
		e.executors[ActionUpdateRecord] = executor
		e.executors[ActionUpdateContact] = executor // backward compat
	}
	if _, ok := e.executors[ActionLogActivity]; !ok {
		e.executors[ActionLogActivity] = NewActivityExecutor(e.db, e.authz)
	}

	// Run migrations
	if err := e.repo.AutoMigrate(); err != nil {
		e.logger.Error("automation: migration failed", "error", err)
	}

	// Crash recovery
	RequeueInFlight(e.ctx, e.repo, e.jobs, e.logger)

	// Start workers
	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker(i)
	}

	// Start scheduler
	e.scheduler = NewScheduler(e.db, e.repo, e, e.logger)
	e.scheduler.Start()

	e.logger.Info("automation engine started")
}

// Stop gracefully shuts down the engine.
func (e *Engine) Stop() {
	e.logger.Info("automation engine stopping")
	e.cancel()

	if e.scheduler != nil {
		e.scheduler.Stop()
	}

	close(e.jobs)

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		e.logger.Info("automation engine stopped cleanly")
	case <-time.After(30 * time.Second):
		e.logger.Warn("automation engine stop timeout — some workers still running")
	}
}

// TriggerEvent is called by external hooks (contact create, deal stage change, webhook inbound)
// to dispatch matching workflows. Fire-and-forget — returns immediately.
func (e *Engine) TriggerEvent(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any) {
	go func() {
		if err := e.triggerEventInternal(ctx, orgID, eventType, payload); err != nil {
			e.logger.Error("automation: TriggerEvent failed",
				"org_id", orgID.String(),
				"event_type", eventType,
				"error", err,
			)
		}
		// A4: materialize/re-arm date_field timers from this record write. Independent
		// of the event fan-out above (a date_field workflow doesn't subscribe to the
		// {slug}_updated event type), and a no-op when the org has no date_field
		// workflow for the object.
		if err := e.materializeDateFieldTimers(ctx, orgID, eventType, payload); err != nil {
			e.logger.Error("automation: date_field materialization failed",
				"org_id", orgID.String(),
				"event_type", eventType,
				"error", err,
			)
		}
	}()
}

// isInternalUpdate reports whether a trigger payload was produced by the automation
// engine itself (e.g. an update_record action mutating the triggering entity). Such
// events must not re-trigger workflows, or a "modify the entity on change" workflow
// would loop forever (contact_updated → update_record → contact_updated → ∞). The
// payload must carry _internal_update=true (a real bool) to be treated as internal.
func isInternalUpdate(payload map[string]any) bool {
	internal, ok := payload["_internal_update"].(bool)
	return ok && internal
}

func (e *Engine) triggerEventInternal(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any) error {
	// ── Infinite-loop guard ────────────────────────────────────────────
	// If the event was caused by the automation engine itself (e.g. an
	// update_contact action modifying a contact), skip re-triggering to
	// prevent contact_updated → update_contact → contact_updated → ∞.
	if isInternalUpdate(payload) {
		e.logger.Debug("automation: skipping re-trigger (internal update)",
			"event_type", eventType,
			"org_id", orgID.String(),
		)
		return nil
	}

	workflows, err := e.repo.GetActiveWorkflowsByTrigger(ctx, orgID, eventType)
	if err != nil {
		return fmt.Errorf("query workflows: %w", err)
	}

	for _, wf := range workflows {
		// --- Field-level trigger filtering (watch_field / watch_value) ---
		// If the workflow's trigger specifies a watched field, skip unless
		// that field actually changed (and optionally changed to the expected value).
		// Works for contact_updated, subscription_updated, etc.
		if strings.HasSuffix(eventType, "_updated") {
			var triggerSpec TriggerSpec
			if err := json.Unmarshal(wf.Trigger, &triggerSpec); err == nil && triggerSpec.Params != nil {
				if watchField, ok := triggerSpec.Params["watch_field"].(string); ok && watchField != "" {
					if !payloadContainsChangedField(payload, watchField) {
						e.logger.Debug("automation: watch_field not in changed_fields, skipping",
							"workflow_id", wf.ID.String(),
							"watch_field", watchField,
						)
						continue
					}
					// If watch_value is set, also check the new value matches
					if watchValue, ok := triggerSpec.Params["watch_value"]; ok {
						newValue := getNestedValue(payload, watchField)
						if !valuesMatch(newValue, watchValue) {
							e.logger.Debug("automation: watch_value mismatch, skipping",
								"workflow_id", wf.ID.String(),
								"watch_field", watchField,
								"watch_value", watchValue,
								"actual_value", newValue,
							)
							continue
						}
					}
				}
			}
		}

		// --- Deal Stage Filtering ---
		if eventType == TriggerDealStageChanged {
			var triggerSpec TriggerSpec
			if err := json.Unmarshal(wf.Trigger, &triggerSpec); err == nil && triggerSpec.Params != nil {
				reqFromStage, _ := triggerSpec.Params["from_stage"].(string)
				reqToStage, _ := triggerSpec.Params["to_stage"].(string)

				oldStage, _ := payload["old_stage_id"].(string)
				newStage, _ := payload["new_stage_id"].(string)

				if reqFromStage != "" && reqFromStage != "*" && reqFromStage != oldStage {
					e.logger.Debug("automation: from_stage mismatch, skipping",
						"workflow_id", wf.ID.String(),
						"req_from_stage", reqFromStage,
						"actual_old_stage", oldStage,
					)
					continue
				}

				if reqToStage != "" && reqToStage != "*" && reqToStage != newStage {
					e.logger.Debug("automation: to_stage mismatch, skipping",
						"workflow_id", wf.ID.String(),
						"req_to_stage", reqToStage,
						"actual_new_stage", newStage,
					)
					continue
				}
			}
		}

		// Build idempotency key
		entityID := ""
		if id, ok := payload["entity_id"]; ok {
			entityID = fmt.Sprintf("%v", id)
		}
		eventTime := time.Now().Truncate(time.Minute).Unix()
		idempKey := fmt.Sprintf("%x", sha256.Sum256(
			[]byte(fmt.Sprintf("%s:%s:%s:%d", wf.ID.String(), eventType, entityID, eventTime)),
		))

		triggerCtx, err := json.Marshal(payload)
		if err != nil {
			e.logger.Error("automation: marshal trigger context", "error", err)
			continue
		}

		run := &WorkflowRun{
			ID:              uuid.New(),
			WorkflowID:      wf.ID,
			WorkflowVersion: wf.Version,
			OrgID:           orgID,
			Status:          StatusPending,
			TriggerContext:  datatypes.JSON(triggerCtx),
			IdempotencyKey:  idempKey,
		}

		inserted, err := e.repo.CreateRun(ctx, run)
		if err != nil {
			e.logger.Error("automation: create run", "error", err, "workflow_id", wf.ID.String())
			continue
		}
		if !inserted {
			e.logger.Debug("automation: duplicate trigger (idempotent skip)",
				"workflow_id", wf.ID.String(),
				"idempotency_key", idempKey,
			)
			continue
		}

		// Non-blocking push to jobs channel
		select {
		case e.jobs <- WorkflowRunJob{RunID: run.ID}:
		default:
			e.logger.Warn("automation: jobs channel full, run will be picked up by scheduler",
				"run_id", run.ID.String(),
			)
		}
	}

	return nil
}

// RunWorkflowNow creates and dispatches a real run for exactly one workflow (Run Now,
// P20), reusing the existing run-creation and worker-dispatch machinery while bypassing
// the natural-event semantics that do not apply to a manual run.
//
// Unlike triggerEventInternal it does NOT call GetActiveWorkflowsByTrigger (so an
// inactive/draft workflow still runs — Req 6.4), does NOT apply the _internal_update
// guard or the watch_field/stage trigger-level filters (so a manual run is never
// silently skipped), and does NOT fan out — it targets only wf.ID, creating exactly one
// run for the targeted workflow (Req 6.1, 6.2). It uses a unique-per-call idempotency
// key so CreateRun always inserts a new run regardless of any prior trigger (Req 6.5),
// sets the run's OrgID to the caller's org and stores the constructed Trigger_Context
// (Req 6.6). A synchronous run-creation failure is returned to the caller with no run id
// so the handler can respond 500 (Req 6.7).
func (e *Engine) RunWorkflowNow(ctx context.Context, orgID uuid.UUID, wf *Workflow, eventType string, payload map[string]any) (uuid.UUID, error) {
	triggerCtx, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshal trigger context: %w", err)
	}

	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: wf.Version,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		IdempotencyKey:  newRunNowIdempotencyKey(),
	}

	inserted, err := e.repo.CreateRun(ctx, run)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create run: %w", err)
	}
	if !inserted {
		// Astronomically unlikely with a unique per-call UUID key; treat as failure
		// so the handler returns no run id rather than reporting a phantom success.
		return uuid.Nil, fmt.Errorf("run not created (idempotency collision)")
	}

	// Reuse the existing dispatch tail; the scheduler is the fallback if the channel
	// is full, mirroring triggerEventInternal.
	select {
	case e.jobs <- WorkflowRunJob{RunID: run.ID}:
	default:
		e.logger.Warn("automation: jobs channel full, run_now run will be picked up by scheduler",
			"run_id", run.ID.String(),
		)
	}

	return run.ID, nil
}

// RetryRun resumes a previously FAILED run from the step that failed (P21). It atomically
// flips the run back to pending — resetting only the retry counters/timers, never the
// CompletedActions set — and then re-queues it on the worker pool. Because processRun
// rebuilds the completed-step set from the run's successful action logs and skips them
// (the same idempotency path used for crash recovery and auto-retries), the steps that
// already ran are NOT re-executed: execution resumes at the failure point and prior side
// effects (emails sent, tasks created) are not repeated.
//
// The caller is expected to have already loaded the run, confirmed it belongs to the
// caller's org, authorized the actor, and observed status == failed; the repository's
// locked (SELECT ... FOR UPDATE) status-guarded reset closes the race between that read and
// this write, returning ErrRunNotRetryable if the run left the failed state in between.
func (e *Engine) RetryRun(ctx context.Context, runID uuid.UUID) error {
	reset, err := e.repo.ResetRunForRetry(ctx, runID)
	if err != nil {
		return fmt.Errorf("reset run for retry: %w", err)
	}
	if !reset {
		return ErrRunNotRetryable
	}

	// Non-blocking dispatch with startup recovery as the fallback if the buffered jobs
	// channel is momentarily full — identical to RunWorkflowNow/triggerEventInternal. A
	// pending run with a null next_retry_at is re-queued by RequeueInFlight on the next
	// engine start, so a dropped push is recovered rather than lost.
	select {
	case e.jobs <- WorkflowRunJob{RunID: runID}:
	default:
		e.logger.Warn("automation: jobs channel full, retried run will be picked up by recovery",
			"run_id", runID.String(),
		)
	}

	return nil
}

// worker is the main processing loop for each worker goroutine.
func (e *Engine) worker(id int) {
	defer e.wg.Done()
	e.logger.Info("automation: worker started", "worker_id", id)

	for job := range e.jobs {
		if e.ctx.Err() != nil {
			return
		}
		e.processRun(job.RunID)
	}

	e.logger.Info("automation: worker stopped", "worker_id", id)
}

func (e *Engine) processRun(runID uuid.UUID) {
	// Phase 1: Acquire row lock and mark as running (single transaction)
	tx := e.repo.BeginTx(e.ctx)
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			e.logger.Error("automation: panic in processRun", "panic", r, "run_id", runID.String())
		}
	}()

	run, err := e.repo.LockAndGetRun(e.ctx, tx, runID)
	if err != nil {
		tx.Rollback()
		e.logger.Error("automation: lock run failed", "error", err, "run_id", runID.String())
		return
	}
	if run == nil {
		tx.Rollback()
		return
	}

	// Mark as running if pending
	if run.Status == StatusPending {
		now := time.Now()
		run.Status = StatusRunning
		run.StartedAt = &now
		if err := e.repo.UpdateRunTx(e.ctx, tx, run); err != nil {
			tx.Rollback()
			e.logger.Error("automation: update run status", "error", err)
			return
		}
	}
	tx.Commit()
	e.logger.Info("automation: locked and marked run running", "run_id", runID.String(), "status", run.Status)

	// Phase 2: Load workflow at the pinned version
	ver, err := e.repo.GetWorkflowVersion(e.ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil || ver == nil {
		e.failRun(run, fmt.Errorf("workflow version %d not found", run.WorkflowVersion))
		return
	}
	e.logger.Info("automation: loaded workflow version", "run_id", runID.String(), "version", run.WorkflowVersion)

	// Load successful action logs for this run
	actionLogs, err := e.repo.GetActionLogsByRunID(e.ctx, run.ID)
	if err != nil {
		e.failRun(run, fmt.Errorf("load action logs: %w", err))
		return
	}
	e.logger.Info("automation: loaded action logs", "run_id", runID.String(), "count", len(actionLogs))

	// Build eval context from trigger context
	evalCtx := e.buildEvalContext(run)

	// Resolve the actor context ONCE per run: the workflow author's Caller (P8
	// run-as-creator), so record-writing executors enforce OLS/FLS/own-scope and
	// attribute the audit trail to the author. Engine bookkeeping below keeps using
	// e.ctx (system) — only action side-effects run under execCtx.
	execCtx := e.actorContext(e.ctx, run)

	// Check if this is a steps-based (P13 tree) workflow
	if len(ver.Steps) > 0 && string(ver.Steps) != "null" {
		var steps []StepSpec
		if err := json.Unmarshal(ver.Steps, &steps); err != nil {
			e.failRun(run, fmt.Errorf("parse steps: %w", err))
			return
		}

		// Rebuild resume state from action logs. Success logs mark steps
		// completed; waiting logs are parked delay steps. Logs are keyed by
		// structural path, so alias them back to step IDs via the tree walk —
		// step outputs must stay addressable as {{actions.<id>}} after resume.
		state := newStepsExecState()
		pathToID := stepPathIndex(steps, "", "")
		for i := range actionLogs {
			lg := &actionLogs[i]
			id := pathToID[lg.ActionPath]
			switch lg.Status {
			case LogStatusSuccess:
				state.markCompleted(id, lg.ActionPath)
				if len(lg.Output) > 0 && string(lg.Output) != "null" {
					var outputVal any
					if err := json.Unmarshal(lg.Output, &outputVal); err == nil {
						evalCtx.Actions[lg.ActionPath] = outputVal
						if id != "" {
							evalCtx.Actions[id] = outputVal
						}
					}
				}
			case LogStatusWaiting:
				logCopy := *lg
				state.waiting[lg.ActionPath] = &logCopy
				state.started[lg.ActionPath] = true
				if id != "" {
					state.started[id] = true
				}
			}
		}

		e.logger.Info("automation: starting steps execution", "run_id", runID.String(), "steps_count", len(steps))
		completed, execErr := e.executeStepsWithState(execCtx, steps, run, state, &evalCtx, "", "")
		e.logger.Info("automation: finished steps execution", "run_id", runID.String(), "completed", completed, "execErr", execErr)
		if execErr != nil {
			return
		}
		if completed {
			now := time.Now()
			run.Status = StatusCompleted
			run.FinishedAt = &now
			if err := e.repo.UpdateRunStandalone(e.ctx, run); err != nil {
				e.logger.Error("automation: failed to mark run completed", "error", err)
			}
			e.logger.Info("automation: run completed", "run_id", run.ID.String())
		}
		return
	}

	// Parse legacy flat actions
	var actions []ActionSpec
	if err := json.Unmarshal(ver.Actions, &actions); err != nil {
		e.failRun(run, fmt.Errorf("parse actions: %w", err))
		return
	}

	// Populate evalCtx.Actions from successful action logs for legacy flat actions
	for _, log := range actionLogs {
		if log.Status == LogStatusSuccess {
			if log.ActionIdx >= 0 && log.ActionIdx < len(actions) {
				actionID := actions[log.ActionIdx].ID
				if len(log.Output) > 0 && string(log.Output) != "null" {
					var outputVal any
					if err := json.Unmarshal(log.Output, &outputVal); err == nil {
						evalCtx.Actions[actionID] = outputVal
					}
				}
			}
		}
	}

	// Evaluate conditions
	if ver.Conditions != nil && len(ver.Conditions) > 0 {
		var conditions ConditionGroup
		if err := json.Unmarshal(ver.Conditions, &conditions); err == nil {
			if !EvaluateConditions(conditions, evalCtx) {
				e.skipRun(run)
				return
			}
		}
	}

	// Phase 3: Execute actions sequentially
	completedSet := GetCompletedActionIndices(run)

	for i := run.CurrentActionIdx; i < len(actions); i++ {
		if e.ctx.Err() != nil {
			return // Engine shutting down
		}

		if completedSet[i] {
			continue // Idempotency: already completed
		}

		action := actions[i]

		// Create pre-execution action log (standalone tx, informational).
		// Loss on crash is acceptable — the action hasn't executed yet.
		actionLog := &WorkflowActionLog{
			ID:         uuid.New(),
			RunID:      run.ID,
			ActionIdx:  i,
			ActionType: action.Type,
			Status:     "running",
			AttemptNo:  run.RetryCount + 1,
			CreatedAt:  time.Now(),
		}

		inputJSON, _ := json.Marshal(action.Params)
		actionLog.Input = datatypes.JSON(inputJSON)
		e.repo.CreateActionLogStandalone(e.ctx, actionLog)

		startTime := time.Now()
		output, execErr := e.executeAction(execCtx, run, action, evalCtx)
		durationMs := time.Since(startTime).Milliseconds()

		actionLog.DurationMs = durationMs

		if execErr != nil {
			if run.RetryCount < 3 && isRetryable(execErr) {
				// Retryable failure: atomically update action log + run
				run.RetryCount++
				retryAt := time.Now().Add(backoff(run.RetryCount))
				run.NextRetryAt = &retryAt
				run.Status = StatusPending
				run.CurrentActionIdx = i
				run.LastError = execErr.Error()

				actionLog.Status = LogStatusRetrying
				actionLog.Error = execErr.Error()

				if err := e.commitActionAndRun(actionLog, run); err != nil {
					e.logger.Error("automation: commit retry tx failed", "error", err, "run_id", run.ID.String())
				}

				e.logger.Warn("automation: action failed, scheduling retry",
					"run_id", run.ID.String(),
					"action_idx", i,
					"retry_count", run.RetryCount,
					"next_retry_at", retryAt,
				)
				return
			}

			// Non-retryable or max retries exceeded: atomically fail
			now := time.Now()
			run.Status = StatusFailed
			run.LastError = execErr.Error()
			run.FinishedAt = &now

			actionLog.Status = LogStatusFailed
			actionLog.Error = execErr.Error()

			if err := e.commitActionAndRun(actionLog, run); err != nil {
				e.logger.Error("automation: commit failure tx failed", "error", err, "run_id", run.ID.String())
			}

			e.logger.Error("automation: run failed",
				"run_id", run.ID.String(),
				"action_idx", i,
				"error", execErr,
			)
			return
		}

		// Success: update eval context with action output
		if evalCtx.Actions == nil {
			evalCtx.Actions = make(map[string]any)
		}
		evalCtx.Actions[action.ID] = output

		// Mark action as completed
		completedSet[i] = true
		var completedSlice []int
		for idx := range completedSet {
			completedSlice = append(completedSlice, idx)
		}
		completedJSON, _ := SetCompletedActions(completedSlice)
		run.CompletedActions = datatypes.JSON(completedJSON)
		run.CurrentActionIdx = i + 1

		// Prepare action log for success
		actionLog.Status = LogStatusSuccess
		if output != nil {
			outputJSON, _ := json.Marshal(output)
			actionLog.Output = datatypes.JSON(outputJSON)
		}

		// If this was the last action, mark run as completed in the same tx
		if i+1 >= len(actions) {
			now := time.Now()
			run.Status = StatusCompleted
			run.FinishedAt = &now
		}

		// Atomically commit action log + run update (§13.3 compliance)
		if err := e.commitActionAndRun(actionLog, run); err != nil {
			e.logger.Error("automation: commit success tx failed", "error", err, "run_id", run.ID.String())
			return
		}
	}

	if run.Status == StatusCompleted {
		e.logger.Info("automation: run completed",
			"run_id", run.ID.String(),
			"actions_count", len(actions),
		)
	}
}

// executeStepsRecursive is the legacy-signature wrapper kept for callers that
// only carry a completed-set (tests, pre-A1 call sites). Delay steps executed
// through it use the durable-wait path when a repository is present.
func (e *Engine) executeStepsRecursive(execCtx context.Context, steps []StepSpec, run *WorkflowRun, completedSteps map[string]bool, evalCtx *EvalContext, parentPath string, branch string) (bool, error) {
	state := newStepsExecState()
	if completedSteps != nil {
		state.completed = completedSteps
		for k := range completedSteps {
			state.started[k] = true
		}
	}
	return e.executeStepsWithState(execCtx, steps, run, state, evalCtx, parentPath, branch)
}

func (e *Engine) executeStepsWithState(execCtx context.Context, steps []StepSpec, run *WorkflowRun, state *stepsExecState, evalCtx *EvalContext, parentPath string, branch string) (bool, error) {
	for i, step := range steps {
		stepPath := BuildStepPath(parentPath, branch, i)
		if e.ctx.Err() != nil {
			return false, e.ctx.Err()
		}

		switch step.Type {
		case "delay":
			if state.completed[step.ID] || state.completed[stepPath] {
				e.logger.Info("automation: step already executed, skipping", "run_id", run.ID.String(), "step_id", step.ID, "step_path", stepPath)
				continue
			}
			proceed, err := e.handleDelayStep(step, stepPath, run, state, evalCtx)
			if err != nil {
				return false, err
			}
			if !proceed {
				// Run parked on the delay (StatusWaiting) — unwind without
				// completing or failing; the sweeper resumes it at wake_at.
				return false, nil
			}

		case "action":
			if state.completed[step.ID] || state.completed[stepPath] {
				e.logger.Info("automation: step already executed, skipping", "run_id", run.ID.String(), "step_id", step.ID, "step_path", stepPath)
				continue
			}

			e.logger.Info("automation: executing step", "run_id", run.ID.String(), "step_id", step.ID, "step_type", step.Type)
			var action ActionSpec
			if step.Action != nil {
				action = *step.Action
			}
			if action.ID == "" {
				action.ID = step.ID
			}

			actionLog := &WorkflowActionLog{
				ID:         uuid.New(),
				RunID:      run.ID,
				ActionPath: stepPath,
				ActionType: action.Type,
				Status:     "running",
				AttemptNo:  run.RetryCount + 1,
				CreatedAt:  time.Now(),
			}
			inputJSON, _ := json.Marshal(action.Params)
			actionLog.Input = datatypes.JSON(inputJSON)
			e.logger.Info("automation: creating action log", "run_id", run.ID.String(), "action_path", stepPath, "step_id", step.ID, "log_id", actionLog.ID.String())
			if e.repo != nil {
				if err := e.repo.CreateActionLogStandalone(e.ctx, actionLog); err != nil {
					e.logger.Error("automation: CreateActionLogStandalone failed", "error", err, "run_id", run.ID.String())
				} else {
					e.logger.Info("automation: CreateActionLogStandalone succeeded", "run_id", run.ID.String())
				}
			}

			startTime := time.Now()
			e.logger.Info("automation: calling executeAction", "run_id", run.ID.String(), "action_type", action.Type)
			output, execErr := e.executeAction(execCtx, run, action, *evalCtx)
			durationMs := time.Since(startTime).Milliseconds()
			actionLog.DurationMs = durationMs
			e.logger.Info("automation: executeAction finished", "run_id", run.ID.String(), "duration_ms", durationMs, "execErr", execErr)

			if execErr != nil {
				if run.RetryCount < 3 && isRetryable(execErr) {
					run.RetryCount++
					retryAt := time.Now().Add(backoff(run.RetryCount))
					run.NextRetryAt = &retryAt
					run.Status = StatusPending
					run.LastError = execErr.Error()

					actionLog.Status = LogStatusRetrying
					actionLog.Error = execErr.Error()

					e.logger.Info("automation: committing retry action status", "run_id", run.ID.String(), "retry_count", run.RetryCount)
					if e.repo != nil {
						if err := e.commitActionAndRun(actionLog, run); err != nil {
							e.logger.Error("automation: commit retry tx failed", "error", err, "run_id", run.ID.String())
						}
					}
					return false, execErr
				}

				now := time.Now()
				run.Status = StatusFailed
				run.LastError = execErr.Error()
				run.FinishedAt = &now

				actionLog.Status = LogStatusFailed
				actionLog.Error = execErr.Error()

				e.logger.Info("automation: committing failure action status", "run_id", run.ID.String())
				if e.repo != nil {
					if err := e.commitActionAndRun(actionLog, run); err != nil {
						e.logger.Error("automation: commit failure tx failed", "error", err, "run_id", run.ID.String())
					}
				}
				return false, execErr
			}

			if evalCtx.Actions == nil {
				evalCtx.Actions = make(map[string]any)
			}
			evalCtx.Actions[step.ID] = output

			state.markCompleted(step.ID, stepPath)
			syncRunCompleted(run, state)
			run.RetryCount = 0
			run.LastError = ""
			run.NextRetryAt = nil

			actionLog.Status = LogStatusSuccess
			if output != nil {
				outputJSON, _ := json.Marshal(output)
				actionLog.Output = datatypes.JSON(outputJSON)
			}

			e.logger.Info("automation: committing success action status", "run_id", run.ID.String())
			if e.repo != nil {
				if err := e.commitActionAndRun(actionLog, run); err != nil {
					e.logger.Error("automation: commit success tx failed", "error", err, "run_id", run.ID.String())
					return false, err
				}
			}
			e.logger.Info("automation: committed success action status successfully", "run_id", run.ID.String())

		case "condition":
			// Pin the branch that already made progress — including a branch
			// whose only executed step is a parked delay (started, not yet
			// completed). Re-evaluating could flip sides on resume and orphan
			// the parked step.
			var runYes bool
			if hasAnyStepStarted(step.YesSteps, state.started, stepPath, "yes") {
				runYes = true
			} else if hasAnyStepStarted(step.NoSteps, state.started, stepPath, "no") {
				runYes = false
			} else {
				if step.Condition != nil {
					runYes = EvaluateConditions(*step.Condition, *evalCtx)
				}
			}

			if runYes {
				completed, err := e.executeStepsWithState(execCtx, step.YesSteps, run, state, evalCtx, stepPath, "yes")
				if err != nil || !completed {
					return completed, err
				}
			} else {
				completed, err := e.executeStepsWithState(execCtx, step.NoSteps, run, state, evalCtx, stepPath, "no")
				if err != nil || !completed {
					return completed, err
				}
			}
		}
	}
	return true, nil
}

// hasAnyStepExecuted is the legacy ID-only pinning check, kept as a wrapper so
// existing callers/tests keep their semantics (an ID-keyed set behaves
// identically; path matching is a strict superset).
func hasAnyStepExecuted(steps []StepSpec, completedSteps map[string]bool) bool {
	return hasAnyStepStarted(steps, completedSteps, "", "")
}

// commitActionAndRun atomically updates an action log and its parent run in a single transaction.
// This prevents the inconsistency where an action log is written but the run's CompletedActions
// is not updated, which would cause duplicate action execution on crash recovery (§13.3).
func (e *Engine) commitActionAndRun(actionLog *WorkflowActionLog, run *WorkflowRun) error {
	tx := e.repo.BeginTx(e.ctx)
	if err := e.repo.UpdateActionLogTx(e.ctx, tx, actionLog); err != nil {
		tx.Rollback()
		return fmt.Errorf("update action log: %w", err)
	}
	if err := e.repo.UpdateRunTx(e.ctx, tx, run); err != nil {
		tx.Rollback()
		return fmt.Errorf("update run: %w", err)
	}

	// Fault injection point: PostActionLogHook fires after both writes land in the
	// transaction buffer but before Commit. A panic here proves the tx rolls back
	// both writes atomically — exactly the §13.3 crash scenario.
	if e.PostActionLogHook != nil {
		e.PostActionLogHook()
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// buildEvalContext creates an EvalContext from the trigger context JSON.
func (e *Engine) buildEvalContext(run *WorkflowRun) EvalContext {
	ctx := EvalContext{
		Actions: make(map[string]any),
		Extra:   make(map[string]any),
	}

	var payload map[string]any
	if err := json.Unmarshal(run.TriggerContext, &payload); err != nil {
		return ctx
	}

	// Known root keys
	knownKeys := map[string]bool{
		"contact": true, "deal": true, "trigger": true,
		"org": true, "user": true, "entity_id": true,
	}

	if contact, ok := payload["contact"].(map[string]any); ok {
		ctx.Contact = contact
	}
	if deal, ok := payload["deal"].(map[string]any); ok {
		ctx.Deal = deal
	}
	if trigger, ok := payload["trigger"].(map[string]any); ok {
		ctx.Trigger = trigger
	}

	// Hydrate contact.* for deal-triggered runs. A deal event carries only
	// deal.contact_id (dealToMap / loadDealForRun), with no contact object — so
	// templates like {{contact.email}} would otherwise resolve empty and an email
	// action would fail at runtime with "'to' is required". Load the deal's contact
	// best-effort so the context matches what a contact trigger produces. A missing
	// contact_id, a deleted/absent contact, or a load error just leaves contact.*
	// empty (the prior behavior — no regression).
	if ctx.Contact == nil && ctx.Deal != nil {
		if cid, _ := ctx.Deal["contact_id"].(string); cid != "" {
			if contactID, err := uuid.Parse(cid); err == nil {
				if c := loadContactForTrigger(e.ctx, e.db, run.OrgID, contactID); c != nil {
					ctx.Contact = c
				}
			}
		}
	}
	if org, ok := payload["org"].(map[string]any); ok {
		ctx.Org = org
	}
	if user, ok := payload["user"].(map[string]any); ok {
		ctx.User = user
	}

	// Extract custom object data: any unknown key with a map value
	// goes into ctx.Extra[slug] for dynamic path resolution
	for key, val := range payload {
		if knownKeys[key] {
			continue
		}
		if m, ok := val.(map[string]any); ok {
			ctx.Extra[key] = m
		}
	}

	// One-hop relation hydration for the system company relation (A2): a deal or
	// contact event carries only company_id, so {{company.*}} would resolve empty.
	// Best-effort load the related company into Extra["company"] when the trigger
	// didn't already carry a company object. A missing/absent company just leaves
	// company.* empty (no regression). Deal→contact hydration above is the sibling
	// case; broader/custom relation hydration via the registry is a later step.
	if _, hasCompany := ctx.Extra["company"]; !hasCompany {
		companyID := ""
		if ctx.Deal != nil {
			companyID, _ = ctx.Deal["company_id"].(string)
		}
		if companyID == "" && ctx.Contact != nil {
			companyID, _ = ctx.Contact["company_id"].(string)
		}
		if companyID != "" {
			if cuid, err := uuid.Parse(companyID); err == nil {
				if co := loadCompanyForTrigger(e.ctx, e.db, run.OrgID, cuid); co != nil {
					ctx.Extra["company"] = co
				}
			}
		}
	}

	return ctx
}

// loadCompanyForTrigger reads a company into the flat interpolation-map shape
// {{company.*}} expects (id, name, industry, website, custom_fields nested),
// mirroring companyAutomationMap so a deal/contact-triggered run resolves company
// fields the same way a company-triggered run does. Best-effort: nil on load
// error or missing/deleted company (the caller then leaves company.* empty).
func loadCompanyForTrigger(ctx context.Context, db *gorm.DB, orgID, companyID uuid.UUID) map[string]any {
	var row struct {
		ID           uuid.UUID `gorm:"column:id"`
		Name         string    `gorm:"column:name"`
		Industry     *string   `gorm:"column:industry"`
		Website      *string   `gorm:"column:website"`
		CustomFields *string   `gorm:"column:custom_fields"`
	}
	if err := db.WithContext(ctx).
		Table("companies").
		Select("id, name, industry, website, custom_fields::text").
		Where("id = ? AND org_id = ? AND deleted_at IS NULL", companyID, orgID).
		Scan(&row).Error; err != nil || row.ID == uuid.Nil {
		return nil
	}
	m := map[string]any{
		"id":   row.ID.String(),
		"name": row.Name,
	}
	if row.Industry != nil {
		m["industry"] = *row.Industry
	}
	if row.Website != nil {
		m["website"] = *row.Website
	}
	if row.CustomFields != nil && *row.CustomFields != "" {
		var cf map[string]any
		if err := json.Unmarshal([]byte(*row.CustomFields), &cf); err == nil {
			m["custom_fields"] = cf
		}
	}
	return m
}

// loadContactForTrigger reads a contact into the flat interpolation-map shape the
// EvalContext.Contact expects (id, first_name, last_name, email, phone,
// owner_user_id, company_id, custom_fields nested), so a deal-triggered run can
// resolve {{contact.*}} the same way a contact-triggered run does. Best-effort:
// returns nil on a load error or a missing/deleted contact (the caller then
// leaves contact.* empty). Mirrors the interpolation shape of
// UpdateRecordExecutor.readContactSnapshot but is read-only and nil-on-absent, so
// it can't corrupt an existing contact context.
func loadContactForTrigger(ctx context.Context, db *gorm.DB, orgID, contactID uuid.UUID) map[string]any {
	var row struct {
		ID           uuid.UUID  `gorm:"column:id"`
		FirstName    string     `gorm:"column:first_name"`
		LastName     string     `gorm:"column:last_name"`
		Email        *string    `gorm:"column:email"`
		Phone        *string    `gorm:"column:phone"`
		OwnerUserID  *uuid.UUID `gorm:"column:owner_user_id"`
		CompanyID    *uuid.UUID `gorm:"column:company_id"`
		CustomFields *string    `gorm:"column:custom_fields"`
	}
	if err := db.WithContext(ctx).
		Table("contacts").
		Select("id, first_name, last_name, email, phone, owner_user_id, company_id, custom_fields::text").
		Where("id = ? AND org_id = ? AND deleted_at IS NULL", contactID, orgID).
		Scan(&row).Error; err != nil || row.ID == uuid.Nil {
		return nil
	}
	m := map[string]any{
		"id":         row.ID.String(),
		"first_name": row.FirstName,
		"last_name":  row.LastName,
	}
	if row.Email != nil {
		m["email"] = *row.Email
	}
	if row.Phone != nil {
		m["phone"] = *row.Phone
	}
	if row.OwnerUserID != nil {
		m["owner_user_id"] = row.OwnerUserID.String()
	}
	if row.CompanyID != nil {
		m["company_id"] = row.CompanyID.String()
	}
	if row.CustomFields != nil && *row.CustomFields != "" {
		var cf map[string]any
		if err := json.Unmarshal([]byte(*row.CustomFields), &cf); err == nil {
			m["custom_fields"] = cf
		}
	}
	return m
}

// failRun marks a run as failed. Uses standalone tx because there is no
// associated action log to keep atomic (version-not-found, parse error, etc.).
func (e *Engine) failRun(run *WorkflowRun, err error) {
	now := time.Now()
	run.Status = StatusFailed
	run.LastError = err.Error()
	run.FinishedAt = &now
	e.repo.UpdateRunStandalone(e.ctx, run)
	e.logger.Error("automation: run failed", "run_id", run.ID.String(), "error", err)
}

// skipRun marks a run as skipped. Uses standalone tx because there is no
// associated action log to keep atomic (conditions not met).
func (e *Engine) skipRun(run *WorkflowRun) {
	now := time.Now()
	run.Status = StatusSkipped
	run.FinishedAt = &now
	e.repo.UpdateRunStandalone(e.ctx, run)
	e.logger.Info("automation: run skipped (conditions not met)", "run_id", run.ID.String())
}

// Repo returns the engine's repository for external use (handlers, etc).
func (e *Engine) Repo() *Repository {
	return e.repo
}

// Jobs returns the jobs channel for external use (recovery, scheduler).
func (e *Engine) Jobs() chan WorkflowRunJob {
	return e.jobs
}

// --- Watch field helpers for field-level trigger filtering ---

// payloadContainsChangedField checks if a field path (e.g. "contact.owner_user_id")
// is present in the payload's "changed_fields" array.
func payloadContainsChangedField(payload map[string]any, watchField string) bool {
	changedRaw, ok := payload["changed_fields"]
	if !ok {
		return false
	}
	// changed_fields can be []string or []any (from JSON unmarshal)
	switch changed := changedRaw.(type) {
	case []string:
		for _, f := range changed {
			if f == watchField {
				return true
			}
		}
	case []any:
		for _, f := range changed {
			if fmt.Sprintf("%v", f) == watchField {
				return true
			}
		}
	}
	return false
}

// getNestedValue resolves a dotted path like "contact.owner_user_id" against a payload.
// It walks the map hierarchy: payload["contact"]["owner_user_id"].
func getNestedValue(payload map[string]any, path string) any {
	parts := splitDotPath(path)
	var current any = payload
	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = m[part]
	}
	return current
}

// splitDotPath splits a dotted field path into segments.
func splitDotPath(path string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if path[i] == '.' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}

// valuesMatch compares two values for equality using string representation.
// This handles the JSON type mismatch problem (e.g., UUID stored as string vs interface{}).
func valuesMatch(actual, expected any) bool {
	if actual == nil && expected == nil {
		return true
	}
	if actual == nil || expected == nil {
		return false
	}
	return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
}
