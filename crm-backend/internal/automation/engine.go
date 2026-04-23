package automation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

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

// WithEmailExecutor registers an email executor.
func WithEmailExecutor(apiKey, fromEmail string) EngineOption {
	return func(e *Engine) {
		e.executors[ActionSendEmail] = NewEmailExecutor(apiKey, fromEmail)
	}
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
		e.executors[ActionCreateTask] = NewTaskExecutor(e.db)
	}
	if _, ok := e.executors[ActionAssignUser]; !ok {
		e.executors[ActionAssignUser] = NewAssignUserExecutor(e.db)
	}
	if _, ok := e.executors[ActionSendWebhook]; !ok {
		e.executors[ActionSendWebhook] = NewWebhookExecutor()
	}
	if _, ok := e.executors[ActionDelay]; !ok {
		e.executors[ActionDelay] = NewDelayExecutor()
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
	}()
}

func (e *Engine) triggerEventInternal(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any) error {
	workflows, err := e.repo.GetActiveWorkflowsByTrigger(ctx, orgID, eventType)
	if err != nil {
		return fmt.Errorf("query workflows: %w", err)
	}

	for _, wf := range workflows {
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

	// Phase 2: Load workflow at the pinned version
	ver, err := e.repo.GetWorkflowVersion(e.ctx, run.WorkflowID, run.WorkflowVersion)
	if err != nil || ver == nil {
		e.failRun(run, fmt.Errorf("workflow version %d not found", run.WorkflowVersion))
		return
	}

	// Parse actions
	var actions []ActionSpec
	if err := json.Unmarshal(ver.Actions, &actions); err != nil {
		e.failRun(run, fmt.Errorf("parse actions: %w", err))
		return
	}

	// Build eval context from trigger context
	evalCtx := e.buildEvalContext(run)

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
		output, execErr := e.executeAction(e.ctx, run, action, evalCtx)
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
	}

	var payload map[string]any
	if err := json.Unmarshal(run.TriggerContext, &payload); err != nil {
		return ctx
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
	if org, ok := payload["org"].(map[string]any); ok {
		ctx.Org = org
	}
	if user, ok := payload["user"].(map[string]any); ok {
		ctx.User = user
	}

	return ctx
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
