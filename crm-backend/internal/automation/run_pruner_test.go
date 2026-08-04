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

// seqTestContact prepares the contacts fixture EnrollContact hydrates from, and returns a
// fresh contact. loadContactForTrigger selects company_id; the shared contacts fixture
// predates that column, and without it the enrol still succeeds but falls back to a minimal
// context — quietly ceasing to exercise the real hydration path.
func seqTestContact(t *testing.T, db *gorm.DB, org uuid.UUID) uuid.UUID {
	t.Helper()
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, first_name, last_name, email)
		VALUES (?, ?, 'Ada', 'Lovelace', ?)`, id, org, "ada-"+id.String()[:8]+"@example.com").Error)
	return id
}

func claimCount(t *testing.T, db *gorm.DB, wfID uuid.UUID, key string) int64 {
	t.Helper()
	return runRowCount(t, db,
		`SELECT count(*) FROM automation_run_idempotency_claims WHERE workflow_id = ? AND idempotency_key = ?`,
		wfID, key)
}

// TestSequenceReEnrollmentSurvivesTheRunPruner is the regression test for a CONFIRMED live
// defect: a contact who had already received an entire drip sequence received the whole
// thing again, from step 1, whenever the segment was re-enrolled more than 90 days later.
//
// It was a four-link chain, and the fix cuts link 2:
//  1. internal/marketing/sequence_feeder.go — sequenceEnrollKey is the per-(sequence,
//     contact) idempotency key, promising to enrol each contact into a given sequence
//     "at most once, FOREVER".
//  2. That promise used to rest on exactly one artifact: the unique index
//     idx_wf_runs_wf_idemp on automation_workflow_runs (workflow_id, idempotency_key) —
//     the run ROW was the whole enrolment ledger. It is not any more. EnrollContact now
//     routes through CreateRunWithDurableClaim, which records the key in
//     automation_run_idempotency_claims (RunIdempotencyClaim) in the SAME transaction as
//     the run. That table is the ledger, and no pruner touches it.
//  3. internal/automation/run_pruner.go — PruneCompletedRuns still hard-deletes terminal
//     runs 90 days after they finish, drip enrolments included. It must: those rows are
//     the heaviest write volume in the schema. It simply no longer deletes the dedupe.
//  4. cmd/server/main.go — the marketing-side guard is a PARTIAL unique index (WHERE
//     status = 'active'), so re-enrolling the same (sequence, segment) after the first
//     completes is still allowed, ON PURPOSE: that is how a re-enrolled segment picks up
//     its NEW members. The feeder re-walks the entire segment and asks to enrol everyone;
//     the per-contact claim is what makes the already-drip'd contacts no-ops.
//
// So the test prunes the enrolment run and re-enrols with the identical key, asserting the
// enrol is refused AND that nothing dispatchable is left behind. If it fails, "forever" has
// silently decayed back to "for 90 days" and live contacts are being re-mailed.
func TestSequenceReEnrollmentSurvivesTheRunPruner(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	repo := NewRepository(db)
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	org := uuid.New()
	seq := createTestWorkflow(t, db, org, 1)
	contact := seqTestContact(t, db, org)

	key := seqEnrollKey(seq.ID, contact)

	// 1. First enrolment: the contact starts the drip, and the claim is written with it.
	inserted, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	require.True(t, inserted, "first enrolment creates the run")
	require.Equal(t, int64(1), claimCount(t, db, seq.ID, key),
		"the enrolment is recorded in the durable ledger, not only in the run row")

	// 2. While the run row exists, the dedupe works exactly as documented.
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
		"the run row is reclaimed, as it must be")

	// 4. The same contact, the same sequence, the same idempotency key — and the claim,
	//    which the pruner does not touch, refuses it.
	reEnrolled, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	assert.False(t, reEnrolled,
		"once the run pruner has removed the run row, the identical sequenceEnrollKey must STILL "+
			"be refused: the enrolment ledger is automation_run_idempotency_claims, which no "+
			"pruner touches. A true here is the re-mail defect, live again.")

	// A refused enrol must leave NOTHING dispatchable behind — not even an inert row. A
	// pending run here is picked up by the scheduler and mails the contact step 1 again.
	assert.Equal(t, int64(0), runRowCount(t, db,
		`SELECT count(*) FROM automation_workflow_runs WHERE workflow_id = ? AND idempotency_key = ?`,
		seq.ID, key),
		"the refused re-enrolment queues nothing — the contact does not re-enter the drip")
	assert.Equal(t, int64(1), claimCount(t, db, seq.ID, key),
		"the claim outlived the run that used to carry the dedupe")
}

// TestPruneCompletedRuns_LeavesDurableClaimsAlone pins the pruner's side of the contract
// directly, without going through the enroll path.
//
// The claims table is the one table in this package with NO retention rule, which reads
// like an oversight to anyone tidying up growth later — and PruneCompletedRuns is exactly
// where such a tidy-up would land, since it already deletes from two tables in one
// transaction. Adding a third DELETE here restores the re-mail defect in full, silently.
func TestPruneCompletedRuns_LeavesDurableClaimsAlone(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	org, wf := uuid.New(), uuid.New()

	// An ancient claim whose run is long gone — the steady state after a drip completes
	// and ages out. Nothing about it is "recent" enough to be spared by a time window.
	require.NoError(t, db.Create(&RunIdempotencyClaim{
		ID: uuid.New(), OrgID: org, WorkflowID: wf, IdempotencyKey: "seq:ancient:contact:x",
	}).Error)
	require.NoError(t, db.Exec(`UPDATE automation_run_idempotency_claims
		SET created_at = NOW() - make_interval(days => 3650) WHERE workflow_id = ?`, wf).Error)

	// A terminal run so the pruner has real work to do on the same call.
	doomed := mkPruneRun(t, db, org, wf, StatusCompleted, 400, 2, true)

	n, err := repo.PruneCompletedRuns(ctx, runPruneRetention)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
	assert.Equal(t, int64(0), runRowCount(t, db, `SELECT count(*) FROM automation_workflow_runs WHERE id = ?`, doomed))

	assert.Equal(t, int64(1), runRowCount(t, db, `SELECT count(*) FROM automation_run_idempotency_claims`),
		"a ten-year-old claim is not garbage — it IS the enrolment ledger, and no retention rule may reach it")
}

// TestDurableClaimIsOnlyWrittenByTheSequencePath is the other half of the bound: the claims
// table must stay narrow.
//
// EnrollRun backs the enroll_records action, whose key (enrollIdempotencyKey) is scoped to
// its SOURCE RUN and so is meaningless once that run is pruned. If it were routed through
// the durable path too — an easy "fix them all consistently" edit — the claims table would
// gain one immortal row per enrolled record per run, recreating exactly the unbounded
// growth PruneCompletedRuns exists to prevent, on a table nothing reclaims.
func TestDurableClaimIsOnlyWrittenByTheSequencePath(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	org := uuid.New()
	wf := createTestWorkflow(t, db, org, 1)
	sourceRun := uuid.New()
	record := uuid.New()
	key := enrollIdempotencyKey(sourceRun, wf.ID, record)

	inserted, err := engine.EnrollRun(ctx, org, wf, map[string]any{"entity_id": record.String()}, key)
	require.NoError(t, err)
	require.True(t, inserted)

	assert.Equal(t, int64(0), runRowCount(t, db, `SELECT count(*) FROM automation_run_idempotency_claims`),
		"a run-scoped idempotency key must not buy a permanent row; its dedupe dies with its run, by design")
	// It still dedupes for the run's own lifetime, which is all it ever promised.
	again, err := engine.EnrollRun(ctx, org, wf, map[string]any{"entity_id": record.String()}, key)
	require.NoError(t, err)
	assert.False(t, again, "the run-lifetime dedupe still works")
}

// TestBackfillSequenceEnrollmentClaims_ProtectsPreExistingEnrollments covers the population
// that is actually at risk right now.
//
// Every contact enrolled BEFORE the claims table shipped has a run row and no claim. The
// write-path fix does nothing for them: their run is still the only ledger, and the pruner
// still reaches it, so they still get re-mailed — for the whole 90 days it takes the last
// pre-fix run to age out. The boot backfill (called from AutoMigrate) is what closes that,
// so this test builds a genuine pre-fix row via the non-durable EnrollRun path, backfills,
// prunes, and then demands the re-enrol be refused.
func TestBackfillSequenceEnrollmentClaims_ProtectsPreExistingEnrollments(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := NewRepository(db)
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	org := uuid.New()
	seq := createTestWorkflow(t, db, org, 1)
	contact := seqTestContact(t, db, org)
	key := seqEnrollKey(seq.ID, contact)

	// Exactly what the pre-fix code wrote: the marketing-tagged run, and nothing else.
	tc := contactEnrollContext(map[string]any{"id": contact.String()}, contact, triggerTypeOf(seq.Trigger), uuid.New())
	inserted, err := engine.EnrollRun(ctx, org, seq, tc, key)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, int64(0), claimCount(t, db, seq.ID, key), "a pre-fix enrolment has no claim")

	// A run with no marketing tag — the backfill must not sweep it in. Its key is scoped
	// to a source run, so an immortal claim for it would be both wrong and unbounded.
	otherKey := enrollIdempotencyKey(uuid.New(), seq.ID, uuid.New())
	_, err = engine.EnrollRun(ctx, org, seq, map[string]any{"entity_id": uuid.New().String()}, otherKey)
	require.NoError(t, err)

	n, err := repo.BackfillSequenceEnrollmentClaims(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "only the marketing-tagged run is claimed")
	assert.Equal(t, int64(1), claimCount(t, db, seq.ID, key))
	assert.Equal(t, int64(0), claimCount(t, db, seq.ID, otherKey),
		"an untagged, run-scoped key is not a sequence enrolment and gets no permanent row")

	// Idempotent: AutoMigrate calls it on every boot.
	n, err = repo.BackfillSequenceEnrollmentClaims(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n, "a second boot writes nothing")

	// Now age the legacy run out and confirm the backfilled claim holds the line.
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs
		SET status = 'completed', finished_at = NOW() - make_interval(days => 100)
		WHERE workflow_id = ? AND idempotency_key = ?`, seq.ID, key).Error)
	_, err = repo.PruneCompletedRuns(ctx, runPruneRetention)
	require.NoError(t, err)

	reEnrolled, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	assert.False(t, reEnrolled,
		"a contact enrolled before the claims table existed must be protected too — otherwise the "+
			"fix leaves the entire live population exposed until their runs age out")
}

