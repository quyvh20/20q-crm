package automation

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"crm-backend/internal/ai"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Live E2E of the copilot draft against the REAL Cloudflare AI gateway. Skipped
// unless CF creds are in the environment (same convention as gateway_test.go), so
// CI stays hermetic; run locally with the backend .env exported to verify the whole
// chain: gateway auth → qwen tool call (incl. the inline <tool_call> parse) →
// finalizeDraft normalization → validation.
func TestLive_GenerateDraft_BranchingPrompt(t *testing.T) {
	acct, gw, tok := os.Getenv("CF_ACCOUNT_ID"), os.Getenv("CF_AI_GATEWAY_ID"), os.Getenv("CF_AI_TOKEN")
	if acct == "" || gw == "" || tok == "" {
		t.Skip("CF_ACCOUNT_ID / CF_AI_GATEWAY_ID / CF_AI_TOKEN not set — skipping live draft test")
	}

	gateway := ai.NewAIGateway(acct, gw, tok, nil, zap.NewNop(), os.Getenv("CF_AI_GATEWAY_TOKEN"))
	orgID := uuid.New()
	h := &Handler{schemaCache: NewSchemaCache(time.Minute), draftAI: gateway}
	h.schemaCache.Set(orgID, &SchemaResponse{
		Entities: []SchemaEntity{
			{Key: "deal", Label: "Deal", Fields: []SchemaField{
				{Path: "deal.value", Type: "number"}, {Path: "deal.stage_id", Type: "string"},
			}},
			{Key: "contact", Label: "Contact", Fields: []SchemaField{
				{Path: "contact.email", Type: "string"}, {Path: "contact.first_name", Type: "string"},
			}},
		},
		Stages: []SchemaStage{{ID: "stage_negotiation", Name: "Negotiation"}, {ID: "stage_won", Name: "Won"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), draftTimeout)
	defer cancel()

	start := time.Now()
	draft, validation, err := h.generateDraft(ctx, orgID, uuid.New(),
		"When a deal moves to the Negotiation stage, wait 3 days, then if the deal's value is over 10000, notify the deal owner and create a high-priority follow-up task; otherwise send the contact a friendly check-in email.", nil)
	elapsed := time.Since(start)

	require.NoError(t, err, "live draft failed after %s", elapsed)
	require.NotNil(t, draft)
	t.Logf("draft completed in %s — name=%q", elapsed, draft.Name)
	t.Logf("trigger: %s", string(draft.Trigger))
	t.Logf("steps: %s", string(draft.Steps))
	if validation != nil {
		t.Logf("validation: valid=%v errors=%+v", validation.Valid, validation.Errors)
	}

	// The load-bearing shape: it must produce a CONDITION step (the offline fallback
	// can't), proving a real model draft with branching came through end-to-end.
	var steps []StepSpec
	require.NoError(t, json.Unmarshal(draft.Steps, &steps))
	hasCondition := false
	for _, s := range steps {
		if s.Type == "condition" {
			hasCondition = true
		}
	}
	require.True(t, hasCondition, "a branching prompt must yield a condition step; got: %s", string(draft.Steps))
	require.Less(t, elapsed, draftTimeout, "must finish inside the draft deadline")
}
