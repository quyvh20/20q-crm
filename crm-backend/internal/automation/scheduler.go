package automation

import (
	"context"
	"encoding/json"
	"log/slog"

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

// sweepRetries implements Job B: finds pending runs with expired retry timers
// and pushes them to the jobs channel.
func (s *Scheduler) sweepRetries() {
	ctx := context.Background()

	ids, err := s.repo.SweepRetries(ctx)
	if err != nil {
		s.logger.Error("automation scheduler: sweep retries failed", "error", err)
		return
	}

	for _, id := range ids {
		select {
		case s.engine.jobs <- WorkflowRunJob{RunID: id}:
		default:
			s.logger.Warn("automation scheduler: jobs channel full, skipping retry sweep for run",
				"run_id", id.String(),
			)
		}
	}

	if len(ids) > 0 {
		s.logger.Info("automation scheduler: retries swept", "count", len(ids))
	}
}
