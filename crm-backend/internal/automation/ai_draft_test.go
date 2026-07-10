package automation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"crm-backend/internal/ai"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeDraftAI returns canned tool-call responses in sequence.
type fakeDraftAI struct {
	responses     []ai.AIResponse
	calls         int
	firstMessages []ai.Message // messages passed on the first CompleteWithTools call
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
