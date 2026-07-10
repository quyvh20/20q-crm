package automation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"crm-backend/internal/ai"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeDraftAI returns canned tool-call responses in sequence.
type fakeDraftAI struct {
	responses     []ai.AIResponse
	calls         int
	firstMessages []ai.Message // messages passed on the first CompleteWithTools call
	// Complete() (health probe) canned result.
	completeResp ai.AIResponse
	completeErr  error
}

func (f *fakeDraftAI) CompleteWithTools(_ context.Context, _, _ uuid.UUID, _ ai.AITask, msgs []ai.Message, _ []ai.Tool) (ai.AIResponse, error) {
	if f.firstMessages == nil {
		f.firstMessages = msgs
	}
	if f.calls >= len(f.responses) {
		return ai.AIResponse{}, nil // no tool calls → loop ends
	}
	r := f.responses[f.calls]
	f.calls++
	return r, nil
}

func (f *fakeDraftAI) Complete(_ context.Context, _, _ uuid.UUID, _ ai.AITask, _ []ai.Message) (ai.AIResponse, error) {
	return f.completeResp, f.completeErr
}

// ── Health probe (GET /api/workflows/ai/health) ──────────────────────

func TestProbeDraftAI_NotConfigured(t *testing.T) {
	h := &Handler{} // draftAI nil → not wired at boot
	res := h.probeDraftAI(context.Background(), uuid.New(), uuid.New())
	require.False(t, res.OK)
	require.False(t, res.Configured)
	require.Contains(t, res.Detail, "not configured")
}

func TestProbeDraftAI_Healthy(t *testing.T) {
	fake := &fakeDraftAI{completeResp: ai.AIResponse{Content: "ok", Model: "@cf/qwen/qwen3-30b-a3b-fp8"}}
	h := &Handler{draftAI: fake}
	res := h.probeDraftAI(context.Background(), uuid.New(), uuid.New())
	require.True(t, res.OK)
	require.True(t, res.Configured)
	require.Equal(t, "@cf/qwen/qwen3-30b-a3b-fp8", res.Model)
	require.Empty(t, res.Detail)
}

func TestProbeDraftAI_Unreachable_SanitizesDetail(t *testing.T) {
	// A transport failure surfaces as a *url.Error whose text embeds the full gateway
	// URL (CF account + gateway ids). The probe must NOT echo that to the client.
	leaky := errors.New(`AI service unavailable: Post "https://gateway.ai.cloudflare.com/v1/ACCT_SECRET/GW_SECRET/workers-ai/v1/chat/completions": dial tcp: connection refused`)
	h := &Handler{draftAI: &fakeDraftAI{completeErr: leaky}}
	res := h.probeDraftAI(context.Background(), uuid.New(), uuid.New())
	require.False(t, res.OK)
	require.True(t, res.Configured) // it IS wired — it just couldn't reach the model
	require.NotEmpty(t, res.Detail, "still gives a usable hint")
	require.NotContains(t, res.Detail, "ACCT_SECRET", "must not leak the CF account id")
	require.NotContains(t, res.Detail, "gateway.ai.cloudflare.com", "must not leak the gateway URL")
}

func TestProbeDraftAI_Timeout(t *testing.T) {
	h := &Handler{draftAI: &fakeDraftAI{completeErr: context.DeadlineExceeded}}
	res := h.probeDraftAI(context.Background(), uuid.New(), uuid.New())
	require.False(t, res.OK)
	require.Contains(t, res.Detail, "timed out")
}

// doDraftHealth exercises the real gin handler so the 200-vs-503 status mapping and
// the {"data":{…}} envelope are covered (not just probeDraftAI in isolation).
func doDraftHealth(t *testing.T, h *Handler) (int, draftHealthResult) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("org_id", uuid.New())
	c.Set("user_id", uuid.New())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/workflows/ai/health", nil)
	h.DraftHealth(c)
	var body struct {
		Data draftHealthResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return w.Code, body.Data
}

