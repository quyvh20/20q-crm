package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// engine_delay_test.go covers the A1 durable-wait rework: a delay step parks
// the run (status=waiting + wake_at) instead of sleeping in a worker, the
// sweeper wakes due runs, resume completes the parked log and continues, and
// a parked branch pins its condition. Docker-gated (skips without Docker).

// idRecordingExecutor records the IDs of the actions it executes.
type idRecordingExecutor struct {
	mu  sync.Mutex
	ids []string
}

func (e *idRecordingExecutor) Execute(_ context.Context, _ *WorkflowRun, action ActionSpec, _ EvalContext) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ids = append(e.ids, action.ID)
	return map[string]any{"executed": action.ID}, nil
}

func (e *idRecordingExecutor) executed() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.ids))
	copy(out, e.ids)
	return out
}

// createStepsWorkflow inserts an active workflow + pinned version whose
// definition is the given steps tree (canonical A1 format; flat actions derived).
func createStepsWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID, steps []StepSpec) *Workflow {
	t.Helper()

	trigger, _ := json.Marshal(map[string]any{"type": "webhook_inbound"})
	stepsJSON, _ := json.Marshal(steps)
	actionsJSON, _ := json.Marshal(FlattenStepsToActions(steps))

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      fmt.Sprintf("delay-test-%s", uuid.New().String()[:8]),
		IsActive:  true,
		Trigger:   datatypes.JSON(trigger),
		Actions:   datatypes.JSON(actionsJSON),
		Steps:     datatypes.JSON(stepsJSON),
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
		Steps:      wf.Steps,
	}
	require.NoError(t, db.Create(ver).Error)
	return wf
}

func createStepsRun(t *testing.T, repo *Repository, wf *Workflow, triggerCtx string) *WorkflowRun {
	t.Helper()
	run := &WorkflowRun{
		ID:              uuid.New(),
		WorkflowID:      wf.ID,
		WorkflowVersion: 1,
		OrgID:           wf.OrgID,
		Status:          StatusPending,
		TriggerContext:  datatypes.JSON(triggerCtx),
		IdempotencyKey:  fmt.Sprintf("delay-test-%s", uuid.New().String()),
	}
	inserted, err := repo.CreateRun(context.Background(), run)
	require.NoError(t, err)
	require.True(t, inserted)
	return run
}

// linearDelaySteps returns: action a1 → delay d1 (durationSec) → action a2.
func linearDelaySteps(durationSec int) []StepSpec {
	return []StepSpec{
		{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}},
		{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: durationSec}},
		{Type: "action", ID: "a2", Action: &ActionSpec{ID: "a2", Type: "test_action", Params: map[string]any{}}},
	}
}

func TestDelay_ParksRunAndFreesWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, linearDelaySteps(3600))
	run := createStepsRun(t, repo, wf, `{"trigger":{"type":"webhook_inbound"}}`)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	start := time.Now()
	engine.processRun(run.ID) // returning at all (instead of sleeping 1h) is the point
	require.Less(t, time.Since(start), 30*time.Second, "processRun must not block on the delay")

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusWaiting, got.Status, "run must park on the delay")
	require.NotNil(t, got.WakeAt, "parked run must persist its wake deadline")
	wantWake := start.Add(3600 * time.Second)
	assert.WithinDuration(t, wantWake, *got.WakeAt, 30*time.Second)

	assert.Equal(t, []string{"a1"}, exec.executed(), "only the pre-delay action runs before parking")

	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	var delayLog *WorkflowActionLog
	for i := range logs {
		if logs[i].ActionType == ActionDelay {
			delayLog = &logs[i]
		}
	}
	require.NotNil(t, delayLog, "the delay step must have an action log")
	assert.Equal(t, LogStatusWaiting, delayLog.Status)
	assert.False(t, wakeAtFromLog(delayLog).IsZero(), "the waiting log must carry the wake deadline")

	// Not due → the sweeper must leave it parked.
	woken, err := repo.WakeDueWaitingRuns(context.Background())
	require.NoError(t, err)
	assert.NotContains(t, woken, run.ID)
}