// TestDurableClaimHealsARunThatHasNoClaimYet covers the branch where the claim insert
// succeeds but the run insert conflicts.
//
// This is the transition state: a live pre-fix run holds the key, with no claim. The enrol
// must still be refused (the run row's own index does that), but it must ALSO leave the
// claim behind — committed, not rolled back with the conflicting run insert. Getting that
// wrong is invisible: the enrol is refused today and re-mails in 90 days, which is the
// original bug wearing the fix's clothes. It is also why the run is inserted with ON
// CONFLICT DO NOTHING rather than by catching a duplicate-key error, which would poison the
// transaction and take the claim down with it.
func TestDurableClaimHealsARunThatHasNoClaimYet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := NewRepository(db)
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	org := uuid.New()
	seq := createTestWorkflow(t, db, org, 1)
	contact := seqTestContact(t, db, org)
	key := seqEnrollKey(seq.ID, contact)

	// A pre-fix run, still live, never backfilled.
	tc := contactEnrollContext(map[string]any{"id": contact.String()}, contact, triggerTypeOf(seq.Trigger), uuid.New())
	inserted, err := engine.EnrollRun(ctx, org, seq, tc, key)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, int64(0), claimCount(t, db, seq.ID, key))

	// The feeder comes back round on the same contact.
	again, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	assert.False(t, again, "the live run row still dedupes")
	assert.Equal(t, int64(1), claimCount(t, db, seq.ID, key),
		"and the claim is committed on the way past, so the dedupe survives the pruner")
	assert.Equal(t, int64(1), runRowCount(t, db,
		`SELECT count(*) FROM automation_workflow_runs WHERE workflow_id = ? AND idempotency_key = ?`, seq.ID, key),
		"no duplicate run was created")

	// And the healed claim genuinely holds once the run is reclaimed.
	require.NoError(t, db.Exec(`UPDATE automation_workflow_runs
		SET status = 'completed', finished_at = NOW() - make_interval(days => 100)
		WHERE workflow_id = ? AND idempotency_key = ?`, seq.ID, key).Error)
	_, err = repo.PruneCompletedRuns(ctx, runPruneRetention)
	require.NoError(t, err)

	reEnrolled, err := engine.EnrollContact(ctx, org, seq, contact, uuid.New(), key)
	require.NoError(t, err)
	assert.False(t, reEnrolled, "the healed claim is a real claim")
}
