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

// EmailExecutor sends emails via the Resend API.
type EmailExecutor struct {
	apiKey  string
	fromEmail string
}

// NewEmailExecutor creates a new email executor.
func NewEmailExecutor(apiKey, fromEmail string) *EmailExecutor {
	return &EmailExecutor{
		apiKey:  apiKey,
		fromEmail: fromEmail,
	}
}

type resendEmailPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Cc      []string `json:"cc,omitempty"`
}

// Execute sends an email using Resend.
func (e *EmailExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
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

	slog.Info("automation: sending email",
		"workflow_run_id", run.ID.String(),
		"to", payload.To,
		"cc", payload.Cc,
		"subject", payload.Subject,
		"from", payload.From,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
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

	slog.Info("automation: email sent",
		"workflow_run_id", run.ID.String(),
		"status", resp.StatusCode,
		"resend_response", respBody,
	)

	return map[string]any{
		"status":     "sent",
		"status_code": resp.StatusCode,
		"response":   respBody,
	}, nil
}
