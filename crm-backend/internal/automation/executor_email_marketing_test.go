package automation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureRawResend spins up a mock Resend that records the raw request body and
// returns an executor pointed at it.
func captureRawResend(t *testing.T, raw *map[string]any) *EmailExecutor {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, raw)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_x"}`))
	}))
	t.Cleanup(srv.Close)
	return &EmailExecutor{apiKey: "test-key", fromEmail: "noreply@20q.io", baseURL: srv.URL}
}

// TestSendMarketingLane_HeadersAndReplyToInBody proves the marketing lane
// (sendEmailWithHeaders) puts the RFC-8058 List-Unsubscribe pair and reply_to into
// the Resend request BODY (not HTTP headers), byte-exact.
func TestSendMarketingLane_HeadersAndReplyToInBody(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)

	headers := map[string]string{
		"List-Unsubscribe":      "<https://api.example.com/api/marketing/u/TOKEN>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
	_, err := exec.sendEmailWithHeaders(context.Background(), "marketing-send", "campaign:1:contact:2",
		"jane@acme.com", "Subject", "<p>Hi</p>", "Acme Marketing", "", "reply@send.acme.com", nil, headers)
	require.NoError(t, err)

	// headers is a nested object in the JSON body.
	gotHeaders, ok := raw["headers"].(map[string]any)
	require.True(t, ok, "marketing send must carry a headers object in the JSON body")
	assert.Equal(t, "<https://api.example.com/api/marketing/u/TOKEN>", gotHeaders["List-Unsubscribe"])
	assert.Equal(t, "List-Unsubscribe=One-Click", gotHeaders["List-Unsubscribe-Post"],
		"List-Unsubscribe-Post must be byte-exact")
	assert.Equal(t, "reply@send.acme.com", raw["reply_to"], "marketing send must carry reply_to")
	assert.Equal(t, "Acme Marketing <noreply@20q.io>", raw["from"])
}

// TestTransactionalSend_NoHeadersNoReplyTo pins Guardrail 9: a normal (transactional)
// send marshals to the exact same shape as before the M3 fields existed — no
// `headers` key and no `reply_to` key (both omitempty). If either leaked onto the
// transactional path, version-pinned in-flight runs would silently gain a footer/
// unsubscribe behavior they never had.
func TestTransactionalSend_NoHeadersNoReplyTo(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)

	// The exact call sendEmail (workflow send_email + SendTestEmail) makes.
	_, err := exec.sendEmail(context.Background(), "run-1", "run-1/step-1",
		"user@example.com", "Subject", "<p>Hi</p>", "", nil)
	require.NoError(t, err)

	_, hasHeaders := raw["headers"]
	assert.False(t, hasHeaders, "transactional send must NOT include a headers key")
	_, hasReplyTo := raw["reply_to"]
	assert.False(t, hasReplyTo, "transactional send must NOT include a reply_to key")
}

// TestSendMarketingEmail_Engine wires the engine seam M7 will call and confirms it
// reaches Resend with the headers attached.
func TestSendMarketingEmail_Engine(t *testing.T) {
	var raw map[string]any
	exec := captureRawResend(t, &raw)
	eng := &Engine{executors: map[string]ActionExecutor{ActionSendEmail: exec}}

	headers := MarketingUnsubHeadersForTest()
	_, err := eng.SendMarketingEmail(context.Background(), "jane@acme.com", "S", "<p>b</p>",
		"Acme", "marketing@acme.com", "reply@x.com", nil, headers, "campaign:1:contact:2")
	require.NoError(t, err)

	gotHeaders, ok := raw["headers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "List-Unsubscribe=One-Click", gotHeaders["List-Unsubscribe-Post"])
	// The per-org verified From address (B1 alignment) rides through the seam.
	assert.Equal(t, "Acme <marketing@acme.com>", raw["from"])
}

// MarketingUnsubHeadersForTest builds the byte-exact header pair the marketing
// package produces, kept here so the automation test does not import marketing.
func MarketingUnsubHeadersForTest() map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<https://api.example.com/api/marketing/u/T>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}