func TestDraftHealth_Healthy_200(t *testing.T) {
	h := &Handler{draftAI: &fakeDraftAI{completeResp: ai.AIResponse{Model: "@cf/qwen/qwen3-30b-a3b-fp8"}}}
	code, body := doDraftHealth(t, h)
	require.Equal(t, http.StatusOK, code)
	require.True(t, body.OK)
	require.Equal(t, "@cf/qwen/qwen3-30b-a3b-fp8", body.Model)
}

func TestDraftHealth_Unreachable_503(t *testing.T) {
	h := &Handler{draftAI: &fakeDraftAI{completeErr: errors.New("boom")}}
	code, body := doDraftHealth(t, h)
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.False(t, body.OK)
	require.True(t, body.Configured)
}

func TestDraftHealth_NotConfigured_503(t *testing.T) {
	h := &Handler{} // draftAI nil → not wired
	code, body := doDraftHealth(t, h)
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.False(t, body.OK)
	require.False(t, body.Configured)
}

func toolCall(id, name, params string) ai.ToolCall {
	return ai.ToolCall{ID: id, Name: name, Params: json.RawMessage(params)}
}

// handlerWithSchema builds a bare Handler whose schema cache is pre-populated, so
// generateDraft resolves the schema without a DB.
func handlerWithSchema(orgID uuid.UUID, ai draftAICaller) *Handler {
	h := &Handler{schemaCache: NewSchemaCache(time.Minute), draftAI: ai}
	h.schemaCache.Set(orgID, &SchemaResponse{
		Entities: []SchemaEntity{{Key: "contact", Label: "Contact", Fields: []SchemaField{{Path: "contact.email", Type: "string"}}}},
	})
	return h
}

const validDraftArgs = `{"name":"Welcome email","trigger":{"type":"contact_created"},"steps":[{"type":"action","action":{"type":"send_email","params":{"to":"{{contact.email}}","subject":"Welcome","body_html":"Hi {{contact.first_name}}"}}}]}`

func TestGenerateDraft_SchemaThenDraft(t *testing.T) {
	orgID := uuid.New()
	fake := &fakeDraftAI{responses: []ai.AIResponse{
		{ToolCalls: []ai.ToolCall{toolCall("1", "get_workflow_schema", "{}")}},
		{ToolCalls: []ai.ToolCall{toolCall("2", "draft_workflow", validDraftArgs)}},
	}}
	h := handlerWithSchema(orgID, fake)

	draft, validation, err := h.generateDraft(context.Background(), orgID, uuid.New(), "email new contacts", nil)
	require.NoError(t, err)
	require.NotNil(t, draft)
	require.Equal(t, "Welcome email", draft.Name)
	require.Equal(t, 2, fake.calls, "should fetch schema then draft")
	require.True(t, validation.Valid, "a well-formed draft validates: %+v", validation.Errors)

	// Steps were id-normalized so the draft is directly applicable.
	var steps []StepSpec
	require.NoError(t, json.Unmarshal(draft.Steps, &steps))
	require.Len(t, steps, 1)
	require.NotEmpty(t, steps[0].ID)
	require.Equal(t, steps[0].ID, steps[0].Action.ID, "action id mirrors step id")
}

func TestGenerateDraft_DirectDraft(t *testing.T) {
	orgID := uuid.New()
	fake := &fakeDraftAI{responses: []ai.AIResponse{
		{ToolCalls: []ai.ToolCall{toolCall("1", "draft_workflow", validDraftArgs)}},
	}}
	h := handlerWithSchema(orgID, fake)

	draft, _, err := h.generateDraft(context.Background(), orgID, uuid.New(), "email new contacts", nil)
	require.NoError(t, err)
	require.Equal(t, "Welcome email", draft.Name)
	require.Equal(t, 1, fake.calls)
}

