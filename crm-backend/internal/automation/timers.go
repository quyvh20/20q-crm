package automation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"gorm.io/datatypes"
	"gorm.io/gorm/clause"
)

// timers.go implements the A4 time-based trigger subsystem: durable automation_timers
// rows fired by the scheduler's timer scan. This file holds the schedule (cron) trigger;
// date_field materialization is layered on later. Correctness rests on three things:
//  1. the unique (workflow_id, dedupe_key) index → one pending row per occurrence, so
//     arm / re-arm / reconcile all converge (never duplicate an occurrence);
//  2. ClaimDueTimers marking rows 'fired' atomically under FOR UPDATE SKIP LOCKED →
//     each due timer is claimed by exactly one scanner pass/instance;
//  3. the fired run carrying an occurrence-derived idempotency key → a second dedup
//     layer, so even a re-claimed timer yields at most one run per occurrence.

const (
	TimerKindSchedule  = "schedule"
	timerStatusPending = "pending"
	timerStatusFired   = "fired"
)

// cronParser parses standard 5-field cron specs (min hour dom month dow) plus the
// @daily/@weekly/@every descriptors — the format the schedule trigger form emits.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// nextScheduleFire returns the next UTC firing strictly after `after` for a cron
// expression evaluated in the given IANA timezone (empty/unknown tz → UTC).
func nextScheduleFire(cronExpr, timezone string, after time.Time) (time.Time, error) {
	sched, err := cronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron %q: %w", cronExpr, err)
	}
	loc := time.UTC
	if timezone != "" {
		if l, lerr := time.LoadLocation(timezone); lerr == nil {
			loc = l
		}
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron %q has no next occurrence", cronExpr)
	}
	return next.UTC(), nil
}

// scheduleParamsFromTrigger extracts {cron, timezone} from a schedule trigger spec.
func scheduleParamsFromTrigger(t TriggerSpec) (cronExpr, timezone string, ok bool) {
	if t.Type != TriggerSchedule || t.Params == nil {
		return "", "", false
	}
	cronExpr, _ = t.Params["cron"].(string)
	timezone, _ = t.Params["timezone"].(string)
	return cronExpr, timezone, cronExpr != ""
}

// scheduleDedupeKey identifies one occurrence (by fire time) so arm/re-arm/reconcile
// converge on a single pending row and the fired run dedups per occurrence.
func scheduleDedupeKey(fireAt time.Time) string {
	return "schedule:" + strconv.FormatInt(fireAt.Unix(), 10)
}

// timerIdempotencyKey derives the fired run's idempotency key from the timer, fixed to
// 64 hex chars to fit WorkflowRun.IdempotencyKey regardless of dedupe_key length.
func timerIdempotencyKey(workflowID uuid.UUID, dedupeKey string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("timer:"+workflowID.String()+":"+dedupeKey)))
}

// ── Repository: timer persistence ─────────────────────────────────────────────

// UpsertTimer inserts a pending timer, or does nothing on the (workflow_id, dedupe_key)
// conflict — arming the same occurrence twice (re-arm + reconcile racing, or a retry)
// is a no-op, and it never resurrects an already-fired/cancelled occurrence.
func (r *Repository) UpsertTimer(ctx context.Context, t *AutomationTimer) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "workflow_id"}, {Name: "dedupe_key"}},
			DoNothing: true,
		}).
		Create(t).Error
}

// CancelWorkflowTimers DELETES a workflow's pending timers of a kind (on deactivate /
// trigger change / re-arm). It deletes rather than tombstoning: a cancelled tombstone
// would keep the (workflow_id, dedupe_key) row around and make a later re-arm of the
// SAME occurrence a no-op under UpsertTimer's OnConflict-DoNothing, silently dropping
// that occurrence. Fired rows are left intact (audit + the occurrence-already-ran guard).
func (r *Repository) CancelWorkflowTimers(ctx context.Context, workflowID uuid.UUID, kind string) error {
	return r.db.WithContext(ctx).
		Where("workflow_id = ? AND kind = ? AND status = ?", workflowID, kind, timerStatusPending).
		Delete(&AutomationTimer{}).Error
}

// HasPendingTimer reports whether a workflow already has a pending timer of a kind —
// the reconciliation guard so at most one next occurrence is armed.
func (r *Repository) HasPendingTimer(ctx context.Context, workflowID uuid.UUID, kind string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&AutomationTimer{}).
		Where("workflow_id = ? AND kind = ? AND status = ?", workflowID, kind, timerStatusPending).
		Count(&count).Error
	return count > 0, err
}

