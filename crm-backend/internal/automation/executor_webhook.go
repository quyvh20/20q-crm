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
)

// WebhookExecutor sends HTTP webhook requests.
type WebhookExecutor struct{}

// NewWebhookExecutor creates a new webhook executor.
func NewWebhookExecutor() *WebhookExecutor {
	return &WebhookExecutor{}
}

// Execute sends an outbound webhook.
func (e *WebhookExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	url := getStringParam(action.Params, "url", evalCtx)
	if url == "" {
		return nil, fmt.Errorf("send_webhook: 'url' is required")
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

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
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

	slog.Info("automation: webhook sent",
		"url", url,
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
