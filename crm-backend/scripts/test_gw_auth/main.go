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

func main() {
	accountID := os.Getenv("CF_ACCOUNT_ID")
	gatewayID := "crm-ai-gateway"
	// Account token (Workers AI + AI Gateway permissions) — for Authorization header
	workersTok := os.Getenv("CF_AI_TOKEN")
	// Gateway-specific token (created when gateway was created) — for cf-aig-authorization header
	gatewayTok := os.Getenv("CF_AI_GATEWAY_TOKEN")

	if accountID == "" || workersTok == "" || gatewayTok == "" {
		fmt.Println("Missing required environment variables CF_ACCOUNT_ID, CF_AI_TOKEN, CF_AI_GATEWAY_TOKEN")
		os.Exit(1)
	}
	model := "@cf/google/embeddinggemma-300m"

	url := fmt.Sprintf(
		"https://gateway.ai.cloudflare.com/v1/%s/%s/workers-ai/%s",
		accountID, gatewayID, model,
	)

	body, _ := json.Marshal(map[string]interface{}{"text": []string{"gateway dual-auth test"}})

	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Authorization",        "Bearer "+workersTok)
	req.Header.Set("cf-aig-authorization", "Bearer "+gatewayTok)
	req.Header.Set("Content-Type",         "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil { fmt.Println("ERR:", err); return }
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	fmt.Printf("HTTP %d\n%s\n", resp.StatusCode, string(data))
}
