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
	"gorm.io/gorm"
)

// ── Pure logic (no DB — always runs) ──────────────────────────────────────────

func TestParseHHMM(t *testing.T) {
	h, m, ok := parseHHMM("09:30")
	require.True(t, ok)
	assert.Equal(t, 9, h)
	assert.Equal(t, 30, m)

	_, _, ok = parseHHMM("24:00")
	assert.False(t, ok, "hour out of range")
	_, _, ok = parseHHMM("9")
	assert.False(t, ok, "missing minute")
	_, _, ok = parseHHMM("09:60")
	assert.False(t, ok, "minute out of range")
	_, _, ok = parseHHMM("")
	assert.False(t, ok, "empty")
}

func TestParseDateValue(t *testing.T) {
	got, ok := parseDateValue("2026-07-15")
	require.True(t, ok)
	assert.Equal(t, 2026, got.Year())
	assert.Equal(t, time.July, got.Month())
	assert.Equal(t, 15, got.Day())

	_, ok = parseDateValue("2026-07-15T23:30:00Z")
	assert.True(t, ok, "RFC3339")

	_, ok = parseDateValue("")
	assert.False(t, ok, "empty string")
	_, ok = parseDateValue("not a date")
	assert.False(t, ok, "garbage")
	_, ok = parseDateValue(12345)
	assert.False(t, ok, "non-string/time")

	now := time.Now()
	back, ok := parseDateValue(now)
	require.True(t, ok, "time.Time passes through")
	assert.Equal(t, now, back)
}

func TestComputeDateFieldFireAt_OffsetBeforeUTC(t *testing.T) {
	p := dateFieldParams{OffsetDays: -3, AtTime: "09:00", Timezone: "UTC"}
	fire, ok := computeDateFieldFireAt("2026-07-15", p, time.Now())
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC), fire)
}

func TestComputeDateFieldFireAt_DefaultTimeIs9am(t *testing.T) {
	p := dateFieldParams{OffsetDays: 0} // no at_time, no tz
	fire, ok := computeDateFieldFireAt("2026-07-15", p, time.Now())
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC), fire)
}

func TestComputeDateFieldFireAt_TimezoneShiftsUTC(t *testing.T) {
	if _, err := time.LoadLocation("America/New_York"); err != nil {
		t.Skip("tzdata unavailable")
	}
	p := dateFieldParams{OffsetDays: -1, AtTime: "09:00", Timezone: "America/New_York"}
	fire, ok := computeDateFieldFireAt("2026-07-15", p, time.Now())
	require.True(t, ok)
	// 9am EDT (UTC-4) the day before → 13:00 UTC on Jul 14.
	assert.Equal(t, time.Date(2026, 7, 14, 13, 0, 0, 0, time.UTC), fire)
}

func TestComputeDateFieldFireAt_InvalidDate(t *testing.T) {
	_, ok := computeDateFieldFireAt("", dateFieldParams{}, time.Now())
	assert.False(t, ok)
}

func TestDateFieldDedupeKey_PerOccurrence(t *testing.T) {
	rec := uuid.NewString()
	a := time.Date(2026, 7, 12, 9, 0, 0, 0, time.UTC)
	b := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	assert.Equal(t, dateFieldDedupeKey(rec, a), dateFieldDedupeKey(rec, a), "stable for the same occurrence")
	assert.NotEqual(t, dateFieldDedupeKey(rec, a), dateFieldDedupeKey(rec, b), "moved date → distinct key")
	assert.Contains(t, dateFieldDedupeKey(rec, a), dateFieldDedupePrefix(rec), "key carries the record prefix")
}

func TestDateFieldParamsFromTrigger(t *testing.T) {
	p, ok := dateFieldParamsFromTrigger(TriggerSpec{Type: TriggerDateField, Params: map[string]any{
		"object": "deal", "field": "deal.expected_close_at", "offset_days": float64(-3), "at_time": "09:00", "timezone": "UTC",
	}})
	require.True(t, ok)
	assert.Equal(t, "deal", p.Object)
	assert.Equal(t, "deal.expected_close_at", p.Field)
	assert.Equal(t, -3, p.OffsetDays)

	_, ok = dateFieldParamsFromTrigger(TriggerSpec{Type: "contact_created"})
	assert.False(t, ok, "non-date_field trigger")
	_, ok = dateFieldParamsFromTrigger(TriggerSpec{Type: TriggerDateField, Params: map[string]any{"object": "deal"}})
	assert.False(t, ok, "missing field")
}

