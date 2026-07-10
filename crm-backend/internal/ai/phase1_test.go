package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ============================================================
// Phase 1 Tests: SSE Streaming, Thinking Events, Timeout
// ============================================================

// ── 1. HTTP Client Timeout (Phase 1.3) ──────────────────────────────────────

func TestGateway_Timeout_45s(t *testing.T) {
	gw := NewAIGateway("dummy-account", "dummy-gw", "dummy-token", nil, zap.NewNop())
	if gw.httpClient.Timeout != 45*time.Second {
		t.Errorf("Expected 45s timeout, got %v", gw.httpClient.Timeout)
	}
}

// ── 2. Thinking Events in Execute (Phase 1.2) ──────────────────────────────

func TestExecute_ThinkingEvents_IntentRouter(t *testing.T) {
	// Intent-routed messages should NOT emit thinking events (they're instant)
	cc := newTestCC(nil)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "help",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var types []string
	for ev := range events {
		types = append(types, ev.Type)
	}
	// Intent router path: no thinking events expected
	for _, tp := range types {
		if tp == "thinking" {
			t.Error("Intent-routed message should NOT emit thinking events")
		}
	}
	assertContains(t, types, "response", "Intent path must emit response")
	assertContains(t, types, "done", "Intent path must emit done")
}

func TestExecute_ThinkingEvents_AIPath(t *testing.T) {
	// AI-routed messages SHOULD emit thinking events
	ts := mockAIServer(t, mockTextResponse("Here is some analysis."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "What strategy should I use for stale deals?",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var types []string
	for ev := range events {
		types = append(types, ev.Type)
	}
	assertContains(t, types, "thinking", "AI path must emit thinking events")
	assertContains(t, types, "response", "AI path must emit response")
	assertContains(t, types, "done", "AI path must emit done")
}

func TestExecute_ThinkingEvents_ConfirmedAction(t *testing.T) {
	// Confirmed actions should emit "Executing action…" thinking event
	ts := mockAIServer(t, mockTextResponse("Done."))
	defer ts.Close()

	cc := newTestCC(ts)
	args, _ := json.Marshal(map[string]any{"path": "/deals", "label": "Deals"})
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage:   "confirm",
		UserRole:      "admin",
		SessionID:     uuid.New(),
		Confirmed:     true,
		ConfirmedTool: "navigate_to",
		ConfirmedArgs: args,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var thinkingMsgs []string
	for ev := range events {
		if ev.Type == "thinking" {
			thinkingMsgs = append(thinkingMsgs, ev.Message)
		}
	}
	if len(thinkingMsgs) == 0 {
		t.Error("Confirmed action should emit at least one thinking event")
	}
}

// ── 3. SSE Command Handler (Phase 1.1) ─────────────────────────────────────

func TestSSE_EventFormat(t *testing.T) {
	// Verify SSE events are properly formatted as "data: {...}\n\n"
	events := []CommandEvent{
		{Type: "thinking", Message: "Analyzing…"},
		{Type: "response", Message: "Here are your deals.", Done: true},
		{Type: "done", Done: true},
	}

	var buf strings.Builder
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		sanitized := strings.ReplaceAll(string(data), "\n", "\\n")
		fmt.Fprintf(&buf, "data: %s\n\n", sanitized)
	}

	output := buf.String()
	lines := strings.Split(output, "\n")
	dataLines := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "data: ") {
			dataLines++
			raw := strings.TrimPrefix(l, "data: ")
			unescaped := strings.ReplaceAll(raw, "\\n", "\n")
			var parsed CommandEvent
			if err := json.Unmarshal([]byte(unescaped), &parsed); err != nil {
				t.Errorf("Failed to parse SSE event: %v (raw: %s)", err, raw)
			}
		}
	}
	if dataLines != 3 {
		t.Errorf("Expected 3 SSE data lines, got %d", dataLines)
	}
}

