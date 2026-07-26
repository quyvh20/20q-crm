package automation

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// TestPruneCompletedRuns_DeletesTerminalWithLogs proves the M8 retention: terminal runs
// (completed/failed/skipped) older than the cutoff are hard-deleted ALONG WITH their
// action logs (no FK cascade), while running and recent runs — and their logs — survive.
// A terminal run with a NULL finished_at falls back to created_at and is still reclaimed.
func TestPruneCompletedRuns_DeletesTerminalWithLogs(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org, wf := uuid.New(), uuid.New()

	mkRun := func(status string, ageDays int, finished bool) uuid.UUID {
		run := &WorkflowRun{
			ID: uuid.New(), OrgID: org, WorkflowID: wf, WorkflowVersion: 1,
			Status: status, TriggerContext: datatypes.JSON("{}"), IdempotencyKey: uuid.NewString(),
		}
		require.NoError(t, db.Create(run).Error)
		require.NoError(t, db.Exec(`UPDATE automation_workflow_runs SET created_at = NOW() - make_interval(days => ?) WHERE id = ?`, ageDays, run.ID).Error)
		if finished {
			require.NoError(t, db.Exec(`UPDATE automation_workflow_runs SET finished_at = NOW() - make_interval(days => ?) WHERE id = ?`, ageDays, run.ID).Error)
		}
		require.NoError(t, db.Create(&WorkflowActionLog{ID: uuid.New(), RunID: run.ID, ActionIdx: 0, ActionType: "send_email", Status: "success"}).Error)
		return run.ID
	}

	oldDone := mkRun("completed", 40, true)
	oldFailed := mkRun("failed", 40, true)
	skippedNoFinish := mkRun("skipped", 40, false) // finished_at NULL → fallback created_at → pruned
	oldRunning := mkRun("running", 40, false)       // not terminal → kept
	recentDone := mkRun("completed", 1, true)       // too recent → kept

	n, err := repo.PruneCompletedRuns(ctx, 30*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "old completed + failed + skipped(no finish) pruned")

	count := func(sql string, args ...any) int64 {
		var c int64
		require.NoError(t, db.Raw(sql, args...).Scan(&c).Error)
		return c
	}

	for _, id := range []uuid.UUID{oldRunning, recentDone} {
		assert.Equal(t, int64(1), count(`SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, id), "non-terminal/recent run kept")
	}
	for _, id := range []uuid.UUID{oldDone, oldFailed, skippedNoFinish} {
		assert.Equal(t, int64(0), count(`SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, id), "old terminal run pruned")
		assert.Equal(t, int64(0), count(`SELECT count(*) FROM automation_workflow_action_logs WHERE run_id = ?`, id), "orphan logs pruned too")
	}
	assert.Equal(t, int64(1), count(`SELECT count(*) FROM automation_workflow_action_logs WHERE run_id = ?`, recentDone), "kept run keeps its logs")
}
