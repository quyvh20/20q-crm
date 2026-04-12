package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const backendURL = "https://20q-crm-production.up.railway.app"

func main() {
	client := &http.Client{Timeout: 60 * time.Second}

	// ── Step 1: Register fresh org ────────────────────────────────────────
	ts := time.Now().Unix()
	email := fmt.Sprintf("ctxtest%d@test.com", ts)
	regBody, _ := json.Marshal(map[string]string{
		"org_name": "CtxOrg", "first_name": "Ctx", "last_name": "Test",
		"email": email, "password": "Test1234!",
	})
	regResp, _ := http.Post(backendURL+"/api/auth/register", "application/json", bytes.NewReader(regBody))
	var regData struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	json.NewDecoder(regResp.Body).Decode(&regData)
	regResp.Body.Close()
	token := regData.Data.AccessToken
	fmt.Printf("✓ Registered: %s\n", email)

	authHeader := "Bearer " + token

	// ── Step 2: Create a contact with rich data ────────────────────────────
	contactBody, _ := json.Marshal(map[string]interface{}{
		"first_name": "Nguyen",
		"last_name":  "Van Anh",
		"email":      "vananh@proptech.vn",
		"phone":      "+84-901-234-567",
		"custom_fields": map[string]string{
			"industry": "Real Estate",
			"city":     "Ho Chi Minh City",
			"budget":   "$500,000",
		},
	})
	req, _ := http.NewRequest("POST", backendURL+"/api/contacts", bytes.NewReader(contactBody))
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := client.Do(req)
	var contactData struct {
		Data struct {
			ID        string `json:"id"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&contactData)
	resp.Body.Close()
	contactID := contactData.Data.ID
	fmt.Printf("✓ Created contact: %s %s (ID: %s)\n\n", contactData.Data.FirstName, contactData.Data.LastName, contactID)

	// ── Step 3: Chat WITHOUT context_id ──────────────────────────────────
	fmt.Println("=== TEST 1: Chat WITHOUT context_id ===")
	streamAndPrint(client, authHeader, "What is this contact's budget?", nil)

	// ── Step 4: Chat WITH context_id = contact UUID ───────────────────────
	fmt.Println("\n=== TEST 2: Chat WITH context_id = contact UUID ===")
	streamAndPrint(client, authHeader, "What do you know about this contact? Summarise their details.", &contactID)

	// ── Step 5: Ask a specific question using the contact context ─────────
	fmt.Println("\n=== TEST 3: Specific question using contact context ===")
	streamAndPrint(client, authHeader, "What city is this contact in and what is their budget?", &contactID)
}

func streamAndPrint(client *http.Client, auth, message string, contextID *string) {
	body := map[string]interface{}{"message": message}
	if contextID != nil {
		body["context_id"] = *contextID
	}
	bodyBytes, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", backendURL+"/api/ai/chat", bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("ERROR:", err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("HTTP %d | Content-Type: %s\n", resp.StatusCode, resp.Header.Get("Content-Type"))
	fmt.Print("Response: ")

	scanner := bufio.NewScanner(resp.Body)
	var full strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		full.WriteString(payload)
		fmt.Print(payload)
	}
	fmt.Println("\n[DONE]")

	// Check if the contact name is mentioned (for context-aware tests)
	if strings.Contains(full.String(), "Nguyen") || strings.Contains(full.String(), "Van Anh") || strings.Contains(full.String(), "vananh") {
		fmt.Println("✓ PASS — response references contact data")
	} else {
		fmt.Println("ℹ  Response did not explicitly name the contact (may still be context-aware)")
	}
}
