package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// countingExecutor tracks how many times Execute is called.
type countingExecutor struct {
	mu    sync.Mutex
	calls []int // action indices executed, in order
	count int64
}

func (e *countingExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	atomic.AddInt64(&e.count, 1)
	e.mu.Lock()
	e.calls = append(e.calls, 0) // placeholder, overwritten below
	idx := len(e.calls) - 1
	e.mu.Unlock()

	// Determine action index from action.ID
	var actionIdx int
	fmt.Sscanf(action.ID, "action_%d", &actionIdx)
	e.mu.Lock()
	e.calls[idx] = actionIdx
	e.mu.Unlock()

	return map[string]any{"executed": action.ID}, nil
}

func (e *countingExecutor) getCallCount() int64 {
	return atomic.LoadInt64(&e.count)
}

func (e *countingExecutor) getCalls() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]int, len(e.calls))
	copy(result, e.calls)
	return result
}

// setupIntegrationDB connects to Postgres and runs migrations.
// Skips the test if DATABASE_URL is not set.
func setupIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set — skipping integration test")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err, "failed to connect to database")

	repo := NewRepository(db)
	require.NoError(t, repo.AutoMigrate(), "migration failed")

	return db
}

// createTestWorkflow inserts a workflow + version with N actions.
func createTestWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID, numActions int) *Workflow {
	t.Helper()

	trigger, _ := json.Marshal(map[string]any{"type": "webhook_inbound"})
	actions := make([]ActionSpec, numActions)
	for i := 0; i < numActions; i++ {
		actions[i] = ActionSpec{
			ID:     fmt.Sprintf("action_%d", i),
			Type:   "test_action",
			Params: map[string]any{"index": float64(i)},
		}
	}
	actionsJSON, _ := json.Marshal(actions)

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      fmt.Sprintf("integration-test-%s", uuid.New().String()[:8]),
		IsActive:  true,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actionsJSON),
		Version:   1,
		CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)

	ver := &WorkflowVersion{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		Version:    1,
		Trigger:    wf.Trigger,
		Actions:    wf.Actions,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, db.Create(ver).Error)

	return wf
}

// cleanupWorkflow removes test data created during the test.
func cleanupWorkflow(t *testing.T, db *gorm.DB, wfID uuid.UUID) {
	t.Helper()
	db.Exec("DELETE FROM automation_workflow_action_logs WHERE run_id IN (SELECT id FROM automation_workflow_runs WHERE workflow_id = ?)", wfID)
	db.Exec("DELETE FROM automation_workflow_runs WHERE workflow_id = ?", wfID)
	db.Exec("DELETE FROM automation_workflow_versions WHERE workflow_id = ?", wfID)
	db.Exec("DELETE FROM automation_workflows WHERE id = ?", wfID)
}

