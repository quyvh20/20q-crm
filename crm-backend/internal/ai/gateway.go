package ai

import (
	"bufio"
	"bytes"
	"context"

	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
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
	TaskMeetingSummary: true,
	TaskDealScore:      true,
	TaskAnalytics:      true,
	TaskFollowup:       true,
}

func IsAdvancedTask(t AITask) bool { return advancedTasks[t] }

// ============================================================
// Provider constants
// ============================================================

type provider string

const (
	providerCFWorkers provider = "cloudflare"
)

// Task → primary provider mapping
// All tasks route through Cloudflare Workers AI (@cf/moonshotai/kimi-k2.6)
var taskPrimaryProvider = map[AITask]provider{
	TaskEmailCompose:      providerCFWorkers,
	TaskAssistantChat:     providerCFWorkers,
	TaskMeetingSummary:    providerCFWorkers,
	TaskDealScore:         providerCFWorkers,
	TaskAnalytics:         providerCFWorkers,
	TaskSentiment:         providerCFWorkers,
	TaskFollowup:          providerCFWorkers,
	TaskVoiceIntelligence: providerCFWorkers,
	TaskCommandCenter:     providerCFWorkers,
}

// Task → model mapping per provider
// Optimized per-task: use the cheapest model that can handle the job well.
// Pricing reference (per M tokens, input/output):
//   llama-3.2-1b:          $0.027 / $0.201  — tiny, good for sentiment/short JSON
//   qwen3-30b-a3b-fp8:     $0.051 / $0.335  — MoE, background tasks (hallucinates for chat)
//   llama-3.2-3b:          $0.051 / $0.335  — small but capable for structured output
//   kimi-k2.6:             $0.950 / $4.000  — frontier 1T, best reasoning on Workers AI
var taskModels = map[AITask]map[provider]string{
	TaskAssistantChat:     {providerCFWorkers: "@cf/moonshotai/kimi-k2.6"},
	TaskCommandCenter:     {providerCFWorkers: "@cf/moonshotai/kimi-k2.6"},
	TaskEmailCompose:      {providerCFWorkers: "@cf/qwen/qwen3-30b-a3b-fp8"},
	TaskMeetingSummary:    {providerCFWorkers: "@cf/qwen/qwen3-30b-a3b-fp8"},
	TaskDealScore:         {providerCFWorkers: "@cf/meta/llama-3.2-3b-instruct"},
	TaskAnalytics:         {providerCFWorkers: "@cf/qwen/qwen3-30b-a3b-fp8"},
	TaskSentiment:         {providerCFWorkers: "@cf/meta/llama-3.2-1b-instruct"},
	TaskFollowup:          {providerCFWorkers: "@cf/meta/llama-3.2-3b-instruct"},
	TaskVoiceIntelligence: {providerCFWorkers: "@cf/qwen/qwen3-30b-a3b-fp8"},
}

// taskFallbackModels — tried when the primary model fails (timeout, error, empty response).
var taskFallbackModels = map[AITask][]string{
	TaskAssistantChat: {"@cf/qwen/qwen3-30b-a3b-fp8"},
	TaskCommandCenter: {"@cf/qwen/qwen3-30b-a3b-fp8"},
	TaskEmailCompose:  {"@cf/moonshotai/kimi-k2.6"},
}

// taskMaxTokens enforces strict output boundaries based on empirically measured p99 usage
var taskMaxTokens = map[AITask]int{
	TaskSentiment:         100,
	TaskDealScore:         500,
	TaskFollowup:          400,
	TaskEmailCompose:      800,
	TaskAssistantChat:     2048,
	TaskCommandCenter:     2048,
	TaskMeetingSummary:    1500,
	TaskAnalytics:         1500,
	TaskVoiceIntelligence: 2000,
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
	gatewayURL     string
	cfToken        string
	cfGatewayToken string
	httpClient     *http.Client
	Budget         *BudgetGuard
	logger         *zap.Logger
}

