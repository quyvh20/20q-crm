package automation

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

// Scheduler manages cron jobs for the automation engine.
type Scheduler struct {
	cron   *cron.Cron
	db     *gorm.DB
	repo   *Repository
	engine *Engine
	logger *slog.Logger
}

// NewScheduler creates a new scheduler.
func NewScheduler(db *gorm.DB, repo *Repository, engine *Engine, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		cron:   cron.New(cron.WithSeconds()),
		db:     db,
		repo:   repo,
		engine: engine,
		logger: logger,
	}
}

// Start registers and starts the cron jobs.
func (s *Scheduler) Start() {
	// Job A: no_activity_days scan — every 5 minutes
	s.cron.AddFunc("0 */5 * * * *", s.scanNoActivityDays)

	// Job B: retry sweeper — every 30 seconds
	s.cron.AddFunc("*/30 * * * * *", s.sweepRetries)

	// Job C: time-based trigger scan (A4) — every 60 seconds
	s.cron.AddFunc("0 * * * * *", s.scanTimers)

	// Job D: prune long-fired timers (A4) — hourly
	s.cron.AddFunc("0 0 * * * *", s.pruneTimers)

	s.cron.Start()
	s.logger.Info("automation scheduler started")
}

// Stop stops the scheduler.
func (s *Scheduler) Stop() {
	if s.cron != nil {
		s.cron.Stop()
		s.logger.Info("automation scheduler stopped")
	}
}

// scanNoActivityDays implements Job A: finds entities with no activity for N days
// and triggers workflows with trigger.type='no_activity_days'.
func (s *Scheduler) scanNoActivityDays() {
	ctx := context.Background()

	// Try advisory lock to prevent concurrent execution across instances
	var lockAcquired bool
	s.db.WithContext(ctx).Raw("SELECT pg_try_advisory_lock(hashtext('wf_cron_no_activity'))").Scan(&lockAcquired)
	if !lockAcquired {
		return // Another instance has the lock
	}
	defer s.db.WithContext(ctx).Exec("SELECT pg_advisory_unlock(hashtext('wf_cron_no_activity'))")

	// Find all active workflows with no_activity_days trigger
	var workflows []Workflow
	err := s.db.WithContext(ctx).
		Where("is_active = ? AND trigger->>'type' = ?", true, TriggerNoActivityDays).
		Find(&workflows).Error
	if err != nil {
		s.logger.Error("automation scheduler: failed to query workflows", "error", err)
		return
	}

	for _, wf := range workflows {
		var trigger TriggerSpec
		if err := json.Unmarshal(wf.Trigger, &trigger); err != nil {
			s.logger.Error("automation scheduler: failed to parse trigger", "error", err, "workflow_id", wf.ID.String())
			continue
		}

		daysVal, ok := trigger.Params["days"]
		if !ok {
			continue
		}
		days, ok := daysVal.(float64)
		if !ok {
			continue
		}

		entityVal, ok := trigger.Params["entity"]
		if !ok {
			continue
		}
		entity, ok := entityVal.(string)
		if !ok || (entity != "contact" && entity != "deal") {
			continue
		}

		// Query entities with no recent activity
		table := entity + "s"
		var entities []struct {
			ID    string `gorm:"column:id"`
			OrgID string `gorm:"column:org_id"`
		}

		query := `
			SELECT t.id, t.org_id 
			FROM ` + table + ` t 
			WHERE t.org_id = ? 
			AND t.deleted_at IS NULL
			AND NOT EXISTS (
				SELECT 1 FROM activities a 
				WHERE a.` + entity + `_id = t.id 
				AND a.occurred_at > NOW() - INTERVAL '1 day' * ?
			)
			AND NOT EXISTS (
				SELECT 1 FROM automation_workflow_runs wr 
				WHERE wr.workflow_id = ? 
				AND wr.created_at > NOW() - INTERVAL '1 day'
				AND wr.trigger_context @> ('{"entity_id":"' || t.id::text || '"}')::jsonb
			)
			LIMIT 100`

		err := s.db.WithContext(ctx).Raw(query, wf.OrgID, int(days), wf.ID).Scan(&entities).Error
		if err != nil {
			s.logger.Error("automation scheduler: query failed", "error", err, "entity", entity)
			continue
		}

		for _, ent := range entities {
			payload := map[string]any{
				"entity_id":   ent.ID,
				"entity_type": entity,
				entity: map[string]any{
					"id": ent.ID,
				},
				"trigger": map[string]any{
					"type":   TriggerNoActivityDays,
					"days":   int(days),
					"entity": entity,
				},
			}
			s.engine.TriggerEvent(ctx, wf.OrgID, TriggerNoActivityDays, payload)
		}

		if len(entities) > 0 {
			s.logger.Info("automation scheduler: no_activity_days triggered",
				"workflow_id", wf.ID.String(),
				"entity", entity,
				"days", int(days),
				"matches", len(entities),
			)
		}
	}
}