// TestIntegration_KillAndResume is a full integration test that:
//  1. Creates a workflow with 3 actions
//  2. Enqueues a run
//  3. Executes action[0] successfully
//  4. Simulates a process kill (PostActionLogHook panics after action[1])
//  5. Verifies crash recovery resets the run to pending
//  6. Re-processes the run
//  7. Asserts: action[0] NOT re-executed, action[1] re-executed (crash before commit),
//     action[2] executed once. Total: 4 executor calls (0,1 first pass + 1,2 on recovery).
func TestIntegration_KillAndResume(t *testing.T) {
	db := setupIntegrationDB(t)
	orgID := uuid.New()

	// Create workflow with 3 actions
	wf := createTestWorkflow(t, db, orgID, 3)
	defer cleanupWorkflow(t, db, wf.ID)

	repo := NewRepository(db)
	executor := &countingExecutor{}

	// --- Phase 1: First engine pass (will crash after action[1]) ---

	ctx1, cancel1 := context.WithCancel(context.Background())
	engine1 := &Engine{
		db:        db,
		repo:      repo,
		logger:    slog.Default(),
		jobs:      make(chan WorkflowRunJob, 100),
		workers:   1,
		ctx:       ctx1,
		cancel:    cancel1,
		executors: map[string]ActionExecutor{"test_action": executor},
	}

	// Set hook to panic after action[1]'s commit attempt
	var hookCallCount int64
	engine1.PostActionLogHook = func() {
		n := atomic.AddInt64(&hookCallCount, 1)
		if n == 2 {
			// After action[1]: both DB writes are in the tx buffer but not committed.
			// Panic simulates process kill — tx rolls back.
			panic("simulated process kill after action[1]")
		}
	}

	// Create a run
	triggerCtx, _ := json.Marshal(map[string]any{
		"contact": map[string]any{"id": uuid.New().String(), "email": "test@example.com"},
		"trigger": map[string]any{"type": "webhook_inbound"},
	})
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           orgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		IdempotencyKey:  fmt.Sprintf("test-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)

	// Process the run — will panic after action[1]
	assert.Panics(t, func() {
		engine1.processRun(run.ID)
	}, "engine must panic when PostActionLogHook panics after action[1]")
	cancel1()

	// Verify state after crash:
	// - action[0] committed (hook call 1 succeeded)
	// - action[1] executed but tx rolled back (hook call 2 panicked)
	crashedRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.NotNil(t, crashedRun)

	completedSet := GetCompletedActionIndices(crashedRun)
	assert.True(t, completedSet[0], "action[0] must be in CompletedActions (committed before crash)")
	assert.False(t, completedSet[1], "action[1] must NOT be in CompletedActions (tx rolled back)")
	assert.Equal(t, 1, crashedRun.CurrentActionIdx, "CurrentActionIdx must be 1 (action[0] committed)")

	// Executor was called twice: action[0] and action[1]
	assert.Equal(t, int64(2), executor.getCallCount(), "executor called for action[0] and action[1]")

	// --- Phase 2: Crash recovery + resume ---

	// Simulate startup recovery: reset running → pending
	RequeueInFlight(context.Background(), repo, make(chan WorkflowRunJob, 100), slog.Default())

	// Reload run to get recovery state
	recoveredRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, recoveredRun.Status, "run must be reset to pending after recovery")
	assert.Equal(t, 1, recoveredRun.RecoveryCount, "RecoveryCount must be 1")

	// Create a new engine (simulating fresh process start) — no crash hook
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	engine2 := &Engine{
		db:        db,
		repo:      repo,
		logger:    slog.Default(),
		jobs:      make(chan WorkflowRunJob, 100),
		workers:   1,
		ctx:       ctx2,
		cancel:    cancel2,
		executors: map[string]ActionExecutor{"test_action": executor},
	}

	// Re-process the run — should resume from action[1]
	engine2.processRun(recoveredRun.ID)

	// --- Phase 3: Verify final state ---

	finalRun, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, finalRun.Status, "run must be completed")
	assert.NotNil(t, finalRun.FinishedAt)

	finalCompleted := GetCompletedActionIndices(finalRun)
	assert.True(t, finalCompleted[0], "action[0] in CompletedActions")
	assert.True(t, finalCompleted[1], "action[1] in CompletedActions")
	assert.True(t, finalCompleted[2], "action[2] in CompletedActions")
	assert.Equal(t, 3, finalRun.CurrentActionIdx, "CurrentActionIdx must be 3 (all done)")

	// Total executor calls: action[0] once, action[1] twice (crash + retry), action[2] once = 4
	assert.Equal(t, int64(4), executor.getCallCount(),
		"total executor calls must be 4: action[0]×1 + action[1]×2 + action[2]×1")

	// Verify execution order
	calls := executor.getCalls()
	assert.Equal(t, []int{0, 1, 1, 2}, calls,
		"execution order: [0,1] first pass, [1,2] recovery pass")

	// Verify action logs
	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(logs), 3, "must have at least 3 action log entries")
}
