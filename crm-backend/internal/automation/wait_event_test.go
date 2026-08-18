package automation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// wait_event_test.go covers the wait-for-event delay mode (A9): park until the
// run's contact opens/clicks a campaign email, or until the timeout — whichever
// lands first. The step ALWAYS completes; `happened` is what a following If/Else
// branches on.

func eventDelayStep(id, event string, timeoutSec int, campaign string) StepSpec {
	return StepSpec{Type: "delay", ID: id, Delay: &DelayParams{
		WaitEvent: event, TimeoutSec: timeoutSec, CampaignID: campaign,
	}}
}

// ── mode discrimination ─────────────────────────────────────────────────────

func TestDelayModes_EventWinsOverDateAndDuration(t *testing.T) {
	// A stale until_field left behind by a mode switch must not turn an event
	// wait back into a date wait — presence-based discriminators are exactly how
	// that regression happens.
	d := &DelayParams{WaitEvent: TriggerEmailClicked, TimeoutSec: 3600, UntilField: "deal.expected_close_at", DurationSec: 60}
	assert.True(t, d.IsWaitEvent())
	assert.False(t, d.IsWaitUntil(), "an event wait is never a date wait")

	date := &DelayParams{UntilField: "deal.expected_close_at"}
	assert.False(t, date.IsWaitEvent())
	assert.True(t, date.IsWaitUntil())

	fixed := &DelayParams{DurationSec: 60}
	assert.False(t, fixed.IsWaitEvent())
	assert.False(t, fixed.IsWaitUntil())

	var nilParams *DelayParams
	assert.False(t, nilParams.IsWaitEvent())
	assert.False(t, nilParams.IsWaitUntil())
}

// ── deadline resolution ─────────────────────────────────────────────────────

func TestResolveDelayWakeAt_EventWaitParksOnItsTimeout(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	step := eventDelayStep("w1", TriggerEmailClicked, 3600, "")

	wakeAt, resolved := resolveDelayWakeAt(step, &EvalContext{}, now)

	require.True(t, resolved, "an event wait always resolves — the timeout is its deadline")
	assert.Equal(t, now.Add(time.Hour), wakeAt,
		"parking on the timeout is what guarantees the clock sweep eventually wakes it")
}

func TestResolveDelayWakeAt_EventWaitWithoutTimeoutIsSatisfiedNotStuck(t *testing.T) {
	// The validator rejects this shape; if one ever reaches the engine it must
	// degrade to "already satisfied" rather than park with no deadline, which
	// would leak a permanently waiting run (the pruner only deletes terminal runs).
	now := time.Now()
	wakeAt, resolved := resolveDelayWakeAt(eventDelayStep("w1", TriggerEmailOpened, 0, ""), &EvalContext{}, now)
	require.True(t, resolved)
	assert.False(t, wakeAt.After(now), "must not park")
}

// ── the durable satisfaction flag ───────────────────────────────────────────

func TestEventSatisfiedFromLog(t *testing.T) {
	mk := func(body string) *WorkflowActionLog {
		return &WorkflowActionLog{Output: datatypes.JSON([]byte(body))}
	}
	assert.True(t, eventSatisfiedFromLog(mk(`{"event_satisfied":true,"wake_at":"2026-08-18T12:00:00Z"}`)))
	assert.False(t, eventSatisfiedFromLog(mk(`{"event_satisfied":false}`)))
	assert.False(t, eventSatisfiedFromLog(mk(`{"wake_at":"2026-08-18T12:00:00Z"}`)))
	// Corrupt or missing output reads as "not satisfied": the run then falls
	// through to its timeout instead of wedging.
	assert.False(t, eventSatisfiedFromLog(mk(`{not json`)))
	assert.False(t, eventSatisfiedFromLog(&WorkflowActionLog{}))
	assert.False(t, eventSatisfiedFromLog(nil))
}

// ── what the step records ───────────────────────────────────────────────────