func TestSplitObjectEvent(t *testing.T) {
	slug, ev, ok := splitObjectEvent("deal_updated")
	require.True(t, ok)
	assert.Equal(t, "deal", slug)
	assert.Equal(t, "updated", ev)

	slug, ev, ok = splitObjectEvent("subscription_created")
	require.True(t, ok)
	assert.Equal(t, "subscription", slug)
	assert.Equal(t, "created", ev)

	_, _, ok = splitObjectEvent("deal_stage_changed")
	assert.False(t, ok, "not a create/update/delete event")
	_, _, ok = splitObjectEvent("schedule")
	assert.False(t, ok)
}

// ── DB-backed (Docker-gated) ──────────────────────────────────────────────────

func createDateFieldWF(t *testing.T, repo *Repository, orgID uuid.UUID, object, field string, offsetDays int, active bool) *Workflow {
	t.Helper()
	trig, _ := json.Marshal(map[string]any{"type": TriggerDateField, "params": map[string]any{
		"object": object, "field": field, "offset_days": offsetDays, "at_time": "09:00", "timezone": "UTC",
	}})
	steps := []StepSpec{{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}}}
	stepsJSON, _ := json.Marshal(steps)
	actJSON, _ := json.Marshal(FlattenStepsToActions(steps))
	wf := &Workflow{
		OrgID:     orgID,
		Name:      "df-" + uuid.NewString()[:8],
		IsActive:  active,
		Trigger:   datatypes.JSON(trig),
		Actions:   datatypes.JSON(actJSON),
		Steps:     datatypes.JSON(stepsJSON),
		CreatedBy: uuid.New(),
	}
	require.NoError(t, repo.CreateWorkflow(context.Background(), wf))
	return wf
}

// dealEventPayload builds the {slug}_updated payload shape fireLifecycleEvent emits.
func dealEventPayload(recordID, closeDate string) map[string]any {
	return map[string]any{
		"entity_id": recordID,
		"deal": map[string]any{
			"id":                recordID,
			"title":             "Big deal",
			"expected_close_at": closeDate,
		},
		"trigger": map[string]any{"type": "deal_updated"},
	}
}

func pendingDateFieldTimers(t *testing.T, db *gorm.DB, wfID uuid.UUID) []AutomationTimer {
	t.Helper()
	var timers []AutomationTimer
	require.NoError(t, db.Where("workflow_id = ? AND kind = ? AND status = ?", wfID, TimerKindDateField, timerStatusPending).
		Order("fire_at").Find(&timers).Error)
	return timers
}

func TestDateField_MaterializeOnEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	repo := engine.repo
	ctx := context.Background()
	orgID := uuid.New()

	// Fire 3 days before expected_close_at.
	wf := createDateFieldWF(t, repo, orgID, "deal", "deal.expected_close_at", -3, true)
	recID := uuid.NewString()
	closeDate := time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)

	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", dealEventPayload(recID, closeDate)))

	timers := pendingDateFieldTimers(t, db, wf.ID)
	require.Len(t, timers, 1, "one pending date_field timer materialized")
	assert.True(t, timers[0].FireAt.After(time.Now()), "fires in the future")
	assert.True(t, timers[0].FireAt.Before(mustParse(t, closeDate)), "fires before the close date (offset -3)")
}

