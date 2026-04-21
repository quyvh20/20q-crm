package ai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)


// ============================================================
// Task constants
// ============================================================

type AITask string

const (
	TaskEmailCompose       AITask = "email_compose"
	TaskAssistantChat      AITask = "assistant_chat"
	TaskMeetingSummary     AITask = "meeting_summary"
	TaskDealScore          AITask = "deal_score"
	TaskAnalytics          AITask = "analytics_insight"
	TaskEmbedding          AITask = "embedding"
	TaskVoiceSTT           AITask = "voice_stt"
	TaskVoiceIntelligence  AITask = "voice_intelligence"
	TaskSentiment          AITask = "sentiment"
	TaskFollowup           AITask = "followup_suggest"
	TaskCommandCenter      AITask = "command_center"
)

// advancedTasks are only available to pro+ plans
var advancedTasks = map[AITask]bool{
	TaskMeetingSummary:    true,
	TaskDealScore:         true,
	TaskAnalytics:         true,
	TaskVoiceSTT:          true,
	TaskVoiceIntelligence: true,
	TaskFollowup:          true,
}

func IsAdvancedTask(t AITask) bool { return advancedTasks[t] }

// ============================================================
// Provider constants
// ============================================================

type provider string

const (
	providerCFWorkers     provider = "cloudflare"
	providerAnthropic     provider = "anthropic"
	providerVercelGateway provider = "vercel_gateway"
)

// Task → primary provider mapping
var taskPrimaryProvider = map[AITask]provider{
	TaskEmailCompose:      providerAnthropic,
	TaskAssistantChat:     providerCFWorkers,
	TaskMeetingSummary:    providerAnthropic,
	TaskDealScore:         providerCFWorkers,
	TaskAnalytics:         providerAnthropic,
	TaskSentiment:         providerCFWorkers,
	TaskFollowup:          providerAnthropic,
	TaskVoiceIntelligence: providerCFWorkers,
	TaskCommandCenter:     providerVercelGateway,
}

