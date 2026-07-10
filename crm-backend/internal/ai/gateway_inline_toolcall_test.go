package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Reasoning models on Workers AI (qwen3) skip the structured tool_calls array and
// emit "<tool_call>{...}</tool_call>" inline in content. These lock in the parser
// that rescues those calls — without it every copilot draft on qwen read as prose
// and failed over to the client's offline fallback.

func draftToolsForTest() []Tool {
	return []Tool{
		{Name: "get_workflow_schema", Desc: "schema", Params: map[string]any{
			"type": "object", "properties": map[string]any{}, "required": []string{},
		}},
		{Name: "draft_workflow", Desc: "draft", Params: map[string]any{
			"type": "object", "properties": map[string]any{}, "required": []string{"name", "trigger", "steps"},
		}},
	}
}

func TestParseInlineToolCalls_WrapperShape(t *testing.T) {
	content := "\n<tool_call>\n{\"name\": \"draft_workflow\", \"arguments\": {\"name\": \"WF\", \"trigger\": {\"type\": \"contact_created\"}, \"steps\": []}}\n</tool_call>"
	calls, remaining := parseInlineToolCalls(content, draftToolsForTest())
	require.Len(t, calls, 1)
	require.Equal(t, "draft_workflow", calls[0].Name)
	require.Empty(t, remaining)
	var args map[string]any
	require.NoError(t, json.Unmarshal(calls[0].Params, &args))
	require.Equal(t, "WF", args["name"])
}

// The exact shape observed live from @cf/qwen/qwen3-30b-a3b-fp8: NO wrapper — the
// tool arguments directly, where "name" is the WORKFLOW's name (not a tool name).
// The parser must not mistake it for a wrapper, and must infer draft_workflow from
// the required params (name/trigger/steps all present).
func TestParseInlineToolCalls_BareArgsShape_ObservedQwen(t *testing.T) {
	content := "\n\n<tool_call>\n{\"name\": \"Won Deal Notification\", \"trigger\": {\"type\": \"deal_stage_changed\", \"params\": {\"to_stage\": \"Won\"}}, \"steps\": [{\"type\": \"delay\", \"delay\": {\"duration_sec\": 259200}}]}\n</tool_call>"
	calls, remaining := parseInlineToolCalls(content, draftToolsForTest())
	require.Len(t, calls, 1)
	require.Equal(t, "draft_workflow", calls[0].Name, "inferred from required params, not the colliding 'name' field")
	require.Empty(t, remaining)
	var args map[string]any
	require.NoError(t, json.Unmarshal(calls[0].Params, &args))
	require.Equal(t, "Won Deal Notification", args["name"], "the workflow name stays an argument")
	require.NotNil(t, args["trigger"])
}

// The exact content the prod draft endpoint failed on (2026-07-10, qwen3-30b): a
// wrapper-shape inline call whose JSON is bracket-miscounted — the model closed the
// "steps" array with "}" instead of "]}" (ending "...]}}}", one "]" short, one "}"
// long). Strict parsing rejects it; the repairer must save it.
const bracketMiscountedProdPayload = "<tool_call>\n{\"name\": \"draft_workflow\", \"arguments\": {\"name\": \"Negotiation Stage Handling\", \"trigger\": {\"type\": \"deal_stage_changed\", \"params\": {\"to_stage\": \"26c73c81-f038-4b62-9a85-4916e8335801\"}}, \"steps\": [{\"type\": \"delay\", \"delay\": {\"duration_sec\": 259200}}, {\"type\": \"condition\", \"condition\": {\"op\": \"AND\", \"rules\": [{\"field\": \"deal.value\", \"operator\": \"gt\", \"value\": 10000}]}, \"yes_steps\": [{\"type\": \"action\", \"action\": {\"type\": \"notify_user\", \"params\": {\"recipient\": \"owner_field\", \"title\": \"High Value Deal in Negotiation: {{deal.title}}\", \"body\": \"The deal {{deal.title}} has moved to Negotiation and is over $10,000.\"}}}, {\"type\": \"action\", \"action\": {\"type\": \"create_task\", \"params\": {\"title\": \"Follow-up on Negotiation\", \"priority\": \"high\", \"due_in_days\": 0, \"assignee_field\": \"deal.owner_user_id\"}}}], \"no_steps\": [{\"type\": \"action\", \"action\": {\"type\": \"send_email\", \"params\": {\"to\": \"{{contact.email}}\", \"subject\": \"Check-in on Your Negotiation\", \"body_html\": \"Hi {{contact.first_name}}, just checking in on the negotiation. Let me know if you need anything!\"}}}]}}}\n</tool_call>"