func TestDelay_WakeAndResumeContinues(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, linearDelaySteps(3600))
	run := createStepsRun(t, repo, wf, `{"trigger":{"type":"webhook_inbound"}}`)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID) // parks

	// Fast-forward: deadline reached (both run row and waiting log).
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_runs SET wake_at = NOW() - interval '1 second' WHERE id = ?`, run.ID).Error)
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_action_logs SET output = jsonb_set(output::jsonb, '{wake_at}', to_jsonb(to_char(NOW() - interval '1 second', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))::jsonb
		 WHERE run_id = ? AND status = ?`, run.ID, LogStatusWaiting).Error)

	woken, err := repo.WakeDueWaitingRuns(context.Background())
	require.NoError(t, err)
	assert.Contains(t, woken, run.ID, "a due waiting run must be woken")

	engine.processRun(run.ID) // resumes

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, got.Status, "resumed run must complete the remaining steps")
	assert.Nil(t, got.WakeAt, "wake_at must clear on resume")
	assert.Equal(t, []string{"a1", "a2"}, exec.executed(), "a1 must not re-execute; a2 runs after the delay")

	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	var sawResumedDelay bool
	for i := range logs {
		if logs[i].ActionType == ActionDelay && logs[i].Status == LogStatusSuccess {
			var out map[string]any
			require.NoError(t, json.Unmarshal(logs[i].Output, &out))
			assert.Equal(t, true, out["resumed"], "completed delay log records the resume")
			sawResumedDelay = true
		}
	}
	assert.True(t, sawResumedDelay, "the waiting log must flip to success on resume")
}

func TestDelay_RecoveryPreservesDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, linearDelaySteps(3600))
	run := createStepsRun(t, repo, wf, `{"trigger":{"type":"webhook_inbound"}}`)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID) // parks
	parked, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusWaiting, parked.Status)
	require.NotNil(t, parked.WakeAt)
	originalWake := *parked.WakeAt

	// Simulate a process restart: crash recovery must leave parked runs parked.
	RequeueInFlight(context.Background(), repo, make(chan WorkflowRunJob, 100), engine.logger)

	after, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusWaiting, after.Status, "recovery must not requeue a waiting run")
	require.NotNil(t, after.WakeAt)
	assert.WithinDuration(t, originalWake, *after.WakeAt, time.Second,
		"the wake deadline must survive a restart — elapsed delay time is never lost")
	assert.Equal(t, []string{"a1"}, exec.executed(), "no step re-executes during recovery")
}

func TestDelay_EarlyRequeueReparks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, linearDelaySteps(3600))
	run := createStepsRun(t, repo, wf, `{"trigger":{"type":"webhook_inbound"}}`)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID) // parks
	parked, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.NotNil(t, parked.WakeAt)
	originalWake := *parked.WakeAt

	// A spurious wake (bug, manual flip) re-queues the run before its deadline.
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_runs SET status = ? WHERE id = ?`, StatusPending, run.ID).Error)
	engine.processRun(run.ID)

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusWaiting, got.Status, "an early-woken run must re-park")
	require.NotNil(t, got.WakeAt)
	assert.WithinDuration(t, originalWake, *got.WakeAt, 2*time.Second,
		"re-parking must keep the original deadline, not restart the delay")
	assert.Equal(t, []string{"a1"}, exec.executed(), "no post-delay step may run early")
}

func TestDelay_WaitingBranchStaysPinned(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()

	condition := &ConditionGroup{Field: "trigger.flag", Operator: "eq", Value: true}
	steps := []StepSpec{
		{
			Type: "condition", ID: "c1", Condition: condition,
			YesSteps: []StepSpec{
				{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: 3600}},
				{Type: "action", ID: "y1", Action: &ActionSpec{ID: "y1", Type: "test_action", Params: map[string]any{}}},
			},
			NoSteps: []StepSpec{
				{Type: "action", ID: "n1", Action: &ActionSpec{ID: "n1", Type: "test_action", Params: map[string]any{}}},
			},
		},
	}
	wf := createStepsWorkflow(t, db, orgID, steps)
	run := createStepsRun(t, repo, wf, `{"trigger":{"type":"webhook_inbound","flag":true}}`)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID) // condition → yes branch → parks on d1
	parked, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusWaiting, parked.Status, "run must park inside the yes branch")

	// The trigger data changes while parked — without pinning the condition
	// would now evaluate false and flip to the no branch, orphaning the delay.
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_runs SET trigger_context = ? WHERE id = ?`,
		`{"trigger":{"type":"webhook_inbound","flag":false}}`, run.ID).Error)

	// Fast-forward the deadline and wake.
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_runs SET wake_at = NOW() - interval '1 second' WHERE id = ?`, run.ID).Error)
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_action_logs SET output = jsonb_set(output::jsonb, '{wake_at}', to_jsonb(to_char(NOW() - interval '1 second', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))::jsonb
		 WHERE run_id = ? AND status = ?`, run.ID, LogStatusWaiting).Error)
	woken, err := repo.WakeDueWaitingRuns(context.Background())
	require.NoError(t, err)
	require.Contains(t, woken, run.ID)

	engine.processRun(run.ID)

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, got.Status)
	assert.Equal(t, []string{"y1"}, exec.executed(),
		"the yes branch stays pinned by its parked delay; the no branch never runs")
}

