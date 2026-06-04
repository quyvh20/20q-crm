package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// engine_retry_test.go covers the manual-retry resume path (P21): a FAILED run that is
// retried must RESUME from the step that failed, not restart from the beginning. The
// engine already provides this for crash recovery / auto-retries (see
// TestIntegration_KillAndResume); RetryRun reuses that machinery by flipping the failed
// run back to pending while preserving CompletedActions. This test proves the end-to-end
// behavior through the real engine + repository against a Postgres container, and SKIPs
// when Docker is unavailable like the rest of the package's integration tests.

// failOnceExecutor records the action indices it runs (like countingExecutor) but returns
// a NON-retryable error the first time it sees failIdx, then succeeds on every later call.
// That drives a run to StatusFailed at failIdx on the first pass and lets the manual retry
// resume and complete on the second pass.
type failOnceExecutor struct {
	mu      sync.Mutex
	calls   []int
	failIdx int
	failed  map[int]bool
}

func newFailOnceExecutor(failIdx int) *failOnceExecutor {
	return &failOnceExecutor{failIdx: failIdx, failed: map[int]bool{}}
}

func (e *failOnceExecutor) Execute(_ context.Context, _ *WorkflowRun, action ActionSpec, _ EvalContext) (any, error) {
	var idx int
	fmt.Sscanf(action.ID, "action_%d", &idx)

	e.mu.Lock()
	e.calls = append(e.calls, idx)
	firstFail := idx == e.failIdx && !e.failed[idx]
	if firstFail {
		e.failed[idx] = true
	}
	e.mu.Unlock()

	if firstFail {
		// A plain error is non-retryable (isRetryable only matches *RetryableError), so
		// processRun fails the run immediately — a clean single failure to retry from.
		return nil, fmt.Errorf("simulated non-retryable failure at action %d", idx)
	}
	return map[string]any{"executed": action.ID}, nil
}

func (e *failOnceExecutor) getCalls() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]int, len(e.calls))
	copy(out, e.calls)
	return out
}

// TestIntegration_RetryRun_ResumesFromFailedStep:
//  1. Runs a 3-action workflow; action[1] fails non-retryably on the first pass.
//  2. Asserts the run is FAILED with action[0,1] completed, action[2] not.
//  3. Calls RetryRun and asserts the run is back to PENDING with retry bookkeeping cleared
//     but CompletedActions preserved, and that it was re-queued on the jobs channel.
//  4. Re-processes and asserts the run COMPLETES, that action[0] and action[1] are NOT
//     re-executed, and that execution resumed exactly at the failed step (calls == [0,1,2,2]).
func TestIntegration_RetryRun_ResumesFromFailedStep(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	wf := createTestWorkflow(t, db, orgID, 3) // flat actions action_0..2
	repo := NewRepository(db)
	exec := newFailOnceExecutor(2) // fail at the LAST action (action[2]) on the first pass

	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	triggerCtx := datatypes.JSON(`{"contact":{"id":"c1"},"trigger":{"type":"webhook_inbound"}}`)
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  triggerCtx,
		IdempotencyKey:  "retry-" + uuid.New().String(),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// --- Pass 1: action[0,1] succeed, action[2] fails ---
	engine.processRun(run.ID)

	failedRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, failedRun.Status, "run must fail at action[2] on the first pass")
	completed := GetCompletedActionIndices(failedRun)
	assert.True(t, completed[0], "action[0] completed before the failure")
	assert.True(t, completed[1], "action[1] completed before the failure")
	assert.False(t, completed[2], "the failed action[2] must not be marked completed")
	assert.Equal(t, []int{0, 1, 2}, exec.getCalls(), "first pass runs action[0], action[1], then fails at action[2]")

	// --- Manual retry: resume, do not restart ---
	require.NoError(t, engine.RetryRun(context.Background(), run.ID))

	pendingRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, pendingRun.Status, "retry flips the failed run back to pending")
	assert.Equal(t, 0, pendingRun.RetryCount, "retry resets the retry counter")
	assert.Empty(t, pendingRun.LastError, "retry clears the last error")
	assert.Nil(t, pendingRun.FinishedAt, "retry clears the terminal finished_at marker")
	assert.True(t, GetCompletedActionIndices(pendingRun)[0] && GetCompletedActionIndices(pendingRun)[1],
		"completed actions[0,1] are preserved across retry (resume from failure point)")

	// RetryRun enqueues the run; drain it so a single processRun mirrors a worker pop.
	select {
	case <-engine.jobs:
	default:
		t.Fatal("RetryRun must enqueue the run on the jobs channel")
	}

	// --- Pass 2: resumes directly at action[2] (now succeeds) ---
	engine.processRun(run.ID)

	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, finalRun.Status, "the resumed run completes")
	assert.NotNil(t, finalRun.FinishedAt)

	// The crux: action[0] and action[1] each ran exactly once (NOT re-executed); action[2]
	// ran twice (fail + resume) — execution resumed at the failed step, not the beginning.
	assert.Equal(t, []int{0, 1, 2, 2}, exec.getCalls())
	counts := map[int]int{}
	for _, i := range exec.getCalls() {
		counts[i]++
	}
	assert.Equal(t, 1, counts[0], "completed action[0] must NOT be re-executed on retry")
	assert.Equal(t, 1, counts[1], "completed action[1] must NOT be re-executed on retry")
	assert.Equal(t, 2, counts[2], "the failed action[2] runs again on resume (fail + retry)")
}