// A7.4: when a current workflow is supplied (update handoff / in-builder edit), the
// model is prompted to EDIT it rather than draft from scratch.
func TestGenerateDraft_EditContextThreaded(t *testing.T) {
	orgID := uuid.New()
	fake := &fakeDraftAI{responses: []ai.AIResponse{
		{ToolCalls: []ai.ToolCall{toolCall("1", "draft_workflow", validDraftArgs)}},
	}}
	h := handlerWithSchema(orgID, fake)

	current := json.RawMessage(`{"name":"Onboarding","trigger":{"type":"contact_created"},"steps":[{"type":"action","action":{"type":"send_email"}}]}`)
	_, _, err := h.generateDraft(context.Background(), orgID, uuid.New(), "also wait 2 days before emailing", current)
	require.NoError(t, err)

	// The user message must carry the current workflow + an edit instruction.
	require.NotEmpty(t, fake.firstMessages)
	userMsg := fake.firstMessages[len(fake.firstMessages)-1].Content
	require.Contains(t, userMsg, "EDITING")
	require.Contains(t, userMsg, "Onboarding")
	require.Contains(t, userMsg, "also wait 2 days before emailing")
}

// A blank/null current workflow drafts from scratch (no edit framing).
func TestGenerateDraft_NoCurrentDraftsFromScratch(t *testing.T) {
	orgID := uuid.New()
	fake := &fakeDraftAI{responses: []ai.AIResponse{
		{ToolCalls: []ai.ToolCall{toolCall("1", "draft_workflow", validDraftArgs)}},
	}}
	h := handlerWithSchema(orgID, fake)

	_, _, err := h.generateDraft(context.Background(), orgID, uuid.New(), "email new contacts", json.RawMessage("null"))
	require.NoError(t, err)
	userMsg := fake.firstMessages[len(fake.firstMessages)-1].Content
	require.Equal(t, "email new contacts", userMsg, "null current ⇒ raw prompt, no edit framing")
}

func TestGenerateDraft_NoToolCallsIsError(t *testing.T) {
	orgID := uuid.New()
	fake := &fakeDraftAI{responses: []ai.AIResponse{{Content: "I can't help with that."}}}
	h := handlerWithSchema(orgID, fake)

	_, _, err := h.generateDraft(context.Background(), orgID, uuid.New(), "hi", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "can't help")
}

func TestFinalizeDraft_MalformedJSON(t *testing.T) {
	_, _, err := finalizeDraft(json.RawMessage(`{not json`))
	require.Error(t, err)
}

func TestFinalizeDraft_MissingTrigger(t *testing.T) {
	_, _, err := finalizeDraft(json.RawMessage(`{"name":"x","steps":[]}`))
	require.Error(t, err)
}

func TestFinalizeDraft_DefaultsBlankName(t *testing.T) {
	draft, _, err := finalizeDraft(json.RawMessage(`{"trigger":{"type":"contact_created"},"steps":[]}`))
	require.NoError(t, err)
	require.Equal(t, "Untitled workflow", draft.Name)
}

func TestNormalizeStepIDs_AssignsUniqueIDsIncludingBranches(t *testing.T) {
	steps := []StepSpec{
		{Type: "condition", Condition: &ConditionGroup{Op: "AND"},
			YesSteps: []StepSpec{{Type: "action", Action: &ActionSpec{Type: "send_email"}}},
			NoSteps:  []StepSpec{{Type: "action", Action: &ActionSpec{Type: "create_task"}}},
		},
	}
	counter := 0
	normalizeStepIDs(steps, &counter)

	seen := map[string]bool{}
	var walk func(ss []StepSpec)
	walk = func(ss []StepSpec) {
		for _, s := range ss {
			require.NotEmpty(t, s.ID)
			require.False(t, seen[s.ID], "duplicate id %s", s.ID)
			seen[s.ID] = true
			if s.Type == "action" {
				require.Equal(t, s.ID, s.Action.ID)
			}
			walk(s.YesSteps)
			walk(s.NoSteps)
		}
	}
	walk(steps)
	require.Len(t, seen, 3, "one condition + two branch actions")
}
