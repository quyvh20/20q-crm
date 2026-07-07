package mailer

import (
	"context"
	"strings"

	"crm-backend/pkg/logger"
	"go.uber.org/zap"
)

type LogMailer struct{}

func NewLogMailer() *LogMailer {
	return &LogMailer{}
}

// redactLink strips the secret from an action link before logging: LogMailer
// output lands in durable application logs, and a raw reset/invite/verify token
// in logs is a working credential for anyone who can read them (P10 P1). A
// short prefix survives for correlation; the sanctioned way to grab a full
// link locally is the debug_token response under APP_ENV=development.
func redactLink(link string) string {
	i := strings.Index(link, "token=")
	if i < 0 {
		return link
	}
	token := link[i+len("token="):]
	if len(token) > 8 {
		token = token[:8]
	}
	return link[:i] + "token=" + token + "…[redacted]"
}

func (m *LogMailer) SendInvite(ctx context.Context, to, inviteLink, orgName string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Workspace_Invite"),
		zap.String("to", to),
		zap.String("org_name", orgName),
		zap.String("invite_link", redactLink(inviteLink)),
	)
	return nil
}

func (m *LogMailer) SendPasswordReset(ctx context.Context, to, resetLink string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Password_Reset"),
		zap.String("to", to),
		zap.String("reset_link", redactLink(resetLink)),
	)
	return nil
}

func (m *LogMailer) SendVerification(ctx context.Context, to, verifyLink string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Email_Verification"),
		zap.String("to", to),
		zap.String("verify_link", redactLink(verifyLink)),
	)
	return nil
}

func (m *LogMailer) SendSecurityAlert(ctx context.Context, to, subject, message string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Security_Alert"),
		zap.String("to", to),
		zap.String("subject", subject),
		zap.String("message", message),
	)
	return nil
}
