package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"crm-backend/pkg/safedial"
)

// maxWebhookTimeoutSec bounds a single outbound call.
//
// The cap is not politeness. The engine runs a small fixed pool of workers, so a
// saved timeout_sec of 86400 does not merely stall one workflow — it parks a worker
// for a day, and a handful of them stop automation for the whole deployment. The
// value was previously unbounded in the executor, in the save-time validator and in
// the client-side schema alike.
const maxWebhookTimeoutSec = 60

// WebhookExecutor sends HTTP webhook requests.
type WebhookExecutor struct {
	// allowPrivate disables the SSRF address guard. Set ONLY by tests, which drive
	// real httptest servers on 127.0.0.1, and by an explicitly opted-in self-hosted
	// deployment. See NewWebhookExecutorAllowingPrivate.
	allowPrivate bool
}

// NewWebhookExecutor creates a webhook executor that refuses to reach private
// network space.
func NewWebhookExecutor() *WebhookExecutor {
	return &WebhookExecutor{}
}

// NewWebhookExecutorAllowingPrivate creates an executor with the address guard OFF.
//
// Two legitimate callers: the integration tests, which point the real executor at
// an httptest server bound to 127.0.0.1, and a single-tenant self-hosted deployment
// whose webhook receivers genuinely live on a private network. It must never be
// used on a multi-tenant deployment — with the guard off, any workflow author can
// read whatever the platform's network can reach, because the response body comes
// back to them as the action's output.
func NewWebhookExecutorAllowingPrivate() *WebhookExecutor {
	return &WebhookExecutor{allowPrivate: true}
}

// Execute sends an outbound webhook.
func (e *WebhookExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	// Interpolated HERE, at run time, from template values that may come from
	// trigger data rather than from the workflow author. That is exactly why the
	// address check cannot live at save time.
	url := getStringParam(action.Params, "url", evalCtx)
	if url == "" {
		return nil, fmt.Errorf("send_webhook: 'url' is required")
	}
	if err := safedial.ValidateURL(url); err != nil {
		// Permanent: the same template resolves the same way on every retry.
		return nil, fmt.Errorf("send_webhook: %w", err)
	}

	method := getStringParam(action.Params, "method", evalCtx)
	if method == "" {
		method = "POST"
	}
	if method != "POST" && method != "PUT" {
		return nil, fmt.Errorf("send_webhook: method must be POST or PUT, got '%s'", method)
	}

	timeoutSec := getIntParam(action.Params, "timeout_sec")
	if timeoutSec <= 0 {
		timeoutSec = 10
	}
	if timeoutSec > maxWebhookTimeoutSec {
		timeoutSec = maxWebhookTimeoutSec
	}

	headers := getMapParam(action.Params, "headers", evalCtx)
	bodyTemplate := getStringParam(action.Params, "body_template", evalCtx)

	var bodyReader io.Reader
	if bodyTemplate != "" {
		bodyReader = bytes.NewBufferString(bodyTemplate)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("send_webhook: request creation error: %w", err)
	}

	// Set default content type
	req.Header.Set("Content-Type", "application/json")

	// Set custom headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := safedial.NewClient(safedial.Options{
		AllowPrivate: e.allowPrivate,
		Timeout:      time.Duration(timeoutSec) * time.Second,
	})
	resp, err := client.Do(req)
	if err != nil {
		// A blocked address is PERMANENT, and separating it from a transient network
		// error matters twice over: retrying reaches the identical address, so the
		// retry schedule is burned for nothing, and each retry re-issues the request
		// the guard just refused.
		if safedial.IsBlockedErr(err) {
			return nil, fmt.Errorf("send_webhook: %w", err)
		}
		return nil, NewRetryableError(fmt.Errorf("send_webhook: network error: %w", err))
	}
	defer resp.Body.Close()

	// Read response body (limit to 1MB)
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var respData any
	if err := json.Unmarshal(respBody, &respData); err != nil {
		respData = string(respBody)
	}

	if resp.StatusCode >= 500 {
		return nil, NewRetryableError(fmt.Errorf("send_webhook: server error %d", resp.StatusCode))
	}
	if resp.StatusCode == 429 {
		return nil, NewRetryableError(fmt.Errorf("send_webhook: rate limited (429)"))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("send_webhook: client error %d", resp.StatusCode)
	}

	// Redacted(): the interpolated URL routinely carries a token in its query
	// string, and this line lands in the platform's log store. Same reasoning as
	// integrations/httpclient.go's redactURLError.
	slog.Info("automation: webhook sent",
		"url", req.URL.Redacted(),
		"status", resp.StatusCode,
		"workflow_run_id", run.ID.String(),
	)

	return map[string]any{
		"status_code": resp.StatusCode,
		"response":    respData,
	}, nil
}

// DelayExecutor implements the delay action (pauses execution).
type DelayExecutor struct{}

// NewDelayExecutor creates a new delay executor.
func NewDelayExecutor() *DelayExecutor {
	return &DelayExecutor{}
}

// Execute pauses for the specified duration.
func (e *DelayExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	durationSec := getIntParam(action.Params, "duration_sec")
	if durationSec <= 0 {
		durationSec = 1
	}

	// Cap at 30 days (must match validator's max of 2_592_000)
	const maxDelaySec = 2592000
	if durationSec > maxDelaySec {
		durationSec = maxDelaySec
	}

	slog.Info("automation: delay started",
		"duration_sec", durationSec,
		"workflow_run_id", run.ID.String(),
	)

	timer := time.NewTimer(time.Duration(durationSec) * time.Second)
	defer timer.Stop()

	select {
	case <-timer.C:
		return map[string]any{
			"delayed_sec": durationSec,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
