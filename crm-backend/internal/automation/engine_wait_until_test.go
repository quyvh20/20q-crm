package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engine_wait_until_test.go covers the A4.4 wait-until delay: a delay step whose
// deadline is a record date field (+offset/at_time/tz) resolved from the run's
// eval context, parked on the same A1 durable-wait machinery as a fixed delay.

// ── Pure resolveDelayWakeAt (no DB — always runs) ─────────────────────────────

func TestResolveDelayWakeAt_FixedDuration(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	step := StepSpec{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: 3600}}
	wakeAt, ok := resolveDelayWakeAt(step, &EvalContext{}, now)
	require.True(t, ok)
	assert.Equal(t, now.Add(time.Hour), wakeAt)
}

func TestResolveDelayWakeAt_UntilField(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ctx := &EvalContext{Deal: map[string]any{"expected_close_at": "2026-07-15T00:00:00Z"}}
	step := StepSpec{Type: "delay", ID: "d1", Delay: &DelayParams{
		UntilField: "deal.expected_close_at", OffsetDays: -3, AtTime: "09:00", Timezone: "UTC",
	}}
	wakeAt, ok := resolveDelayWakeAt(step, ctx, now)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC), wakeAt)
}

func TestResolveDelayWakeAt_UntilFieldUnresolvable(t *testing.T) {
	now := time.Now()
	// Field not present in the eval context → can't resolve a wait target.
	step := StepSpec{Type: "delay", ID: "d1", Delay: &DelayParams{UntilField: "deal.expected_close_at"}}
	_, ok := resolveDelayWakeAt(step, &EvalContext{}, now)
	assert.False(t, ok, "an unresolvable date field yields no wait target")
}

func TestResolveDelayWakeAt_UntilFieldEmptyString(t *testing.T) {
	now := time.Now()
	ctx := &EvalContext{Deal: map[string]any{"expected_close_at": ""}}
	step := StepSpec{Type: "delay", ID: "d1", Delay: &DelayParams{UntilField: "deal.expected_close_at"}}
	_, ok := resolveDelayWakeAt(step, ctx, now)
	assert.False(t, ok, "an empty date value yields no wait target")
}

func TestDelayInputOutputFields_WaitUntil(t *testing.T) {
	step := StepSpec{Type: "delay", ID: "d1", Delay: &DelayParams{
		UntilField: "deal.expected_close_at", OffsetDays: -3, AtTime: "09:00", Timezone: "UTC",
	}}
	in := delayInputFields(step)
	assert.Equal(t, "deal.expected_close_at", in["until_field"])
	assert.NotContains(t, in, "duration_sec")

	wakeAt := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	out := delayOutputFields(step, wakeAt, true)
	assert.Equal(t, wakeAt, out["wake_at"])
	assert.Equal(t, true, out["resumed"])
	assert.Equal(t, "deal.expected_close_at", out["until_field"])
}

func TestValidateDelayParams_WaitUntilLiftsCap(t *testing.T) {
	// A wait-until delay is not bounded by the 30-day fixed-delay cap.
	res := &ValidationResult{Valid: true}
	validateDelayParams(&DelayParams{UntilField: "deal.expected_close_at", AtTime: "09:00", Timezone: "UTC"}, "steps[0].delay", res)
	assert.True(t, res.Valid, "a valid wait-until delay passes")

	res = &ValidationResult{Valid: true}
	validateDelayParams(&DelayParams{UntilField: "deal.x", AtTime: "99:99"}, "steps[0].delay", res)
	assert.False(t, res.Valid, "a bad at_time is rejected")

	res = &ValidationResult{Valid: true}
	validateDelayParams(&DelayParams{DurationSec: 0}, "steps[0].delay", res)
	assert.False(t, res.Valid, "a fixed delay still requires a positive duration")

	res = &ValidationResult{Valid: true}
	validateDelayParams(&DelayParams{DurationSec: 2592001}, "steps[0].delay", res)
	assert.False(t, res.Valid, "a fixed delay is still capped at 30 days")
}

// TestValidateActions_WaitUntilDelayFlat guards the deprecated flat-actions
// validation path: a wait-until delay expressed as an actions-only body (no steps)
// must validate the same as in the steps tree. Regression — the flat path used to
// require duration_sec>0 and ignore until_field, wrongly rejecting a valid delay.
func TestValidateActions_WaitUntilDelayFlat(t *testing.T) {
	trigger := []byte(`{"type":"deal_stage_changed","params":{"to_stage":"s1"}}`)

	// Actions-only body (no steps arg) → forces the validateActions path.
	valid := []byte(`[{"type":"delay","id":"d1","params":{"until_field":"deal.expected_close_at","offset_days":-3,"at_time":"09:00","timezone":"UTC"}}]`)
	res := ValidateWorkflowPayload(trigger, nil, valid)
	assert.True(t, res.Valid, "wait-until delay in a flat actions body is valid: %+v", res.Errors)

	// duration_sec absent is fine in wait-until mode (it's ignored).
	noDur := []byte(`[{"type":"delay","id":"d1","params":{"until_field":"deal.expected_close_at"}}]`)
	res = ValidateWorkflowPayload(trigger, nil, noDur)
	assert.True(t, res.Valid, "wait-until delay without duration_sec is valid: %+v", res.Errors)

	// A malformed at_time is still rejected on the flat path.
	badTime := []byte(`[{"type":"delay","id":"d1","params":{"until_field":"deal.x","at_time":"99:99"}}]`)
	res = ValidateWorkflowPayload(trigger, nil, badTime)
	assert.False(t, res.Valid, "invalid at_time rejected on the flat path")

	// A fixed delay on the flat path still requires a positive duration_sec.
	badFixed := []byte(`[{"type":"delay","id":"d1","params":{"duration_sec":0}}]`)
	res = ValidateWorkflowPayload(trigger, nil, badFixed)
	assert.False(t, res.Valid, "fixed delay with duration_sec=0 still rejected")
}

