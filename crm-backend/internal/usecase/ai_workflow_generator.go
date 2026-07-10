package usecase

import (
	"context"
	"errors"

	"crm-backend/internal/ai"

	"github.com/google/uuid"
)

// AIWorkflowGenerator adapts the AI gateway to the automation ai_generate step's
// narrow AITextGenerator port. It runs the bounded TaskWorkflowAI generation and
// classifies failures: a budget-exhausted error is permanent (a retry inside the
// same period can't succeed); anything else (model outage / 5xx / timeout) is
// transient and worth retrying.
type AIWorkflowGenerator struct {
	gateway *ai.AIGateway
}

func NewAIWorkflowGenerator(gateway *ai.AIGateway) *AIWorkflowGenerator {
	return &AIWorkflowGenerator{gateway: gateway}
}

// GenerateText satisfies automation.AITextGenerator. retryable is false for a
// permanent failure (no generator, budget exhausted) and true for a transient one.
func (a *AIWorkflowGenerator) GenerateText(ctx context.Context, orgID, userID uuid.UUID, prompt string, maxTokens int) (string, bool, error) {
	if a == nil || a.gateway == nil {
		return "", false, errors.New("AI gateway is not configured")
	}
	resp, err := a.gateway.CompleteBounded(ctx, orgID, userID, ai.TaskWorkflowAI,
		[]ai.Message{{Role: "user", Content: prompt}}, maxTokens)
	if err != nil {
		var budget ai.ErrBudgetExceeded
		if errors.As(err, &budget) {
			return "", false, err // permanent: budget won't clear on an immediate retry
		}
		return "", true, err // transient: AI outage, worth retrying
	}
	return resp.Content, false, nil
}
