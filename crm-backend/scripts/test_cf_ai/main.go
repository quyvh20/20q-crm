// cmd/test_cf_ai/main.go
// Connectivity test for Cloudflare Workers AI — embeddinggemma-300m model.
//
// Tests two paths in order:
//   1. CF AI Gateway  (gateway.ai.cloudflare.com) — needs a gateway-specific auth token
//   2. Direct CF API  (api.cloudflare.com)         — works with any cfut_* user token
//
// The service code (internal/ai/embedding.go) uses the same fallback logic.
//
// Usage:
//   go run ./cmd/test_cf_ai <ACCOUNT_ID> <TOKEN> [GATEWAY_ID]
// Or set CF_ACCOUNT_ID / CF_AI_TOKEN / CF_AI_GATEWAY_ID in .env

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

const testText = "Hello from CRM semantic search test — Nguyen Van An, CEO, sales@acme.com"
const model = "@cf/google/embeddinggemma-300m"

func main() {
	accountID  := os.Getenv("CF_ACCOUNT_ID")
	token      := os.Getenv("CF_AI_TOKEN")
	gatewayID  := os.Getenv("CF_AI_GATEWAY_ID")
	gatewayTok := os.Getenv("CF_AI_GATEWAY_TOKEN")

	args := os.Args[1:]
	if len(args) >= 1 && args[0] != "" { accountID = args[0] }
	if len(args) >= 2 && args[1] != "" { token = args[1] }
	if len(args) >= 3 && args[2] != "" { gatewayID = args[2] }
	if len(args) >= 4 && args[3] != "" { gatewayTok = args[3] }

	if accountID == "" || token == "" {
		fmt.Println("❌  CF_ACCOUNT_ID or CF_AI_TOKEN is empty.")
		fmt.Println("    go run ./cmd/test_cf_ai <ACCOUNT_ID> <TOKEN> [GATEWAY_ID]")
		os.Exit(1)
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// ── Endpoints to test ────────────────────────────────────
	type endpoint struct {
		label string
		url   string
	}

	var endpoints []endpoint

	if gatewayID != "" {
		endpoints = append(endpoints, endpoint{
			label: "CF AI Gateway",
			// Correct provider prefix is /workers-ai/ (not /cloudflare/)
			url: fmt.Sprintf("https://gateway.ai.cloudflare.com/v1/%s/%s/workers-ai/%s", accountID, gatewayID, model),
		})
	}
	endpoints = append(endpoints, endpoint{
		label: "Direct CF API",
		url:   fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", accountID, model),
	})

	fmt.Printf("Model : %s\n", model)
	fmt.Printf("Text  : %q\n\n", testText)

	anySuccess := false

	for _, ep := range endpoints {
		fmt.Printf("── %s ──\n", ep.label)
		fmt.Printf("   URL: %s\n", ep.url)

		reqBody, _ := json.Marshal(map[string]interface{}{
			"text": []string{testText},
		})

		req, err := http.NewRequest("POST", ep.url, bytes.NewReader(reqBody))
		if err != nil {
			fmt.Printf("   ❌  Build request error: %v\n\n", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if ep.label == "CF AI Gateway" && gatewayTok != "" {
			req.Header.Set("cf-aig-authorization", "Bearer "+gatewayTok)
		}
		req.Header.Set("Content-Type", "application/json")

		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start).Round(time.Millisecond)

		if err != nil {
			fmt.Printf("   ❌  Network error: %v\n\n", err)
			continue
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		fmt.Printf("   HTTP %d  (%s)\n", resp.StatusCode, elapsed)

		if resp.StatusCode != 200 {
			msg := truncate(string(raw), 220)
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				fmt.Printf("   ❌  Auth failure — this token cannot access the %s endpoint.\n", ep.label)
				if ep.label == "CF AI Gateway" {
					fmt.Println("       ℹ️  The gateway needs a Workers AI token (not a user/cfut_ token).")
					fmt.Println("          The EmbeddingService will automatically fall back to the Direct CF API.")
				}
			} else {
				fmt.Printf("   ❌  Error body: %s\n", msg)
			}
			fmt.Println()
			continue
		}

		// Parse response
		var parsed struct {
			Result struct {
				Data  [][]float32 `json:"data"`
				Shape []int       `json:"shape"`
			} `json:"result"`
			Success bool `json:"success"`
		}
		if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Result.Data) == 0 {
			fmt.Printf("   ❌  Parse error or empty result: %v\n\n", err)
			continue
		}

		vec := parsed.Result.Data[0]
		fmt.Printf("   ✅  SUCCESS\n")
		fmt.Printf("       Embedding dims : %d\n", len(vec))
		fmt.Printf("       Shape          : %v\n", parsed.Result.Shape)
		fmt.Printf("       First 4 values : [%.6f, %.6f, %.6f, %.6f]\n\n",
			vec[0], vec[1], vec[2], vec[3])
		anySuccess = true
	}

	fmt.Println("══════════════════════════════════════════")
	if anySuccess {
		fmt.Println("🎉  Verification PASSED — embeddinggemma-300m is reachable!")
		fmt.Printf("    EmbeddingService will use the working endpoint automatically.\n")
		if gatewayID != "" {
			fmt.Println()
			fmt.Println("    💡 To enable the AI Gateway path (for observability/caching/analytics),")
			fmt.Println("       create a Workers AI API Token at dash.cloudflare.com and set it as CF_AI_TOKEN.")
			fmt.Println("       Your current cfut_* token works for the direct API only.")
		}
	} else {
		fmt.Println("💥  Verification FAILED — no endpoint succeeded.")
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