// Task → model mapping per provider
var taskModels = map[AITask]map[provider]string{
	TaskEmailCompose:      {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskAssistantChat:     {providerCFWorkers: "@cf/meta/llama-3.3-70b-instruct-fp8-fast", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskMeetingSummary:    {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskDealScore:         {providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskAnalytics:         {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskSentiment:         {providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskFollowup:          {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskVoiceIntelligence: {providerCFWorkers: "@cf/moonshotai/kimi-k2.5", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskCommandCenter:     {providerVercelGateway: "anthropic/claude-haiku-4.5", providerCFWorkers: "@cf/moonshotai/kimi-k2.5"},
}

// taskMaxTokens enforces strict output boundaries based on empirically measured p99 usage
var taskMaxTokens = map[AITask]int{
	TaskSentiment:         50,
	TaskDealScore:         300,
	TaskFollowup:          200,
	TaskEmailCompose:      400,
	TaskAssistantChat:     500,
	TaskCommandCenter:     800,
	TaskMeetingSummary:    1000,
	TaskAnalytics:         1000,
	TaskVoiceIntelligence: 1500,
}

func maxTokensFor(task AITask) int {
	if n, ok := taskMaxTokens[task]; ok {
		return n
	}
	return 1024 // safe default
}

// ============================================================
// Message / Response types
// ============================================================

type Message struct {
	Role      string     `json:"role"`    // "system" | "user" | "assistant" | "tool"
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolUseID string     `json:"tool_use_id,omitempty"` // for tool result messages
}

type AIResponse struct {
	Content             string
	Model               string
	Provider            string
	InputTokens         int
	OutputTokens        int
	CachedInputTokens   int    // from usage.cache_read_input_tokens
	CacheCreationTokens int    // from usage.cache_creation_input_tokens
	LatencyMs           int64  // wall-clock ms
	StopReason          string // "end_turn" | "max_tokens" | "stop_sequence" | "tool_use"
	ToolCalls           []ToolCall
}

// ============================================================
// AIGateway
// ============================================================

type AIGateway struct {
	gatewayURL        string
	cfToken           string
	cfGatewayToken    string
	anthropicKey      string
	vercelGatewayURL  string // https://ai-gateway.vercel.sh/v1
	vercelGatewayKey  string // vck_... API key
	httpClient        *http.Client
	Budget            *BudgetGuard
	logger            *zap.Logger
}

func NewAIGateway(cfAccountID, cfAIGatewayID, cfToken, anthropicKey string, budget *BudgetGuard, logger *zap.Logger, cfGatewayToken ...string) *AIGateway {
	gwTok := ""
	if len(cfGatewayToken) > 0 {
		gwTok = cfGatewayToken[0]
	}
	return &AIGateway{
		gatewayURL:     fmt.Sprintf("https://gateway.ai.cloudflare.com/v1/%s/%s", cfAccountID, cfAIGatewayID),
		cfToken:        cfToken,
		cfGatewayToken: gwTok,
		anthropicKey:   anthropicKey,
		httpClient:     &http.Client{Timeout: 120 * time.Second},
		Budget:         budget,
		logger:         logger,
	}
}

// SetVercelGateway configures the Vercel AI Gateway provider.
func (g *AIGateway) SetVercelGateway(url, key string) {
	g.vercelGatewayURL = url
	g.vercelGatewayKey = key
}

// Complete runs a full inference call with budget check + fallback.
func (g *AIGateway) Complete(ctx context.Context, orgID, userID uuid.UUID, task AITask, messages []Message) (AIResponse, error) {
	estimated := estimateTokens(messages)

	if g.Budget != nil {
		if err := g.Budget.Check(ctx, orgID, task, estimated); err != nil {
			return AIResponse{}, err
		}
	}

	// Deterministic fallback chain:
	// 1. Vercel AI Gateway (Anthropic Haiku — primary for command center)
	// 2. CF → Anthropic (via CF AI Gateway — separate billing, reliable)
	// 3. CF → Llama (our own Cloudflare infra — last resort, never goes down)
	primaryP := g.routePrimary(task)
	chain := g.buildFallbackChain(primaryP)

	var result AIResponse
	var err error
	for _, p := range chain {
		result, err = g.callProvider(ctx, task, p, messages)
		if err == nil {
			break
		}
		g.logger.Warn("provider failed, trying next",
			zap.String("provider", string(p)),
			zap.String("task", string(task)),
			zap.Error(err))
	}

	if err != nil {
		// All providers failed — return graceful response instead of crashing
		g.logger.Error("all providers failed — returning graceful fallback", zap.Error(err))
		return AIResponse{
			Content:  "I'm having trouble connecting right now. Please try again in a moment.",
			Provider: "fallback",
			Model:    "none",
		}, nil
	}

	// Persist usage synchronously to prevent context cancellation races
	if g.Budget != nil {
		g.Budget.Record(ctx, orgID, userID, task, result.Model, result.Provider, result.InputTokens, result.OutputTokens,
			WithCache(result.CachedInputTokens, result.CacheCreationTokens),
			WithLatency(result.LatencyMs),
			WithStopReason(result.StopReason),
			WithPromptHash(hashMessages(messages)),
		)
	}

	return result, nil
}

// CompleteWithTools runs inference with tool definitions.
// Fallback chain: Vercel (Claude) → CF Anthropic → CF Llama (guaranteed last resort)
func (g *AIGateway) CompleteWithTools(ctx context.Context, orgID, userID uuid.UUID, task AITask, messages []Message, tools []Tool) (AIResponse, error) {
	estimated := 5000 // generous estimate for tool calls
	if g.Budget != nil {
		if err := g.Budget.Check(ctx, orgID, task, estimated); err != nil {
			return AIResponse{}, err
		}
	}

	var result AIResponse
	var err error

	// Deterministic fallback chain for tool calling:
	// 1. Vercel AI Gateway (Haiku) — best tool calling, prompt caching
	// 2. CF Kimi K2.5 — OUR OWN INFRA, frontier model, native multi-turn tool calling
	// 3. CF → Anthropic — last resort Claude fallback
	type namedCall struct {
		name string
		call func() (AIResponse, error)
	}
	chain := []namedCall{}

	// 1. Always start with Vercel if configured
	if g.vercelGatewayURL != "" && g.vercelGatewayKey != "" {
		chain = append(chain, namedCall{
			"vercel_gateway",
			func() (AIResponse, error) {
				return g.callVercelGatewayWithTools(ctx, task, g.modelFor(task, providerVercelGateway), messages, tools)
			},
		})
	}
	// 2. CF Kimi K2.5 — our own infra, always available, strongest CF model for tool calling
	chain = append(chain, namedCall{
		"cf_kimi_k2.5",
		func() (AIResponse, error) {
			return g.callCFWorkersWithTools(ctx, task, g.modelFor(task, providerCFWorkers), messages, tools)
		},
	})
	// 3. Anthropic via CF Gateway — last resort
	chain = append(chain, namedCall{
		"cf_anthropic",
		func() (AIResponse, error) {
			return g.callAnthropicWithTools(ctx, task, g.modelFor(task, providerAnthropic), messages, tools)
		},
	})

	for _, p := range chain {
		result, err = p.call()
		if err == nil {
			break
		}
		g.logger.Warn("tool call provider failed, trying next",
			zap.String("provider", p.name),
			zap.String("task", string(task)),
			zap.Error(err))
	}

	if err != nil {
		// All providers failed — return graceful response, do not crash
		g.logger.Error("all tool-call providers failed — returning graceful fallback", zap.Error(err))
		return AIResponse{
			Content:  "I'm having trouble connecting right now. Please try again in a moment.",
			Provider: "fallback",
			Model:    "none",
		}, nil
	}

	if g.Budget != nil {
		g.Budget.Record(ctx, orgID, userID, task, result.Model, result.Provider, result.InputTokens, result.OutputTokens,
			WithCache(result.CachedInputTokens, result.CacheCreationTokens),
			WithLatency(result.LatencyMs),
			WithStopReason(result.StopReason),
			WithPromptHash(hashMessages(messages)),
		)
	}

	return result, nil
}

// callCFWorkersWithTools calls Cloudflare Workers AI using the OpenAI-compatible
// function-calling protocol and parses the tool_calls from the response.
func (g *AIGateway) callCFWorkersWithTools(ctx context.Context, task AITask, model string, messages []Message, tools []Tool) (AIResponse, error) {
	url := fmt.Sprintf("%s/workers-ai/%s", g.gatewayURL, model)

	// Build the messages array in OpenAI format.
	// CF Workers AI expects: [{"role":"system","content":"..."},{"role":"user","content":"..."},...]
	var chatMsgs []map[string]interface{}
	for _, m := range messages {
		switch m.Role {
		case "system", "user":
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role":    m.Role,
				"content": m.Content,
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				// Format tool calls in the OpenAI assistant message format
				cfToolCalls := make([]map[string]interface{}, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					cfToolCalls[i] = map[string]interface{}{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      tc.Name,
							"arguments": string(tc.Params),
						},
					}
				}
				msg := map[string]interface{}{
					"role":       "assistant",
					"tool_calls": cfToolCalls,
				}
				if m.Content != "" {
					msg["content"] = m.Content
				}
				chatMsgs = append(chatMsgs, msg)
			} else {
				chatMsgs = append(chatMsgs, map[string]interface{}{
					"role":    "assistant",
					"content": m.Content,
				})
			}
		case "tool":
			// Tool result message in OpenAI format
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": m.ToolUseID,
				"content":      m.Content,
			})
		}
	}

	reqBody := map[string]interface{}{
		"messages": chatMsgs,
		"tools":    BuildToolsForCFWorkers(),
	}

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"Authorization": "Bearer " + g.cfToken,
		"Content-Type":  "application/json",
	}, reqBody)
	if err != nil {
		return AIResponse{}, fmt.Errorf("cf workers tool call: %w", err)
	}

	// CF Workers AI response with tool_calls follows OpenAI format:
	// { "result": { "response": "...", "tool_calls": [{"id":"..","type":"function","function":{"name":"..","arguments":"{...}"}}], "usage":{...} } }
	// Actual CF Workers AI response shape (NOT standard OpenAI format):
	// {"result":{"response":null,"tool_calls":[{"name":"fn","arguments":{...}}],"usage":{...}}}
	var cfResp struct {
		Result struct {
			Response  interface{} `json:"response"` // null or string
			ToolCalls []struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"` // already a JSON object
			} `json:"tool_calls"`
			Usage struct {
				InputTokens  int `json:"prompt_tokens"`
				OutputTokens int `json:"completion_tokens"`
			} `json:"usage"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(resp, &cfResp); err != nil {
		return AIResponse{}, fmt.Errorf("cf workers tool unmarshal: %w (body: %s)", err, string(resp))
	}

	// response is null when tool_calls are returned
	responseText := ""
	if cfResp.Result.Response != nil {
		if s, ok := cfResp.Result.Response.(string); ok {
			responseText = s
		}
	}

	result := AIResponse{
		Content:      responseText,
		Model:        model,
		Provider:     string(providerCFWorkers),
		InputTokens:  cfResp.Result.Usage.InputTokens,
		OutputTokens: cfResp.Result.Usage.OutputTokens,
	}

	// arguments is already a decoded JSON object (RawMessage), not a string
	for i, tc := range cfResp.Result.ToolCalls {
		params := tc.Arguments
		if !json.Valid(params) || len(params) == 0 {
			params = json.RawMessage("{}")
		}
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:     fmt.Sprintf("call_%d_%s", i, tc.Name),
			Name:   tc.Name,
			Params: params,
		})
	}

	return result, nil
}

// callAnthropicWithTools sends tool definitions to Anthropic and parses tool_use blocks.
func (g *AIGateway) callAnthropicWithTools(ctx context.Context, task AITask, model string, messages []Message, tools []Tool) (AIResponse, error) {
	url := fmt.Sprintf("%s/anthropic/v1/messages", g.gatewayURL)

	reqBody := BuildAnthropicRequestBody(model, maxTokensFor(task), messages, tools)

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"x-api-key":         g.anthropicKey,
		"anthropic-version": "2023-06-01",
		"Content-Type":      "application/json",
	}, reqBody)
	if err != nil {
		return AIResponse{}, err
	}

	// Parse Anthropic response with potential tool_use blocks
	var anthResp struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(resp, &anthResp); err != nil {
		return AIResponse{}, fmt.Errorf("anthropic tool unmarshal: %w", err)
	}

	result := AIResponse{
		Model:        model,
		Provider:     string(providerAnthropic),
		InputTokens:  anthResp.Usage.InputTokens,
		OutputTokens: anthResp.Usage.OutputTokens,
	}

	for _, block := range anthResp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:     block.ID,
				Name:   block.Name,
				Params: block.Input,
			})
		}
	}

	return result, nil
}

func (g *AIGateway) routePrimary(task AITask) provider {
	if p, ok := taskPrimaryProvider[task]; ok {
		// If Vercel Gateway is configured, use it; otherwise fall back to CF
		if p == providerVercelGateway && (g.vercelGatewayURL == "" || g.vercelGatewayKey == "") {
			return providerCFWorkers
		}
		return p
	}
	return providerCFWorkers
}

func (g *AIGateway) routeFallback(task AITask, used provider) provider {
	if used == providerCFWorkers {
		return providerAnthropic
	}
	return providerCFWorkers
}

// buildFallbackChain returns an ordered provider list for Complete().
// Chain: primary → CF Kimi K2.5 (our infra, strongest) → Anthropic (last resort)
func (g *AIGateway) buildFallbackChain(primary provider) []provider {
	seen := map[provider]bool{}
	var chain []provider
	add := func(p provider) {
		if !seen[p] {
			seen[p] = true
			chain = append(chain, p)
		}
	}
	// 1. Primary first
	add(primary)
	// 2. Vercel if configured and not already primary
	if g.vercelGatewayURL != "" && g.vercelGatewayKey != "" {
		add(providerVercelGateway)
	}
	// 3. CF Kimi K2.5 — our own infra, strongest model for fallback
	add(providerCFWorkers)
	// 4. Anthropic via CF Gateway — last resort
	add(providerAnthropic)
	return chain
}

// ============================================================
// Provider-specific call implementations
// ============================================================

func (g *AIGateway) callProvider(ctx context.Context, task AITask, p provider, messages []Message) (AIResponse, error) {
	model := g.modelFor(task, p)
	switch p {
	case providerVercelGateway:
		return g.callVercelGateway(ctx, task, model, messages)
	case providerAnthropic:
		return g.callAnthropic(ctx, task, model, messages)
	case providerCFWorkers:
		return g.callCFWorkers(ctx, task, model, messages)
	default:
		return AIResponse{}, fmt.Errorf("unknown provider: %s", p)
	}
}

func (g *AIGateway) modelFor(task AITask, p provider) string {
	if models, ok := taskModels[task]; ok {
		if m, ok := models[p]; ok {
			return m
		}
	}
	switch p {
	case providerVercelGateway:
		return "anthropic/claude-haiku-4.5"
	case providerAnthropic:
		return "claude-3-5-haiku-20241022"
	default:
		return "@cf/meta/llama-3.1-8b-instruct"
	}
}

// callCFWorkers — CF AI Gateway → Cloudflare Workers AI
func (g *AIGateway) callCFWorkers(ctx context.Context, task AITask, model string, messages []Message) (AIResponse, error) {
	url := fmt.Sprintf("%s/workers-ai/%s", g.gatewayURL, model)

	// Build messages in the format CF expects, handling all roles including tool results.
	var chatMsgs []map[string]interface{}
	for _, m := range messages {
		switch m.Role {
		case "system", "user":
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role":    m.Role,
				"content": m.Content,
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				cfToolCalls := make([]map[string]interface{}, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					cfToolCalls[i] = map[string]interface{}{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      tc.Name,
							"arguments": string(tc.Params),
						},
					}
				}
				msg := map[string]interface{}{
					"role":       "assistant",
					"content":    m.Content, // empty string is fine, not null
					"tool_calls": cfToolCalls,
				}
				chatMsgs = append(chatMsgs, msg)
			} else {
				chatMsgs = append(chatMsgs, map[string]interface{}{
					"role":    "assistant",
					"content": m.Content,
				})
			}
		case "tool":
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": m.ToolUseID,
				"content":      m.Content,
			})
		}
	}

	body := map[string]interface{}{
		"messages": chatMsgs,
	}

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"Authorization": "Bearer " + g.cfToken,
		"Content-Type":  "application/json",
	}, body)
	if err != nil {
		return AIResponse{}, err
	}

	// CF response: { "result": { "response": "...", "usage": { "prompt_tokens": N, "completion_tokens": M } } }
	var cfResp struct {
		Result struct {
			Response interface{} `json:"response"` // null or string
			Usage    struct {
				InputTokens  int `json:"prompt_tokens"`
				OutputTokens int `json:"completion_tokens"`
			} `json:"usage"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(resp, &cfResp); err != nil {
		return AIResponse{}, fmt.Errorf("cf workers unmarshal: %w", err)
	}

	responseText := ""
	if cfResp.Result.Response != nil {
		if s, ok := cfResp.Result.Response.(string); ok {
			responseText = s
		}
	}

	return AIResponse{
		Content:      responseText,
		Model:        model,
		Provider:     string(providerCFWorkers),
		InputTokens:  cfResp.Result.Usage.InputTokens,
		OutputTokens: cfResp.Result.Usage.OutputTokens,
	}, nil
}

// callAnthropic — CF AI Gateway → Anthropic (Claude)
func (g *AIGateway) callAnthropic(ctx context.Context, task AITask, model string, messages []Message) (AIResponse, error) {
	url := fmt.Sprintf("%s/anthropic/v1/messages", g.gatewayURL)

	reqBody := BuildAnthropicRequestBody(model, maxTokensFor(task), messages, nil)

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"x-api-key":         g.anthropicKey,
		"anthropic-version": "2023-06-01",
		"Content-Type":      "application/json",
	}, reqBody)
	if err != nil {
		return AIResponse{}, err
	}

	// Anthropic response: { "content": [{"text":"..."}], "usage": { "input_tokens": N, "output_tokens": M } }
	var anthResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(resp, &anthResp); err != nil {
		return AIResponse{}, fmt.Errorf("anthropic unmarshal: %w", err)
	}

	text := ""
	if len(anthResp.Content) > 0 {
		text = anthResp.Content[0].Text
	}

	return AIResponse{
		Content:      text,
		Model:        model,
		Provider:     string(providerAnthropic),
		InputTokens:  anthResp.Usage.InputTokens,
		OutputTokens: anthResp.Usage.OutputTokens,
	}, nil
}

// ============================================================
// HTTP helper
// ============================================================

func (g *AIGateway) doRequest(ctx context.Context, url, method string, headers map[string]string, body interface{}) ([]byte, error) {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyBytes = b
	}

	maxRetries := 3
	var lastData []byte
	var lastStatus int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if g.cfGatewayToken != "" {
			req.Header.Set("cf-aig-authorization", "Bearer "+g.cfGatewayToken)
		}

		res, err := g.httpClient.Do(req)
		
		// Handle timeout or connectivity errors
		if err != nil {
			if isTimeoutErr(err) && attempt < maxRetries {
				g.logger.Warn("AI API Timeout, retrying...", zap.Int("attempt", attempt+1))
				time.Sleep(time.Duration(1<<attempt) * time.Second) // exponential backoff
				continue
			}
			if isTimeoutErr(err) {
				return nil, ErrAITimeout{Provider: url, After: 120}
			}
			return nil, err
		}

		data, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {

			return nil, err
		}

		// Handle 502 / 504 timeouts gracefully with retries
		if res.StatusCode == http.StatusBadGateway || res.StatusCode == http.StatusGatewayTimeout {
			lastStatus = res.StatusCode
			lastData = data
			if attempt < maxRetries {
				g.logger.Warn("CF Gateway 502/504 timeout, retrying...", zap.Int("attempt", attempt+1), zap.Int("status", res.StatusCode))
				time.Sleep(time.Duration(1<<attempt) * time.Second) // exponential backoff
				continue
			} else {
				// Break out of loop to return final exhaustion error
				break
			}
		}

		// Any other bad statusCode without retry
		if res.StatusCode >= 400 {
			return nil, fmt.Errorf("provider returned %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
		}

		// Success scenario
		return data, nil
	}

	// If we exhausted all 3 retries because of 502/504:
	return nil, fmt.Errorf("provider returned %d: %s after %d retries", lastStatus, strings.TrimSpace(string(lastData)), maxRetries)
}