// ── DB-backed (Docker-gated) ──────────────────────────────────────────────────

// waitUntilSteps: action a1 → wait-until d1 → action a2.
func waitUntilSteps(field string, offsetDays int) []StepSpec {
	return []StepSpec{
		{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}},
		{Type: "delay", ID: "d1", Delay: &DelayParams{UntilField: field, OffsetDays: offsetDays, AtTime: "09:00", Timezone: "UTC"}},
		{Type: "action", ID: "a2", Action: &ActionSpec{ID: "a2", Type: "test_action", Params: map[string]any{}}},
	}
}

func TestWaitUntil_ParksOnFieldDate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitUntilSteps("deal.expected_close_at", -3))

	closeDate := time.Now().Add(40 * 24 * time.Hour).UTC()
	triggerCtx := fmt.Sprintf(`{"deal":{"id":"%s","expected_close_at":"%s"},"trigger":{"type":"webhook_inbound"}}`,
		uuid.NewString(), closeDate.Format(time.RFC3339))
	run := createStepsRun(t, repo, wf, triggerCtx)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID)

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusWaiting, got.Status, "run parks on the wait-until deadline")
	require.NotNil(t, got.WakeAt)
	// 3 days before the close date, at 09:00 UTC.
	expected := time.Date(closeDate.Year(), closeDate.Month(), closeDate.Day(), 9, 0, 0, 0, time.UTC).AddDate(0, 0, -3)
	assert.WithinDuration(t, expected, *got.WakeAt, time.Second)
	assert.Equal(t, []string{"a1"}, exec.executed(), "only the pre-delay action runs before parking")
}

func TestWaitUntil_ResumeCompletes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitUntilSteps("deal.expected_close_at", 0))

	closeDate := time.Now().Add(20 * 24 * time.Hour).UTC()
	triggerCtx := fmt.Sprintf(`{"deal":{"id":"%s","expected_close_at":"%s"},"trigger":{"type":"webhook_inbound"}}`,
		uuid.NewString(), closeDate.Format(time.RFC3339))
	run := createStepsRun(t, repo, wf, triggerCtx)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID) // parks

	// Fast-forward the deadline (run row + waiting log) and wake.
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_runs SET wake_at = NOW() - interval '1 second' WHERE id = ?`, run.ID).Error)
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_action_logs SET output = jsonb_set(output::jsonb, '{wake_at}', to_jsonb(to_char(NOW() - interval '1 second', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))::jsonb
		 WHERE run_id = ? AND status = ?`, run.ID, LogStatusWaiting).Error)
	woken, err := repo.WakeDueWaitingRuns(context.Background())
	require.NoError(t, err)
	require.Contains(t, woken, run.ID)

	engine.processRun(run.ID) // resumes

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, got.Status, "resumed wait-until run completes the remaining steps")
	assert.Nil(t, got.WakeAt)
	assert.Equal(t, []string{"a1", "a2"}, exec.executed(), "a1 not re-run; a2 runs after the wait")
}

func TestWaitUntil_UnresolvableFieldProceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitUntilSteps("deal.expected_close_at", -3))

	// No expected_close_at in context → nothing to wait for → run proceeds to completion.
	triggerCtx := fmt.Sprintf(`{"deal":{"id":"%s"},"trigger":{"type":"webhook_inbound"}}`, uuid.NewString())
	run := createStepsRun(t, repo, wf, triggerCtx)

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID)

	got, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, got.Status, "an unresolvable wait-until proceeds immediately")
	assert.Equal(t, []string{"a1", "a2"}, exec.executed())

	// The delay log records success (proceeded), not waiting.
	logs, err := repo.GetActionLogsByRunID(context.Background(), run.ID)
	require.NoError(t, err)
	var sawDelay bool
	for i := range logs {
		if logs[i].ActionType == ActionDelay {
			sawDelay = true
			assert.Equal(t, LogStatusSuccess, logs[i].Status)
		}
	}
	assert.True(t, sawDelay)
}

func TestWaitUntil_FlattenCarriesFields(t *testing.T) {
	steps := waitUntilSteps("deal.expected_close_at", -3)
	flat := FlattenStepsToActions(steps)
	require.Len(t, flat, 3)
	assert.Equal(t, ActionDelay, flat[1].Type)
	assert.Equal(t, "deal.expected_close_at", flat[1].Params["until_field"])
	assert.Equal(t, -3, flat[1].Params["offset_days"])
	assert.Equal(t, "09:00", flat[1].Params["at_time"])
}

func TestFlatten_FixedDelayUnchanged(t *testing.T) {
	// A fixed delay must still flatten to exactly {duration_sec} (no wait-until keys).
	steps := []StepSpec{{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: 3600}}}
	flat := FlattenStepsToActions(steps)
	require.Len(t, flat, 1)
	assert.Equal(t, map[string]any{"duration_sec": 3600}, flat[0].Params)
	b, _ := json.Marshal(flat[0].Params)
	assert.NotContains(t, string(b), "until_field")
}
