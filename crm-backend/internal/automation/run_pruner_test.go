package automation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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

// ── shared fixtures for the retention tests below ────────────────────────────

// mkPruneRun inserts one workflow run, ages created_at (and finished_at, when the status
// is terminal enough to have one) by ageDays, and attaches nLogs action logs. Ageing is
// done in SQL so the row's timestamps come from the same clock NOW() reads in the pruner.
func mkPruneRun(t *testing.T, db *gorm.DB, org, wf uuid.UUID, status string, ageDays, nLogs int, finished bool) uuid.UUID {
	t.Helper()
	run := &WorkflowRun{
		ID: uuid.New(), OrgID: org, WorkflowID: wf, WorkflowVersion: 1,
		Status: status, TriggerContext: datatypes.JSON("{}"), IdempotencyKey: uuid.NewString(),
	}
	require.NoError(t, db.Create(run).Error)
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs SET created_at = NOW() - make_interval(days => ?) WHERE id = ?`, ageDays, run.ID).Error)
	if finished {
		require.NoError(t, db.Exec(`UPDATE automation_workflow_runs SET finished_at = NOW() - make_interval(days => ?) WHERE id = ?`, ageDays, run.ID).Error)
	}
	for i := 0; i < nLogs; i++ {
		require.NoError(t, db.Create(&WorkflowActionLog{
			ID: uuid.New(), RunID: run.ID, ActionIdx: i, ActionPath: fmt.Sprint(i),
			ActionType: "send_email", Status: "success",
		}).Error)
	}
	return run.ID
}

func runRowCount(t *testing.T, db *gorm.DB, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(sql, args...).Scan(&n).Error)
	return n
}

// TestPruneCompletedRuns_NeverTouchesAnotherOrgsRowsBeyondTheWindowRule pins that the
// retention rule is the ONLY thing selecting rows. PruneCompletedRuns takes no orgID and
// the ticker calls it once for the whole deployment (run_pruner.go:67), so orgA and orgB
// must be treated identically: each loses its 40-day row and keeps its 1-day row on the
// same call. A stray `org_id = ?` in the predicate would leave orgB's table growing
// forever; a per-org loop that skipped an org would do the same. Both fail here.
func TestPruneCompletedRuns_NeverTouchesAnotherOrgsRowsBeyondTheWindowRule(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgA, orgB := uuid.New(), uuid.New()
	wfA, wfB := uuid.New(), uuid.New()

	oldA := mkPruneRun(t, db, orgA, wfA, "completed", 40, 1, true)
	newA := mkPruneRun(t, db, orgA, wfA, "completed", 1, 1, true)
	oldB := mkPruneRun(t, db, orgB, wfB, "completed", 40, 1, true)
	newB := mkPruneRun(t, db, orgB, wfB, "completed", 1, 1, true)

	n, err := repo.PruneCompletedRuns(ctx, 30*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "one old run from EACH org")

	for _, id := range []uuid.UUID{oldA, oldB} {
		assert.Equal(t, int64(0), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, id),
			"every org's out-of-window run is pruned by the same fleet-wide call")
	}
	for _, id := range []uuid.UUID{newA, newB} {
		assert.Equal(t, int64(1), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, id),
			"every org's in-window run survives")
	}
}

// TestPruneCompletedRuns_BoundaryRowSurvives pins the direction of the cutoff comparison:
// `COALESCE(finished_at, created_at) < NOW() - retention` is strict, so a run finished
// just inside the window stays.
//
// Both the fixture and the predicate read the DATABASE clock (finished_at is stamped from
// NOW() in SQL, not from Go), so the only drift is the latency of the two statements —
// microseconds. The ±1s band makes that drift irrelevant while still resolving the edge to
// one second. A flipped comparison or a unit slip (days read as hours) fails here.
func TestPruneCompletedRuns_BoundaryRowSurvives(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org, wf := uuid.New(), uuid.New()
	const retention = 30 * 24 * time.Hour
	secs := retention.Seconds()

	edge := mkPruneRun(t, db, org, wf, "completed", 40, 1, true)
	older := mkPruneRun(t, db, org, wf, "completed", 40, 1, true)
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs
		SET finished_at = NOW() - make_interval(secs => ?) + interval '1 second' WHERE id = ?`, secs, edge).Error)
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs
		SET finished_at = NOW() - make_interval(secs => ?) - interval '1 second' WHERE id = ?`, secs, older).Error)

	n, err := repo.PruneCompletedRuns(ctx, retention)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	assert.Equal(t, int64(1), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, edge),
		"a run finished exactly at the edge of the window is still inside it")
	assert.Equal(t, int64(1), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_action_logs WHERE run_id = ?`, edge),
		"and its logs stay with it")
	assert.Equal(t, int64(0), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, older),
		"one second past the edge is out")
}