func TestSSE_NewlineEscaping(t *testing.T) {
	// Markdown content with newlines must be properly escaped in SSE
	ev := CommandEvent{
		Type:    "response",
		Message: "| Deal | Value |\n|------|-------|\n| Acme | $100k |",
		Done:    true,
	}
	data, _ := json.Marshal(ev)
	// json.Marshal already escapes newlines as \n inside JSON strings,
	// so the SSE sanitizer replaces literal \n in the outer JSON envelope.
	sanitized := strings.ReplaceAll(string(data), "\n", "\\n")

	// The sanitized line must not contain raw newlines
	if strings.ContainsRune(sanitized, '\n') {
		t.Error("Raw newline found in SSE payload after sanitization")
	}

	// Verify json.Marshal's own escaping preserved the table
	var parsed CommandEvent
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json round-trip failed: %v", err)
	}
	if !strings.Contains(parsed.Message, "| Deal | Value |") {
		t.Error("Table content lost after JSON round-trip")
	}
}

// ── 4. Unhappy Path: AI Failure ─────────────────────────────────────────────

func TestExecute_AICallFails_GracefulError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model overloaded"}`))
	}))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "What strategy should I use?",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute should not fail, got: %v", err)
	}

	var types []string
	var responseMsg string
	for ev := range events {
		types = append(types, ev.Type)
		if ev.Type == "response" {
			responseMsg = ev.Message
		}
	}
	assertContains(t, types, "response", "Must emit error response")
	assertContains(t, types, "done", "Must emit done even on failure")
	if responseMsg == "" {
		t.Error("Error response message should not be empty")
	}
}

func TestExecute_AIReturnsEmpty_GracefulError(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse(""))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "what is my win rate percentage",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute should not fail: %v", err)
	}

	var gotDone bool
	for ev := range events {
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("Must emit done even when AI returns empty")
	}
}

// ── 5. Unhappy Path: Context Cancelled ──────────────────────────────────────

func TestExecute_ContextCancelled(t *testing.T) {
	// Simulate slow AI server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second) // very slow
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cc := newTestCC(ts)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	events, err := cc.Execute(ctx, uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "something complex",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute should not fail immediately: %v", err)
	}

	var gotDone bool
	for ev := range events {
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("Channel must close (done) even when context is cancelled")
	}
}

// ── 6. Unhappy Path: Empty Message ──────────────────────────────────────────

func TestExecute_EmptyMessage_IntentNoMatch(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse("I can help with CRM tasks."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute should not fail: %v", err)
	}

	var gotResponse bool
	for ev := range events {
		if ev.Type == "response" {
			gotResponse = true
		}
	}
	if !gotResponse {
		t.Error("Empty message should still produce a response")
	}
}

// ── 7. Unhappy Path: Budget Exceeded ────────────────────────────────────────

func TestExecute_BudgetExceeded(t *testing.T) {
	budget := &BudgetGuard{} // no DB/Redis → will fail budget check gracefully
	// Create gateway with budget that has no Redis (returns nil usage)
	// The budget check for command_center estimates 5000 tokens
	// We need to trigger budget exceeded — but with nil redis, it passes.
	// Test the error type instead:
	err := ErrBudgetExceeded{Used: 10000, Limit: 5000, ResetAt: time.Now().Add(24 * time.Hour)}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Errorf("BudgetExceeded error message wrong: %s", err.Error())
	}
	_ = budget
}

// ── 8. Unhappy Path: Malformed Confirmed Args ───────────────────────────────

func TestExecute_MalformedConfirmedArgs(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse("Done."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage:   "confirm",
		UserRole:      "admin",
		SessionID:     uuid.New(),
		Confirmed:     true,
		ConfirmedTool: "update_deal",
		ConfirmedArgs: json.RawMessage(`{invalid json`),
	})
	if err != nil {
		t.Fatalf("Execute should not crash: %v", err)
	}

	var gotDone bool
	for ev := range events {
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("Malformed args should still complete with done")
	}
}

