package automation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// ── Pure cron logic (no DB — always runs) ─────────────────────────────────────

func TestNextScheduleFire_Daily9amUTC(t *testing.T) {
	after := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) // noon → next 9am is tomorrow
	next, err := nextScheduleFire("0 9 * * *", "UTC", after)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), next)
}

func TestNextScheduleFire_WeeklyMonday(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	next, err := nextScheduleFire("0 9 * * 1", "UTC", after) // "every Monday 9am"
	require.NoError(t, err)
	assert.Equal(t, time.Monday, next.Weekday(), "must land on a Monday")
	assert.Equal(t, 9, next.Hour())
	assert.True(t, next.After(after))
}

func TestNextScheduleFire_TimezoneShiftsUTCHour(t *testing.T) {
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Skip("tzdata unavailable in this environment")
	}
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// 9am America/New_York in January (EST = UTC-5) → 14:00 UTC.
	ny, err := nextScheduleFire("0 9 * * *", "America/New_York", after)
	require.NoError(t, err)
	assert.Equal(t, 14, ny.Hour(), "9am EST should be 14:00 UTC")
}

func TestNextScheduleFire_StrictlyAfter(t *testing.T) {
	// Re-arming from a fire time must yield the NEXT occurrence, never the same one.
	base := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC) // a Monday 9am
	next, err := nextScheduleFire("0 9 * * 1", "UTC", base)
	require.NoError(t, err)
	assert.True(t, next.After(base))
	assert.Equal(t, time.Monday, next.Weekday())
}

func TestNextScheduleFire_InvalidCron(t *testing.T) {
	_, err := nextScheduleFire("not a cron expr", "UTC", time.Now())
	assert.Error(t, err)
}

func TestScheduleParamsFromTrigger(t *testing.T) {
	cronExpr, tz, ok := scheduleParamsFromTrigger(TriggerSpec{Type: TriggerSchedule, Params: map[string]any{"cron": "0 9 * * 1", "timezone": "UTC"}})
	assert.True(t, ok)
	assert.Equal(t, "0 9 * * 1", cronExpr)
	assert.Equal(t, "UTC", tz)

	_, _, ok = scheduleParamsFromTrigger(TriggerSpec{Type: "contact_created"})
	assert.False(t, ok, "non-schedule trigger")

	_, _, ok = scheduleParamsFromTrigger(TriggerSpec{Type: TriggerSchedule, Params: map[string]any{"timezone": "UTC"}})
	assert.False(t, ok, "missing cron")
}

func TestScheduleDedupeKey_PerOccurrence(t *testing.T) {
	a := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	b := time.Date(2026, 1, 12, 9, 0, 0, 0, time.UTC)
	assert.Equal(t, scheduleDedupeKey(a), scheduleDedupeKey(a), "stable for the same occurrence")
	assert.NotEqual(t, scheduleDedupeKey(a), scheduleDedupeKey(b), "distinct occurrences differ")
}

// ── DB-backed (Docker-gated) ──────────────────────────────────────────────────

func createScheduleWF(t *testing.T, repo *Repository, orgID uuid.UUID, cronExpr, tz string, active bool) *Workflow {
	t.Helper()
	trig, _ := json.Marshal(map[string]any{"type": TriggerSchedule, "params": map[string]any{"cron": cronExpr, "timezone": tz}})
	steps := []StepSpec{{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}}}
	stepsJSON, _ := json.Marshal(steps)
	actJSON, _ := json.Marshal(FlattenStepsToActions(steps))
	wf := &Workflow{
		OrgID:     orgID,
		Name:      "sched-" + uuid.NewString()[:8],
		IsActive:  active,
		Trigger:   datatypes.JSON(trig),
		Actions:   datatypes.JSON(actJSON),
		Steps:     datatypes.JSON(stepsJSON),
		CreatedBy: uuid.New(),
	}
	require.NoError(t, repo.CreateWorkflow(context.Background(), wf))
	return wf
}

func TestTimers_UpsertIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()
	wf := createScheduleWF(t, repo, orgID, "0 9 * * 1", "UTC", true)

	fireAt := time.Now().Add(time.Hour).Truncate(time.Second)
	mk := func() *AutomationTimer {
		return &AutomationTimer{WorkflowID: wf.ID, OrgID: orgID, Kind: TimerKindSchedule, Status: timerStatusPending, FireAt: fireAt, DedupeKey: scheduleDedupeKey(fireAt)}
	}
	require.NoError(t, repo.UpsertTimer(ctx, mk()))
	require.NoError(t, repo.UpsertTimer(ctx, mk())) // same occurrence → no-op

	var count int64
	require.NoError(t, db.Model(&AutomationTimer{}).Where("workflow_id = ?", wf.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count, "arming the same occurrence twice must not duplicate")
}

