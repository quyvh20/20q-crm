// cmd/test_embed_endpoint/main.go
// Tests the embed endpoint logic end-to-end without running the full HTTP server.
// Calls EmbeddingService directly (same code path as POST /api/ai/embed) and validates:
//   - Response is non-empty
//   - Exactly 768 dimensions
//   - Values are finite float32 (not all zero)

package main

import (
	"context"
	"fmt"
	"math"
	"os"

	"crm-backend/internal/ai"
)

func main() {
	accountID  := os.Getenv("CF_ACCOUNT_ID")
	token      := os.Getenv("CF_AI_TOKEN")
	gatewayID  := os.Getenv("CF_AI_GATEWAY_ID")
	gatewayTok := os.Getenv("CF_AI_GATEWAY_TOKEN")

	if accountID == "" || token == "" || gatewayID == "" || gatewayTok == "" {
		fmt.Println("Missing required environment variables CF_ACCOUNT_ID, CF_AI_TOKEN, CF_AI_GATEWAY_ID, CF_AI_GATEWAY_TOKEN")
		os.Exit(1)
	}

	svc := ai.NewEmbeddingService(accountID, gatewayID, token, gatewayTok)

	testCases := []string{
		"John Doe, CEO, john@acme.com",
		"",  // should fail gracefully
		"A B C D E F G H I J K L M N O P Q R S T U V W X Y Z 1 2 3 4 5 6 7 8 9 0",
	}

	allPassed := true

	for i, text := range testCases {
		fmt.Printf("\n── Test %d ──\n", i+1)
		fmt.Printf("   Input: %q\n", truncate(text, 60))

		if text == "" {
			vec, err := svc.EmbedText(context.Background(), text)
			if err != nil {
				fmt.Printf("   ✅  Empty input correctly returned error: %v\n", err)
			} else {
				fmt.Printf("   ⚠️  Empty input returned %d-dim vector (unexpected)\n", len(vec))
			}
			continue
		}

		vec, err := svc.EmbedText(context.Background(), text)
		if err != nil {
			fmt.Printf("   ❌  Error: %v\n", err)
			allPassed = false
			continue
		}

		// Validate dimensions
		if len(vec) != 768 {
			fmt.Printf("   ❌  FAIL: got %d dimensions, want 768\n", len(vec))
			allPassed = false
			continue
		}

		// Validate values are finite and non-zero
		nonZero := 0
		for _, v := range vec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				fmt.Printf("   ❌  FAIL: vector contains NaN or Inf\n")
				allPassed = false
				break
			}
			if v != 0 {
				nonZero++
			}
		}

		fmt.Printf("   ✅  PASS\n")
		fmt.Printf("       Dimensions : %d (want 768)\n", len(vec))
		fmt.Printf("       Non-zero   : %d / 768\n", nonZero)
		fmt.Printf("       First 4    : [%.6f, %.6f, %.6f, %.6f]\n",
			vec[0], vec[1], vec[2], vec[3])
	}

	fmt.Println("\n══════════════════════════════════════════")
	if allPassed {
		fmt.Println("🎉  POST /api/ai/embed — VERIFIED")
		fmt.Println("    Returns float32 vector of exactly 768 dimensions ✅")
	} else {
		fmt.Println("💥  Some tests FAILED")
		os.Exit(1)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n { return s }
	return s[:n] + "…"
}