// isTimeoutErr reports whether err represents a network or context timeout.
func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// ============================================================
// StreamChat — real SSE streaming via CF Workers AI
// ============================================================

// StreamChat starts a streaming inference call and writes raw SSE chunks
// ("data: ...\n\n") to w as they arrive from the provider.
// Budget check is performed before the call; usage is estimated post-call.
// writeSSEHeaders is called exactly once when the upstream connection is
// established — before any bytes reach the client. flush drains the buffer.
func (g *AIGateway) StreamChat(ctx context.Context, orgID, userID uuid.UUID, task AITask, messages []Message, w io.Writer, writeSSEHeaders func(), flush func()) error {
	start := time.Now()
	estimated := estimateTokens(messages)
	if g.Budget != nil {
		if err := g.Budget.Check(ctx, orgID, task, estimated); err != nil {
			return err
		}
	}

	model := g.modelFor(task, g.routePrimary(task))
	url := fmt.Sprintf("%s/workers-ai/%s", g.gatewayURL, model)

	// Build CF Workers AI streaming request.
	// Llama 3.1 respects system-role messages in the messages array more reliably
	// than the separate top-level "system" field.
	var allMsgs []map[string]string
	for _, m := range messages {
		allMsgs = append(allMsgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	body := map[string]interface{}{
		"messages": allMsgs,
		"stream":   true,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.cfToken)
	if g.cfGatewayToken != "" {
		req.Header.Set("cf-aig-authorization", "Bearer "+g.cfGatewayToken)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		if isTimeoutErr(err) {
			return ErrAITimeout{Provider: g.gatewayURL, After: 5}
		}
		return fmt.Errorf("stream request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("stream provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	// Connection established successfully — commit SSE headers BEFORE first write.
	writeSSEHeaders()

	// Relay each SSE line from the provider straight to the client.
	// CF Workers AI sends: data: {"response":"token"}\n\n
	// We re-emit: data: token\n\n   (extract just the text)
	// Relaying loop
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 512*1024), 512*1024)
	var totalOutput int

	// Persist usage synchronously without client cancellation destroying the DB context
	defer func() {
		if g.Budget != nil {
			telemetryCtx := context.WithoutCancel(ctx)
			latencyMs := time.Since(start).Milliseconds()
			stopReason := "end_turn"
			if ctx.Err() != nil {
				stopReason = "client_aborted"
			}
			
			g.Budget.Record(telemetryCtx, orgID, userID, task, model, string(providerCFWorkers), estimated, totalOutput/4,
				WithLatency(latencyMs),
				WithStopReason(stopReason),
			)
		}
	}()

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flush()
			break
		}
		
		var chunk struct {
			Response string `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			fmt.Fprintf(w, "data: %s\n\n", payload)
		} else {
			fmt.Fprintf(w, "data: %s\n\n", chunk.Response)
			totalOutput += len(chunk.Response)
		}
		flush()
	}
	
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("stream read error (aborted): %w", err)
	}

	return nil
}
// ============================================================
// Vercel AI Gateway — Anthropic Messages API
// ============================================================

// anthropicResponse is the parsed Anthropic Messages API response.
type anthropicResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text,omitempty"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name,omitempty"`
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content"`
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	StopReason string `json:"stop_reason"`
	Model      string `json:"model"`
}

// parseAnthropicResponse converts the raw Anthropic response to AIResponse.
func parseAnthropicResponse(raw []byte, model string, latencyMs int64) (AIResponse, error) {
	var resp anthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return AIResponse{}, fmt.Errorf("anthropic unmarshal: %w (body: %.500s)", err, string(raw))
	}

	result := AIResponse{
		Model:               model,
		Provider:            string(providerVercelGateway),
		InputTokens:         resp.Usage.InputTokens,
		OutputTokens:        resp.Usage.OutputTokens,
		CachedInputTokens:   resp.Usage.CacheReadInputTokens,
		CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
		LatencyMs:           latencyMs,
		StopReason:          resp.StopReason,
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			params := block.Input
			if !json.Valid(params) || len(params) == 0 {
				params = json.RawMessage("{}")
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:     block.ID,
				Name:   block.Name,
				Params: params,
			})
		}
	}

	return result, nil
}

