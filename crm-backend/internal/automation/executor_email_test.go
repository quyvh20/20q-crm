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
	exec := &EmailExecutor{
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

// TestEmailAction_BodyHTMLEscapesRecordData verifies that record field values
// merged into body_html are HTML-escaped in the Resend payload (content-injection
// guard), while the template's own markup and the plain-text subject stay raw.
func TestEmailAction_BodyHTMLEscapesRecordData(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_789"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	action := ActionSpec{
		ID:   "email_escape_test",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":        "{{contact.email}}",
			"subject":   "Update for {{contact.first_name}}",
			"body_html": "<h1>Hi {{contact.first_name}}</h1>",
		},
	}

	evalCtx := EvalContext{
		Contact: map[string]any{
			"email":      "jane@acme.com",
			"first_name": `<img src=x onerror="alert(1)">`,
		},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)

	assert.Equal(t, `<h1>Hi &lt;img src=x onerror=&#34;alert(1)&#34;&gt;</h1>`, capturedPayload.HTML,
		"merged record data must be escaped; the template's own <h1> markup must not be")
	assert.Equal(t, `Update for <img src=x onerror="alert(1)">`, capturedPayload.Subject,
		"subject is a plain-text header, not HTML — it must not be entity-escaped")
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

	exec := &EmailExecutor{
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

	exec := &EmailExecutor{
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

	exec := &EmailExecutor{
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

// =============================================================================
// Pitfall tests — edge cases that can break CC in production
// =============================================================================

// TestPitfall_TemplateResolvesToGarbage — if {{contact.manager_email}} resolves
// to a non-email string, the executor must reject it, not send to Resend.
func TestPitfall_TemplateResolvesToGarbage(t *testing.T) {
	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("Resend should NOT be called when 'to' resolves to garbage")
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	// 'to' is a template that resolves to a non-email value
	action := ActionSpec{
		ID:   "email_garbage_to",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "{{contact.name}}", // resolves to "John Doe" — not an email!
			"subject": "Test",
		},
	}

	evalCtx := EvalContext{
		Contact: map[string]any{
			"name": "John Doe",
		},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid email")
	assert.Contains(t, err.Error(), "John Doe")
}

// TestPitfall_CCTemplateResolvesToGarbage — CC template resolves to non-email,
// it should be dropped silently (not crash), valid addresses preserved.
func TestPitfall_CCTemplateResolvesToGarbage(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_filter"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	// CC has a mix of valid email and garbage template value
	action := ActionSpec{
		ID:   "email_cc_mixed",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"cc":      "{{contact.name}}, valid@company.com",
			"subject": "Test",
		},
	}

	evalCtx := EvalContext{
		Contact: map[string]any{
			"name": "Jane Smith", // not an email
		},
	}

	result, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err, "Should succeed — invalid CC dropped, valid CC kept")
	require.NotNil(t, result)

	// "Jane Smith" should be dropped, "valid@company.com" should be kept
	assert.Equal(t, []string{"valid@company.com"}, capturedPayload.Cc,
		"Only valid emails should remain in CC after runtime validation")
}

// TestPitfall_AllCCInvalid — if all CC addresses resolve to garbage, CC should be nil.
func TestPitfall_AllCCInvalid(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_nil_cc"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	action := ActionSpec{
		ID:   "email_all_cc_bad",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"cc":      "{{contact.name}}, {{contact.phone}}",
			"subject": "Test",
		},
	}

	evalCtx := EvalContext{
		Contact: map[string]any{
			"name":  "Jane Smith",
			"phone": "+1234567890",
		},
	}

	result, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Nil(t, capturedPayload.Cc,
		"CC must be nil when all resolved addresses are invalid (omitempty)")
}

