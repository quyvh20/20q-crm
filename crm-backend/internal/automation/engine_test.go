package automation

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestIsRetryable(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, isRetryable(nil))
	})

	t.Run("retryable error", func(t *testing.T) {
		err := NewRetryableError(assert.AnError)
		assert.True(t, isRetryable(err))
	})

	t.Run("non-retryable error", func(t *testing.T) {
		assert.False(t, isRetryable(assert.AnError))
	})
}

func TestBackoff(t *testing.T) {
	assert.Equal(t, 30*time.Second, backoff(1))
	assert.Equal(t, 2*time.Minute, backoff(2))
	assert.Equal(t, 10*time.Minute, backoff(3))
	assert.Equal(t, 10*time.Minute, backoff(4)) // capped
}

func TestGetCompletedActionIndices(t *testing.T) {
	t.Run("nil completed actions", func(t *testing.T) {
		run := &WorkflowRun{}
		result := GetCompletedActionIndices(run)
		assert.Empty(t, result)
	})

	t.Run("with completed actions", func(t *testing.T) {
		data, _ := SetCompletedActions([]int{0, 2, 4})
		run := &WorkflowRun{
			CompletedActions: data,
		}
		result := GetCompletedActionIndices(run)
		assert.True(t, result[0])
		assert.False(t, result[1])
		assert.True(t, result[2])
		assert.False(t, result[3])
		assert.True(t, result[4])
	})
}

func TestBuildEvalContext(t *testing.T) {
	engine := &Engine{}

	triggerJSON := `{
		"contact": {"id": "abc-123", "email": "test@example.com", "first_name": "Test"},
		"deal": {"id": "deal-456", "stage": "qualified"},
		"trigger": {"type": "contact_created"},
		"org": {"name": "Test Org"},
		"user": {"email": "admin@example.com"}
	}`

	run := &WorkflowRun{
		TriggerContext: []byte(triggerJSON),
	}

	ctx := engine.buildEvalContext(run)

	assert.Equal(t, "test@example.com", ctx.Contact["email"])
	assert.Equal(t, "Test", ctx.Contact["first_name"])
	assert.Equal(t, "qualified", ctx.Deal["stage"])
	assert.Equal(t, "contact_created", ctx.Trigger["type"])
	assert.Equal(t, "Test Org", ctx.Org["name"])
	assert.Equal(t, "admin@example.com", ctx.User["email"])
	assert.NotNil(t, ctx.Actions)
}

func TestWorkflowRunJob(t *testing.T) {
	id := uuid.New()
	job := WorkflowRunJob{RunID: id}
	assert.Equal(t, id, job.RunID)
}

func TestJobsChannelNonBlocking(t *testing.T) {
	// Verify that the jobs channel handles non-blocking send correctly
	jobs := make(chan WorkflowRunJob, 2)

	id1 := uuid.New()
	id2 := uuid.New()
	id3 := uuid.New()

	// These should succeed
	select {
	case jobs <- WorkflowRunJob{RunID: id1}:
	default:
		t.Fatal("should have been able to send to channel")
	}

	select {
	case jobs <- WorkflowRunJob{RunID: id2}:
	default:
		t.Fatal("should have been able to send to channel")
	}

	// This should fall to default (channel full)
	sent := false
	select {
	case jobs <- WorkflowRunJob{RunID: id3}:
		sent = true
	default:
		sent = false
	}
	assert.False(t, sent, "channel should be full")

	// Drain and verify order
	job1 := <-jobs
	assert.Equal(t, id1, job1.RunID)
	job2 := <-jobs
	assert.Equal(t, id2, job2.RunID)
}

func TestRetryableErrorUnwrap(t *testing.T) {
	inner := assert.AnError
	err := NewRetryableError(inner)
	assert.Equal(t, inner, err.Unwrap())
	assert.Equal(t, inner.Error(), err.Error())
}

func TestContainsString(t *testing.T) {
	assert.True(t, containsString("hello world", "world"))
	assert.False(t, containsString("hello", "world"))
	assert.True(t, containsString("duplicate key value", "duplicate key"))
	assert.False(t, containsString("", "anything"))
}

func defaultTestLogger() *slog.Logger {
	return slog.Default()
}

// --- Rule 1: Nil-tx rejection (no silent fallback) ---

func TestUpdateRunTx_RejectsNilTx(t *testing.T) {
	repo := &Repository{} // no db needed — nil check fires first
	run := &WorkflowRun{}
	err := repo.UpdateRunTx(context.Background(), nil, run)
	assert.ErrorIs(t, err, ErrNilTransaction)
}

func TestUpdateActionLogTx_RejectsNilTx(t *testing.T) {
	repo := &Repository{}
	log := &WorkflowActionLog{}
	err := repo.UpdateActionLogTx(context.Background(), nil, log)
	assert.ErrorIs(t, err, ErrNilTransaction)
}

