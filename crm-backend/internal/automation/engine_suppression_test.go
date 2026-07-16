package automation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// ── Pure logic (no DB — always runs, incl. CI's -short) ───────────────────────

// TestIsEnrollmentSuppressed pins the suppression predicate, the sibling of
// isInternalUpdate: only a payload carrying _suppressed=true (a real bool) skips
// enrollment. The strictness matters in the same way it does for the loop guard —
// a flag that survived a JSON round-trip as the STRING "true" must not count, or
// a real write's workflows would silently never fire (leads rotting unenrolled is
// exactly the failure this subsystem is supposed to make impossible). Our own
// emitters hand the engine a Go map with a real bool, so the strict form is what
// production actually produces.
func TestIsEnrollmentSuppressed(t *testing.T) {
	t.Run("true → suppressed", func(t *testing.T) {
		assert.True(t, isEnrollmentSuppressed(map[string]any{domain.AutomationSuppressedPayloadKey: true}))
	})

	t.Run("false → enrolls", func(t *testing.T) {
		assert.False(t, isEnrollmentSuppressed(map[string]any{domain.AutomationSuppressedPayloadKey: false}))
	})

	t.Run("absent → enrolls", func(t *testing.T) {
		assert.False(t, isEnrollmentSuppressed(map[string]any{"deal": map[string]any{"id": "x"}}))
	})

	t.Run("nil payload → enrolls", func(t *testing.T) {
		assert.False(t, isEnrollmentSuppressed(nil))
	})

	t.Run("wrong type (string \"true\") → enrolls", func(t *testing.T) {
		assert.False(t, isEnrollmentSuppressed(map[string]any{domain.AutomationSuppressedPayloadKey: "true"}))
	})
}

// ── DB-backed (Docker-gated) ──────────────────────────────────────────────────

// createDealUpdatedWF inserts an active workflow that enrolls on deal_updated.
func createDealUpdatedWF(t *testing.T, repo *Repository, orgID uuid.UUID) *Workflow {
	t.Helper()
	trig, _ := json.Marshal(map[string]any{"type": "deal_updated"})
	steps := []StepSpec{{Type: "action", ID: "a1", Action: &ActionSpec{ID: "a1", Type: "test_action", Params: map[string]any{}}}}
	stepsJSON, _ := json.Marshal(steps)
	actJSON, _ := json.Marshal(FlattenStepsToActions(steps))
	wf := &Workflow{
		OrgID:     orgID,
		Name:      "enroll-" + uuid.NewString()[:8],
		IsActive:  true,
		Trigger:   datatypes.JSON(trig),
		Actions:   datatypes.JSON(actJSON),
		Steps:     datatypes.JSON(stepsJSON),
		CreatedBy: uuid.New(),
	}
	require.NoError(t, repo.CreateWorkflow(context.Background(), wf))
	return wf
}

// suppressionFixture wires one write that two workflows care about: one enrolls on
// deal_updated, the other arms a date_field timer from the same event.
func suppressionFixture(t *testing.T) (engine *Engine, orgID uuid.UUID, enrollWF, timerWF *Workflow, payload map[string]any, cleanup func()) {
	t.Helper()
	db, dbCleanup := setupTestDB(t)
	engine = makeEngine(db, nil)
	orgID = uuid.New()
	enrollWF = createDealUpdatedWF(t, engine.repo, orgID)
	timerWF = createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)
	// A close date far enough out that "3 days before" is still in the future —
	// a past occurrence would be cancelled rather than armed.
	closeDate := time.Now().AddDate(0, 0, 30).Format("2006-01-02")
	payload = dealEventPayload(uuid.NewString(), closeDate)
	return engine, orgID, enrollWF, timerWF, payload, func() {
		engine.cancel()
		dbCleanup()
	}
}

func countRuns(t *testing.T, engine *Engine, wfID uuid.UUID) int64 {
	t.Helper()
	var n int64
	require.NoError(t, engine.db.Model(&WorkflowRun{}).Where("workflow_id = ?", wfID).Count(&n).Error)
	return n
}

// awaitDateFieldTimer polls for the workflow's pending timer. It doubles as the
// synchronization point for the assertions below: TriggerEvent's goroutine runs
// triggerEventInternal FIRST and materializes timers SECOND, so once the timer
// exists the enrollment decision has provably already been made — which turns
// "assert zero runs" from a race into a deterministic check.
func awaitDateFieldTimer(t *testing.T, engine *Engine, wfID uuid.UUID) {
	t.Helper()
	require.Eventually(t, func() bool {
		var n int64
		engine.db.Model(&AutomationTimer{}).
			Where("workflow_id = ? AND kind = ? AND status = ?", wfID, TimerKindDateField, timerStatusPending).
			Count(&n)
		return n == 1
	}, 5*time.Second, 20*time.Millisecond, "date_field timer was never materialized")
}

// TestTriggerEvent_Suppressed_SkipsEnrollmentButStillArmsDateFieldTimer is the
// contract L0 exists to guarantee: a bulk/synthetic write (a lead-ads backfill,
// an admin's test lead) must not enroll workflows, but must NOT lose the record's
// own future schedule. Suppression stops run creation only.
func TestTriggerEvent_Suppressed_SkipsEnrollmentButStillArmsDateFieldTimer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	engine, orgID, enrollWF, timerWF, payload, cleanup := suppressionFixture(t)
	defer cleanup()

	payload[domain.AutomationSuppressedPayloadKey] = true
	engine.TriggerEvent(context.Background(), orgID, "deal_updated", payload)

	awaitDateFieldTimer(t, engine, timerWF.ID)
	assert.Zero(t, countRuns(t, engine, enrollWF.ID), "a suppressed write must not enroll a workflow run")
}

// TestTriggerEvent_Unsuppressed_Enrolls is the control: without the flag the very
// same fixture enrolls, so the test above proves suppression rather than a broken
// fixture that could never have enrolled in the first place.
func TestTriggerEvent_Unsuppressed_Enrolls(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	engine, orgID, enrollWF, timerWF, payload, cleanup := suppressionFixture(t)
	defer cleanup()

	engine.TriggerEvent(context.Background(), orgID, "deal_updated", payload)

	awaitDateFieldTimer(t, engine, timerWF.ID)
	assert.Equal(t, int64(1), countRuns(t, engine, enrollWF.ID), "an ordinary write must enroll")
}