// DueTimers returns up to `limit` pending timers whose fire time has arrived, WITHOUT
// consuming them. The scanner marks each fired only AFTER its run is durably created
// (MarkTimerFired), so a crash in the fire→mark window leaves the timer pending and it
// is retried on the next scan — the run's occurrence-derived idempotency key makes that
// retry produce no duplicate run. At-least-once fire + idempotent run = exactly-once
// run, and no occurrence is lost across a redeploy mid-scan.
func (r *Repository) DueTimers(ctx context.Context, limit int) ([]AutomationTimer, error) {
	var timers []AutomationTimer
	err := r.db.WithContext(ctx).
		Where("status = ? AND fire_at <= now()", timerStatusPending).
		Order("fire_at").
		Limit(limit).
		Find(&timers).Error
	return timers, err
}

// MarkTimerFired consumes a timer after its run has been created. Idempotent.
func (r *Repository) MarkTimerFired(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Model(&AutomationTimer{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": timerStatusFired, "fired_at": time.Now(), "updated_at": time.Now()}).Error
}

// PruneFiredTimers deletes fired timers older than `olderThan` — each occurrence is its
// own row, so without pruning a high-frequency schedule accumulates rows forever. Fired
// rows carry no post-fire value beyond debugging.
func (r *Repository) PruneFiredTimers(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan)
	return r.db.WithContext(ctx).
		Where("status = ? AND fired_at < ?", timerStatusFired, cutoff).
		Delete(&AutomationTimer{}).Error
}

// ActiveScheduleWorkflows lists active schedule-triggered workflows across all orgs
// (reconciliation self-heal).
func (r *Repository) ActiveScheduleWorkflows(ctx context.Context) ([]Workflow, error) {
	var wfs []Workflow
	err := r.db.WithContext(ctx).
		Where("is_active = ? AND trigger->>'type' = ?", true, TriggerSchedule).
		Find(&wfs).Error
	return wfs, err
}

// ── Arming ────────────────────────────────────────────────────────────────────

// ArmScheduleTimer reconciles a workflow's schedule timer after a save/toggle/fire:
// an active schedule workflow with a valid cron gets its next-occurrence-after-`now`
// pending timer upserted; anything else has its pending schedule timers cancelled.
// Idempotent (safe to call repeatedly). Returns an error only for an invalid cron.
func (r *Repository) ArmScheduleTimer(ctx context.Context, wf *Workflow, now time.Time) error {
	var trig TriggerSpec
	_ = json.Unmarshal(wf.Trigger, &trig)
	cronExpr, tz, ok := scheduleParamsFromTrigger(trig)

	// Clear the currently-armed occurrence first so a re-arm converges to exactly one
	// pending timer for the CURRENT schedule: a cron/timezone edit drops the stale
	// occurrence (no spurious fire at the old time), a deactivate/trigger-change leaves
	// none, and a toggle-off→on re-inserts the next occurrence cleanly. A just-fired
	// occurrence is status='fired' (not pending), so it is untouched here.
	if err := r.CancelWorkflowTimers(ctx, wf.ID, TimerKindSchedule); err != nil {
		return err
	}
	if !wf.IsActive || !ok {
		return nil
	}
	next, err := nextScheduleFire(cronExpr, tz, now)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"trigger": map[string]any{"type": TriggerSchedule, "cron": cronExpr, "timezone": tz},
		"fire_at": next.Format(time.RFC3339),
	}
	pj, _ := json.Marshal(payload)
	return r.UpsertTimer(ctx, &AutomationTimer{
		WorkflowID: wf.ID,
		OrgID:      wf.OrgID,
		Kind:       TimerKindSchedule,
		Status:     timerStatusPending,
		FireAt:     next,
		DedupeKey:  scheduleDedupeKey(next),
		Payload:    datatypes.JSON(pj),
	})
}

// ── Engine: firing ──────────────────────────────────────────────────────────

// fireTimerRun creates and dispatches a run for the timer's workflow, using an
// occurrence-derived idempotency key so a re-claimed/duplicate timer still yields at
// most one run per occurrence. A duplicate (idempotent) insert is a silent no-op.
func (e *Engine) fireTimerRun(ctx context.Context, wf *Workflow, t *AutomationTimer) error {
	payload := map[string]any{}
	if len(t.Payload) > 0 {
		_ = json.Unmarshal(t.Payload, &payload)
	}
	if _, ok := payload["trigger"]; !ok {
		payload["trigger"] = map[string]any{"type": t.Kind}
	}
	triggerCtx, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal timer payload: %w", err)
	}
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: wf.Version,
		OrgID:           wf.OrgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		IdempotencyKey:  timerIdempotencyKey(wf.ID, t.DedupeKey),
	}
	inserted, err := e.repo.CreateRun(ctx, run)
	if err != nil {
		return fmt.Errorf("create timer run: %w", err)
	}
	if !inserted {
		return nil // occurrence already produced a run
	}
	select {
	case e.jobs <- WorkflowRunJob{RunID: run.ID}:
	default:
		e.logger.Warn("automation: jobs channel full, timer run will be recovered", "run_id", run.ID.String())
	}
	return nil
}
