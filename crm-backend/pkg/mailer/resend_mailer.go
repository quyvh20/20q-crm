package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"time"
)

type ResendMailer struct {
	APIKey string
	From   string
}

func NewResendMailer(apiKey, from string) *ResendMailer {
	return &ResendMailer{
		APIKey: apiKey,
		From:   from,
	}
}

type resendPayload struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// send POSTs one email to the Resend API. Shared by every Send* method so the
// HTTP/auth/timeout handling lives in one place.
func (m *ResendMailer) send(ctx context.Context, to, subject, htmlBody string) error {
	payload := resendPayload{From: m.From, To: to, Subject: subject, HTML: htmlBody}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend API error: status %d", resp.StatusCode)
	}

	return nil
}

func (m *ResendMailer) SendInvite(ctx context.Context, to, inviteLink, orgName, inviterName string) error {
	subject := fmt.Sprintf("You've been invited to join %s", orgName)
	intro := fmt.Sprintf("You have been invited to join <strong>%s</strong> in the CRM.", html.EscapeString(orgName))
	if inviterName != "" {
		intro = fmt.Sprintf("<strong>%s</strong> has invited you to join <strong>%s</strong> in the CRM.",
			html.EscapeString(inviterName), html.EscapeString(orgName))
	}
	body := ctaEmail("You've been invited", intro, "Accept invitation", inviteLink)
	return m.send(ctx, to, subject, body)
}

func (m *ResendMailer) SendPasswordReset(ctx context.Context, to, resetLink string) error {
	body := ctaEmail(
		"Reset your password",
		"We received a request to reset your password. Click the button below to choose a new one. This link expires in 1 hour and can be used once. If you didn't request this, you can safely ignore this email.",
		"Reset password", resetLink,
	)
	return m.send(ctx, to, "Reset your password", body)
}

func (m *ResendMailer) SendVerification(ctx context.Context, to, verifyLink string) error {
	body := ctaEmail(
		"Verify your email",
		"Confirm this is your email address to finish setting up your account. This link expires in 24 hours.",
		"Verify email", verifyLink,
	)
	return m.send(ctx, to, "Verify your email address", body)
}

func (m *ResendMailer) SendSecurityAlert(ctx context.Context, to, subject, message string) error {
	body := plainEmail(subject, message)
	return m.send(ctx, to, subject, body)
}