// TestPruneCompletedRuns_KeepsWaitingAndPendingRuns guards the status list.
//
// `waiting` is the row that matters. A run parked on a long delay step sits in `waiting`
// with a future wake_at and an ancient created_at, and it is the retry sweeper's job to
// wake it — it is live work, not history. Its finished_at is NULL, so the pruner's
// COALESCE falls back to created_at and the timestamp half of the predicate MATCHES: only
// the status list keeps it alive. Add 'waiting' to that list (or typo it into a
// NOT IN ('running')) and every long-delay drip in the fleet evaporates mid-sequence with
// no error anywhere. Today only `running` is exercised as a negative, and `running` never
// has an old created_at in practice, so it cannot catch this.
func TestPruneCompletedRuns_KeepsWaitingAndPendingRuns(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org, wf := uuid.New(), uuid.New()

	waiting := mkPruneRun(t, db, org, wf, StatusWaiting, 400, 2, false)
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs SET wake_at = NOW() + interval '30 days' WHERE id = ?`, waiting).Error)
	pending := mkPruneRun(t, db, org, wf, StatusPending, 400, 2, false)
	// A terminal control so the test would notice a pruner that deleted nothing at all.
	doomed := mkPruneRun(t, db, org, wf, StatusCompleted, 400, 2, true)

	n, err := repo.PruneCompletedRuns(ctx, 90*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the terminal run is eligible")

	for _, id := range []uuid.UUID{waiting, pending} {
		assert.Equal(t, int64(1), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, id),
			"a non-terminal run is live work, however old the row is")
		assert.Equal(t, int64(2), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_action_logs WHERE run_id = ?`, id),
			"its already-executed steps must survive too — the executor resumes from them")
	}
	assert.Equal(t, int64(0), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, doomed))
}