// sweepRetries implements Job B: wakes waiting runs whose delay deadline has
// arrived, then finds pending runs with expired retry timers, pushing both to
// the jobs channel.
func (s *Scheduler) sweepRetries() {
	ctx := context.Background()

	woken, err := s.repo.WakeDueWaitingRuns(ctx)
	if err != nil {
		s.logger.Error("automation scheduler: wake waiting runs failed", "error", err)
	}

	ids, err := s.repo.SweepRetries(ctx)
	if err != nil {
		s.logger.Error("automation scheduler: sweep retries failed", "error", err)
		return
	}

	// WakeDueWaitingRuns already set next_retry_at=now() on woken runs, so any
	// id present in both lists is pushed once; the run-level FOR UPDATE SKIP
	// LOCKED makes a duplicate push harmless anyway.
	seen := make(map[uuid.UUID]bool, len(woken)+len(ids))
	for _, id := range append(woken, ids...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		select {
		case s.engine.jobs <- WorkflowRunJob{RunID: id}:
		default:
			s.logger.Warn("automation scheduler: jobs channel full, skipping retry sweep for run",
				"run_id", id.String(),
			)
		}
	}

	if len(woken) > 0 {
		s.logger.Info("automation scheduler: waiting runs woken", "count", len(woken))
	}
	if len(ids) > 0 {
		s.logger.Info("automation scheduler: retries swept", "count", len(ids))
	}
}

// scanTimers implements Job C (A4): reconciles schedule timers (self-heals a missing
// next-occurrence row), then fires each due timer and re-arms the following occurrence
// for schedules. Order is create-run-THEN-mark-fired: a due timer stays pending until
// its run is durably created, so a crash in the fire→mark window retries next scan
// (the run's occurrence idempotency key prevents a double run). Advisory-locked so only
// one instance scans at a time (correctness doesn't depend on it — firing is idempotent
// — it just avoids redundant work).
func (s *Scheduler) scanTimers() {
	ctx := context.Background()

	// Acquire the session-scoped advisory lock on a PINNED connection and release it on
	// the SAME connection before returning it to the pool — acquiring and unlocking on
	// different pooled connections would leak the (cluster-global) lock and silently
	// stall timer scans on every instance.
	sqlDB, err := s.db.DB()
	if err != nil {
		s.logger.Error("automation scheduler: get sql.DB failed", "error", err)
		return
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		s.logger.Error("automation scheduler: acquire pinned conn failed", "error", err)
		return
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtext('wf_cron_timers'))").Scan(&locked); err != nil {
		s.logger.Error("automation scheduler: advisory lock check failed", "error", err)
		return
	}
	if !locked {
		return // another instance is scanning
	}
	defer conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext('wf_cron_timers'))")

	s.reconcileScheduleTimers(ctx)

	due, err := s.repo.DueTimers(ctx, 200)
	if err != nil {
		s.logger.Error("automation scheduler: due timers query failed", "error", err)
		return
	}

	fired := 0
	for i := range due {
		t := &due[i]
		wf, err := s.repo.GetWorkflowByID(ctx, t.OrgID, t.WorkflowID)
		if err != nil || wf == nil || !wf.IsActive {
			// Orphan/inactive workflow — consume the timer so it isn't retried forever.
			_ = s.repo.MarkTimerFired(ctx, t.ID)
			continue
		}
		if err := s.engine.fireTimerRun(ctx, wf, t); err != nil {
			s.logger.Error("automation scheduler: fire timer failed", "error", err, "timer_id", t.ID.String())
			continue // leave pending; retried next scan (idempotent run) — do not re-arm
		}
		if err := s.repo.MarkTimerFired(ctx, t.ID); err != nil {
			s.logger.Error("automation scheduler: mark timer fired failed", "error", err, "timer_id", t.ID.String())
			continue // run created; next scan re-fires idempotently then marks + re-arms
		}
		fired++
		// Re-arm the next occurrence (computed after now, so occurrences missed during a
		// long outage are not replayed — only the one already-pending timer fires late).
		if t.Kind == TimerKindSchedule {
			if err := s.repo.ArmScheduleTimer(ctx, wf, time.Now()); err != nil {
				s.logger.Error("automation scheduler: re-arm schedule failed", "error", err, "workflow_id", wf.ID.String())
			}
		}
	}

	if fired > 0 {
		s.logger.Info("automation scheduler: timers fired", "count", fired)
	}
}

// pruneTimers deletes long-fired timers so the table doesn't grow without bound.
func (s *Scheduler) pruneTimers() {
	if err := s.repo.PruneFiredTimers(context.Background(), 7*24*time.Hour); err != nil {
		s.logger.Error("automation scheduler: prune fired timers failed", "error", err)
	}
}

// reconcileScheduleTimers ensures every active schedule workflow has a pending timer,
// arming the next occurrence for any that don't (self-heal after a missed arm on save
// or a crash between fire and re-arm). Idempotent via the unique (workflow_id,
// dedupe_key) index.
func (s *Scheduler) reconcileScheduleTimers(ctx context.Context) {
	wfs, err := s.repo.ActiveScheduleWorkflows(ctx)
	if err != nil {
		s.logger.Error("automation scheduler: list schedule workflows failed", "error", err)
		return
	}
	now := time.Now()
	for i := range wfs {
		wf := &wfs[i]
		has, err := s.repo.HasPendingTimer(ctx, wf.ID, TimerKindSchedule)
		if err != nil || has {
			continue
		}
		if err := s.repo.ArmScheduleTimer(ctx, wf, now); err != nil {
			s.logger.Warn("automation scheduler: reconcile arm failed", "error", err, "workflow_id", wf.ID.String())
		}
	}
}