// buildAnthropicMessages converts our internal Message format to Anthropic
// Messages API format, extracting system prompt separately.
// BuildAnthropicRequestBody standardizes the construction of the JSON payload for Anthropic's Messages API.
// CRITICAL: It explicitly injects Anthropic Prompt Caching markers `cache_control: {"type": "ephemeral"}`
// onto the system prompt block and the final tool definition to enforce massive cost reductions.
func BuildAnthropicRequestBody(model string, maxTokens int, messages []Message, tools []Tool) map[string]interface{} {
	var system []map[string]interface{}
	var chatMsgs []map[string]interface{}

	for _, m := range messages {
		switch m.Role {
		case "system":
			// 1. Prompt Caching: Inject cache_control on the System Prompt
			system = append(system, map[string]interface{}{
				"type":          "text",
				"text":          m.Content,
				"cache_control": map[string]string{"type": "ephemeral"},
			})
		case "user":
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role":    "user",
				"content": m.Content,
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				var contentBlocks []map[string]interface{}
				if m.Content != "" {
					contentBlocks = append(contentBlocks, map[string]interface{}{
						"type": "text",
						"text": m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					var input interface{}
					json.Unmarshal(tc.Params, &input)
					contentBlocks = append(contentBlocks, map[string]interface{}{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": input,
					})
				}
				chatMsgs = append(chatMsgs, map[string]interface{}{
					"role":    "assistant",
					"content": contentBlocks,
				})
			} else {
				chatMsgs = append(chatMsgs, map[string]interface{}{
					"role":    "assistant",
					"content": m.Content,
				})
			}
		case "tool":
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": m.ToolUseID,
						"content":     m.Content,
					},
				},
			})
		}
	}

	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   chatMsgs,
	}

	if len(system) > 0 {
		reqBody["system"] = system
	}

	if len(tools) > 0 {
		anthropicTools := buildAnthropicTools(tools)
		// 2. Prompt Caching: Inject cache_control on the final Tool definition block.
		// Combined with the system prompt cache_control above, this ensures the
		// static prefix (system + tools) exceeds the 4096 token minimum for Haiku 4.5.
		if len(anthropicTools) > 0 {
			anthropicTools[len(anthropicTools)-1]["cache_control"] = map[string]string{"type": "ephemeral"}
		}
		reqBody["tools"] = anthropicTools
	}

	return reqBody
}