func TestParseInlineToolCalls_RepairsBracketMiscount_ObservedProd(t *testing.T) {
	calls, remaining := parseInlineToolCalls(bracketMiscountedProdPayload, draftToolsForTest())
	require.Len(t, calls, 1)
	require.Equal(t, "draft_workflow", calls[0].Name)
	require.Empty(t, remaining)

	var args struct {
		Name    string `json:"name"`
		Trigger struct {
			Type string `json:"type"`
		} `json:"trigger"`
		Steps []map[string]json.RawMessage `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(calls[0].Params, &args))
	require.Equal(t, "Negotiation Stage Handling", args.Name)
	require.Equal(t, "deal_stage_changed", args.Trigger.Type)
	require.Len(t, args.Steps, 2, "the repaired steps array keeps both the delay and the condition")
}

func TestRepairJSONBrackets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"valid stays valid", `{"a": [1, 2]}`, `{"a": [1, 2]}`},
		{"array closed with brace", `{"a": [1, 2}}`, `{"a": [1, 2]}`},
		{"object closed with bracket", `{"a": {"b": 1]}`, `{"a": {"b": 1}}`},
		{"missing closers at end", `{"a": [1, {"b": 2`, `{"a": [1, {"b": 2}]}`},
		{"extra trailing closer dropped", `{"a": 1}}`, `{"a": 1}`},
		{"brackets inside strings untouched", `{"a": "}]{[", "b": 1}`, `{"a": "}]{[", "b": 1}`},
		{"escaped quote inside string", `{"a": "say \"}\" ok"}`, `{"a": "say \"}\" ok"}`},
		{"truncated mid-string", `{"a": "cut of`, `{"a": "cut of"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repairJSONBrackets(tc.in)
			require.Equal(t, tc.want, got)
			require.True(t, json.Valid([]byte(got)), "repaired output must be valid JSON")
		})
	}
}

// A block whose closing </tool_call> never arrived (truncated output or a forgotten
// tag) must still be rescued — the repairer closes what the model left open.
func TestParseInlineToolCalls_UnclosedBlock(t *testing.T) {
	content := "<tool_call>\n{\"name\": \"draft_workflow\", \"arguments\": {\"name\": \"WF\", \"trigger\": {\"type\": \"contact_created\"}, \"steps\": []}}"
	calls, remaining := parseInlineToolCalls(content, draftToolsForTest())
	require.Len(t, calls, 1)
	require.Equal(t, "draft_workflow", calls[0].Name)
	require.Empty(t, remaining)
	var args map[string]any
	require.NoError(t, json.Unmarshal(calls[0].Params, &args))
	require.Equal(t, "WF", args["name"])
}

func TestParseInlineToolCalls_NoBlocks(t *testing.T) {
	calls, remaining := parseInlineToolCalls("just prose, no tool call", draftToolsForTest())
	require.Nil(t, calls)
	require.Equal(t, "just prose, no tool call", remaining)
}

func TestParseInlineToolCalls_InvalidJSONSkipped(t *testing.T) {
	calls, remaining := parseInlineToolCalls("<tool_call>{not json}</tool_call>", draftToolsForTest())
	require.Nil(t, calls)
	require.Equal(t, "<tool_call>{not json}</tool_call>", remaining)
}

func TestParseInlineToolCalls_SingleToolFallback(t *testing.T) {
	tools := []Tool{{Name: "only_tool", Params: map[string]any{"required": []string{}}}}
	calls, _ := parseInlineToolCalls(`<tool_call>{"whatever": 1}</tool_call>`, tools)
	require.Len(t, calls, 1)
	require.Equal(t, "only_tool", calls[0].Name)
}

// End-to-end through callCFWorkersWithTools with the REAL response body shape the
// live gateway returned for qwen (content-inline tool call, empty tool_calls array).
func TestCompleteWithTools_ParsesQwenInlineToolCall(t *testing.T) {
	inline := `\n\n<tool_call>\n{\"name\": \"My WF\", \"trigger\": {\"type\": \"contact_created\"}, \"steps\": []}\n</tool_call>`
	body := fmt.Sprintf(`{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"%s","role":"assistant","tool_calls":[]}}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`, inline)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	g := &AIGateway{gatewayURL: srv.URL, cfToken: "t", httpClient: srv.Client(), logger: zap.NewNop()}
	resp, err := g.CompleteWithTools(context.Background(), uuid.New(), uuid.New(), TaskWorkflowDraft, []Message{{Role: "user", Content: "draft it"}}, draftToolsForTest())
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "draft_workflow", resp.ToolCalls[0].Name)
	require.Empty(t, resp.Content, "tool_call block stripped from content")
}

// Same rescue through the LEGACY Workers AI response format ({"result":{"response":
// "<tool_call>..."}}). Before the fix only the OpenAI-format branch parsed inline
// blocks — a legacy-shaped response silently returned the call as prose.
func TestCompleteWithTools_ParsesInlineToolCall_LegacyFormat(t *testing.T) {
	inline := `<tool_call>{\"name\": \"draft_workflow\", \"arguments\": {\"name\": \"WF\", \"trigger\": {\"type\": \"contact_created\"}, \"steps\": []}}</tool_call>`
	body := fmt.Sprintf(`{"result":{"response":"%s","tool_calls":null,"usage":{"prompt_tokens":10,"completion_tokens":20}}}`, inline)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	g := &AIGateway{gatewayURL: srv.URL, cfToken: "t", httpClient: srv.Client(), logger: zap.NewNop()}
	resp, err := g.CompleteWithTools(context.Background(), uuid.New(), uuid.New(), TaskWorkflowDraft, []Message{{Role: "user", Content: "draft it"}}, draftToolsForTest())
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "draft_workflow", resp.ToolCalls[0].Name)
	require.Empty(t, resp.Content, "tool_call block stripped from content")
}