// TestIntegration_RetryRun_RaceWithCrashRecovery proves the SELECT ... FOR UPDATE lock in
// ResetRunForRetry serializes concurrent attempts to retry the same failed run — e.g. a
// user clicking Retry at the same moment a crash-recovery requeue targets it. Exactly one
// caller wins the failed→pending flip; the other observes a non-failed run
// (ErrRunNotRetryable). Only the winner enqueues a job, so the run is never double-dispatched.
//
// Without the row lock both callers could read "failed" and both flip + enqueue (a double
// run); this test fails in that case.
func TestIntegration_RetryRun_RaceWithCrashRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	repo := NewRepository(db)
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	// A failed run left behind by a worker that gave up.
	run := &WorkflowRun{
		ID:               uuid.New(),
		WorkflowID:       uuid.New(),
		WorkflowVersion:  1,
		OrgID:            orgID,
		Status:           StatusFailed,
		TriggerContext:   datatypes.JSON(`{}`),
		CompletedActions: datatypes.JSON(`[0]`),
		CurrentActionIdx: 1,
		IdempotencyKey:   "race-" + uuid.New().String(),
	}
	require.NoError(t, db.Create(run).Error)

	// Two callers race to retry the same run (a user click vs. a recovery requeue). They
	// start together and contend on the row lock.
	const racers = 2
	var wg sync.WaitGroup
	results := make([]error, racers)
	startGate := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startGate
			results[idx] = engine.RetryRun(context.Background(), run.ID)
		}(i)
	}
	close(startGate) // release both at once
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRunNotRetryable):
			conflicts++
		default:
			t.Fatalf("unexpected RetryRun error: %v", err)
		}
	}
	assert.Equal(t, 1, successes, "exactly one retry wins the FOR UPDATE lock")
	assert.Equal(t, 1, conflicts, "the loser observes a non-failed run (ErrRunNotRetryable)")

	reloaded, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, reloaded.Status, "the run ends pending exactly once")
	assert.Equal(t, 1, len(engine.jobs), "only the winner enqueues a job — no double dispatch")
}

// TestIntegration_RetryRun_UsesPinnedWorkflowVersion proves a retry resumes against the
// workflow version the run was pinned to at creation, NOT a newer edited version. A run
// pinned to v1 (one action) fails; the workflow is then edited to v2 (a second action is
// added); the retry must still execute only v1's action. This is consistent with resume
// semantics — CompletedActions / CurrentActionIdx index into the pinned version, so adopting
// a newer version mid-run would corrupt the resume point.
func TestIntegration_RetryRun_UsesPinnedWorkflowVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	repo := NewRepository(db)
	exec := newFailOnceExecutor(0) // action_0 fails once, then succeeds

	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	// v1: a single action (action_0).
	wf := createTestWorkflow(t, db, orgID, 1)

	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(`{}`),
		IdempotencyKey:  "pin-" + uuid.New().String(),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// Pass 1: action_0 fails → run failed, pinned at version 1.
	engine.processRun(run.ID)
	failed, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, failed.Status)
	require.Equal(t, 1, failed.WorkflowVersion)
	require.Equal(t, []int{0}, exec.getCalls())

	// Edit the workflow → v2 with a SECOND action. The failed run stays pinned to v1.
	twoActions, _ := json.Marshal([]ActionSpec{
		{ID: "action_0", Type: "test_action", Params: map[string]any{}},
		{ID: "action_1", Type: "test_action", Params: map[string]any{}},
	})
	wf.Actions = datatypes.JSON(twoActions)
	wf.Steps = nil // keep the flat-actions path; UpdateWorkflow snapshots whatever is set
	require.NoError(t, repo.UpdateWorkflow(context.Background(), wf))
	require.Equal(t, 2, wf.Version, "editing the workflow must bump it to v2")

	// Retry + resume.
	require.NoError(t, engine.RetryRun(context.Background(), run.ID))
	select {
	case <-engine.jobs:
	default:
		t.Fatal("RetryRun must enqueue the run")
	}
	engine.processRun(run.ID)

	final, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, final.Status)
	assert.Equal(t, 1, final.WorkflowVersion, "retry stays pinned to v1, never adopts v2")
	// v1 has ONE action; action_1 (only in v2) must NEVER run. action_0 ran twice (fail + resume).
	assert.Equal(t, []int{0, 0}, exec.getCalls(),
		"resume executes only the pinned v1 action; v2's action_1 must not run")
}

// dodRetryExecutor models the Definition-of-Done scenario: action_0 and action_1 always
// succeed, while action_2 (standing in for a send_webhook that 500s) returns a RETRYABLE
// error — the same error the webhook executor produces on a 5xx — for its first 4 calls
// (initial attempt + 3 automatic retries), then SUCCEEDS on the 5th call (the manual-retry
// resume).
type dodRetryExecutor struct {
	mu           sync.Mutex
	action2Calls int
}

