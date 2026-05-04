package automation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmailAction_CCFieldRoundTrip_ActualRun verifies that when the EmailExecutor
// runs with a CC param (comma-separated string), the Resend API payload contains
// both "to" and "cc" arrays with the correct resolved values.
func TestEmailAction_CCFieldRoundTrip_ActualRun(t *testing.T) {
	// Capture the payload sent to "Resend"
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)

		// Verify headers
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_123"}`))
	}))
	defer mockResend.Close()

	// Patch the executor to hit our mock server instead of Resend
	exec := &testableEmailExecutor{
		apiKey:    "test-api-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{
		ID: uuid.New(),
	}

	action := ActionSpec{
		ID:   "email_cc_test",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":        "{{contact.email}}",
			"cc":        "{{contact.email}}, manager@company.com, cfo@company.com",
			"from_name": "Sales Team",
			"subject":   "Deal update for {{contact.first_name}}",
			"body_html": "<h1>Hi {{contact.first_name}}</h1>",
		},
	}

	evalCtx := EvalContext{
		Contact: map[string]any{
			"email":      "jane@acme.com",
			"first_name": "Jane",
		},
	}

	result, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify To
	assert.Equal(t, []string{"jane@acme.com"}, capturedPayload.To,
		"To must contain the resolved contact email")

	// Verify CC — comma-separated string split into array
	assert.Equal(t, []string{"jane@acme.com", "manager@company.com", "cfo@company.com"}, capturedPayload.Cc,
		"CC must contain all 3 resolved addresses: template + 2 literals")

	// Verify From
	assert.Equal(t, "Sales Team <noreply@20q.io>", capturedPayload.From,
		"From must include from_name")

	// Verify Subject with template interpolation
	assert.Equal(t, "Deal update for Jane", capturedPayload.Subject)

	// Verify Body HTML with template interpolation
	assert.Equal(t, "<h1>Hi Jane</h1>", capturedPayload.HTML)
}

// TestEmailAction_NoCCField — email sent without CC, CC array must be nil/omitted.
func TestEmailAction_NoCCField(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_456"}`))
	}))
	defer mockResend.Close()

	exec := &testableEmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	action := ActionSpec{
		ID:   "email_no_cc",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":        "user@example.com",
			"subject":   "Hello",
			"body_html": "<p>World</p>",
		},
	}

	result, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []string{"user@example.com"}, capturedPayload.To)
	assert.Nil(t, capturedPayload.Cc, "CC must be nil when not provided (omitempty)")
}

// TestEmailAction_EmptyCCString — CC is empty string, should be treated as nil.
func TestEmailAction_EmptyCCString(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_789"}`))
	}))
	defer mockResend.Close()

	exec := &testableEmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	action := ActionSpec{
		ID:   "email_empty_cc",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":        "user@example.com",
			"cc":        "",
			"subject":   "Test",
			"body_html": "",
		},
	}

	result, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Nil(t, capturedPayload.Cc, "CC must be nil for empty string (omitempty)")
}

// TestEmailAction_CCTemplateOnly — CC is only a template variable.
func TestEmailAction_CCTemplateOnly(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_template"}`))
	}))
	defer mockResend.Close()

	exec := &testableEmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	action := ActionSpec{
		ID:   "email_cc_template",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "direct@example.com",
			"cc":      "{{contact.email}}",
			"subject": "FYI",
		},
	}

	evalCtx := EvalContext{
		Contact: map[string]any{"email": "boss@corp.com"},
	}

	result, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, []string{"boss@corp.com"}, capturedPayload.Cc,
		"CC must resolve template to single email")
}