// vercelAnthropicHeaders returns auth headers for Vercel Gateway Anthropic endpoint.
func (g *AIGateway) vercelAnthropicHeaders() map[string]string {
	return map[string]string{
		"x-api-key":         g.vercelGatewayKey,
		"anthropic-version": "2023-06-01",
		"Content-Type":      "application/json",
	}
}

// hashMessages returns a SHA-256 hex digest of message contents for cache keying.
func hashMessages(messages []Message) string {
	h := sha256.New()
	for _, m := range messages {
		h.Write([]byte(m.Role))
		h.Write([]byte(m.Content))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// callVercelGateway sends a completion via the Vercel AI Gateway Anthropic
// Messages API (POST /v1/messages). Supports prompt caching via cache_control.
func (g *AIGateway) callVercelGateway(ctx context.Context, task AITask, model string, messages []Message) (AIResponse, error) {
	start := time.Now()
	url := fmt.Sprintf("%s/messages", g.vercelGatewayURL)

	reqBody := BuildAnthropicRequestBody(model, maxTokensFor(task), messages, nil)

	resp, err := g.doRequest(ctx, url, "POST", g.vercelAnthropicHeaders(), reqBody)
	if err != nil {
		return AIResponse{}, fmt.Errorf("vercel gateway: %w", err)
	}

	result, err := parseAnthropicResponse(resp, model, time.Since(start).Milliseconds())
	if err != nil {
		return AIResponse{}, err
	}

	return result, nil
}

// callVercelGatewayWithTools sends a tool-calling request via the Vercel AI
// Gateway Anthropic Messages API with tool definitions and prompt caching.
func (g *AIGateway) callVercelGatewayWithTools(ctx context.Context, task AITask, model string, messages []Message, tools []Tool) (AIResponse, error) {
	start := time.Now()
	url := fmt.Sprintf("%s/messages", g.vercelGatewayURL)

	reqBody := BuildAnthropicRequestBody(model, maxTokensFor(task), messages, tools)

	resp, err := g.doRequest(ctx, url, "POST", g.vercelAnthropicHeaders(), reqBody)
	if err != nil {
		return AIResponse{}, fmt.Errorf("vercel gateway tool call: %w", err)
	}

	result, err := parseAnthropicResponse(resp, model, time.Since(start).Milliseconds())
	if err != nil {
		return AIResponse{}, err
	}

	return result, nil
}

// ============================================================
// Token estimation (rough pre-flight check)
// ============================================================

func estimateTokens(messages []Message) int {
	total := 0
	for _, m := range messages {
		// ~4 chars per token is a reasonable heuristic
		total += len(m.Content)/4 + 10
	}
	return total + 100 // buffer for response
}

// ============================================================
// TranscribeAudio — Whisper-large-v3-turbo via CF Workers AI
// ============================================================

type TranscribeResult struct {
	Text     string  `json:"text"`
	Language string  `json:"language,omitempty"`
	Duration float64 `json:"duration,omitempty"`
}

func (g *AIGateway) TranscribeAudio(ctx context.Context, audioBytes []byte, filename, languageCode string) (*TranscribeResult, error) {
	model := "@cf/openai/whisper-large-v3-turbo"
	url := fmt.Sprintf("%s/workers-ai/%s", g.gatewayURL, model)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile("audio", filename)
	if err != nil {
		return nil, fmt.Errorf("whisper: create form file: %w", err)
	}
	if _, err = fw.Write(audioBytes); err != nil {
		return nil, fmt.Errorf("whisper: write audio bytes: %w", err)
	}

	if languageCode != "" && languageCode != "auto" {
		if err = mw.WriteField("language", languageCode); err != nil {
			return nil, fmt.Errorf("whisper: write language field: %w", err)
		}
	}
	mw.Close()

	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("whisper: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.cfToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if g.cfGatewayToken != "" {
		req.Header.Set("cf-aig-authorization", "Bearer "+g.cfGatewayToken)
	}

	res, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("whisper: http do: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("whisper: read response: %w", err)
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("whisper: provider returned %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}

	var cfResp struct {
		Result struct {
			Text     string  `json:"text"`
			Language string  `json:"detected_language"`
			Duration float64 `json:"duration"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(data, &cfResp); err != nil {
		return nil, fmt.Errorf("whisper: unmarshal: %w (body: %s)", err, string(data))
	}

	return &TranscribeResult{
		Text:     strings.TrimSpace(cfResp.Result.Text),
		Language: cfResp.Result.Language,
		Duration: cfResp.Result.Duration,
	}, nil
}
