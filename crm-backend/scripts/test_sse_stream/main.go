package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	cfToken := os.Getenv("CF_AI_TOKEN")
	cfGWToken := os.Getenv("CF_AI_GATEWAY_TOKEN")
	accountID := os.Getenv("CF_ACCOUNT_ID")
	gatewayID := os.Getenv("CF_AI_GATEWAY_ID")
	if gatewayID == "" {
		gatewayID = "crm-ai-gateway"
	}

	// ── 1. Test CF Workers AI streaming directly ─────────────────────────────
	fmt.Println("=== Testing CF Workers AI streaming directly ===")
	url := fmt.Sprintf(
		"https://gateway.ai.cloudflare.com/v1/%s/%s/workers-ai/@cf/meta/llama-3.1-8b-instruct",
		accountID, gatewayID,
	)
	body := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "user", "content": "In exactly one sentence, what is a CRM system?"},
		},
		"stream": true,
	}
	bodyBytes, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+cfToken)
	req.Header.Set("cf-aig-authorization", "Bearer "+cfGWToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Printf("HTTP Status: %d\nContent-Type: %s\n\n", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		fmt.Println("Error body:", string(data))
		os.Exit(1)
	}

	fmt.Println("--- RAW SSE CHUNKS ARRIVING ---")
	scanner := bufio.NewScanner(resp.Body)
	chunkCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		chunkCount++
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			fmt.Printf("\n[DONE] — received %d chunks\n", chunkCount)
			break
		}
		var chunk struct {
			Response string `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
			fmt.Printf("chunk #%d: %q\n", chunkCount, chunk.Response)
		} else {
			fmt.Printf("chunk #%d (raw): %s\n", chunkCount, payload)
		}
	}

	// ── 2. Test the live /api/ai/chat SSE endpoint ───────────────────────────
	fmt.Println("\n=== Testing POST /api/ai/chat against production ===")
	backendURL := "https://20q-crm-production.up.railway.app"

	// Register fresh user
	ts := time.Now().Unix()
	regBody, _ := json.Marshal(map[string]string{
		"org_name":   "SSETest",
		"first_name": "SSE",
		"last_name":  "Test",
		"email":      fmt.Sprintf("ssetest%d@test.com", ts),
		"password":   "Test1234!",
	})
	regResp, err := http.Post(backendURL+"/api/auth/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		fmt.Println("register error:", err)
		os.Exit(1)
	}
	var regData struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	json.NewDecoder(regResp.Body).Decode(&regData)
	regResp.Body.Close()
	token := regData.Data.AccessToken
	fmt.Printf("Registered. Token: %s...\n\n", token[:20])

	chatBody, _ := json.Marshal(map[string]string{
		"message": "In one sentence, what is a CRM?",
	})
	chatReq, _ := http.NewRequest("POST", backendURL+"/api/ai/chat", bytes.NewReader(chatBody))
	chatReq.Header.Set("Authorization", "Bearer "+token)
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.Header.Set("Accept", "text/event-stream")

	chatResp, err := client.Do(chatReq)
	if err != nil {
		fmt.Println("chat error:", err)
		os.Exit(1)
	}
	defer chatResp.Body.Close()
	fmt.Printf("HTTP Status: %d\nContent-Type: %s\n\n", chatResp.StatusCode, chatResp.Header.Get("Content-Type"))
	fmt.Println("--- RAW SSE CHUNKS FROM /api/ai/chat ---")
	scanner2 := bufio.NewScanner(chatResp.Body)
	chunk2Count := 0
	for scanner2.Scan() {
		line := scanner2.Text()
		if line == "" {
			continue
		}
		chunk2Count++
		fmt.Printf("line #%d: %s\n", chunk2Count, line)
		if strings.Contains(line, "[DONE]") {
			break
		}
	}
	fmt.Printf("\nTotal lines received: %d\n", chunk2Count)
}