func NewAIGateway(cfAccountID, cfAIGatewayID, cfToken string, budget *BudgetGuard, logger *zap.Logger, cfGatewayToken ...string) *AIGateway {
	gwTok := ""
	if len(cfGatewayToken) > 0 {
		gwTok = cfGatewayToken[0]
	}
	return &AIGateway{
		gatewayURL:     fmt.Sprintf("https://gateway.ai.cloudflare.com/v1/%s/%s", cfAccountID, cfAIGatewayID),
		cfToken:        cfToken,
		cfGatewayToken: gwTok,
		httpClient:     &http.Client{Timeout: 120 * time.Second},
		Budget:         budget,
		logger:         logger,
	}
}

// Complete runs a full inference call with budget check + fallback.
func (g *AIGateway) Complete(ctx context.Context, orgID, userID uuid.UUID, task AITask, messages []Message) (AIResponse, error) {
	estimated := estimateTokens(messages)

	if g.Budget != nil {
		if err := g.Budget.Check(ctx, orgID, task, estimated); err != nil {
			return AIResponse{}, err
		}
	}

	// Try primary model, then fallbacks if available
	primaryModel := g.modelFor(task, providerCFWorkers)
	modelsToTry := []string{primaryModel}
	for _, fb := range taskFallbackModels[task] {
		if fb != primaryModel {
			modelsToTry = append(modelsToTry, fb)
		}
	}

	var result AIResponse
	var err error
	for _, model := range modelsToTry {
		result, err = g.callCFWorkers(ctx, task, model, messages)
		if err == nil && result.Content != "" {
			break
		}
		if err == nil && result.Content == "" {
			err = fmt.Errorf("model %s returned empty response", model)
		}
		g.logger.Warn("model failed, trying next",
			zap.String("model", model),
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
		)
	}

	return result, nil
}

// CompleteWithTools runs inference with tool definitions via CF Workers AI.
func (g *AIGateway) CompleteWithTools(ctx context.Context, orgID, userID uuid.UUID, task AITask, messages []Message, tools []Tool) (AIResponse, error) {
	estimated := 5000 // generous estimate for tool calls
	if g.Budget != nil {
		if err := g.Budget.Check(ctx, orgID, task, estimated); err != nil {
			return AIResponse{}, err
		}
	}

	model := g.modelFor(task, providerCFWorkers)
	result, err := g.callCFWorkersWithTools(ctx, task, model, messages, tools)

	// Fallback: try alternative models if primary fails
	if (err != nil || (result.Content == "" && len(result.ToolCalls) == 0)) && len(taskFallbackModels[task]) > 0 {
		for _, fbModel := range taskFallbackModels[task] {
			if fbModel == model {
				continue
			}
			g.logger.Warn("primary model failed for tools, trying fallback",
				zap.String("primary", model),
				zap.String("fallback", fbModel),
				zap.Error(err))
			result, err = g.callCFWorkersWithTools(ctx, task, fbModel, messages, tools)
			if err == nil && (result.Content != "" || len(result.ToolCalls) > 0) {
				break
			}
		}
	}

	if err != nil {
		g.logger.Error("CF Workers tool call failed — returning graceful fallback",
			zap.String("model", model),
			zap.String("task", string(task)),
			zap.Error(err))
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
		)
	}

	return result, nil
}

