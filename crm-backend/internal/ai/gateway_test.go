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

var runLive = flag.Bool("live", false, "Run live integration tests against Anthropic")

// ─────────────────────────────────────────────────────────────────────────────
// 1. Local Isolated Tests using httptest (No Network)
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_VercelAnthropic_Success(t *testing.T) {
	// 1. Mock Server exactly replicating the expected Anthropic Messages API format
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify expected headers exist (anthropic-beta removed — caching is GA for Haiku 4.5)
		if r.Header.Get("anthropic-version") == "" {
			t.Error("Missing anthropic-version header")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   "msg_123",
			"type": "message",
			"role": "assistant",
			"model": "claude-3-5-haiku-20241022",
			"stop_reason": "end_turn",
			"usage": map[string]interface{}{
				"input_tokens": 150,
				"output_tokens": 25,
				"cache_read_input_tokens": 120,
			},
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": "Tested successfully internally.",
				},
			},
		})
	}))
	defer ts.Close()

	// 2. Point gateway to local mock
	gw := NewAIGatewayForTest("http://dummy", "dummy-cf", nil, 5*time.Second, "dummy-gw")
	gw.SetVercelGateway(ts.URL, "dummy-vercel-key")

	// 3. Fire request to mock
	ctx := context.Background()
	task := TaskCommandCenter
	msgs := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Hello testing"},
	}

	result, err := gw.callVercelGateway(ctx, task, "dummy-model", msgs)
	if err != nil {
		t.Fatalf("callVercelGateway failed: %v", err)
	}

	// 4. Assert correctness
	if result.Content != "Tested successfully internally." {
		t.Errorf("Unexpected content: %v", result.Content)
	}
	if result.CachedInputTokens != 120 {
		t.Errorf("Expected 120 cached tokens, got %d", result.CachedInputTokens)
	}
	if result.OutputTokens != 25 {
		t.Errorf("Expected 25 output tokens, got %d", result.OutputTokens)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("Expected stop_reason end_turn, got %s", result.StopReason)
	}
}

func TestGateway_VercelAnthropic_WithTools(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":   "msg_tools",
			"type": "message",
			"role": "assistant",
			"model": "claude-3-5-haiku",
			"stop_reason": "tool_use",
			"usage": map[string]interface{}{
				"input_tokens": 500,
				"output_tokens": 50,
			},
			"content": []map[string]interface{}{
				{
					"type": "tool_use",
					"id": "toolu_01",
					"name": "get_weather",
					"input": map[string]interface{}{
						"location": "San Francisco",
					},
				},
			},
		})
	}))
	defer ts.Close()

	gw := NewAIGatewayForTest("http://dummy", "dummy", nil, 5*time.Second, "")
	gw.SetVercelGateway(ts.URL, "dummy-vercel-key")

	result, err := gw.callVercelGatewayWithTools(context.Background(), TaskCommandCenter, "claude-3", []Message{{Role:"user", Content:"test"}}, []Tool{{Name:"get_weather"}})
	if err != nil {
		t.Fatalf("callVercelGatewayWithTools failed: %v", err)
	}

	if len(result.ToolCalls) == 0 {
		t.Fatal("Expected tool calls to be parsed, found 0")
	}
	if result.ToolCalls[0].Name != "get_weather" {
		t.Errorf("Expected get_weather, got %s", result.ToolCalls[0].Name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Isolated Payload Integrity Test (No network)
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_Payload_EnforcesCacheLimits(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "CRITICAL DIRECTIVE..."},
		{Role: "user", Content: "Hello"},
	}
	tools := []Tool{
		{Name: "fetch_data"},
		{Name: "submit_order"},
	}

	reqBody := BuildAnthropicRequestBody("test-model", 400, msgs, tools)

	// Verify the system prompt correctly received cache_control
	sysBlocks, ok := reqBody["system"].([]map[string]interface{})
	if !ok || len(sysBlocks) == 0 {
		t.Fatal("System prompt improperly parsed")
	}
	if _, hasCache := sysBlocks[0]["cache_control"]; !hasCache {
		t.Fatal("cache_control explicitly missing from system block construction")
	}

	// Verify tools correctly received cache_control on the very last block
	authTools, ok := reqBody["tools"].([]map[string]interface{})
	if !ok || len(authTools) == 0 {
		t.Fatal("Tools improperly handled")
	}
	
	if _, hasCache := authTools[0]["cache_control"]; hasCache {
		t.Fatal("cache_control shouldn't be on the first tool")
	}
	if _, hasCache := authTools[len(authTools)-1]["cache_control"]; !hasCache {
		t.Fatal("cache_control explicitly missing from last tool construction")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Live Integration Tests 
// Guarded by `-live` flag. Absolutely never runs in CI pipeline automatically.
// ─────────────────────────────────────────────────────────────────────────────

func TestGateway_LiveIntegration(t *testing.T) {
	if !*runLive {
		t.Skip("Skipping live integration test. Run with 'go test -live' to execute.")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set. Cannot run live test without key.")
	}

	gw := NewAIGateway("", "", "", "", nil, nil) // Live implementation
	gw.SetVercelGateway("https://api.anthropic.com/v1", apiKey)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	msgs := []Message{
		{Role: "user", Content: "Reply with the exact text: 'live test pass'"},
	}

	res, err := gw.callVercelGateway(ctx, TaskAssistantChat, "claude-3-haiku-20240307", msgs)
	if err != nil {
		t.Fatalf("Live request failed: %v", err)
	}

	if res.Content == "" {
		t.Error("Empty response from live endpoint")
	}
	t.Logf("Live test success. Tokens output: %d", res.OutputTokens)
}
