package mailer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func (m *ResendMailer) SendInvite(ctx context.Context, to, inviteLink, orgName string) error {
	payload := resendPayload{
		From:    m.From,
		To:      to,
		Subject: fmt.Sprintf("You've been invited to join %s", orgName),
		HTML:    fmt.Sprintf("<p>You have been invited to join <strong>%s</strong> in the CRM.</p><p><a href='%s'>Click here to accept your invitation</a></p>", orgName, inviteLink),
	}

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
