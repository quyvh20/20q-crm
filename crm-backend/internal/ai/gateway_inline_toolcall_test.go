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