func TestDelayFields_EventWaitCarriesItsConfigAndOutcome(t *testing.T) {
	step := eventDelayStep("w1", TriggerEmailClicked, 7200, "f47ac10b-58cc-4372-a567-0e02b2c3d479")

	in := delayInputFields(step)
	assert.Equal(t, TriggerEmailClicked, in["wait_event"])
	assert.Equal(t, 7200, in["timeout_sec"])
	assert.Equal(t, "f47ac10b-58cc-4372-a567-0e02b2c3d479", in["campaign_id"])
	assert.NotContains(t, in, "duration_sec", "an event wait is not a fixed delay")

	out := delayOutputFields(step, time.Now(), false)
	assert.Equal(t, TriggerEmailClicked, out["wait_event"])
	assert.Contains(t, out, "wake_at", "the durable deadline must always be present")
	assert.NotContains(t, out, "duration_sec")
}

// ── the payload → contact resolution the resume keys on ─────────────────────

func TestPayloadContactID(t *testing.T) {
	id := uuid.New()

	got, ok := payloadContactID(map[string]any{"entity_id": id.String()})
	require.True(t, ok)
	assert.Equal(t, id, got)

	// Falls back to the contact map when entity_id is absent or unusable.
	got, ok = payloadContactID(map[string]any{"contact": map[string]any{"id": id.String()}})
	require.True(t, ok)
	assert.Equal(t, id, got)

	_, ok = payloadContactID(map[string]any{"entity_id": "not-a-uuid"})
	assert.False(t, ok)
	_, ok = payloadContactID(map[string]any{})
	assert.False(t, ok)
}

// ── validation ──────────────────────────────────────────────────────────────

func validateDelay(t *testing.T, d *DelayParams) *ValidationResult {
	t.Helper()
	res := &ValidationResult{Valid: true}
	validateDelayParams(d, "steps[0].delay", res)
	return res
}

func TestValidateDelay_EventWaitRules(t *testing.T) {
	ok := validateDelay(t, &DelayParams{WaitEvent: TriggerEmailOpened, TimeoutSec: 3600})
	assert.True(t, ok.Valid, "a well-formed event wait is valid: %v", ok.Errors)

	withCampaign := validateDelay(t, &DelayParams{WaitEvent: TriggerEmailClicked, TimeoutSec: 60, CampaignID: uuid.NewString()})
	assert.True(t, withCampaign.Valid, "%v", withCampaign.Errors)

	noTimeout := validateDelay(t, &DelayParams{WaitEvent: TriggerEmailOpened})
	require.False(t, noTimeout.Valid, "a timeout is mandatory — it is the only thing that guarantees the run continues")
	assert.Contains(t, noTimeout.Errors[0].Message, "timeout")

	tooLong := validateDelay(t, &DelayParams{WaitEvent: TriggerEmailOpened, TimeoutSec: 2592001})
	assert.False(t, tooLong.Valid)

	badEvent := validateDelay(t, &DelayParams{WaitEvent: "contact_created", TimeoutSec: 60})
	require.False(t, badEvent.Valid, "only engagement events can be waited on")
	assert.Contains(t, badEvent.Errors[0].Message, "cannot wait for")

	badCampaign := validateDelay(t, &DelayParams{WaitEvent: TriggerEmailOpened, TimeoutSec: 60, CampaignID: "nope"})
	assert.False(t, badCampaign.Valid)
}

func TestValidateDelay_OtherModesUnaffected(t *testing.T) {
	assert.True(t, validateDelay(t, &DelayParams{DurationSec: 60}).Valid)
	assert.True(t, validateDelay(t, &DelayParams{UntilField: "deal.expected_close_at"}).Valid)
	assert.False(t, validateDelay(t, &DelayParams{}).Valid, "no mode at all is still invalid")
}

// ── the wait row the webhook path looks up ──────────────────────────────────

func TestEventWait_TableAndShape(t *testing.T) {
	assert.Equal(t, "automation_event_waits", EventWait{}.TableName())

	// The row must be serialisable with a nil campaign (wait on any campaign).
	w := EventWait{OrgID: uuid.New(), RunID: uuid.New(), StepPath: "1|yes|0", EventType: TriggerEmailClicked, ContactID: uuid.New(), ExpiresAt: time.Now().Add(time.Hour)}
	body, err := json.Marshal(w)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "campaign_id", "an unpinned wait carries no campaign")
}