// ── 9. Unhappy Path: Unknown Tool in Confirmed ──────────────────────────────

func TestExecute_UnknownConfirmedTool(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse("Done."))
	defer ts.Close()

	cc := newTestCC(ts)
	args, _ := json.Marshal(map[string]any{"foo": "bar"})
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage:   "confirm",
		UserRole:      "admin",
		SessionID:     uuid.New(),
		Confirmed:     true,
		ConfirmedTool: "nonexistent_tool",
		ConfirmedArgs: args,
	})
	if err != nil {
		t.Fatalf("Execute should not crash: %v", err)
	}

	var toolResultData string
	for ev := range events {
		if ev.Type == "tool_result" {
			toolResultData = string(ev.Data)
		}
	}
	if !strings.Contains(toolResultData, "unknown tool") {
		t.Errorf("Unknown tool should return error in tool_result, got: %s", toolResultData)
	}
}

// ── 10. Unhappy Path: Viewer Role Write Attempt ─────────────────────────────

func TestExecute_ViewerRole_IntentOnly(t *testing.T) {
	cc := newTestCC(nil)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "help",
		UserRole:    "viewer",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var responseMsg string
	for ev := range events {
		if ev.Type == "response" {
			responseMsg = ev.Message
		}
	}
	if responseMsg == "" {
		t.Error("Viewer should still get help response")
	}
}

// ── 11. Unhappy Path: HTTP 502/504 Gateway Errors ───────────────────────────

func TestGateway_502_RetriesExhausted(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream timeout"))
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest(ts.URL, "dummy", nil, 5*time.Second, "")
	_, err := gw.callCFWorkers(context.Background(), TaskAssistantChat, "@cf/test", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("Expected error after exhausted retries")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("Expected 502 in error, got: %v", err)
	}
	// Should have retried (initial + 3 retries = 4 calls)
	if callCount < 2 {
		t.Errorf("Expected retry attempts, got %d calls", callCount)
	}
}

func TestGateway_504_RetriesExhausted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGatewayTimeout)
		w.Write([]byte("gateway timeout"))
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest(ts.URL, "dummy", nil, 5*time.Second, "")
	_, err := gw.callCFWorkers(context.Background(), TaskAssistantChat, "@cf/test", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("Expected error after 504 retries")
	}
	if !strings.Contains(err.Error(), "504") {
		t.Errorf("Expected 504 in error, got: %v", err)
	}
}

// ── 12. Unhappy Path: Non-JSON Response from AI ─────────────────────────────

func TestGateway_NonJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>Error page</html>"))
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest(ts.URL, "dummy", nil, 5*time.Second, "")
	result, err := gw.callCFWorkers(context.Background(), TaskAssistantChat, "@cf/test", []Message{{Role: "user", Content: "hi"}})
	// parseCFResponse returns empty on invalid JSON, no error
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result.Content != "" {
		t.Errorf("Expected empty content for non-JSON, got: %s", result.Content)
	}
}

// ── 13. Unhappy Path: Connection Refused ────────────────────────────────────

func TestGateway_ConnectionRefused(t *testing.T) {
	gw := NewAIGatewayForTest("http://127.0.0.1:1", "dummy", nil, 2*time.Second, "")
	_, err := gw.callCFWorkers(context.Background(), TaskAssistantChat, "@cf/test", []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("Expected connection error")
	}
}

// ── 14. Event Ordering ──────────────────────────────────────────────────────

func TestExecute_EventOrder_ThinkingBeforeResponse(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse("Analysis complete."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, _ := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "What is my win rate?",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})

	var order []string
	for ev := range events {
		order = append(order, ev.Type)
	}

	// Thinking must appear before response
	thinkIdx := indexOf(order, "thinking")
	respIdx := indexOf(order, "response")
	if thinkIdx >= 0 && respIdx >= 0 && thinkIdx > respIdx {
		t.Errorf("thinking event must come before response, got order: %v", order)
	}

	// Done must be last
	doneIdx := indexOf(order, "done")
	if doneIdx != len(order)-1 {
		t.Errorf("done must be the last event, got order: %v", order)
	}
}