func (e *dodRetryExecutor) Execute(_ context.Context, _ *WorkflowRun, action ActionSpec, _ EvalContext) (any, error) {
	var idx int
	fmt.Sscanf(action.ID, "action_%d", &idx)
	if idx != 2 {
		return map[string]any{"ok": action.ID}, nil
	}
	e.mu.Lock()
	e.action2Calls++
	n := e.action2Calls
	e.mu.Unlock()
	if n <= 4 { // initial attempt + 3 automatic retries all 500
		return nil, NewRetryableError(fmt.Errorf("send_webhook responded 500"))
	}
	return map[string]any{"status": 200}, nil // succeeds on the manual-retry resume
}

// TestIntegration_RetryRun_DefinitionOfDone is the end-to-end P21 acceptance test:
//
//	Workflow with 3 actions; action[2] (send_webhook) fails 500 through 3 auto-retries
//	→ run status=failed. Click Retry → status=pending → resume → action[2] re-executes
//	(action[0,1] skipped) → completed. action_logs: action[0]=1, action[1]=1, action[2]=5.
//
// The auto phase is driven by calling processRun directly (which re-processes a pending run
// regardless of next_retry_at), so the test does not wait out the real 30s/2m/10m backoff.
//
// On the log count: the engine writes one row per attempt (created "running", then updated
// to retrying/failed/success). action[2]'s 5 rows are 3×retrying (initial + retries 1–2
// that scheduled another retry), 1×failed (retry 3 — auto budget exhausted), and 1×success
// (the manual resume). That is the DoD's "5 entries" — its "3 auto + 1 manual + 1 success"
// is the same five rows described loosely.
func TestIntegration_RetryRun_DefinitionOfDone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	repo := NewRepository(db)
	exec := &dodRetryExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	wf := createTestWorkflow(t, db, orgID, 3) // action_0, action_1, action_2

	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(`{}`),
		IdempotencyKey:  "dod-" + uuid.New().String(),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// --- Auto phase: drive processRun until the run exhausts retries and fails. ---
	for attempt := 0; attempt < 4; attempt++ {
		engine.processRun(run.ID)
		r, err := repo.GetRunByID(context.Background(), run.ID)
		require.NoError(t, err)
		if r.Status == StatusFailed {
			break
		}
		require.Equal(t, StatusPending, r.Status, "between auto-retries the run is pending")
	}

	failed, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, failed.Status, "run fails after the initial attempt + 3 auto-retries")
	require.Equal(t, 4, exec.action2Calls, "action[2] attempted 4 times in the auto phase (initial + 3 retries)")
	completed := GetCompletedActionIndices(failed)
	require.True(t, completed[0] && completed[1], "action[0,1] completed before action[2] failed")
	require.False(t, completed[2], "the failed action[2] is not marked completed")

	// --- Manual retry: pending → resume → action[2] re-executes (0,1 skipped) → completed. ---
	require.NoError(t, engine.RetryRun(context.Background(), run.ID))
	pending, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusPending, pending.Status)
	require.Equal(t, 0, pending.RetryCount, "manual retry resets the retry budget")
	select {
	case <-engine.jobs:
	default:
		t.Fatal("RetryRun must enqueue the run")
	}

	engine.processRun(run.ID)
	final, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, final.Status, "the resumed run completes")
	assert.Equal(t, 5, exec.action2Calls, "action[2] ran a 5th time on the manual resume and succeeded")

	// --- Verify action_logs: action[0]=1, action[1]=1, action[2]=5. ---
	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)

	perIdx := map[int]int{}
	statusOf := map[int]map[string]int{}
	for _, l := range logs {
		perIdx[l.ActionIdx]++
		if statusOf[l.ActionIdx] == nil {
			statusOf[l.ActionIdx] = map[string]int{}
		}
		statusOf[l.ActionIdx][l.Status]++
	}

	assert.Equal(t, 1, perIdx[0], "action[0] has exactly one log entry (ran once, never re-executed)")
	assert.Equal(t, 1, perIdx[1], "action[1] has exactly one log entry")
	assert.Equal(t, 5, perIdx[2], "action[2] has five log entries")

	assert.Equal(t, LogStatusSuccess, onlyStatus(statusOf[0]), "action[0]'s single entry is success")
	assert.Equal(t, LogStatusSuccess, onlyStatus(statusOf[1]), "action[1]'s single entry is success")
	assert.Equal(t, 3, statusOf[2][LogStatusRetrying], "action[2]: three retrying entries (auto)")
	assert.Equal(t, 1, statusOf[2][LogStatusFailed], "action[2]: one failed entry (auto budget exhausted)")
	assert.Equal(t, 1, statusOf[2][LogStatusSuccess], "action[2]: one success entry (manual resume)")
}

// onlyStatus returns the single status key in a {status: count} map (count is assumed 1),
// or "" if the map does not hold exactly one entry.
func onlyStatus(m map[string]int) string {
	if len(m) != 1 {
		return ""
	}
	for s := range m {
		return s
	}
	return ""
}
