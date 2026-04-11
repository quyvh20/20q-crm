package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	accountID  := "2d565dd4fbeedd42f9f1cc6f6209e30f"
	gatewayID  := "crm-ai-gateway"
	// Account token (Workers AI + AI Gateway permissions) — for Authorization header
	workersTok := "cfat_a4DrbblbqAj6tHN1IU7o8a7hpCXnKo7InkMgcxLt9bbd8af8"
	// Gateway-specific token (created when gateway was created) — for cf-aig-authorization header
	gatewayTok := "cfut_5zVKDFSU3SQOysXddzSKfi1jpFRzxyT14w69ahKUadbc1947"
	model      := "@cf/google/embeddinggemma-300m"

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
