package automation

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engine_run_now_test.go covers Engine.RunWorkflowNow (Run Now, P20 — task 2.2).
//
// RunWorkflowNow's data-access tail (repo.CreateRun) is exercised through the real
// gorm-backed *Repository, matching how the rest of this package tests run creation
// (setupTestDB + makeEngine in integration_test.go). The repo is a concrete type with
// no interface seam, so these tests use a real Postgres (via testcontainers) and assert
// on the persisted rows rather than a hand-rolled fake. The one exception is the
// synchronous marshal-failure path, which returns before any DB access and therefore
// runs without Docker.
//
// Validates: Requirements 6.2, 6.5, 6.6, 6.7

// countRunsForWorkflow returns the number of automation_workflow_runs rows for a workflow.
func countRunsForWorkflow(t *testing.T, e *Engine, workflowID uuid.UUID) int64 {
	t.Helper()
	var n int64
	require.NoError(t, e.repo.DB().Model(&WorkflowRun{}).
		Where("workflow_id = ?", workflowID).
		Count(&n).Error)
	return n
}

// TestRunWorkflowNow_CreatesSingleRunForTargetedWorkflow verifies that one Run Now call
// creates exactly one run for the targeted workflow (and none for a sibling workflow
// sharing the same trigger type), that the run carries the caller's OrgID and the stored
// Trigger_Context (the marshaled payload), that the idempotency key is a Run Now key, and
// that a job was enqueued for the worker.
//
// Validates: Requirements 6.2, 6.6
func TestRunWorkflowNow_CreatesSingleRunForTargetedWorkflow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	contactID := uuid.New()

	// Target workflow + a sibling sharing the same trigger type. RunWorkflowNow must
	// target only wf.ID and never fan out to the sibling (Req 6.2).
	wf := createTestWorkflow(t, db, orgID, 1)
	sibling := createTestWorkflow(t, db, orgID, 1)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	// Build a realistic contact-shaped trigger context via the production helper.
	entity := map[string]any{
		"id":         contactID.String(),
		"email":      "vip@example.com",
		"first_name": "Jane",
		"last_name":  "Doe",
	}
	payload := buildRunNowTriggerContext("contact", TriggerContactCreated, entity)

	runID, err := engine.RunWorkflowNow(context.Background(), orgID, wf, TriggerContactCreated, payload)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, runID, "a successful run must return a non-nil run id")

	// Exactly one run for the targeted workflow, zero for the sibling, one in total.
	assert.Equal(t, int64(1), countRunsForWorkflow(t, engine, wf.ID),
		"exactly one run must be created for the targeted workflow")
	assert.Equal(t, int64(0), countRunsForWorkflow(t, engine, sibling.ID),
		"no run must be created for a sibling workflow sharing the trigger type")

	var totalRuns int64
	require.NoError(t, db.Model(&WorkflowRun{}).Count(&totalRuns).Error)
	assert.Equal(t, int64(1), totalRuns, "Run Now must create exactly one run overall")

	// Inspect the created run.
	run, err := engine.repo.GetRunByID(context.Background(), runID)
	require.NoError(t, err)
	require.NotNil(t, run)

	assert.Equal(t, wf.ID, run.WorkflowID, "run must target the requested workflow")
	assert.Equal(t, wf.Version, run.WorkflowVersion, "run must pin the workflow version")
	assert.Equal(t, orgID, run.OrgID, "run must carry the caller's org id (Req 6.6)")
	assert.Equal(t, StatusPending, run.Status, "a freshly created run must be pending")

	// The stored Trigger_Context must equal the marshaled payload (Req 6.6).
	expectedCtx, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.JSONEq(t, string(expectedCtx), string(run.TriggerContext),
		"stored trigger context must be the marshaled payload")

	// And it must round-trip back to the original payload.
	var roundTripped map[string]any
	require.NoError(t, json.Unmarshal(run.TriggerContext, &roundTripped))
	assert.Equal(t, contactID.String(), roundTripped["entity_id"])
	if contact, ok := roundTripped["contact"].(map[string]any); assert.True(t, ok, "context must carry the contact map") {
		assert.Equal(t, contactID.String(), contact["id"])
	}

	// A Run Now idempotency key was used (Req 6.5 surface).
	assert.True(t, strings.HasPrefix(run.IdempotencyKey, "run_now:"),
		"run must use a run_now idempotency key, got %q", run.IdempotencyKey)

	// A job was enqueued for the worker (non-blocking push succeeded).
	require.Len(t, engine.jobs, 1, "exactly one job must be enqueued")
	job := <-engine.jobs
	assert.Equal(t, runID, job.RunID, "enqueued job must reference the created run")
}

