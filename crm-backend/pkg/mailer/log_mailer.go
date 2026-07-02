package mailer

import (
	"context"
	"crm-backend/pkg/logger"
	"go.uber.org/zap"
)

type LogMailer struct{}

func NewLogMailer() *LogMailer {
	return &LogMailer{}
}

func (m *LogMailer) SendInvite(ctx context.Context, to, inviteLink, orgName string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Workspace_Invite"),
		zap.String("to", to),
		zap.String("org_name", orgName),
		zap.String("invite_link", inviteLink),
	)
	return nil
}

func (m *LogMailer) SendPasswordReset(ctx context.Context, to, resetLink string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Password_Reset"),
		zap.String("to", to),
		zap.String("reset_link", resetLink),
	)
	return nil
}

func (m *LogMailer) SendVerification(ctx context.Context, to, verifyLink string) error {
	logger.Log.Info("📧 [EMAIL SENT_LOG_ONLY]",
		zap.String("type", "Email_Verification"),
		zap.String("to", to),
		zap.String("verify_link", verifyLink),
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