// callCFWorkersWithTools calls Cloudflare Workers AI using the OpenAI-compatible
// function-calling protocol and parses the tool_calls from the response.
func (g *AIGateway) callCFWorkersWithTools(ctx context.Context, task AITask, model string, messages []Message, tools []Tool) (AIResponse, error) {
	url := g.resolveModelURL(model)

	chatMsgs := buildOpenAIMessages(messages)

	reqBody := map[string]interface{}{
		"model":    model,
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

	// Hosted/pinned models (Kimi K2.6, Qwen3, etc.) return OpenAI-compatible format:
	//   {"choices":[{"message":{"content":"...","tool_calls":[{"id":"...","type":"function","function":{"name":"...","arguments":"{...}"}}]}}],"usage":{...}}
	// Legacy Workers AI format:
	//   {"result":{"response":null,"tool_calls":[{"name":"fn","arguments":{...}}],"usage":{...}}}

	// Try OpenAI-compatible format first
	var oaiResp struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"` // JSON string in OpenAI format
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(resp, &oaiResp); err == nil && len(oaiResp.Choices) > 0 {
		msg := oaiResp.Choices[0].Message
		result := AIResponse{
			Content:      msg.Content,
			Model:        model,
			Provider:     string(providerCFWorkers),
			InputTokens:  oaiResp.Usage.PromptTokens,
			OutputTokens: oaiResp.Usage.CompletionTokens,
		}
		for _, tc := range msg.ToolCalls {
			params := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(params) || len(params) == 0 {
				params = json.RawMessage("{}")
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:     tc.ID,
				Name:   tc.Function.Name,
				Params: params,
			})
		}
		if msg.Content != "" || len(result.ToolCalls) > 0 {
			return result, nil
		}
	}

	// Fallback: legacy Workers AI format
	var cfResp struct {
		Result struct {
			Response  interface{} `json:"response"` // null or string
			ToolCalls []struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"` // already a JSON object
			} `json:"tool_calls"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &cfResp); err != nil {
		return AIResponse{}, fmt.Errorf("cf workers tool unmarshal: %w (body: %.500s)", err, string(resp))
	}

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
		InputTokens:  cfResp.Result.Usage.PromptTokens,
		OutputTokens: cfResp.Result.Usage.CompletionTokens,
	}

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



func (g *AIGateway) routePrimary(_ AITask) provider {
	return providerCFWorkers
}

// buildFallbackChain returns CF Workers only — all AI runs on Cloudflare.
func (g *AIGateway) buildFallbackChain(_ provider) []provider {
	return []provider{providerCFWorkers}
}

// ============================================================
// Provider-specific call implementations
// ============================================================

func (g *AIGateway) callProvider(ctx context.Context, task AITask, _ provider, messages []Message) (AIResponse, error) {
	model := g.modelFor(task, providerCFWorkers)
	return g.callCFWorkers(ctx, task, model, messages)
}

func (g *AIGateway) modelFor(task AITask, _ provider) string {
	if models, ok := taskModels[task]; ok {
		if m, ok := models[providerCFWorkers]; ok {
			return m
		}
	}
	return "@cf/moonshotai/kimi-k2.6"
}

// resolveModelURL returns the correct AI Gateway URL for the given model.
// - "anthropic/..." models → /compat/chat/completions (proxied via AI Gateway)
// - "@cf/..." models       → /workers-ai/v1/chat/completions (native Workers AI)
func (g *AIGateway) resolveModelURL(model string) string {
	if strings.HasPrefix(model, "anthropic/") {
		return fmt.Sprintf("%s/compat/chat/completions", g.gatewayURL)
	}
	return fmt.Sprintf("%s/workers-ai/v1/chat/completions", g.gatewayURL)
}

// callCFWorkers — CF AI Gateway → Workers AI or proxied provider (OpenAI-compatible endpoint)
func (g *AIGateway) callCFWorkers(ctx context.Context, task AITask, model string, messages []Message) (AIResponse, error) {
	url := g.resolveModelURL(model)

	chatMsgs := buildOpenAIMessages(messages)

	maxTokens := taskMaxTokens[task]
	if maxTokens == 0 {
		maxTokens = 1024
	}

	body := map[string]interface{}{
		"model":      model,
		"messages":   chatMsgs,
		"max_tokens": maxTokens,
	}

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"Authorization": "Bearer " + g.cfToken,
		"Content-Type":  "application/json",
	}, body)
	if err != nil {
		return AIResponse{}, err
	}

	// Hosted/pinned models (Kimi K2.6, Qwen3, etc.) return OpenAI-compatible format:
	//   {"id":"...","choices":[{"message":{"content":"..."}}],"usage":{"prompt_tokens":N,"completion_tokens":M}}
	// Older Workers AI models return legacy format:
	//   {"result":{"response":"...","usage":{"prompt_tokens":N,"completion_tokens":M}}}
	responseText, inputTokens, outputTokens := parseCFResponse(resp)

	if responseText == "" {
		g.logger.Warn("CF Workers AI returned empty response",
			zap.String("model", model),
			zap.String("raw_response", string(resp[:min(len(resp), 500)])),
		)
	}

	return AIResponse{
		Content:      sanitizeKimiResponse(responseText),
		Model:        model,
		Provider:     string(providerCFWorkers),
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}

// buildOpenAIMessages converts internal Message structs to OpenAI-compatible format.
func buildOpenAIMessages(messages []Message) []map[string]interface{} {
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
			chatMsgs = append(chatMsgs, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": m.ToolUseID,
				"content":      m.Content,
			})
		}
	}
	return chatMsgs
}

// parseCFResponse handles both OpenAI-compatible and legacy Workers AI response formats.
func parseCFResponse(data []byte) (content string, inputTokens, outputTokens int) {
	// Try OpenAI-compatible format first (used by hosted/pinned models)
	var oaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &oaiResp); err == nil && len(oaiResp.Choices) > 0 && oaiResp.Choices[0].Message.Content != "" {
		return oaiResp.Choices[0].Message.Content, oaiResp.Usage.PromptTokens, oaiResp.Usage.CompletionTokens
	}

	// Fallback: legacy Workers AI format
	var cfResp struct {
		Result struct {
			Response interface{} `json:"response"`
			Usage    struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &cfResp); err == nil && cfResp.Result.Response != nil {
		switch v := cfResp.Result.Response.(type) {
		case string:
			content = v
		default:
			b, _ := json.Marshal(v)
			content = string(b)
		}
		return content, cfResp.Result.Usage.PromptTokens, cfResp.Result.Usage.CompletionTokens
	}

	return "", 0, 0
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

	// Build models to try: primary + fallbacks
	modelsToTry := []string{model}
	for _, fb := range taskFallbackModels[task] {
		if fb != model {
			modelsToTry = append(modelsToTry, fb)
		}
	}

	allMsgs := buildOpenAIMessages(messages)

	// Try each model until one responds successfully.
	// Fallback is only possible BEFORE we commit SSE headers to the client.
	var resp *http.Response
	var lastErr error
	for _, tryModel := range modelsToTry {
		body := map[string]interface{}{
			"model":    tryModel,
			"messages": allMsgs,
			"stream":   true,
		}

		bodyBytes, err := json.Marshal(body)
		if err != nil {
			return err
		}

		tryURL := g.resolveModelURL(tryModel)
		req, err := http.NewRequestWithContext(ctx, "POST", tryURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+g.cfToken)
		if g.cfGatewayToken != "" {
			req.Header.Set("cf-aig-authorization", "Bearer "+g.cfGatewayToken)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")

		resp, err = g.httpClient.Do(req)
		if err != nil {
			lastErr = err
			g.logger.Warn("stream model failed, trying next",
				zap.String("model", tryModel), zap.Error(err))
			continue
		}

		if resp.StatusCode >= 400 {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("stream provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
			g.logger.Warn("stream model returned error, trying next",
				zap.String("model", tryModel), zap.Int("status", resp.StatusCode))
			continue
		}

		// Success — use this model
		model = tryModel
		break
	}

	if resp == nil || (resp.StatusCode >= 400) {
		if lastErr != nil {
			if isTimeoutErr(lastErr) {
				return ErrAITimeout{Provider: g.gatewayURL, After: 5}
			}
			return fmt.Errorf("stream request failed: %w", lastErr)
		}
		return fmt.Errorf("stream: all models failed")
	}
	defer resp.Body.Close()

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
			// OpenAI-compatible format (used by hosted/pinned models like Kimi K2.6)
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			// Legacy Workers AI format
			Response string `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			fmt.Fprintf(w, "data: %s\n\n", payload)
		} else {
			// Extract text from whichever format is present
			text := chunk.Response
			if text == "" && len(chunk.Choices) > 0 {
				text = chunk.Choices[0].Delta.Content
			}
			if text != "" {
				text = sanitizeKimiResponse(text)
				if text != "" {
					fmt.Fprintf(w, "data: %s\n\n", text)
					totalOutput += len(text)
				}
			}
		}
		flush()
	}
	
	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("stream read error (aborted): %w", err)
	}

	return nil
}