// TestPruneCompletedRuns_DoesNotDeleteLogsOfRunsItKeeps isolates the child DELETE.
//
// The logs are removed by their own statement (`WHERE run_id IN (SELECT ...)`) because
// automation_workflow_action_logs.run_id has no ON DELETE CASCADE. That subquery repeats
// the cutoff predicate by hand, so it can drift from the parent DELETE's — and if it ever
// widened (say, to every log of every workflow that had a pruned run) it would strip the
// step history off runs that are still in the table, leaving surviving rows with no
// audit trail and no failure anywhere.
func TestPruneCompletedRuns_DoesNotDeleteLogsOfRunsItKeeps(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org, wf := uuid.New(), uuid.New()

	// Same workflow on purpose: a log DELETE that joined on workflow_id instead of the
	// run's own cutoff would take the kept run's logs with it.
	kept := mkPruneRun(t, db, org, wf, "completed", 1, 3, true)
	pruned := mkPruneRun(t, db, org, wf, "completed", 200, 3, true)

	n, err := repo.PruneCompletedRuns(ctx, 90*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	assert.Equal(t, int64(3), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_action_logs WHERE run_id = ?`, kept),
		"the surviving run keeps all three of its action logs")
	assert.Equal(t, int64(0), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_action_logs WHERE run_id = ?`, pruned),
		"the pruned run's logs go with it (no FK cascade would leave them orphaned)")
	assert.Equal(t, int64(3), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_action_logs`),
		"exactly three log rows remain in the table")
}

// seqEnrollKey mirrors marketing.sequenceEnrollKey (sequence_feeder.go:178) verbatim.
// It is duplicated rather than imported because it is unexported and internal/marketing
// imports internal/automation, not the reverse.
func seqEnrollKey(seqWFID, contactID uuid.UUID) string {
	return fmt.Sprintf("seq:%s:contact:%s", seqWFID, contactID)
}

// TestRunPrunerErasesTheSequenceEnrollDedupe documents a CONFIRMED live defect. It
// asserts what the code does today, not what it should do — read the whole comment before
// "fixing" the assertions.
//
// THE HAZARD: a contact who already received an entire drip sequence can receive the
// whole thing again, from step 1, if the segment is re-enrolled more than 90 days later.
//
// The chain, all four links verified by this test:
//  1. internal/marketing/sequence_feeder.go:175-180 — sequenceEnrollKey is the per-
//     (sequence, contact) idempotency key, and its doc comment promises it "enrolls each
//     contact into a given sequence at most once, FOREVER".
//  2. internal/automation/repository.go:48 — that promise is enforced by exactly one
//     thing: the unique index idx_wf_runs_wf_idemp on
//     automation_workflow_runs (workflow_id, idempotency_key). The run ROW is the ledger.
//     There is no separate enrolment-history table.
//  3. internal/automation/run_pruner.go:26 — PruneCompletedRuns hard-deletes terminal
//     runs 90 days after they finish. A completed drip enrolment is exactly such a row.
//     Deleting it deletes the only record that the contact was ever enrolled.
//  4. cmd/server/main.go:1908-1909 — the enrolment guard on the marketing side is a
//     PARTIAL unique index (WHERE status = 'active'), so once the first enrolment of
//     (sequence, segment) completes, re-enrolling the same pair is permitted. The feeder
//     then walks the segment again and re-enrols every contact whose dedupe row has aged
//     out.
//
// So "forever" is really "for 90 days". The window is a package const with no env knob
// (run_pruner.go:13), and nothing in either subsystem references the other.
//
// Fixing it is out of scope for R3. A fix has to give the enrolment ledger a life
// independent of the run row — e.g. a marketing_sequence_enrollment_contacts table
// written at enrol time, or excluding runs whose trigger_context carries
// _marketing_enrollment_id from the pruner (which only defers the problem), or making the
// pruner's retention longer than the longest sequence and stating that as the guarantee.
//
// WHEN THAT FIX LANDS: this test will fail at the final assertion. That is the intended
// signal. Flip it back to require.False and rename it to
// TestSequenceReEnrollmentSurvivesTheRunPruner.
func TestRunPrunerErasesTheSequenceEnrollDedupe(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	// loadContactForTrigger selects company_id; the shared contacts fixture predates it.
	// Without this the enrol still works but falls back to a minimal context, which would
	// quietly stop exercising the real hydration path.
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)

	repo := NewRepository(db)
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	org := uuid.New()
	seq := createTestWorkflow(t, db, org, 1)
	contact := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, first_name, last_name, email)
		VALUES (?, ?, 'Ada', 'Lovelace', ?)`, contact, org, "ada-"+contact.String()[:8]+"@example.com").Error)

	key := seqEnrollKey(seq.ID, contact)

	// 1. First enrolment: the contact starts the drip.
	inserted, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	require.True(t, inserted, "first enrolment creates the run")

	// 2. While the run row exists, the dedupe works exactly as documented — this is the
	//    control that proves the pruner (and nothing else) is what breaks it below.
	again, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	require.False(t, again, "with the run row present, a re-enrol is correctly a no-op")

	// 3. The sequence finishes and the row ages past the 90-day retention.
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs
		SET status = 'completed', finished_at = NOW() - make_interval(days => 100)
		WHERE workflow_id = ? AND idempotency_key = ?`, seq.ID, key).Error)

	n, err := repo.PruneCompletedRuns(ctx, runPruneRetention)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the completed enrolment run is past the 90-day window")
	require.Equal(t, int64(0), runRowCount(t, db,
		`SELECT count(*) FROM automation_workflow_runs WHERE workflow_id = ? AND idempotency_key = ?`, seq.ID, key),
		"the only record of the enrolment is gone")

	// 4. The same contact, the same sequence, the same idempotency key — and it enrols.
	reEnrolled, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	assert.True(t, reEnrolled,
		"CONFIRMED DEFECT (see this test's doc comment): after the 90-day run pruner removes the "+
			"dedupe row, the identical sequenceEnrollKey enrols the contact a second time. If this "+
			"assertion starts failing, the re-mail bug has been fixed — flip it to require.False.")

	// And the second enrolment is a real, dispatchable run, not an inert row: it is
	// pending, so the scheduler picks it up and the contact is mailed step 1 again.
	assert.Equal(t, int64(1), runRowCount(t, db,
		`SELECT count(*) FROM automation_workflow_runs WHERE workflow_id = ? AND idempotency_key = ? AND status = ?`,
		seq.ID, key, StatusPending),
		"the duplicate enrolment is queued for execution — the contact re-enters the drip at step 1")
}