// TestPitfall_EmptyCCString_OmittedFromJSON verifies that CC="" produces
// a JSON payload WITHOUT the "cc" key (omitempty).
func TestPitfall_EmptyCCString_OmittedFromJSON(t *testing.T) {
	var rawPayload map[string]any

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rawPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_empty"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	action := ActionSpec{
		ID:   "email_cc_empty_str",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"cc":      "",
			"subject": "Test",
		},
	}

	result, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.NoError(t, err)
	require.NotNil(t, result)

	// The raw JSON payload should NOT contain a "cc" key
	_, hasCCKey := rawPayload["cc"]
	assert.False(t, hasCCKey,
		"Empty CC string must result in cc key being omitted from JSON payload (omitempty)")
}

// TestPitfall_WireFormat_CommaStringSplitCorrectly verifies end-to-end:
// Frontend sends string "a@x.com, b@x.com" → backend splits → Resend receives ["a@x.com", "b@x.com"]
func TestPitfall_WireFormat_CommaStringSplitCorrectly(t *testing.T) {
	var capturedPayload resendEmailPayload

	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedPayload)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_wire"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}

	// This is exactly how the frontend sends CC — a comma-separated string
	action := ActionSpec{
		ID:   "email_wire_format",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "primary@example.com",
			"cc":      "a@x.com, b@x.com, c@x.com",
			"subject": "Wire format test",
		},
	}

	result, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Resend must receive a proper array, NOT a single string with commas
	assert.Equal(t, []string{"a@x.com", "b@x.com", "c@x.com"}, capturedPayload.Cc,
		"Comma-separated CC string must be split into individual email array entries")
	assert.Len(t, capturedPayload.Cc, 3, "Must be 3 separate CC recipients, not 1 string with commas")
}

// =============================================================================
// Resend response classification (retryable vs permanent) + idempotency
// =============================================================================

// resendStatusExecutor spins up a mock Resend that always answers with the
// given status and returns an executor pointed at it.
func resendStatusExecutor(t *testing.T, status int, respBody string) *EmailExecutor {
	t.Helper()
	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(respBody))
	}))
	t.Cleanup(mockResend.Close)
	return &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}
}

// TestEmailAction_RateLimit429_IsRetryable — a Resend 429 is transient by
// definition, so it must surface as a RetryableError (engine backs off and
// retries) instead of failing the run permanently. Mirrors the webhook executor.
func TestEmailAction_RateLimit429_IsRetryable(t *testing.T) {
	exec := resendStatusExecutor(t, http.StatusTooManyRequests, `{"message":"rate limit exceeded"}`)

	run := &WorkflowRun{ID: uuid.New()}
	action := ActionSpec{
		ID:   "email_429",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"subject": "Test",
		},
	}

	_, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.Error(t, err)
	assert.True(t, isRetryable(err), "429 must be retryable, not a permanent client error")
	assert.Contains(t, err.Error(), "429")
}

// TestEmailAction_ServerError5xx_IsRetryable pins the existing 5xx classification.
func TestEmailAction_ServerError5xx_IsRetryable(t *testing.T) {
	exec := resendStatusExecutor(t, http.StatusBadGateway, `{"message":"upstream error"}`)

	run := &WorkflowRun{ID: uuid.New()}
	action := ActionSpec{
		ID:   "email_502",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"subject": "Test",
		},
	}

	_, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.Error(t, err)
	assert.True(t, isRetryable(err), "5xx must be retryable")
}

// TestEmailAction_OtherClientError_IsPermanent — a non-429 4xx (e.g. 422 bad
// payload) can never succeed on retry, so it must stay a permanent failure.
func TestEmailAction_OtherClientError_IsPermanent(t *testing.T) {
	exec := resendStatusExecutor(t, http.StatusUnprocessableEntity, `{"message":"invalid from address"}`)

	run := &WorkflowRun{ID: uuid.New()}
	action := ActionSpec{
		ID:   "email_422",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"subject": "Test",
		},
	}

	_, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.Error(t, err)
	assert.False(t, isRetryable(err), "non-429 4xx must remain a permanent failure")
	assert.Contains(t, err.Error(), "422")
}

