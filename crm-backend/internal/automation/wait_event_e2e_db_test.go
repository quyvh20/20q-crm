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

// wait_event_e2e_db_test.go walks the whole A9 chain against a real Postgres:
// park → register the wait → an engagement webhook claims it → the run resumes
// early with happened=true. And the other ending: nothing arrives, the clock
// sweep wakes it, and the step completes with happened=false so a following
// If/Else can still branch. Each link is unit-tested elsewhere; only this
// proves they connect.

func waitEventSteps(event string, timeoutSec int, campaignID string) []StepSpec {
	return []StepSpec{
		{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}},
		{Type: "delay", ID: "w1", Delay: &DelayParams{WaitEvent: event, TimeoutSec: timeoutSec, CampaignID: campaignID}},
		{Type: "action", ID: "a2", Action: &ActionSpec{ID: "a2", Type: "test_action", Params: map[string]any{}}},
	}
}

func contactTriggerCtx(contactID uuid.UUID) string {
	return fmt.Sprintf(`{"contact":{"id":%q,"email":"c@example.com"},"entity_id":%q,"trigger":{"type":"contact_created"}}`, contactID, contactID)
}

// delayOutput returns the completed delay log's output for step w1.
func delayOutput(t *testing.T, repo *Repository, runID uuid.UUID) map[string]any {
	t.Helper()
	logs, err := repo.GetActionLogsByRunID(context.Background(), runID)
	require.NoError(t, err)
	for i := range logs {
		if logs[i].ActionType == ActionDelay && logs[i].Status == LogStatusSuccess {
			var out map[string]any
			require.NoError(t, json.Unmarshal(logs[i].Output, &out))
			return out
		}
	}
	t.Fatalf("no completed delay log on run %s", runID)
	return nil
}

func TestWaitEvent_EngagementResumesTheRunEarly_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID, contactID := uuid.New(), uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitEventSteps(TriggerEmailClicked, 3600, ""))
	run := createStepsRun(t, repo, wf, contactTriggerCtx(contactID))

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID) // parks on the wait

	parked, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusWaiting, parked.Status)
	require.NotNil(t, parked.WakeAt, "an event wait still parks on its timeout — that deadline is the only guaranteed wake")
	assert.Equal(t, []string{"a1"}, exec.executed(), "the step after the wait must not have run yet")

	var wait EventWait
	require.NoError(t, db.First(&wait, "run_id = ?", run.ID).Error)
	assert.Equal(t, contactID, wait.ContactID, "the SUBJECT of the wait is the run's contact, stamped at park time")
	assert.Equal(t, TriggerEmailClicked, wait.EventType)
	assert.Nil(t, wait.CampaignID, "an unpinned wait matches any campaign")

	// The webhook path: this contact clicked.
	engine.resumeEventWaits(context.Background(), orgID, TriggerEmailClicked, map[string]any{
		"entity_id":   contactID.String(),
		"campaign_id": uuid.NewString(),
		"link":        "https://example.com/pricing",
	})

	woken, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusPending, woken.Status, "the event, not the clock, released it")
	assert.Nil(t, woken.WakeAt, "the timeout deadline is dropped once the event lands")

	engine.processRun(run.ID)

	done, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, done.Status)
	assert.Equal(t, []string{"a1", "a2"}, exec.executed(), "a1 must not re-run; a2 runs after the wait")

	out := delayOutput(t, repo, run.ID)
	assert.Equal(t, true, out["happened"], "this is what a following If/Else branches on")
	assert.Equal(t, false, out["timed_out"])

	var remaining int64
	require.NoError(t, db.Model(&EventWait{}).Where("run_id = ?", run.ID).Count(&remaining).Error)
	assert.Zero(t, remaining, "the registration is dropped once the wait ends, so a late event can't resume a moved-on run")
}

