package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMarketingSend_CarriesTags proves a channel=marketing send stamps campaign_id (the
// run's workflow) + contact_id as Resend tags in the request body, so the delivery
// webhook can attribute engagement to the campaign (M9).
func TestMarketingSend_CarriesTags(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	cid := uuid.New()
	exec.mktPreparer = &fakePreparer{dec: MarketingSendDecision{
		ToAddress:   "jane@acme.com",
		Subject:     "Hi",
		BodyHTML:    "<p>x</p>",
		FromName:    "Acme",
		FromAddress: "marketing@send.acme.com",
	}}

	run := marketingRun()
	_, err := exec.Execute(context.Background(), run, marketingAction(uuid.NewString()), marketingEval(cid))
	require.NoError(t, err)

	tagArr, ok := raw["tags"].([]any)
	require.True(t, ok, "marketing send must carry a tags array in the body")
	got := map[string]string{}
	for _, tg := range tagArr {
		m := tg.(map[string]any)
		got[m["name"].(string)] = m["value"].(string)
	}
	assert.Equal(t, run.WorkflowID.String(), got["campaign_id"])
	assert.Equal(t, cid.String(), got["contact_id"])
}

// TestTransactionalSend_NoTags pins Guardrail 9 for the new field: a transactional send
// marshals with NO tags key (omitempty + nil), so its request bytes are unchanged.
func TestTransactionalSend_NoTags(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	_, err := exec.sendEmail(context.Background(), "run-1", "run-1/step-1", "user@example.com", "S", "<p>Hi</p>", "", nil)
	require.NoError(t, err)
	_, has := raw["tags"]
	assert.False(t, has, "transactional send must not include a tags key (Guardrail 9)")
}

// TestToResendTags_DropsEmptyAndNil proves empty maps/values never emit a tag (so the
// omitempty key stays absent) and non-empty values become {name,value} entries.
func TestToResendTags_DropsEmptyAndNil(t *testing.T) {
	assert.Nil(t, toResendTags(nil))
	assert.Nil(t, toResendTags(map[string]string{}))
	assert.Nil(t, toResendTags(map[string]string{"campaign_id": ""}), "an all-empty map emits nothing")
	got := toResendTags(map[string]string{"campaign_id": "abc"})
	require.Len(t, got, 1)
	assert.Equal(t, resendTag{Name: "campaign_id", Value: "abc"}, got[0])
}