// TestRunWorkflowNow_UsesUniqueIdempotencyKeyPerCall verifies that two Run Now calls for
// the same workflow/entity each create a distinct run with a distinct, prefixed
// idempotency key — i.e. the per-minute idempotency de-duplication is bypassed (Req 6.5).
//
// Validates: Requirements 6.5
func TestRunWorkflowNow_UsesUniqueIdempotencyKeyPerCall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	contactID := uuid.New()
	wf := createTestWorkflow(t, db, orgID, 1)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	entity := map[string]any{"id": contactID.String(), "email": "a@b.com"}
	payload := buildRunNowTriggerContext("contact", TriggerContactCreated, entity)

	// Two confirmed invocations sharing workflow + entity (same minute).
	runID1, err := engine.RunWorkflowNow(context.Background(), orgID, wf, TriggerContactCreated, payload)
	require.NoError(t, err)
	runID2, err := engine.RunWorkflowNow(context.Background(), orgID, wf, TriggerContactCreated, payload)
	require.NoError(t, err)

	assert.NotEqual(t, runID1, runID2, "each Run Now call must create a distinct run")
	assert.Equal(t, int64(2), countRunsForWorkflow(t, engine, wf.ID),
		"both confirmed runs must be created (idempotency bypass)")

	run1, err := engine.repo.GetRunByID(context.Background(), runID1)
	require.NoError(t, err)
	require.NotNil(t, run1)
	run2, err := engine.repo.GetRunByID(context.Background(), runID2)
	require.NoError(t, err)
	require.NotNil(t, run2)

	assert.NotEqual(t, run1.IdempotencyKey, run2.IdempotencyKey,
		"the two runs must use distinct idempotency keys")
	assert.True(t, strings.HasPrefix(run1.IdempotencyKey, "run_now:"))
	assert.True(t, strings.HasPrefix(run2.IdempotencyKey, "run_now:"))
}

// TestRunWorkflowNow_CreateRunFailureReturnsErrorNoRunID verifies that when the
// underlying CreateRun fails, RunWorkflowNow surfaces a synchronous error, returns no run
// id (uuid.Nil), and enqueues no job (Req 6.7). The failure is induced by dropping the
// runs table so the insert errors.
//
// Validates: Requirements 6.7
func TestRunWorkflowNow_CreateRunFailureReturnsErrorNoRunID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()
	wf := createTestWorkflow(t, db, orgID, 1)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	// Force CreateRun to fail: remove the table the insert targets.
	require.NoError(t, db.Migrator().DropTable(&WorkflowRun{}))

	entity := map[string]any{"id": uuid.New().String(), "email": "a@b.com"}
	payload := buildRunNowTriggerContext("contact", TriggerContactCreated, entity)

	runID, err := engine.RunWorkflowNow(context.Background(), orgID, wf, TriggerContactCreated, payload)

	require.Error(t, err, "a CreateRun failure must surface as a returned error")
	assert.Equal(t, uuid.Nil, runID, "a failed run must return no run id (Req 6.7)")
	assert.Len(t, engine.jobs, 0, "no job must be enqueued when run creation fails")
}

// TestRunWorkflowNow_MarshalFailureReturnsErrorNoRunID verifies the other synchronous
// failure path: when the payload cannot be marshaled to JSON, RunWorkflowNow returns an
// error with no run id and enqueues no job, without touching the database (Req 6.7). This
// runs without Docker.
//
// Validates: Requirements 6.7
func TestRunWorkflowNow_MarshalFailureReturnsErrorNoRunID(t *testing.T) {
	// repo is never reached on the marshal-failure path, so a nil DB is safe here.
	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{
		repo:   NewRepository(nil),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		jobs:   make(chan WorkflowRunJob, 8),
		ctx:    ctx,
		cancel: cancel,
	}
	defer engine.cancel()

	wf := &Workflow{ID: uuid.New(), Version: 1}

	// A channel value cannot be JSON-marshaled, so json.Marshal(payload) fails.
	payload := map[string]any{"bad": make(chan int)}

	runID, err := engine.RunWorkflowNow(context.Background(), uuid.New(), wf, TriggerContactCreated, payload)

	require.Error(t, err, "an unmarshalable payload must surface as a returned error")
	assert.Equal(t, uuid.Nil, runID, "a failed run must return no run id (Req 6.7)")
	assert.Len(t, engine.jobs, 0, "no job must be enqueued when the payload cannot be marshaled")
}
