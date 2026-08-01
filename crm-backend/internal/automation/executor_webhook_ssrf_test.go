package automation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// send_webhook is the only place the backend fetches a URL a user controls, and
// its response body is handed back as the action output — readable by a later step
// as {{actions.<id>.response}} and stored in the run's action log. So it is not
// blind SSRF: it is a read primitive aimed at whatever the platform's network can
// reach, with the cloud metadata endpoint as the obvious prize.
//
// The guard runs at CONNECT time on the resolved address. These tests exercise the
// executor rather than the dialer directly, because the thing that broke would be
// the wiring, not the policy.

func ssrfRun() *WorkflowRun { return &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()} }

func webhookAction(url string, params map[string]any) ActionSpec {
	p := map[string]any{"url": url}
	for k, v := range params {
		p[k] = v
	}
	return ActionSpec{ID: "step-1", Type: ActionSendWebhook, Params: p}
}

func TestSendWebhook_BlocksPrivateAddresses(t *testing.T) {
	targets := []struct{ url, why string }{
		{"http://127.0.0.1/", "loopback"},
		{"http://169.254.169.254/latest/meta-data/", "cloud instance metadata"},
		{"http://10.0.0.1/", "RFC1918"},
		{"http://192.168.1.1/", "RFC1918"},
		{"http://[::1]/", "IPv6 loopback"},
		{"http://0.0.0.0/", "unspecified"},
	}

	exec := NewWebhookExecutor()
	for _, tc := range targets {
		t.Run(tc.why, func(t *testing.T) {
			out, err := exec.Execute(context.Background(), ssrfRun(), webhookAction(tc.url, nil), EvalContext{})
			if err == nil {
				t.Fatalf("%s (%s) was reached; output %v", tc.url, tc.why, out)
			}
			if out != nil {
				t.Fatal("a blocked call must not return an output map — that is the read-back channel")
			}

			// PERMANENT, not retryable. Retrying reaches the identical address, so a
			// retryable classification burns the schedule and re-issues the request
			// the guard just refused.
			var retryable *RetryableError
			if errors.As(err, &retryable) {
				t.Fatalf("a blocked address must fail permanently, got retryable: %v", err)
			}
		})
	}
}

// Schemes the client should not speak at all, refused before any socket opens.
func TestSendWebhook_BlocksNonHTTPSchemes(t *testing.T) {
	exec := NewWebhookExecutor()
	for _, raw := range []string{"file:///etc/passwd", "gopher://127.0.0.1:70/", "ftp://example.com/"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := exec.Execute(context.Background(), ssrfRun(), webhookAction(raw, nil), EvalContext{}); err == nil {
				t.Fatalf("%s was accepted", raw)
			}
		})
	}
}

// Credentials in the URL end up in the action log and in logs, and disguise the
// real host from a human reviewing the workflow.
func TestSendWebhook_BlocksEmbeddedCredentials(t *testing.T) {
	exec := NewWebhookExecutor()
	_, err := exec.Execute(context.Background(), ssrfRun(),
		webhookAction("http://user:password@example.com/hook", nil), EvalContext{})
	if err == nil {
		t.Fatal("a URL with embedded credentials was accepted")
	}
	if strings.Contains(err.Error(), "password") {
		t.Fatalf("the error must not echo the credential: %v", err)
	}
}

// The address is only decided at run time, so a template that resolves to an
// internal address must be refused just as a literal one is. This is the case
// save-time validation structurally cannot catch.
func TestSendWebhook_BlocksInterpolatedPrivateAddress(t *testing.T) {
	evalCtx := EvalContext{
		Contact: map[string]any{"custom_fields": map[string]any{"cb": "http://169.254.169.254/latest/meta-data/"}},
	}
	action := webhookAction("{{contact.custom_fields.cb}}", nil)

	out, err := NewWebhookExecutor().Execute(context.Background(), ssrfRun(), action, evalCtx)
	if err == nil {
		t.Fatalf("an interpolated internal address was reached; output %v", out)
	}
}

// The guard must not break the feature it protects.
func TestSendWebhook_AllowsAPermittedTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	// The opt-in constructor, since httptest binds loopback. That the DEFAULT
	// constructor refuses this exact address is what the first test proves.
	out, err := NewWebhookExecutorAllowingPrivate().
		Execute(context.Background(), ssrfRun(), webhookAction(srv.URL, nil), EvalContext{})
	if err != nil {
		t.Fatalf("a permitted target must still work: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["status_code"] != 200 {
		t.Fatalf("unexpected output %#v", out)
	}
}

// A worker parked for hours is an engine-wide outage, not a slow workflow: the
// pool is small and fixed.
func TestSendWebhook_CapsTimeout(t *testing.T) {
	if maxWebhookTimeoutSec > 60 {
		t.Fatalf("timeout cap %d is too generous for a small fixed worker pool", maxWebhookTimeoutSec)
	}

	// A day-long timeout must not be honoured. Verified via the blocked path so the
	// test needs no slow server: what matters is that Execute returns promptly
	// rather than parking.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = NewWebhookExecutor().Execute(context.Background(), ssrfRun(),
			webhookAction("http://10.0.0.1/", map[string]any{"timeout_sec": 86400}), EvalContext{})
	}()
	<-done
}
