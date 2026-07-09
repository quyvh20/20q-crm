package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// resendBaseURL is the default Resend API base. It is a struct field (not a const)
// so tests can point the executor at an httptest server.
const resendBaseURL = "https://api.resend.com"

// EmailExecutor sends emails via the Resend API.
type EmailExecutor struct {
	apiKey    string
	fromEmail string
	// baseURL overrides the Resend host (tests point this at an httptest server).
	// Empty falls back to resendBaseURL.
	baseURL string
	// templates loads library email templates for the template_id path (A5). nil
	// disables template_id (a template_id then fails permanently with a clear error).
	templates *EmailTemplateRepository
}

// NewEmailExecutor creates a new email executor. db is used to load library
// templates for the send_email template_id path (A5); pass nil to disable it.
func NewEmailExecutor(db *gorm.DB, apiKey, fromEmail string) *EmailExecutor {
	var templates *EmailTemplateRepository
	if db != nil {
		templates = NewEmailTemplateRepository(db)
	}
	return &EmailExecutor{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		baseURL:   resendBaseURL,
		templates: templates,
	}
}

type resendEmailPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	Cc      []string `json:"cc,omitempty"`
}

// Execute sends an email using Resend. Subject/body come from inline params or,
// when template_id is set (A5), from a library template — inline params override
// the template when non-empty. All strings are interpolated with the run's
// EvalContext via the shared InterpolateTemplate primitive.
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

	// A5: a library template supplies subject/body when template_id is set. Inline
	// subject/body_html still win when non-empty (spec: "inline subject/body keeps
	// working"). template_id itself is an id, not a template string — read it raw.
	if templateID, _ := action.Params["template_id"].(string); templateID != "" {
		if e.templates == nil {
			return nil, fmt.Errorf("send_email: template_id set but no template store is configured")
		}
		id, err := uuid.Parse(templateID)
		if err != nil {
			// Malformed id can never resolve — permanent failure.
			return nil, fmt.Errorf("send_email: invalid template_id '%s': %w", templateID, err)
		}
		tmpl, err := e.templates.Get(ctx, run.OrgID, id)
		if err != nil {
			// DB error is transient — let the engine retry.
			return nil, NewRetryableError(fmt.Errorf("send_email: load template: %w", err))
		}
		if tmpl == nil {
			// Not found or soft-deleted: a plain error = permanent failure (no retry).
			return nil, fmt.Errorf("send_email: email template %s not found or has been deleted", templateID)
		}
		if subject == "" {
			subject = InterpolateTemplate(tmpl.Subject, evalCtx)
		}
		if bodyHTML == "" {
			bodyHTML = InterpolateTemplate(tmpl.BodyHTML, evalCtx)
		}
	}

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

	return e.sendEmail(ctx, run.ID.String(), to, subject, bodyHTML, fromName, cc)
}

// sendEmail performs the actual Resend POST for already-resolved fields. It is
// shared by the workflow send_email action and the email-template test-send
// (Engine.SendTestEmail). logID labels the log lines (a run id, or "test-send").
func (e *EmailExecutor) sendEmail(ctx context.Context, logID, to, subject, bodyHTML, fromName string, cc []string) (any, error) {
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
		"log_id", logID,
		"to", payload.To,
		"cc", payload.Cc,
		"subject", payload.Subject,
		"from", payload.From,
	)

	base := e.baseURL
	if base == "" {
		base = resendBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/emails", bytes.NewReader(body))
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
		"log_id", logID,
		"status", resp.StatusCode,
		"resend_response", respBody,
	)

	return map[string]any{
		"status":      "sent",
		"status_code": resp.StatusCode,
		"response":    respBody,
	}, nil
}