// TestEmailAction_IdempotencyKey_StableAcrossRetries — every attempt of the same
// run+step must send the same Idempotency-Key, so an engine retry after a
// timeout/5xx doesn't double-send an email Resend already accepted.
func TestEmailAction_IdempotencyKey_StableAcrossRetries(t *testing.T) {
	var keys []string
	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if len(keys) == 1 {
			// First attempt dies with a transient 5xx → engine will retry.
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_retry"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}
	action := ActionSpec{
		ID:   "email_idem",
		Type: ActionSendEmail,
		Params: map[string]any{
			"to":      "user@example.com",
			"subject": "Test",
		},
	}

	_, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.Error(t, err)
	require.True(t, isRetryable(err))

	// The engine re-executes the same step of the same run on retry.
	result, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, keys, 2)
	assert.NotEmpty(t, keys[0], "Idempotency-Key must be sent on the first attempt")
	assert.Equal(t, keys[0], keys[1], "retry must reuse the first attempt's key")
	assert.Equal(t, run.ID.String()+"/"+action.ID, keys[0],
		"key must be derived from run id + step id")
}

// TestEmailAction_IdempotencyKey_DistinctPerRunAndStep — different runs, and
// different steps within one run, must not share a key or Resend would dedupe
// legitimately distinct emails.
func TestEmailAction_IdempotencyKey_DistinctPerRunAndStep(t *testing.T) {
	var keys []string
	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_distinct"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	params := map[string]any{"to": "user@example.com", "subject": "Test"}
	runA := &WorkflowRun{ID: uuid.New()}
	runB := &WorkflowRun{ID: uuid.New()}
	step1 := ActionSpec{ID: "email_step_1", Type: ActionSendEmail, Params: params}
	step2 := ActionSpec{ID: "email_step_2", Type: ActionSendEmail, Params: params}

	for _, c := range []struct {
		run    *WorkflowRun
		action ActionSpec
	}{{runA, step1}, {runA, step2}, {runB, step1}} {
		_, err := exec.Execute(context.Background(), c.run, c.action, EvalContext{})
		require.NoError(t, err)
	}

	require.Len(t, keys, 3)
	assert.NotEqual(t, keys[0], keys[1], "two steps in the same run must get distinct keys")
	assert.NotEqual(t, keys[0], keys[2], "the same step in two runs must get distinct keys")
}

// TestEmailAction_IdempotencyKey_OmittedWithoutStepID — a step with no id sends
// NO key at all: two id-less steps sharing one key would make Resend silently
// swallow the second email.
func TestEmailAction_IdempotencyKey_OmittedWithoutStepID(t *testing.T) {
	var headerValues []string
	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerValues = r.Header.Values("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_no_step_id"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	run := &WorkflowRun{ID: uuid.New()}
	action := ActionSpec{
		Type: ActionSendEmail, // no ID
		Params: map[string]any{
			"to":      "user@example.com",
			"subject": "Test",
		},
	}

	_, err := exec.Execute(context.Background(), run, action, EvalContext{})
	require.NoError(t, err)
	assert.Empty(t, headerValues, "no step id → the Idempotency-Key header must be absent")
}

// TestSendTestEmail_NoIdempotencyKey — the test-send path deliberately sends no
// key: every click of "send test email" should actually deliver, never dedupe.
func TestSendTestEmail_NoIdempotencyKey(t *testing.T) {
	var headerValues []string
	mockResend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerValues = r.Header.Values("Idempotency-Key")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test_send"}`))
	}))
	defer mockResend.Close()

	exec := &EmailExecutor{
		apiKey:    "test-key",
		fromEmail: "noreply@20q.io",
		baseURL:   mockResend.URL,
	}

	_, err := exec.sendEmail(context.Background(), "test-send", "", "user@example.com", "Test", "<p>Hi</p>", "", nil)
	require.NoError(t, err)
	assert.Empty(t, headerValues, "test-send must not carry an Idempotency-Key header")
}