func TestTimers_ArmActiveThenCancelOnDeactivate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()
	wf := createScheduleWF(t, repo, orgID, "0 9 * * 1", "UTC", true)

	now := time.Now()
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now)) // idempotent (same next occurrence)

	has, err := repo.HasPendingTimer(ctx, wf.ID, TimerKindSchedule)
	require.NoError(t, err)
	assert.True(t, has, "active schedule workflow should have a pending timer")

	var pending int64
	db.Model(&AutomationTimer{}).Where("workflow_id = ? AND status = ?", wf.ID, timerStatusPending).Count(&pending)
	assert.Equal(t, int64(1), pending, "at most one pending timer per workflow")

	// Deactivate → arming cancels the pending timer.
	wf.IsActive = false
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))
	has, err = repo.HasPendingTimer(ctx, wf.ID, TimerKindSchedule)
	require.NoError(t, err)
	assert.False(t, has, "deactivating should cancel the pending timer")
}

func TestTimers_DueFireMarkExactlyOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()
	wf := createScheduleWF(t, repo, orgID, "0 9 * * 1", "UTC", true)

	// A due (past) timer.
	past := time.Now().Add(-time.Minute).Truncate(time.Second)
	timer := &AutomationTimer{WorkflowID: wf.ID, OrgID: orgID, Kind: TimerKindSchedule, Status: timerStatusPending, FireAt: past, DedupeKey: scheduleDedupeKey(past)}
	require.NoError(t, repo.UpsertTimer(ctx, timer))

	// DueTimers returns it WITHOUT consuming it (still pending until the run is made).
	due, err := repo.DueTimers(ctx, 200)
	require.NoError(t, err)
	found := false
	for i := range due {
		if due[i].ID == timer.ID {
			found = true
		}
	}
	assert.True(t, found, "the due timer must be returned")

	var stillPending AutomationTimer
	require.NoError(t, db.First(&stillPending, "id = ?", timer.ID).Error)
	assert.Equal(t, timerStatusPending, stillPending.Status, "DueTimers must not consume the timer")

	// Fire is idempotent per occurrence (create-then-mark, retried safely): firing twice
	// then marking fired yields exactly one run, and the timer is no longer due.
	engine := makeEngine(db, nil)
	defer engine.cancel()
	require.NoError(t, engine.fireTimerRun(ctx, wf, timer))
	require.NoError(t, engine.fireTimerRun(ctx, wf, timer)) // simulates a crash-before-mark retry
	require.NoError(t, repo.MarkTimerFired(ctx, timer.ID))

	var runCount int64
	require.NoError(t, db.Model(&WorkflowRun{}).Where("workflow_id = ?", wf.ID).Count(&runCount).Error)
	assert.Equal(t, int64(1), runCount, "a timer occurrence must produce exactly one run")

	due2, err := repo.DueTimers(ctx, 200)
	require.NoError(t, err)
	for i := range due2 {
		assert.NotEqual(t, timer.ID, due2[i].ID, "a fired timer must not be due again")
	}
}

// Toggling a schedule off then on before the occurrence must NOT drop it: cancel
// DELETEs the pending row so re-arm can re-insert the same occurrence.
func TestTimers_ReArmAfterToggleOffOn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()
	wf := createScheduleWF(t, repo, orgID, "0 9 * * 1", "UTC", true)
	now := time.Now()

	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))
	var armed AutomationTimer
	require.NoError(t, db.Where("workflow_id = ? AND status = ?", wf.ID, timerStatusPending).First(&armed).Error)

	// Toggle off → pending deleted.
	wf.IsActive = false
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))
	has, _ := repo.HasPendingTimer(ctx, wf.ID, TimerKindSchedule)
	assert.False(t, has)

	// Toggle on before the occurrence → the SAME occurrence is re-armed (not blocked).
	wf.IsActive = true
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))
	var rearmed AutomationTimer
	require.NoError(t, db.Where("workflow_id = ? AND status = ?", wf.ID, timerStatusPending).First(&rearmed).Error)
	assert.Equal(t, armed.DedupeKey, rearmed.DedupeKey, "same occurrence must be re-armed after toggle off→on")
}

// Editing an active schedule's cron must drop the stale occurrence — exactly one
// pending timer, at the NEW time, with no spurious fire at the old time.
func TestTimers_ReArmOnCronEditDropsStale(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := uuid.New()
	wf := createScheduleWF(t, repo, orgID, "0 9 * * *", "UTC", true) // daily 9am
	now := time.Now()
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))

	// Edit the cron to daily 10am and re-arm.
	trig, _ := json.Marshal(map[string]any{"type": TriggerSchedule, "params": map[string]any{"cron": "0 10 * * *", "timezone": "UTC"}})
	wf.Trigger = datatypes.JSON(trig)
	require.NoError(t, repo.ArmScheduleTimer(ctx, wf, now))

	var pending []AutomationTimer
	require.NoError(t, db.Where("workflow_id = ? AND status = ?", wf.ID, timerStatusPending).Find(&pending).Error)
	require.Len(t, pending, 1, "a cron edit must leave exactly one pending timer (the new occurrence)")
	assert.Equal(t, 10, pending[0].FireAt.UTC().Hour(), "the pending timer must be at the NEW hour")
}
