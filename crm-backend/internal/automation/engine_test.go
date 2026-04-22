package automation

import (
	"log/slog"
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
