package ai

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

var runLive = flag.Bool("live", false, "Run live integration tests against Cloudflare Workers AI")

// ─────────────────────────────────────────────────────────────────────────────
// 1. Local Isolated Tests using httptest (No Network)
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_CFWorkers_Success(t *testing.T) {
	// Mock Server replicating CF Workers AI response format
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify CF auth header
		if r.Header.Get("Authorization") == "" {
			t.Error("Missing Authorization header")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"response": "Tested successfully via CF Workers AI.",
				"usage": map[string]interface{}{
					"prompt_tokens":     150,
					"completion_tokens": 25,
				},
			},
			"success": true,
		})
	}))
	defer ts.Close()

	// Point gateway to local mock
	gw := NewAIGatewayForTest(ts.URL, "dummy-cf", nil, 5*time.Second, "dummy-gw")

	ctx := context.Background()
	task := TaskAssistantChat
	msgs := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello testing"},
	}

	result, err := gw.callCFWorkers(ctx, task, "@cf/moonshotai/kimi-k2.6", msgs)
	if err != nil {
		t.Fatalf("callCFWorkers failed: %v", err)
	}

	if result.Content != "Tested successfully via CF Workers AI." {
		t.Errorf("Unexpected content: %v", result.Content)
	}
	if result.OutputTokens != 25 {
		t.Errorf("Expected 25 output tokens, got %d", result.OutputTokens)
	}
	if result.InputTokens != 150 {
		t.Errorf("Expected 150 input tokens, got %d", result.InputTokens)
	}
}

func TestGateway_CFWorkers_WithTools(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"result": map[string]interface{}{
				"response": nil,
				"tool_calls": []map[string]interface{}{
					{
						"name": "search_deals",
						"arguments": map[string]interface{}{
							"query": "top deals",
							"limit": 5,
						},
					},
				},
				"usage": map[string]interface{}{
					"prompt_tokens":     500,
					"completion_tokens": 50,
				},
			},
			"success": true,
		})
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest(ts.URL, "dummy", nil, 5*time.Second, "")

	result, err := gw.callCFWorkersWithTools(context.Background(), TaskCommandCenter, "@cf/moonshotai/kimi-k2.6", []Message{{Role: "user", Content: "test"}}, []Tool{{Name: "search_deals"}})
	if err != nil {
		t.Fatalf("callCFWorkersWithTools failed: %v", err)
	}

	if len(result.ToolCalls) == 0 {
		t.Fatal("Expected tool calls to be parsed, found 0")
	}
	if result.ToolCalls[0].Name != "search_deals" {
		t.Errorf("Expected search_deals, got %s", result.ToolCalls[0].Name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Model routing test
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_ModelRouting_OptimizedPerTask(t *testing.T) {
	gw := NewAIGatewayForTest("http://dummy", "dummy", nil, 5*time.Second, "")

	// Verify each task maps to the optimal model for cost
	expected := map[AITask]string{
		TaskAssistantChat:     "@cf/qwen/qwen3-30b-a3b-fp8",
		TaskCommandCenter:     "@cf/qwen/qwen3-30b-a3b-fp8",
		TaskEmailCompose:      "@cf/qwen/qwen3-30b-a3b-fp8",
		TaskMeetingSummary:    "@cf/qwen/qwen3-30b-a3b-fp8",
		TaskAnalytics:         "@cf/qwen/qwen3-30b-a3b-fp8",
		TaskVoiceIntelligence: "@cf/qwen/qwen3-30b-a3b-fp8",
		TaskSentiment:         "@cf/meta/llama-3.2-1b-instruct",
		TaskDealScore:         "@cf/meta/llama-3.2-3b-instruct",
		TaskFollowup:          "@cf/meta/llama-3.2-3b-instruct",
	}

	for task, want := range expected {
		got := gw.modelFor(task, providerCFWorkers)
		if got != want {
			t.Errorf("Task %s: expected %s, got %s", task, want, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. OpenAI-compatible format tests (hosted/pinned models like Kimi K2.6)
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_OpenAIFormat_Complete(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// OpenAI-compatible format that Kimi K2.6 returns
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":      "chatcmpl-abc123",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   "@cf/moonshotai/kimi-k2.6",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Hello from OpenAI format!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     100,
				"completion_tokens": 20,
			},
		})
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest(ts.URL, "dummy", nil, 5*time.Second, "")
	result, err := gw.callCFWorkers(context.Background(), TaskAssistantChat, "@cf/moonshotai/kimi-k2.6", []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("callCFWorkers failed: %v", err)
	}
	if result.Content != "Hello from OpenAI format!" {
		t.Errorf("Expected OpenAI format content, got: %s", result.Content)
	}
	if result.InputTokens != 100 {
		t.Errorf("Expected 100 input tokens, got %d", result.InputTokens)
	}
	if result.OutputTokens != 20 {
		t.Errorf("Expected 20 output tokens, got %d", result.OutputTokens)
	}
}

func TestGateway_OpenAIFormat_WithTools(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-tools-456",
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "",
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call_xyz",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "search_contacts",
									"arguments": `{"query":"John"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     300,
				"completion_tokens": 40,
			},
		})
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest(ts.URL, "dummy", nil, 5*time.Second, "")
	result, err := gw.callCFWorkersWithTools(context.Background(), TaskCommandCenter, "@cf/moonshotai/kimi-k2.6", []Message{{Role: "user", Content: "find John"}}, []Tool{{Name: "search_contacts"}})
	if err != nil {
		t.Fatalf("callCFWorkersWithTools failed: %v", err)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "search_contacts" {
		t.Errorf("Expected search_contacts, got %s", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].ID != "call_xyz" {
		t.Errorf("Expected call_xyz ID, got %s", result.ToolCalls[0].ID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Live Integration Tests
// Guarded by `-live` flag. Absolutely never runs in CI pipeline automatically.
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_LiveIntegration(t *testing.T) {
	if !*runLive {
		t.Skip("Skipping live integration test. Run with 'go test -live' to execute.")
	}

	cfAccountID := os.Getenv("CF_ACCOUNT_ID")
	cfGatewayID := os.Getenv("CF_AI_GATEWAY_ID")
	cfToken := os.Getenv("CF_AI_TOKEN")
	if cfAccountID == "" || cfToken == "" {
		t.Skip("CF_ACCOUNT_ID or CF_AI_TOKEN not set. Cannot run live test.")
	}

	gw := NewAIGateway(cfAccountID, cfGatewayID, cfToken, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msgs := []Message{
		{Role: "user", Content: "Reply with the exact text: 'live test pass'"},
	}

	res, err := gw.callCFWorkers(ctx, TaskAssistantChat, "@cf/moonshotai/kimi-k2.6", msgs)
	if err != nil {
		t.Fatalf("Live request failed: %v", err)
	}

	if res.Content == "" {
		t.Error("Empty response from live endpoint")
	}
	t.Logf("Live test success. Tokens output: %d, content: %s", res.OutputTokens, res.Content)
}