func TestDateField_MovedDateReArms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	repo := engine.repo
	ctx := context.Background()
	orgID := uuid.New()

	wf := createDateFieldWF(t, repo, orgID, "deal", "deal.expected_close_at", 0, true)
	recID := uuid.NewString()

	first := time.Now().Add(20 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_created", dealEventPayload(recID, first)))
	require.Len(t, pendingDateFieldTimers(t, db, wf.ID), 1)

	// Same date again → idempotent (still one).
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", dealEventPayload(recID, first)))
	require.Len(t, pendingDateFieldTimers(t, db, wf.ID), 1, "re-materializing the same occurrence is a no-op")

	firstFire := pendingDateFieldTimers(t, db, wf.ID)[0].FireAt

	// Moved date → the stale occurrence is cancelled and the new one armed (still one).
	moved := time.Now().Add(40 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", dealEventPayload(recID, moved)))
	timers := pendingDateFieldTimers(t, db, wf.ID)
	require.Len(t, timers, 1, "a moved date re-arms to a single pending timer")
	// fire_at resolves to at_time (09:00 UTC) on the field's calendar date, not the raw
	// timestamp — so compare against the recomputed occurrence, not moved's wall clock.
	expected, ok := computeDateFieldFireAt(moved, dateFieldParams{AtTime: "09:00", Timezone: "UTC"}, time.Now())
	require.True(t, ok)
	assert.Equal(t, expected.Unix(), timers[0].FireAt.Unix(), "armed at the recomputed new date")
	assert.True(t, timers[0].FireAt.After(firstFire), "re-armed later than the original date")
}

func TestDateField_DeleteAndPastCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	repo := engine.repo
	ctx := context.Background()
	orgID := uuid.New()

	wf := createDateFieldWF(t, repo, orgID, "deal", "deal.expected_close_at", 0, true)
	recID := uuid.NewString()
	future := time.Now().Add(20 * 24 * time.Hour).Format(time.RFC3339)

	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_created", dealEventPayload(recID, future)))
	require.Len(t, pendingDateFieldTimers(t, db, wf.ID), 1)

	// A delete event cancels the record's pending timer.
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_deleted", dealEventPayload(recID, future)))
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 0, "delete cancels the timer")

	// Re-arm, then move the date into the past → cancelled (no future occurrence).
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", dealEventPayload(recID, future)))
	require.Len(t, pendingDateFieldTimers(t, db, wf.ID), 1)
	past := time.Now().Add(-5 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", dealEventPayload(recID, past)))
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 0, "a past date cancels the timer")
}

func TestDateField_InactiveWorkflowNotArmed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	repo := engine.repo
	ctx := context.Background()
	orgID := uuid.New()

	wf := createDateFieldWF(t, repo, orgID, "deal", "deal.expected_close_at", 0, false) // inactive
	recID := uuid.NewString()
	future := time.Now().Add(20 * 24 * time.Hour).Format(time.RFC3339)
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", dealEventPayload(recID, future)))
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 0, "inactive workflow is not materialized")
}

func TestDateField_DueTimerFiresExactlyOneRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	repo := engine.repo
	ctx := context.Background()
	orgID := uuid.New()

	wf := createDateFieldWF(t, repo, orgID, "deal", "deal.expected_close_at", 0, true)
	recID := uuid.NewString()

	// A due (past) timer, materialized directly.
	past := time.Now().Add(-time.Minute).Truncate(time.Second)
	timer := &AutomationTimer{
		WorkflowID: wf.ID, OrgID: orgID, Kind: TimerKindDateField, Status: timerStatusPending,
		FireAt: past, DedupeKey: dateFieldDedupeKey(recID, past),
		Payload: datatypes.JSON(mustMarshal(t, dealEventPayload(recID, past.Format(time.RFC3339)))),
	}
	require.NoError(t, repo.MaterializeDateFieldTimer(ctx, timer, recID))

	due, err := repo.DueTimers(ctx, 200)
	require.NoError(t, err)
	require.NotEmpty(t, due)

	// Fire twice (crash-before-mark retry) then mark → exactly one run.
	require.NoError(t, engine.fireTimerRun(ctx, wf, timer))
	require.NoError(t, engine.fireTimerRun(ctx, wf, timer))
	require.NoError(t, repo.MarkTimerFired(ctx, timer.ID))

	var runCount int64
	require.NoError(t, db.Model(&WorkflowRun{}).Where("workflow_id = ?", wf.ID).Count(&runCount).Error)
	assert.Equal(t, int64(1), runCount, "a date_field occurrence produces exactly one run")
}

func mustParse(t *testing.T, rfc string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, rfc)
	require.NoError(t, err)
	return ts
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
