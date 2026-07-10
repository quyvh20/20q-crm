package automation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// aiGenerateMaxTokens is the hard ceiling on an ai_generate step's output. The
// per-action max_tokens can only shrink below this.
const aiGenerateMaxTokens = 1024

// aiGenerateDefaultTokens is used when the action omits max_tokens.
const aiGenerateDefaultTokens = 512

// AITextGenerator is the narrow port the ai_generate step uses — satisfied by an
// adapter over the AI gateway. It returns retryable=true for transient failures
// (model outage / 5xx) and retryable=false for permanent ones (budget exhausted),
// so the executor can requeue only what a retry could fix. Kept free of any AI
// package types so the automation package stays decoupled and the executor is
// unit-testable with a fake.
type AITextGenerator interface {
	GenerateText(ctx context.Context, orgID, userID uuid.UUID, prompt string, maxTokens int) (text string, retryable bool, err error)
}

// AIGenerateExecutor runs a bounded AI text generation and puts the result in the
// action output as {{actions.<id>.text}} for downstream steps.
type AIGenerateExecutor struct {
	gen AITextGenerator
}

func NewAIGenerateExecutor(gen AITextGenerator) *AIGenerateExecutor {
	return &AIGenerateExecutor{gen: gen}
}

func (e *AIGenerateExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	if e.gen == nil {
		return nil, fmt.Errorf("ai_generate: AI is not configured")
	}

	prompt := getStringParam(action.Params, "prompt", evalCtx)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("ai_generate: prompt is required")
	}

	maxTokens := getIntParam(action.Params, "max_tokens")
	if maxTokens <= 0 {
		maxTokens = aiGenerateDefaultTokens
	}
	if maxTokens > aiGenerateMaxTokens {
		maxTokens = aiGenerateMaxTokens
	}

	// Run under the workflow author's identity (P8) for budget attribution.
	caller, _ := domain.CallerFromContext(ctx)

	text, retryable, err := e.gen.GenerateText(ctx, run.OrgID, caller.UserID, prompt, maxTokens)
	if err != nil {
		if retryable {
			return nil, NewRetryableError(fmt.Errorf("ai_generate: %w", err))
		}
		return nil, fmt.Errorf("ai_generate: %w", err)
	}

	slog.Info("automation: ai_generate produced text",
		"chars", len(text),
		"workflow_run_id", run.ID.String(),
	)

	// {{actions.<step id>.text}} — the canonical reference downstream steps use.
	return map[string]any{"text": text}, nil
}