func TestWaitEvent_TimeoutCompletesWithHappenedFalse_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID, contactID := uuid.New(), uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitEventSteps(TriggerEmailOpened, 3600, ""))
	run := createStepsRun(t, repo, wf, contactTriggerCtx(contactID))

	exec := &idRecordingExecutor{}
	engine := makeEngine(db, map[string]ActionExecutor{"test_action": exec})
	defer engine.cancel()

	engine.processRun(run.ID)

	// Nothing arrives; fast-forward past the deadline on both the run and its log.
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_runs SET wake_at = NOW() - interval '1 second' WHERE id = ?`, run.ID).Error)
	require.NoError(t, db.Exec(
		`UPDATE automation_workflow_action_logs SET output = jsonb_set(output::jsonb, '{wake_at}', to_jsonb(to_char(NOW() - interval '1 second', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')))::jsonb
		 WHERE run_id = ? AND status = ?`, run.ID, LogStatusWaiting).Error)

	wokenIDs, err := repo.WakeDueWaitingRuns(context.Background())
	require.NoError(t, err)
	require.Contains(t, wokenIDs, run.ID, "the clock still owns a wait whose event never came")

	engine.processRun(run.ID)

	done, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, done.Status, "the automation carries on either way — a wait never strands a run")
	assert.Equal(t, []string{"a1", "a2"}, exec.executed())

	out := delayOutput(t, repo, run.ID)
	assert.Equal(t, false, out["happened"])
	assert.Equal(t, true, out["timed_out"])
	assert.Equal(t, TriggerEmailOpened, out["wait_event"])
}

func TestWaitEvent_CampaignPinIgnoresOtherCampaigns_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID, contactID := uuid.New(), uuid.New()
	pinned := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitEventSteps(TriggerEmailClicked, 3600, pinned.String()))
	run := createStepsRun(t, repo, wf, contactTriggerCtx(contactID))

	engine := makeEngine(db, map[string]ActionExecutor{"test_action": &idRecordingExecutor{}})
	defer engine.cancel()
	engine.processRun(run.ID)

	var wait EventWait
	require.NoError(t, db.First(&wait, "run_id = ?", run.ID).Error)
	require.NotNil(t, wait.CampaignID)
	assert.Equal(t, pinned, *wait.CampaignID)

	// A click in a DIFFERENT campaign must not release it.
	engine.resumeEventWaits(context.Background(), orgID, TriggerEmailClicked, map[string]any{
		"entity_id": contactID.String(), "campaign_id": uuid.NewString(),
	})
	still, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusWaiting, still.Status)

	// So must the right event from the wrong contact.
	engine.resumeEventWaits(context.Background(), orgID, TriggerEmailClicked, map[string]any{
		"entity_id": uuid.NewString(), "campaign_id": pinned.String(),
	})
	still, err = repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusWaiting, still.Status)

	// The pinned campaign does.
	engine.resumeEventWaits(context.Background(), orgID, TriggerEmailClicked, map[string]any{
		"entity_id": contactID.String(), "campaign_id": pinned.String(),
	})
	released, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, released.Status)
}

func TestWaitEvent_NoContactInContextTimesOutInsteadOfStranding_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	orgID := uuid.New()
	wf := createStepsWorkflow(t, db, orgID, waitEventSteps(TriggerEmailOpened, 3600, ""))
	// No contact at all — the builder blocks this shape, but a workflow saved
	// before its trigger changed can still reach the engine with one.
	run := createStepsRun(t, repo, wf, `{"trigger":{"type":"schedule"}}`)

	engine := makeEngine(db, map[string]ActionExecutor{"test_action": &idRecordingExecutor{}})
	defer engine.cancel()
	engine.processRun(run.ID)

	parked, err := repo.GetRunByID(context.Background(), run.ID)
	require.NoError(t, err)
	require.Equal(t, StatusWaiting, parked.Status)
	require.NotNil(t, parked.WakeAt)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *parked.WakeAt, time.Minute,
		"registration failed, but the run still parks on its timeout — it degrades to a plain delay, it does not strand")

	var waits int64
	require.NoError(t, db.Model(&EventWait{}).Where("run_id = ?", run.ID).Count(&waits).Error)
	assert.Zero(t, waits, "there is nobody to wait on, so nothing is registered")
}