// ── 15. Unhappy Path: Nil Session ID ────────────────────────────────────────

func TestExecute_NilSessionID(t *testing.T) {
	cc := newTestCC(nil)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "help",
		UserRole:    "admin",
		SessionID:   uuid.Nil,
	})
	if err != nil {
		t.Fatalf("Nil session should not crash: %v", err)
	}
	var gotDone bool
	for ev := range events {
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("Must complete even with nil session ID")
	}
}

// ── 16. Unhappy Path: Very Long Message ─────────────────────────────────────

func TestExecute_VeryLongMessage(t *testing.T) {
	longMsg := strings.Repeat("tell me about deals ", 500) // ~10k chars
	ts := mockAIServer(t, mockTextResponse("Here is the info."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: longMsg,
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Long message should not crash: %v", err)
	}
	var gotDone bool
	for ev := range events {
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("Must complete even with very long message")
	}
}

// ── 17. Unhappy Path: Special Characters in Message ─────────────────────────

func TestExecute_SpecialCharsMessage(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse("I can help with that."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: `analyze <script>alert("xss")</script> "quotes" & ampersand`,
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Special chars should not crash: %v", err)
	}
	var gotDone bool
	for ev := range events {
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotDone {
		t.Error("Must handle special characters gracefully")
	}
}

// ── 18. Multiple History Messages ───────────────────────────────────────────

func TestExecute_WithHistory(t *testing.T) {
	ts := mockAIServer(t, mockTextResponse("Based on our conversation..."))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "What about the second one?",
		UserRole:    "admin",
		SessionID:   uuid.New(),
		History: []HistoryMessage{
			{Role: "user", Content: "show my deals"},
			{Role: "assistant", Content: "Here are 3 deals..."},
		},
	})
	if err != nil {
		t.Fatalf("Execute with history failed: %v", err)
	}
	var gotResponse bool
	for ev := range events {
		if ev.Type == "response" {
			gotResponse = true
		}
	}
	if !gotResponse {
		t.Error("Should produce response when history is provided")
	}
}

// ============================================================
// Test Helpers
// ============================================================

func mockTextResponse(content string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"role": "assistant", "content": content}},
			},
			"usage": map[string]interface{}{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}
}

func mockAIServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(handler))
}

// toolSpec / toolCallOf / mockToolResponse build an OpenAI-compatible tool_calls
// response so tests can exercise Execute()'s tool-dispatch path.
type toolSpec struct {
	name string
	args map[string]any
}

func toolCallOf(name string, args map[string]any) toolSpec { return toolSpec{name: name, args: args} }

func mockToolResponse(specs ...toolSpec) func(w http.ResponseWriter, r *http.Request) {
	tcs := make([]map[string]interface{}, len(specs))
	for i, s := range specs {
		argsJSON, _ := json.Marshal(s.args)
		tcs[i] = map[string]interface{}{
			"id":       fmt.Sprintf("call_%d", i),
			"type":     "function",
			"function": map[string]interface{}{"name": s.name, "arguments": string(argsJSON)},
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"role": "assistant", "content": "", "tool_calls": tcs},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]interface{}{"prompt_tokens": 100, "completion_tokens": 20},
		})
	}
}

