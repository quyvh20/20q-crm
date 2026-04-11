package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	TaskEmailCompose   AITask = "email_compose"
	TaskAssistantChat  AITask = "assistant_chat"
	TaskMeetingSummary AITask = "meeting_summary"
	TaskDealScore      AITask = "deal_score"
	TaskAnalytics      AITask = "analytics_insight"
	TaskEmbedding      AITask = "embedding"
	TaskVoiceSTT       AITask = "voice_stt"
	TaskSentiment      AITask = "sentiment"
	TaskFollowup       AITask = "followup_suggest"
)

// advancedTasks are only available to pro+ plans
var advancedTasks = map[AITask]bool{
	TaskMeetingSummary: true,
	TaskDealScore:      true,
	TaskAnalytics:      true,
	TaskVoiceSTT:       true,
	TaskFollowup:       true,
}

func IsAdvancedTask(t AITask) bool { return advancedTasks[t] }

// ============================================================
// Provider constants
// ============================================================

type provider string

const (
	providerCFWorkers provider = "cloudflare"
	providerAnthropic provider = "anthropic"
)

// Task → primary provider mapping
var taskPrimaryProvider = map[AITask]provider{
	TaskEmailCompose:   providerAnthropic,
	TaskAssistantChat:  providerCFWorkers,
	TaskMeetingSummary: providerAnthropic,
	TaskDealScore:      providerCFWorkers,
	TaskAnalytics:      providerAnthropic,
	TaskSentiment:      providerCFWorkers,
	TaskFollowup:       providerAnthropic,
}

// Task → model mapping per provider
var taskModels = map[AITask]map[provider]string{
	TaskEmailCompose:   {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskAssistantChat:  {providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskMeetingSummary: {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskDealScore:      {providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskAnalytics:      {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
	TaskSentiment:      {providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct", providerAnthropic: "claude-3-5-haiku-20241022"},
	TaskFollowup:       {providerAnthropic: "claude-3-5-haiku-20241022", providerCFWorkers: "@cf/meta/llama-3.1-8b-instruct"},
}

// ============================================================
// Message / Response types
// ============================================================

type Message struct {
	Role    string `json:"role"`    // "system" | "user" | "assistant"
	Content string `json:"content"`
}

type AIResponse struct {
	Content      string
	Model        string
	Provider     string
	InputTokens  int
	OutputTokens int
}

// ============================================================
// AIGateway
// ============================================================

type AIGateway struct {
	gatewayURL   string
	cfToken      string
	anthropicKey string
	httpClient   *http.Client
	Budget       *BudgetGuard
	logger       *zap.Logger
}

func NewAIGateway(cfAccountID, cfAIGatewayID, cfToken, anthropicKey string, budget *BudgetGuard, logger *zap.Logger) *AIGateway {
	return &AIGateway{
		gatewayURL:   fmt.Sprintf("https://gateway.ai.cloudflare.com/v1/%s/%s", cfAccountID, cfAIGatewayID),
		cfToken:      cfToken,
		anthropicKey: anthropicKey,
		httpClient:   &http.Client{Timeout: 60 * time.Second},
		Budget:       budget,
		logger:       logger,
	}
}

// Complete runs a full inference call with budget check + fallback.
func (g *AIGateway) Complete(ctx context.Context, orgID, userID uuid.UUID, task AITask, messages []Message) (AIResponse, error) {
	estimated := estimateTokens(messages)

	if err := g.Budget.Check(ctx, orgID, task, estimated); err != nil {
		return AIResponse{}, err
	}

	primaryP := g.routePrimary(task)
	result, err := g.callProvider(ctx, task, primaryP, messages)
	if err != nil {
		g.logger.Warn("primary provider failed, trying fallback",
			zap.String("task", string(task)),
			zap.String("primary", string(primaryP)),
			zap.Error(err))
		fallbackP := g.routeFallback(task, primaryP)
		if fallbackP != "" {
			result, err = g.callProvider(ctx, task, fallbackP, messages)
		}
	}

	if err != nil {
		return AIResponse{}, fmt.Errorf("all providers failed: %w", err)
	}

	// Persist usage asynchronously
	go g.Budget.Record(ctx, orgID, userID, task, result.Model, result.Provider, result.InputTokens, result.OutputTokens)

	return result, nil
}

func (g *AIGateway) routePrimary(task AITask) provider {
	if p, ok := taskPrimaryProvider[task]; ok {
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

// ============================================================
// Provider-specific call implementations
// ============================================================

func (g *AIGateway) callProvider(ctx context.Context, task AITask, p provider, messages []Message) (AIResponse, error) {
	model := g.modelFor(task, p)
	switch p {
	case providerAnthropic:
		return g.callAnthropic(ctx, model, messages)
	case providerCFWorkers:
		return g.callCFWorkers(ctx, model, messages)
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
	if p == providerAnthropic {
		return "claude-3-5-haiku-20241022"
	}
	return "@cf/meta/llama-3.1-8b-instruct"
}

// callCFWorkers — CF AI Gateway → Cloudflare Workers AI
func (g *AIGateway) callCFWorkers(ctx context.Context, model string, messages []Message) (AIResponse, error) {
	// Correct gateway path: {gatewayURL}/workers-ai/{model}
	url := fmt.Sprintf("%s/workers-ai/%s", g.gatewayURL, model)

	// Split system from user messages (CF Workers AI has separate system field)
	var system string
	var chatMsgs []map[string]string
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
		} else {
			chatMsgs = append(chatMsgs, map[string]string{"role": m.Role, "content": m.Content})
		}
	}

	body := map[string]interface{}{
		"messages": chatMsgs,
	}
	if system != "" {
		body["system"] = system
	}

	resp, err := g.doRequest(ctx, url, "POST", map[string]string{
		"Authorization": "Bearer " + g.cfToken,
		"Content-Type":  "application/json",
	}, body)
	if err != nil {
		return AIResponse{}, err
	}

	// CF response: { "result": { "response": "...", "usage": { "input_tokens": N, "output_tokens": M } } }
	var cfResp struct {
		Result struct {
			Response string `json:"response"`
			Usage    struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(resp, &cfResp); err != nil {
		return AIResponse{}, fmt.Errorf("cf workers unmarshal: %w", err)
	}

	return AIResponse{
		Content:      cfResp.Result.Response,
		Model:        model,
		Provider:     string(providerCFWorkers),
		InputTokens:  cfResp.Result.Usage.InputTokens,
		OutputTokens: cfResp.Result.Usage.OutputTokens,
	}, nil
}

// callAnthropic — CF AI Gateway → Anthropic (Claude)
func (g *AIGateway) callAnthropic(ctx context.Context, model string, messages []Message) (AIResponse, error) {
	url := fmt.Sprintf("%s/anthropic/v1/messages", g.gatewayURL)

	var system string
	var chatMsgs []map[string]string
	for _, m := range messages {
		if m.Role == "system" {
			system = m.Content
		} else {
			chatMsgs = append(chatMsgs, map[string]string{"role": m.Role, "content": m.Content})
		}
	}

	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 2048,
		"messages":   chatMsgs,
	}
	if system != "" {
		reqBody["system"] = system
	}

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
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("provider returned %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}

	return data, nil
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
