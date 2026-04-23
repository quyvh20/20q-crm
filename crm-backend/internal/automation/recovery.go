package automation

import (
	"context"
	"log/slog"
)

// RequeueInFlight re-queues interrupted and pending runs on engine startup.
// This is the crash recovery mechanism:
// 1. Runs with status='running' were interrupted → set to pending + increment recovery_count
// 2. Runs with status='pending' and due for processing → push to jobs channel
// Because executors check CompletedActions before running each action,
// resumed runs never re-execute completed actions (core idempotency guarantee).
func RequeueInFlight(ctx context.Context, repo *Repository, jobs chan WorkflowRunJob, logger *slog.Logger) {
	// Step 1: Mark interrupted runs as pending
	runningRuns, err := repo.GetRunningRuns(ctx)
	if err != nil {
		logger.Error("automation recovery: failed to get running runs", "error", err)
	} else {
		for i := range runningRuns {
			run := &runningRuns[i]
			run.Status = StatusPending
			run.RecoveryCount++
			if err := repo.UpdateRunStandalone(ctx, run); err != nil {
				logger.Error("automation recovery: failed to reset running run",
					"run_id", run.ID.String(),
					"error", err,
				)
				continue
			}
			logger.Info("automation recovery: reset interrupted run",
				"run_id", run.ID.String(),
				"recovery_count", run.RecoveryCount,
			)
		}
	}

	// Step 2: Load pending runs ready for processing
	pendingRuns, err := repo.GetPendingRuns(ctx, 500)
	if err != nil {
		logger.Error("automation recovery: failed to get pending runs", "error", err)
		return
	}

	requeuedCount := 0
	for _, run := range pendingRuns {
		// Blocking send — startup can wait
		select {
		case jobs <- WorkflowRunJob{RunID: run.ID}:
			requeuedCount++
		case <-ctx.Done():
			logger.Warn("automation recovery: context cancelled during requeue")
			return
		}
	}

	if requeuedCount > 0 || len(runningRuns) > 0 {
		logger.Info("automation recovery: complete",
			"interrupted_reset", len(runningRuns),
			"pending_requeued", requeuedCount,
		)
	}
}
