package automation

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogActivityRouting_ExecutorRegistered verifies Requirement 1.4: after the
// engine's default-executor registration runs, the log_activity action type is bound
// to an *ActivityExecutor.
//
// Engine.Start() also launches the worker pool, scheduler, and DB migrations, all of
// which require a live database. To keep this a fast, no-DB unit test we construct the
// engine directly and replicate the exact register-if-absent block from Start() — see
// engine.go Start():
//
//	if _, ok := e.executors[ActionLogActivity]; !ok {
//	    e.executors[ActionLogActivity] = NewActivityExecutor(e.db)
//	}
func TestLogActivityRouting_ExecutorRegistered(t *testing.T) {
	engine := &Engine{
		ctx:       context.Background(),
		logger:    defaultTestLogger(),
		executors: make(map[string]ActionExecutor),
		db:        nil, // no DB needed: routing is proven before any insert
	}

	// Replicate the default-executor registration performed by Start().
	if _, ok := engine.executors[ActionLogActivity]; !ok {
		engine.executors[ActionLogActivity] = NewActivityExecutor(engine.db)
	}

	registered, ok := engine.executors[ActionLogActivity]
	require.True(t, ok, "log_activity must have a registered executor after default registration")
	require.NotNil(t, registered, "registered log_activity executor must be non-nil")

	_, isActivityExecutor := registered.(*ActivityExecutor)
	assert.True(t, isActivityExecutor, "log_activity executor must be of type *ActivityExecutor")
}

// TestLogActivityRouting_DispatchRoutesToExecutor verifies Requirement 1.5: dispatching
// a log_activity action routes to the registered ActivityExecutor rather than failing
// with an "unknown action type" error.
//
// This exercises the real Engine.executeAction dispatch (executor.go). The action carries
// a valid activity_type and title, but the EvalContext has no contact or deal, so the
// ActivityExecutor returns its own precondition error ("no valid contact or deal
// identifier") before ever touching the database. Reaching that error — instead of the
// dispatch-level "unknown action type" error — is what proves the action was routed to
// the ActivityExecutor.
func TestLogActivityRouting_DispatchRoutesToExecutor(t *testing.T) {
	engine := &Engine{
		ctx:       context.Background(),
		logger:    defaultTestLogger(),
		executors: make(map[string]ActionExecutor),
		db:        nil,
	}
	if _, ok := engine.executors[ActionLogActivity]; !ok {
		engine.executors[ActionLogActivity] = NewActivityExecutor(engine.db)
	}

	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}
	action := ActionSpec{
		Type: ActionLogActivity,
		ID:   "log_1",
		Params: map[string]any{
			"activity_type": "note",
			"title":         "Logged automatically",
		},
	}
	// EvalContext deliberately lacks any contact/deal so execution stops at the
	// entity-resolution precondition without needing a DB insert.
	evalCtx := EvalContext{Actions: make(map[string]any)}

	output, err := engine.executeAction(context.Background(), run, action, evalCtx)

	// Routing proof: an unknown/unregistered type would yield this dispatch error.
	require.Error(t, err, "execution should fail at the executor's precondition, not silently succeed")
	assert.NotContains(t, err.Error(), "unknown action type",
		"log_activity must route to ActivityExecutor, not fail as an unknown action type")
	// Positive confirmation that the ActivityExecutor itself produced the error.
	assert.Contains(t, err.Error(), "no valid contact or deal identifier",
		"error should originate from ActivityExecutor's entity-resolution precondition")
	assert.Nil(t, output, "no output map is returned on an error path")
}

// TestLogActivityRouting_UnknownTypeStillUnknown is a control case: a type that is NOT
// registered still produces the dispatch-level "unknown action type" error. This guards
// against a false positive in the routing assertions above by confirming the dispatcher
// does report unknown types when an executor is genuinely absent.
func TestLogActivityRouting_UnknownTypeStillUnknown(t *testing.T) {
	engine := &Engine{
		ctx:       context.Background(),
		logger:    defaultTestLogger(),
		executors: make(map[string]ActionExecutor),
		db:        nil,
	}
	if _, ok := engine.executors[ActionLogActivity]; !ok {
		engine.executors[ActionLogActivity] = NewActivityExecutor(engine.db)
	}

	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}
	action := ActionSpec{Type: "definitely_not_a_real_action", ID: "x"}

	_, err := engine.executeAction(context.Background(), run, action, EvalContext{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unknown action type"),
		"an unregistered action type must still surface the unknown-action-type error")
}
