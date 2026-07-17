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

// closeDateFor returns a close date far enough out that the fixture's "3 days before"
// occurrence is still in the future — a past one would be cancelled, not armed, and
// the test would pass for the wrong reason.
func closeDateFor(t *testing.T) string {
	t.Helper()
	return time.Now().AddDate(0, 0, 30).Format("2006-01-02")
}

// withFlags stamps automation flags onto an already-stored timer payload.
func withFlags(t *testing.T, raw datatypes.JSON, keys ...string) datatypes.JSON {
	t.Helper()
	payload := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &payload))
	for _, k := range keys {
		payload[k] = true
	}
	out, err := json.Marshal(payload)
	require.NoError(t, err)
	return datatypes.JSON(out)
}

// ── Pure logic (no DB — always runs, incl. CI's -short) ───────────────────────

// TestIsAutomationSilenced mirrors TestIsEnrollmentSuppressed's strictness for the
// same reason: a flag that survived a JSON round-trip as the STRING "true" must not
// count. Our emitters hand the engine a Go map with a real bool.
func TestIsAutomationSilenced(t *testing.T) {
	t.Run("true → silenced", func(t *testing.T) {
		assert.True(t, isAutomationSilenced(map[string]any{domain.AutomationSilencedPayloadKey: true}))
	})
	t.Run("false → arms", func(t *testing.T) {
		assert.False(t, isAutomationSilenced(map[string]any{domain.AutomationSilencedPayloadKey: false}))
	})
	t.Run("absent → arms", func(t *testing.T) {
		assert.False(t, isAutomationSilenced(map[string]any{"deal": map[string]any{"id": "x"}}))
	})
	t.Run("nil payload → arms", func(t *testing.T) {
		assert.False(t, isAutomationSilenced(nil))
	})
	t.Run(`wrong type (string "true") → arms`, func(t *testing.T) {
		assert.False(t, isAutomationSilenced(map[string]any{domain.AutomationSilencedPayloadKey: "true"}))
	})
}

// TestSilenceImpliesEnrollmentSuppression pins the belt-and-braces OR in
// isEnrollmentSuppressed. The emitters already stamp both keys, so this covers the
// remaining case: a payload built by hand (a fixture, a future emitter) that carries
// only the stricter key must still skip enrollment, not sail past the guard.
func TestSilenceImpliesEnrollmentSuppression(t *testing.T) {
	assert.True(t, isEnrollmentSuppressed(map[string]any{domain.AutomationSilencedPayloadKey: true}),
		"a silenced payload must skip enrollment even without the suppression key")
}

// ── DB-backed ────────────────────────────────────────────────────────────────

// TestMaterializeDateFieldTimers_SilencedArmsNothing is the contract L2 adds on top
// of L0's: a silenced write leaves NO future trace at all.
//
// The unsilenced control is the load-bearing half — it proves the fixture could arm.
// Without it, a materialization that was broken for any unrelated reason would make
// the silenced assertion pass for the wrong reason and look like a working feature.
//
// Called synchronously rather than through TriggerEvent on purpose. TriggerEvent's
// goroutine offers no observable a silenced write produces, so the natural sync point
// (awaitDateFieldTimer) is the very thing silence deletes — "assert zero timers"
// would then be race-green whether or not the flag was wired to anything.
func TestMaterializeDateFieldTimers_SilencedArmsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	engine, orgID, _, timerWF, payload, cleanup := suppressionFixture(t)
	defer cleanup()
	ctx := context.Background()

	// Control first: this fixture demonstrably arms.
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", payload))
	require.Len(t, pendingDateFieldTimers(t, engine.db, timerWF.ID), 1,
		"control: an ordinary write must arm the timer, or the silenced assertion proves nothing")

	// A different record, silenced.
	silenced := dealEventPayload(uuid.NewString(), closeDateFor(t))
	silenced[domain.AutomationSilencedPayloadKey] = true
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", silenced))

	assert.Len(t, pendingDateFieldTimers(t, engine.db, timerWF.ID), 1,
		"a silenced write must arm nothing — the control's timer should be the only one")
}

// TestMaterializeDateFieldTimers_SilencedDeleteStillCancels pins the guard's
// PLACEMENT, which is the subtle half of this change.
//
// The guard wraps only the arm. Skipping the whole function when silenced would
// strand the exact timer silence exists to prevent: the record's delete would stop
// cancelling it, and it would fire days later against a record that no longer exists.
// A cancel only ever disarms, so it is always safe to run.
func TestMaterializeDateFieldTimers_SilencedDeleteStillCancels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	engine, orgID, _, timerWF, payload, cleanup := suppressionFixture(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_created", payload))
	require.Len(t, pendingDateFieldTimers(t, engine.db, timerWF.ID), 1)

	// The same record, deleted, on a silenced write.
	deleted := map[string]any{}
	for k, v := range payload {
		deleted[k] = v
	}
	deleted[domain.AutomationSilencedPayloadKey] = true
	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_deleted", deleted))

	assert.Empty(t, pendingDateFieldTimers(t, engine.db, timerWF.ID),
		"a silenced DELETE must still cancel: silencing a cancel would strand the timer forever")
}

// TestTriggerEventInternal_SilencedSkipsEnrollment closes the loop: silence implies
// no runs, not just no timers.
func TestTriggerEventInternal_SilencedSkipsEnrollment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	engine, orgID, enrollWF, _, payload, cleanup := suppressionFixture(t)
	defer cleanup()
	ctx := context.Background()

	// Control: the fixture enrolls.
	require.NoError(t, engine.triggerEventInternal(ctx, orgID, "deal_updated", payload))
	require.Equal(t, int64(1), countRuns(t, engine, enrollWF.ID),
		"control: an ordinary write must enroll, or the silenced assertion proves nothing")

	silenced := dealEventPayload(uuid.NewString(), closeDateFor(t))
	silenced[domain.AutomationSilencedPayloadKey] = true
	require.NoError(t, engine.triggerEventInternal(ctx, orgID, "deal_updated", silenced))

	assert.Equal(t, int64(1), countRuns(t, engine, enrollWF.ID),
		"a silenced write must create no run — the control's should be the only one")
}

// TestFireTimerRun_IgnoresSuppressionFlags pins why the guard had to go at the arm.
//
// fireTimerRun creates its run straight from the stored timer payload and consults no
// suppression predicate. So propagating a flag into that payload — the obvious fix,
// and the one that reads as correct in review — changes nothing. This test fails the
// day someone "simplifies" the arm guard into payload propagation.
func TestFireTimerRun_IgnoresSuppressionFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	engine, orgID, _, timerWF, payload, cleanup := suppressionFixture(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_updated", payload))
	timers := pendingDateFieldTimers(t, engine.db, timerWF.ID)
	require.Len(t, timers, 1)

	// Stamp both flags onto the ARMED timer's payload, then fire it.
	timer := timers[0]
	timer.Payload = withFlags(t, timer.Payload,
		domain.AutomationSuppressedPayloadKey, domain.AutomationSilencedPayloadKey)

	require.NoError(t, engine.fireTimerRun(ctx, timerWF, &timer))

	assert.Equal(t, int64(1), countRuns(t, engine, timerWF.ID),
		"fireTimerRun creates a run regardless of the flags — which is why silence must prevent the ARMING, not the firing")
}
