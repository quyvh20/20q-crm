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
