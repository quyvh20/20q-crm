package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// testableEmailExecutor is a test variant of EmailExecutor with a configurable base URL.
// This allows tests to point it at httptest.NewServer instead of the real Resend API.
type testableEmailExecutor struct {
	apiKey    string
	fromEmail string
	baseURL   string // override for testing (default: https://api.resend.com)
}

func (e *testableEmailExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	to := getStringParam(action.Params, "to", evalCtx)
	if to == "" {
		return nil, fmt.Errorf("send_email: 'to' is required")
	}

	// Runtime validation: 'to' must be a valid email after template resolution
	if !isValidEmail(to) {
		return nil, fmt.Errorf("send_email: resolved 'to' address is not a valid email: '%s'", to)
	}

	subject := getStringParam(action.Params, "subject", evalCtx)
	bodyHTML := getStringParam(action.Params, "body_html", evalCtx)
	fromName := getStringParam(action.Params, "from_name", evalCtx)
	cc := getStringSliceParam(action.Params, "cc", evalCtx)

	// Runtime validation: filter invalid resolved CC addresses
	if len(cc) > 0 {
		validCC := make([]string, 0, len(cc))
		for _, addr := range cc {
			if isValidEmail(addr) {
				validCC = append(validCC, addr)
			} else {
				slog.Warn("automation: dropping invalid CC address after template resolution",
					"workflow_run_id", run.ID.String(),
					"invalid_address", addr,
				)
			}
		}
		if len(validCC) == 0 {
			cc = nil
		} else {
			cc = validCC
		}
	}

	from := e.fromEmail
	if fromName != "" {
		from = fmt.Sprintf("%s <%s>", fromName, e.fromEmail)
	}

	payload := resendEmailPayload{
		From:    from,
		To:      []string{to},
		Subject: subject,
		HTML:    bodyHTML,
		Cc:      cc,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("send_email: marshal error: %w", err)
	}

	url := e.baseURL + "/emails"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("send_email: request creation error: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, NewRetryableError(fmt.Errorf("send_email: network error: %w", err))
	}
	defer resp.Body.Close()

	var respBody map[string]any
	json.NewDecoder(resp.Body).Decode(&respBody)

	if resp.StatusCode >= 500 {
		return nil, NewRetryableError(fmt.Errorf("send_email: server error %d", resp.StatusCode))
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("send_email: client error %d: %v", resp.StatusCode, respBody)
	}

	slog.Info("automation: email sent (test)",
		"workflow_run_id", run.ID.String(),
		"status", resp.StatusCode,
	)

	return map[string]any{
		"status":      "sent",
		"status_code": resp.StatusCode,
		"response":    respBody,
	}, nil
}
