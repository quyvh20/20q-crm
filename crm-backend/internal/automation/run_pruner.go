package automation

import (
	"context"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

const (
	runPruneInterval  = 6 * time.Hour
	runPruneRetention = 90 * 24 * time.Hour // keep terminal runs + their logs 90 days
)

// PruneCompletedRuns hard-deletes terminal (completed/failed/skipped) workflow runs that
// finished more than olderThan ago, AND their action logs. Both automation_workflow_runs
// and automation_workflow_action_logs grew UNBOUNDED before M8 (only automation_timers
// had retention); at thousands of drip recipients × steps this is the heaviest write
// volume in the schema. automation_workflow_action_logs.run_id has NO ON DELETE CASCADE,
// so the logs are deleted FIRST (children) then the runs (parents), in one transaction —
// otherwise the logs orphan and keep growing. Both are HARD deletes (neither model has
// gorm.DeletedAt), matching PruneFiredTimers / PruneCompletedRoster. A never-finished
// terminal row (finished_at NULL) falls back to created_at so it can still be reclaimed.
// Returns the number of runs deleted.
//
// It deliberately does NOT touch automation_run_idempotency_claims. A run row used to be
// the only record that a contact had been enrolled into a drip sequence, so deleting it
// here silently released a dedupe that marketing had promised was permanent, and a segment
// re-enrolled after the window re-mailed the whole sequence to contacts who had already
// finished it. The claim row is now that record; it is narrow, it outlives the run on
// purpose, and no retention rule may be extended over it. See RunIdempotencyClaim.
func (r *Repository) PruneCompletedRuns(ctx context.Context, olderThan time.Duration) (int64, error) {
	const cutoffPredicate = `status IN ('completed','failed','skipped')
		AND COALESCE(finished_at, created_at) < NOW() - make_interval(secs => ?)`
	secs := olderThan.Seconds()
	var deleted int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Children first (no FK cascade on run_id).
		if err := tx.Exec(`DELETE FROM automation_workflow_action_logs
			WHERE run_id IN (SELECT id FROM automation_workflow_runs WHERE `+cutoffPredicate+`)`, secs).Error; err != nil {
			return err
		}
		// NOW() is fixed for the transaction's duration, so the parent DELETE matches the
		// exact same rows whose logs were just removed (snapshot-consistent).
		res := tx.Exec(`DELETE FROM automation_workflow_runs WHERE `+cutoffPredicate, secs)
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return nil
	})
	return deleted, err
}

// runPrunerStore is the persistence slice the run pruner needs.
type runPrunerStore interface {
	PruneCompletedRuns(ctx context.Context, olderThan time.Duration) (int64, error)
}

// StartAutomationRunPruner periodically reclaims terminal workflow runs + their action
// logs (M8). Runs until ctx is done.
func StartAutomationRunPruner(ctx context.Context, store runPrunerStore, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(runPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := store.PruneCompletedRuns(ctx, runPruneRetention); err != nil {
				logger.Error("automation: run pruner failed", "error", err)
			} else if n > 0 {
				logger.Info("automation: pruned completed workflow runs", "count", n)
			}
		}
	}
}