func TestWakeDueWaitingRuns_Contention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, linearDelaySteps(1))

	const n = 10
	past := time.Now().Add(-time.Minute)
	for i := 0; i < n; i++ {
		run := &WorkflowRun{
			ID:              uuid.New(),
			WorkflowID:      wf.ID,
			WorkflowVersion: 1,
			OrgID:           orgID,
			Status:          StatusWaiting,
			WakeAt:          &past,
			TriggerContext:  datatypes.JSON(`{}`),
			IdempotencyKey:  fmt.Sprintf("contention-%d-%s", i, uuid.New().String()),
		}
		require.NoError(t, db.Create(run).Error)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = make(map[uuid.UUID]int)
	)
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids, err := repo.WakeDueWaitingRuns(context.Background())
			assert.NoError(t, err)
			mu.Lock()
			for _, id := range ids {
				seen[id]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Len(t, seen, n, "every due waiting run is woken exactly once across concurrent sweepers")
	for id, count := range seen {
		assert.Equal(t, 1, count, "run %s claimed by exactly one sweeper", id)
	}
}

// --- pure unit tests (no DB) ---

func TestFlattenStepsToActions(t *testing.T) {
	steps := []StepSpec{
		{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x"}}},
		{
			Type: "condition", ID: "c1",
			Condition: &ConditionGroup{Field: "deal.is_won", Operator: "eq", Value: true},
			YesSteps: []StepSpec{
				{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: 86400}},
				{Type: "action", ID: "y1", Action: &ActionSpec{ID: "y1", Type: "create_task", Params: map[string]any{}}},
			},
			NoSteps: []StepSpec{
				{Type: "action", ID: "n1", Action: &ActionSpec{ID: "n1", Type: "log_activity", Params: map[string]any{}}},
			},
		},
		{Type: "action", ID: "a2", Action: &ActionSpec{ID: "a2", Type: "send_webhook", Params: map[string]any{}}},
	}

	flat := FlattenStepsToActions(steps)

	// DFS order, condition nodes dropped, branches inlined yes-then-no —
	// mirroring the frontend's former flattenSteps exactly.
	var ids []string
	for _, a := range flat {
		ids = append(ids, a.ID)
	}
	assert.Equal(t, []string{"a1", "d1", "y1", "n1", "a2"}, ids)

	assert.Equal(t, ActionDelay, flat[1].Type)
	assert.Equal(t, map[string]any{"duration_sec": 86400}, flat[1].Params)

	assert.Empty(t, FlattenStepsToActions(nil))
}

func TestDeriveActionsFromSteps(t *testing.T) {
	assert.False(t, hasSteps(nil))
	assert.False(t, hasSteps(datatypes.JSON(`null`)))
	assert.False(t, hasSteps(datatypes.JSON(`[]`)))
	assert.True(t, hasSteps(datatypes.JSON(`[{"type":"action","id":"a1"}]`)))

	stepsJSON := datatypes.JSON(`[
		{"type":"action","id":"a1","action":{"id":"a1","type":"send_email","params":{"to":"x"}}},
		{"type":"delay","id":"d1","delay":{"duration_sec":60}}
	]`)
	derived, err := deriveActionsFromSteps(stepsJSON)
	require.NoError(t, err)
	var actions []ActionSpec
	require.NoError(t, json.Unmarshal(derived, &actions))
	require.Len(t, actions, 2)
	assert.Equal(t, "a1", actions[0].ID)
	assert.Equal(t, ActionDelay, actions[1].Type)

	// An all-condition tree (no executable steps) derives an empty array, not null.
	condOnly := datatypes.JSON(`[{"type":"condition","id":"c1","condition":{"field":"x","operator":"eq","value":1}}]`)
	derived, err = deriveActionsFromSteps(condOnly)
	require.NoError(t, err)
	assert.Equal(t, `[]`, string(derived))

	_, err = deriveActionsFromSteps(datatypes.JSON(`{not valid`))
	require.Error(t, err)
}

func TestHasAnyStepStarted_MatchesStructuralPaths(t *testing.T) {
	steps := []StepSpec{
		{Type: "action", ID: "a1"},
		{
			Type: "condition", ID: "c1",
			YesSteps: []StepSpec{{Type: "delay", ID: "d1"}},
			NoSteps:  []StepSpec{{Type: "action", ID: "n1"}},
		},
	}
	cond := steps[1]
	condPath := BuildStepPath("", "", 1)

	// Path-keyed progress (how resumed runs record it) must pin the branch.
	started := map[string]bool{BuildStepPath(condPath, "yes", 0): true}
	assert.True(t, hasAnyStepStarted(cond.YesSteps, started, condPath, "yes"),
		"a structural-path entry must pin the yes branch")
	assert.False(t, hasAnyStepStarted(cond.NoSteps, started, condPath, "no"))

	// ID-keyed progress (fresh runs / legacy logs) must keep working.
	started = map[string]bool{"d1": true}
	assert.True(t, hasAnyStepStarted(cond.YesSteps, started, condPath, "yes"))
}