// ============================================================
// Kimi response sanitizer — strip leaked special tokens
// ============================================================

// kimiTokenPattern matches Kimi K2.6's internal special tokens that leak into
// streaming/non-streaming responses when the model attempts tool use without
// proper tool-calling context. These are NOT user-visible content.
var kimiTokenPattern = regexp.MustCompile(`<\|(?:tool_calls_section_begin|tool_calls_section_end|tool_call_begin|tool_call_end|tool_call_argument_begin|tool_call_argument_end|tool_sep|im_end|im_start)\|>`)

// kimiFuncCallPattern matches Kimi's hallucinated function call syntax in all observed forms:
//   "functions.search_contacts:0"
//   "contact.functions.search_deals:1{"sort_by":"value","limit":10}"
//   "text.functions.navigate_to:0"
// These appear when Kimi tries to use tools as plain text instead of proper tool_calls.
var kimiFuncCallPattern = regexp.MustCompile(`(?:\w+\.)*functions\.[a-z_]+:\d+(?:\{[^}]*\})?`)

// kimiJSONArgBlock matches orphaned JSON argument blocks that Kimi emits on their own lines
// after a function call line, e.g.:
//   {"sort_by": "value", "limit": 10}
// Only matches when the JSON is on its own line and looks like tool arguments.
var kimiJSONArgBlock = regexp.MustCompile(`(?m)^\s*\{"[a-z_]+":\s*(?:"[^"]*"|\d+|true|false)(?:,\s*"[a-z_]+":\s*(?:"[^"]*"|\d+|true|false))*\}\s*$`)

