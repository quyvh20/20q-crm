package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EmbeddingService wraps CF Workers AI for vector generation.
// It tries the AI Gateway first; if that fails with 401/403 it falls back
// to the direct Cloudflare API (api.cloudflare.com), which works with
// any user token including cfut_* tokens.
type EmbeddingService struct {
	gatewayURL   string // https://gateway.ai.cloudflare.com/v1/{acct}/{gw}
	directURL    string // https://api.cloudflare.com/client/v4/accounts/{acct}/ai/run
	cfToken      string // Workers AI token (Authorization header)
	gatewayToken string // Gateway auth token (cf-aig-authorization header)
	httpClient   *http.Client
}

// embeddingModel is the Cloudflare Workers AI text embedding model.
// @cf/google/embeddinggemma-300m produces 768-dimensional vectors.
const embeddingModel = "@cf/google/embeddinggemma-300m"

func NewEmbeddingService(cfAccountID, cfAIGatewayID, cfToken, gatewayToken string) *EmbeddingService {
	return &EmbeddingService{
		gatewayURL:   fmt.Sprintf("https://gateway.ai.cloudflare.com/v1/%s/%s", cfAccountID, cfAIGatewayID),
		directURL:    fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run", cfAccountID),
		cfToken:      cfToken,
		gatewayToken: gatewayToken,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
	}
}

// EmbedText returns a 768-dimensional vector for the given text.
// It first attempts the AI Gateway; on 401/403 it retries via direct API.
func (s *EmbeddingService) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if s.cfToken == "" {
		return nil, fmt.Errorf("embedding service not configured: CF_AI_TOKEN is empty")
	}

	// embeddinggemma-300m accepts { "text": ["...string..."] }
	reqBody, _ := json.Marshal(map[string]interface{}{
		"text": []string{text},
	})

	// Try gateway first, fall back to direct API on auth failure
	// Gateway path: {gatewayURL}/workers-ai/{model}  ← correct provider prefix
	endpoints := []struct{ label, url string }{
		{"gateway", fmt.Sprintf("%s/workers-ai/%s", s.gatewayURL, embeddingModel)},
		{"direct", fmt.Sprintf("%s/%s", s.directURL, embeddingModel)},
	}

	for _, ep := range endpoints {
		useGW := ep.label == "gateway"
		vec, err, retry := s.callEndpoint(ctx, ep.url, reqBody, useGW)
		if err == nil {
			return vec, nil
		}
		if !retry {
			return nil, fmt.Errorf("[%s] %w", ep.label, err)
		}
		// 401/403 → try next endpoint
	}

	return nil, fmt.Errorf("all embedding endpoints failed — check CF_AI_TOKEN and gateway configuration")
}

// callEndpoint makes one embedding request.
// Returns (vec, nil, false) on success,
// (nil, err, false) on a hard error,
// (nil, err, true) on 401/403 to trigger fallback.
func (s *EmbeddingService) callEndpoint(ctx context.Context, url string, body []byte, useGatewayToken bool) ([]float32, error, bool) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err, false
	}
	req.Header.Set("Authorization", "Bearer "+s.cfToken)
	// Add gateway-specific auth header when calling through the AI Gateway
	if useGatewayToken && s.gatewayToken != "" {
		req.Header.Set("cf-aig-authorization", "Bearer "+s.gatewayToken)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err), false
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err, false
	}

	// 401/403 → trigger fallback to next endpoint
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, fmt.Errorf("auth failed (%d)", res.StatusCode), true
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("embedding API returned %d: %s", res.StatusCode, string(data)), false
	}

	// CF response: { "result": { "data": [[...768 floats...]] } }
	var cfResp struct {
		Result struct {
			Data [][]float32 `json:"data"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(data, &cfResp); err != nil {
		return nil, fmt.Errorf("embedding unmarshal: %w", err), false
	}
	if len(cfResp.Result.Data) == 0 {
		return nil, fmt.Errorf("empty embedding result"), false
	}

	return cfResp.Result.Data[0], nil, false
}

// EmbedContact builds a rich text representation of a contact for embedding.
func EmbedContact(firstName, lastName string, email, phone, company *string, customFields map[string]interface{}) string {
	parts := []string{firstName, lastName}
	if email != nil {
		parts = append(parts, *email)
	}
	if phone != nil {
		parts = append(parts, *phone)
	}
	if company != nil {
		parts = append(parts, *company)
	}
	for k, v := range customFields {
		parts = append(parts, fmt.Sprintf("%s: %v", k, v))
	}

	result := ""
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i > 0 {
			result += " "
		}
		result += p
	}
	return result
}