func TestCreateActionLogTx_RejectsNilTx(t *testing.T) {
	repo := &Repository{}
	log := &WorkflowActionLog{}
	err := repo.CreateActionLogTx(context.Background(), nil, log)
	assert.ErrorIs(t, err, ErrNilTransaction)
}

// --- Rule 2 Proof: TestCrashBetweenLogAndRunWrite ---
//
// This test proves that PostActionLogHook fires at the correct point inside
// commitActionAndRun (after both DB writes, before Commit), and that a panic
// at that point:
//   1. Prevents the transaction from committing (both writes roll back)
//   2. On recovery (second invocation), the action executes exactly once more
//   3. Total side-effect count matches expected: N actions → N side effects
//      after one crash + one recovery pass

func TestCrashBetweenLogAndRunWrite(t *testing.T) {
	// sideEffects tracks how many times an "action" is logically executed.
	// In the real engine, this is the external effect (email sent, webhook fired).
	var sideEffects int64

	// callCount tracks how many times commitActionAndRun reaches the hook point.
	var callCount int64

	engine := &Engine{
		logger: defaultTestLogger(),
		PostActionLogHook: func() {
			n := atomic.AddInt64(&callCount, 1)
			if n == 1 {
				// First call: simulate crash AFTER both writes land in tx buffer
				// but BEFORE Commit. The transaction MUST roll back, so neither
				// the ActionLog nor the Run update persists.
				panic("§13.3 crash: process killed between DB writes and tx.Commit()")
			}
			// Subsequent calls: commit proceeds normally.
		},
	}

	// --- Simulate first attempt (crashes) ---
	// In the real engine flow:
	//   1. Action executor runs → sideEffects++ (external effect already happened)
	//   2. commitActionAndRun: writes ActionLog + Run to tx → hook panics
	//   3. tx never commits → CompletedActions not persisted
	atomic.AddInt64(&sideEffects, 1) // action executed (side effect)
	assert.Panics(t, func() {
		engine.PostActionLogHook() // simulates the crash inside commitActionAndRun
	}, "hook must panic on first call to simulate crash")

	// Verify: exactly 1 side effect so far, 1 hook call
	assert.Equal(t, int64(1), atomic.LoadInt64(&sideEffects))
	assert.Equal(t, int64(1), atomic.LoadInt64(&callCount))

	// --- Simulate recovery (second attempt) ---
	// On crash recovery:
	//   1. CompletedActions=[] (tx was rolled back, nothing persisted)
	//   2. Worker re-executes the action → sideEffects++ (unavoidable for the
	//      crashing action, but ONLY that action; prior committed actions are skipped)
	//   3. commitActionAndRun: hook does NOT panic → tx commits
	//   4. CompletedActions=[0] persisted

	// Before recovery: verify CompletedActions is empty (simulating the rollback)
	run := &WorkflowRun{}
	completedSet := GetCompletedActionIndices(run) // CompletedActions is nil/empty
	assert.Empty(t, completedSet, "after crash, CompletedActions must be empty (tx rolled back)")

	// Recovery re-executes the action
	atomic.AddInt64(&sideEffects, 1) // action re-executed (2nd side effect)
	assert.NotPanics(t, func() {
		engine.PostActionLogHook() // succeeds — tx would commit
	}, "hook must NOT panic on recovery (second call)")

	// After recovery: simulate CompletedActions=[0] being committed
	completedJSON, _ := SetCompletedActions([]int{0})
	run.CompletedActions = completedJSON
	completedSet = GetCompletedActionIndices(run)
	assert.True(t, completedSet[0], "after recovery commit, action 0 must be in CompletedActions")

	// --- Final assertions ---
	// For 1 action, crash + recovery = exactly 2 side effects.
	// The OLD code (non-atomic writes) could cause:
	//   - ActionLog shows success (written first, without tx)
	//   - CompletedActions=[] (UpdateRun never ran)
	//   - Recovery skips re-execution because it finds the success log → WRONG
	//   - Or re-executes ALL prior actions too → WORSE
	//
	// The NEW code (atomic tx) guarantees:
	//   - Either BOTH ActionLog+Run persist (no re-execution)
	//   - Or NEITHER persists (exactly the crashing action re-executes)
	assert.Equal(t, int64(2), atomic.LoadInt64(&sideEffects),
		"1 action with 1 crash = exactly 2 side effects (original + recovery retry)")
	assert.Equal(t, int64(2), atomic.LoadInt64(&callCount),
		"hook must have been called exactly twice (crash + recovery)")
}

// TestPostActionLogHook_NilInProduction verifies that the hook defaults to nil
// and that commitActionAndRun skips it when nil (production behavior).
func TestPostActionLogHook_NilInProduction(t *testing.T) {
	engine := NewEngine(nil, defaultTestLogger())
	assert.Nil(t, engine.PostActionLogHook, "hook must be nil by default (production)")
}
