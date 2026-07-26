package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePreparer is a stand-in for the package-marketing MarketingSendPreparer so the
// executor branch can be tested without importing marketing (which would cycle).
type fakePreparer struct {
	dec   MarketingSendDecision
	err   error
	got   MarketingSendRequest
	calls int
}

func (f *fakePreparer) PrepareMarketingSend(_ context.Context, req MarketingSendRequest) (MarketingSendDecision, error) {
	f.calls++
	f.got = req
	return f.dec, f.err
}

func marketingRun() *WorkflowRun {
	return &WorkflowRun{ID: uuid.New(), OrgID: uuid.New(), WorkflowID: uuid.New()}
}

func marketingAction(contentID string) ActionSpec {
	return ActionSpec{ID: "step-1", Type: ActionSendEmail, Params: map[string]any{
		"channel":    "marketing",
		"to":         "jane@acme.com",
		"content_id": contentID,
	}}
}

func marketingEval(cid uuid.UUID) EvalContext {
	return EvalContext{
		Contact: map[string]any{"id": cid.String(), "email": "jane@acme.com"},
		Actions: map[string]any{},
		Extra:   map[string]any{},
	}
}

// TestExecuteMarketing_Sends proves a channel=marketing step routes through the
// preparer and sends with the preparer's verified From, reply-to and List-Unsubscribe
// headers — and that the request it hands the preparer is fully populated.
func TestExecuteMarketing_Sends(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	cid := uuid.New()
	contentID := uuid.New()
	fp := &fakePreparer{dec: MarketingSendDecision{
		ToAddress:   "jane@acme.com",
		Subject:     "Hi",
		BodyHTML:    "<p>x</p>",
		FromName:    "Acme",
		FromAddress: "marketing@send.acme.com",
		ReplyTo:     "r@acme.com",
		Headers: map[string]string{
			"List-Unsubscribe":      "<https://api.example.com/api/marketing/u/T>",
			"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
		},
	}}
	exec.mktPreparer = fp

	run := marketingRun()
	_, err := exec.Execute(context.Background(), run, marketingAction(contentID.String()), marketingEval(cid))
	require.NoError(t, err)

	// The request carried the run's org/workflow, the enrolled contact, the content id
	// and the recipient.
	assert.Equal(t, run.OrgID, fp.got.OrgID)
	assert.Equal(t, run.WorkflowID, fp.got.WorkflowID)
	assert.Equal(t, cid, fp.got.ContactID)
	assert.Equal(t, contentID, fp.got.ContentID)
	assert.Equal(t, "jane@acme.com", fp.got.ToEmail)

	// The verified From + reply-to + List-Unsubscribe headers rode into the Resend body.
	assert.Equal(t, "Acme <marketing@send.acme.com>", raw["from"])
	assert.Equal(t, "r@acme.com", raw["reply_to"])
	gotHeaders, ok := raw["headers"].(map[string]any)
	require.True(t, ok, "marketing send must carry headers in the body")
	assert.Equal(t, "List-Unsubscribe=One-Click", gotHeaders["List-Unsubscribe-Post"])
}

// TestExecuteMarketing_SkipTerminal_NoSend proves a suppressed/unlawful recipient is
// dropped at the send step (the run continues) and NEVER hits Resend.
func TestExecuteMarketing_SkipTerminal_NoSend(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	exec.mktPreparer = &fakePreparer{dec: MarketingSendDecision{Skip: true, SkipReason: "suppressed:unsubscribe"}}

	res, err := exec.Execute(context.Background(), marketingRun(), marketingAction(uuid.NewString()), marketingEval(uuid.New()))
	require.NoError(t, err)
	m, ok := res.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "skipped", m["status"])
	assert.Equal(t, "suppressed:unsubscribe", m["reason"])
	assert.Nil(t, raw, "a skipped marketing send must not POST to Resend")
}

// TestExecuteMarketing_SkipRetry_Retryable proves a transient skip (no verified domain
// yet, ledger blip) surfaces as a retryable error and does not send.
func TestExecuteMarketing_SkipRetry_Retryable(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	exec.mktPreparer = &fakePreparer{dec: MarketingSendDecision{Skip: true, Retry: true, SkipReason: "no_verified_domain"}}

	_, err := exec.Execute(context.Background(), marketingRun(), marketingAction(uuid.NewString()), marketingEval(uuid.New()))
	require.Error(t, err)
	assert.True(t, isRetryable(err), "a transient marketing skip must be retryable")
	assert.Nil(t, raw)
}

// TestExecuteMarketing_NilPreparer_PermanentError proves channel=marketing fails CLOSED
// (permanent, non-retryable) when marketing is not configured — it never falls back to
// the global MAIL_FROM.
func TestExecuteMarketing_NilPreparer_PermanentError(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)

	_, err := exec.Execute(context.Background(), marketingRun(), marketingAction(uuid.NewString()), marketingEval(uuid.New()))
	require.Error(t, err)
	assert.False(t, isRetryable(err), "no preparer → channel=marketing must fail permanently (fail-closed)")
	assert.Nil(t, raw)
}

// TestExecuteMarketing_MissingContentID_PermanentError proves a marketing step without
// a content_id fails permanently (no content to render).
func TestExecuteMarketing_MissingContentID_PermanentError(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	exec.mktPreparer = &fakePreparer{}

	action := ActionSpec{ID: "s1", Type: ActionSendEmail, Params: map[string]any{"channel": "marketing", "to": "jane@acme.com"}}
	_, err := exec.Execute(context.Background(), marketingRun(), action, marketingEval(uuid.New()))
	require.Error(t, err)
	assert.False(t, isRetryable(err))
	assert.Nil(t, raw)
}

// TestExecute_NoChannel_UsesTransactional pins Guardrail 9 at the routing layer: a step
// with no channel param never consults the marketing preparer and sends transactionally
// (no headers / reply_to).
func TestExecute_NoChannel_UsesTransactional(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	fp := &fakePreparer{}
	exec.mktPreparer = fp

	action := ActionSpec{ID: "s1", Type: ActionSendEmail, Params: map[string]any{
		"to": "user@example.com", "subject": "S", "body_html": "<p>hi</p>",
	}}
	_, err := exec.Execute(context.Background(), marketingRun(), action, EvalContext{Actions: map[string]any{}, Extra: map[string]any{}})
	require.NoError(t, err)
	assert.Equal(t, 0, fp.calls, "no channel=marketing → the preparer must not be consulted")
	_, hasHeaders := raw["headers"]
	assert.False(t, hasHeaders, "transactional send must stay byte-identical (Guardrail 9)")
}