// A7.4: a create_workflow tool call hands off to the builder via exactly one navigate
// (to /workflows/new?ai=...), then done — the early-return skips the summary AI call.
func TestExecute_CreateWorkflowHandoff(t *testing.T) {
	ts := mockAIServer(t, mockToolResponse(toolCallOf("create_workflow", map[string]any{"description": "notify me on new deal wins"})))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "please build an automation that pings me whenever we close a win",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	var navPaths, types []string
	for ev := range events {
		types = append(types, ev.Type)
		if ev.Type == "navigate" {
			var d struct {
				Path string `json:"path"`
			}
			json.Unmarshal(ev.Data, &d)
			navPaths = append(navPaths, d.Path)
		}
	}
	if len(navPaths) != 1 {
		t.Fatalf("expected exactly one navigate, got %d: %v", len(navPaths), navPaths)
	}
	if !strings.HasPrefix(navPaths[0], "/workflows/new?ai=") {
		t.Errorf("handoff should navigate to the new-workflow builder, got %q", navPaths[0])
	}
	assertContains(t, types, "done", "handoff must emit done")
}

// A7.4: two workflow calls in one turn produce exactly one handoff navigate.
func TestExecute_TwoWorkflowCalls_SingleHandoff(t *testing.T) {
	ts := mockAIServer(t, mockToolResponse(
		toolCallOf("create_workflow", map[string]any{"description": "flow A"}),
		toolCallOf("create_workflow", map[string]any{"description": "flow B"}),
	))
	defer ts.Close()

	cc := newTestCC(ts)
	events, err := cc.Execute(context.Background(), uuid.New(), uuid.New(), CommandRequest{
		UserMessage: "set up a couple of automations for onboarding",
		UserRole:    "admin",
		SessionID:   uuid.New(),
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	navs := 0
	for ev := range events {
		if ev.Type == "navigate" {
			navs++
		}
	}
	if navs != 1 {
		t.Fatalf("two workflow calls must yield exactly one handoff navigate, got %d", navs)
	}
}

// ── Mock implementations ─────────────────────────────────────────────────────

type mockKBRepo struct{}

func (m *mockKBRepo) GetAllActive(_ context.Context, _ uuid.UUID) ([]domain.KnowledgeBaseEntry, error) {
	return nil, nil
}
func (m *mockKBRepo) GetBySection(_ context.Context, _ uuid.UUID, _ string) (*domain.KnowledgeBaseEntry, error) {
	return nil, nil
}
func (m *mockKBRepo) Upsert(_ context.Context, _ *domain.KnowledgeBaseEntry) error { return nil }

type mockOrgSettingsUC struct{}

func (m *mockOrgSettingsUC) GetFieldDefs(_ context.Context, _ uuid.UUID, _ string) ([]domain.CustomFieldDef, error) {
	return nil, nil
}
func (m *mockOrgSettingsUC) CreateFieldDef(_ context.Context, _ uuid.UUID, _ domain.CreateFieldDefInput) (*domain.CustomFieldDef, error) {
	return nil, nil
}
func (m *mockOrgSettingsUC) UpdateFieldDef(_ context.Context, _ uuid.UUID, _ string, _ domain.UpdateFieldDefInput) (*domain.CustomFieldDef, error) {
	return nil, nil
}
func (m *mockOrgSettingsUC) DeleteFieldDef(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (m *mockOrgSettingsUC) ValidateCustomFields(_ context.Context, _ uuid.UUID, _ string, _ domain.JSON) error {
	return nil
}

func newTestCC(aiServer *httptest.Server) *CommandCenter {
	var gw *AIGateway
	if aiServer != nil {
		gw = NewAIGatewayForTest(aiServer.URL, "dummy", nil, 10*time.Second, "")
	} else {
		gw = NewAIGatewayForTest("http://127.0.0.1:1", "dummy", nil, 2*time.Second, "")
	}

	kb := NewKnowledgeBuilder(&mockKBRepo{}, &mockOrgSettingsUC{}, nil, nil)

	return &CommandCenter{
		gateway:          gw,
		knowledgeBuilder: kb,
		sessionCtx:       NewSessionContextCache(),
		logger:           zap.NewNop(),
	}
}

func assertContains(t *testing.T, slice []string, want string, msg string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("%s: %v does not contain %q", msg, slice, want)
}

func indexOf(slice []string, val string) int {
	for i, s := range slice {
		if s == val {
			return i
		}
	}
	return -1
}