// sanitizeKimiResponse strips Kimi's leaked internal tokens from response text.
// This prevents raw markup like <|tool_calls_section_begin|> from appearing in
// the user-facing chat UI.
func sanitizeKimiResponse(text string) string {
	cleaned := kimiTokenPattern.ReplaceAllString(text, "")
	// Strip hallucinated function call syntax (all observed forms)
	cleaned = kimiFuncCallPattern.ReplaceAllString(cleaned, "")
	// Strip orphaned JSON argument blocks on their own lines
	cleaned = kimiJSONArgBlock.ReplaceAllString(cleaned, "")
	// Collapse multiple newlines left by stripping
	cleaned = regexp.MustCompile(`\n{3,}`).ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
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

	// CF Workers AI Whisper expects JSON with base64-encoded audio
	encoded := base64.StdEncoding.EncodeToString(audioBytes)
	body := map[string]interface{}{
		"audio": encoded,
		"task":  "transcribe",
	}
	if languageCode != "" && languageCode != "auto" {
		body["language"] = languageCode
	}

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"Authorization": "Bearer " + g.cfToken,
		"Content-Type":  "application/json",
	}, body)
	if err != nil {
		return nil, fmt.Errorf("whisper: %w", err)
	}

	var cfResp struct {
		Result struct {
			Text     string  `json:"text"`
			Language string  `json:"detected_language"`
			Duration float64 `json:"duration"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(resp, &cfResp); err != nil {
		return nil, fmt.Errorf("whisper: unmarshal: %w (body: %.500s)", err, string(resp))
	}

	return &TranscribeResult{
		Text:     strings.TrimSpace(cfResp.Result.Text),
		Language: cfResp.Result.Language,
		Duration: cfResp.Result.Duration,
	}, nil
}
