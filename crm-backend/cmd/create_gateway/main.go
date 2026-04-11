// cmd/create_gateway/main.go
// Creates (or verifies) the crm-ai-gateway using the Cloudflare AI Gateway REST API.
// Uses the same token format that works for Workers AI inference calls.
//
// Usage:
//   go run ./cmd/create_gateway <ACCOUNT_ID> <CF_AI_TOKEN>

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const gatewaySlug = "crm-ai-gateway"

func main() {
	accountID := os.Getenv("CF_ACCOUNT_ID")
	token := os.Getenv("CF_AI_TOKEN")

	args := os.Args[1:]
	if len(args) >= 1 && args[0] != "" {
		accountID = args[0]
	}
	if len(args) >= 2 && args[1] != "" {
		token = args[1]
	}

	if accountID == "" || token == "" {
		fmt.Println("Usage: go run ./cmd/create_gateway <ACCOUNT_ID> <CF_AI_TOKEN>")
		os.Exit(1)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	baseURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai-gateway/gateways", accountID)

	// ── Step 1: List existing gateways ───────────────────────
	fmt.Println("1. Checking for existing gateways...")
	listBody, listStatus := doRequest(client, "GET", baseURL, token, nil)
	fmt.Printf("   GET %s → %d\n", baseURL, listStatus)

	if listStatus == 200 {
		var listResp struct {
			Result []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"result"`
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(listBody, &listResp); err == nil && listResp.Success {
			for _, gw := range listResp.Result {
				fmt.Printf("   Found gateway: %s (id=%s)\n", gw.Name, gw.ID)
				if gw.ID == gatewaySlug || gw.Name == gatewaySlug {
					fmt.Printf("\n✅  Gateway '%s' already exists!\n", gatewaySlug)
					runFinalTest(client, accountID, token, gatewaySlug)
					return
				}
			}
		}
	} else {
		fmt.Printf("   Response: %s\n", truncate(string(listBody), 200))
	}

	// ── Step 2: Create the gateway ───────────────────────────
	fmt.Printf("\n2. Creating gateway '%s'...\n", gatewaySlug)
	payload := map[string]interface{}{
		"name":                         gatewaySlug,
		"slug":                         gatewaySlug,
		"collect_logs":                 true,
		"cache_invalidate_on_update":   false,
		"rate_limiting_enabled":        false,
	}
	payloadBytes, _ := json.Marshal(payload)

	createBody, createStatus := doRequest(client, "POST", baseURL, token, payloadBytes)
	fmt.Printf("   POST %s → %d\n", baseURL, createStatus)
	fmt.Printf("   Response: %s\n", truncate(string(createBody), 400))

	if createStatus == 200 || createStatus == 201 {
		fmt.Printf("\n✅  Gateway '%s' created!\n", gatewaySlug)
		runFinalTest(client, accountID, token, gatewaySlug)
	} else {
		fmt.Println("\n❌  Gateway creation failed.")
		fmt.Println("    The gateway must be created from the Cloudflare dashboard:")
		fmt.Printf("    https://dash.cloudflare.com/%s/ai/ai-gateway/overview\n", accountID)
		os.Exit(1)
	}
}

func runFinalTest(client *http.Client, accountID, token, gatewaySlug string) {
	fmt.Println("\n3. Running live inference test through gateway...")
	url := fmt.Sprintf(
		"https://gateway.ai.cloudflare.com/v1/%s/%s/cloudflare/@cf/google/embeddinggemma-300m",
		accountID, gatewaySlug,
	)
	body, _ := json.Marshal(map[string]interface{}{"text": []string{"gateway test ping"}})
	resp, status := doRequest(client, "POST", url, token, body)
	fmt.Printf("   POST gateway URL → %d\n", status)
	if status == 200 {
		var parsed struct {
			Result struct {
				Data [][]float32 `json:"data"`
			} `json:"result"`
		}
		json.Unmarshal(resp, &parsed)
		if len(parsed.Result.Data) > 0 && len(parsed.Result.Data[0]) > 0 {
			vec := parsed.Result.Data[0]
			fmt.Printf("   ✅  Gateway + model working! dims=%d, vec[0]=%.6f\n", len(vec), vec[0])
		}
	} else {
		fmt.Printf("   Response: %s\n", truncate(string(resp), 300))
	}
}

func doRequest(client *http.Client, method, url, token string, body []byte) ([]byte, int) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return []byte(err.Error()), 0
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return []byte(err.Error()), 0
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
