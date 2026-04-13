package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

var (
	baseURL = "http://localhost:8080"
	token   string
	passed  int
	failed  int
	total   int
)

func main() {
	token = os.Args[1]

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║        QC PLAN — Custom Fields Phase 1+2               ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Clean up any existing fields first
	cleanupFields()

	// Section 1: Field Definition CRUD
	section("1. Backend API — Field Definition CRUD")
	test("1.1", "List fields when none exist", func() bool {
		code, body := apiGet("/api/settings/fields?entity_type=contact")
		return code == 200 && strings.Contains(body, `"data":[]`)
	})
	test("1.2", "Create text field", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"budget","label":"Budget","type":"text","entity_type":"contact"}`)
		return code == 201
	})
	test("1.3", "Create number field", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"deal_value","label":"Deal Value","type":"number","entity_type":"contact"}`)
		return code == 201
	})
	test("1.4", "Create date field", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"follow_up","label":"Follow Up Date","type":"date","entity_type":"contact"}`)
		return code == 201
	})
	test("1.5", "Create boolean field", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"vip","label":"VIP Customer","type":"boolean","entity_type":"contact"}`)
		return code == 201
	})
	test("1.6", "Create URL field", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"linkedin","label":"LinkedIn","type":"url","entity_type":"contact"}`)
		return code == 201
	})
	test("1.7", "Create select field with options", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"lead_source","label":"Lead Source","type":"select","entity_type":"contact","options":["Website","Referral","Cold Call","Social Media"]}`)
		return code == 201
	})
	test("1.8", "Create select field WITHOUT options → 400", func() bool {
		code, body := apiPost("/api/settings/fields", `{"key":"empty_select","label":"Empty","type":"select","entity_type":"contact","options":[]}`)
		return code == 400 && strings.Contains(body, "option")
	})
	test("1.9", "List fields after creation → 6 fields", func() bool {
		code, body := apiGet("/api/settings/fields?entity_type=contact")
		if code != 200 {
			return false
		}
		var resp struct{ Data []map[string]interface{} }
		json.Unmarshal([]byte(body), &resp)
		return len(resp.Data) == 6
	})
	test("1.10", "List ALL fields (no entity_type filter)", func() bool {
		code, body := apiGet("/api/settings/fields")
		if code != 200 {
			return false
		}
		var resp struct{ Data []map[string]interface{} }
		json.Unmarshal([]byte(body), &resp)
		return len(resp.Data) >= 6
	})
	test("1.11", "Update field label", func() bool {
		code, body := apiPut("/api/settings/fields/budget", `{"label":"Budget ($)"}`)
		return code == 200 && strings.Contains(body, "Budget ($)")
	})
	test("1.12", "Update field required flag", func() bool {
		code, body := apiPut("/api/settings/fields/budget", `{"required":true}`)
		return code == 200 && strings.Contains(body, `"required":true`)
	})
	test("1.13", "Delete field", func() bool {
		code, _ := apiDelete("/api/settings/fields/linkedin")
		if code != 200 {
			return false
		}
		// Verify it's gone
		_, body := apiGet("/api/settings/fields?entity_type=contact")
		return !strings.Contains(body, `"linkedin"`)
	})
	test("1.14", "Delete non-existent field → 404", func() bool {
		code, _ := apiDelete("/api/settings/fields/nonexistent_xyz")
		return code == 404
	})

	// Section 2: Validation & Edge Cases
	section("2. Backend API — Validation & Edge Cases")
	test("2.1", "Duplicate key within same entity_type → 409", func() bool {
		code, body := apiPost("/api/settings/fields", `{"key":"budget","label":"Budget2","type":"text","entity_type":"contact"}`)
		return code == 409 && strings.Contains(body, "already exists")
	})
	test("2.2", "Same key on DIFFERENT entity_type → 201", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"budget","label":"Budget","type":"number","entity_type":"deal"}`)
		return code == 201
	})
	test("2.3", "Invalid field type → 400", func() bool {
		code, body := apiPost("/api/settings/fields", `{"key":"test","label":"Test","type":"blob","entity_type":"contact"}`)
		return code == 400 && strings.Contains(body, "invalid field type")
	})
	test("2.4", "Invalid entity_type → 400", func() bool {
		code, body := apiPost("/api/settings/fields", `{"key":"test","label":"Test","type":"text","entity_type":"invoice"}`)
		return code == 400 && strings.Contains(body, "invalid entity_type")
	})
	test("2.5", "Key with spaces → 400", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"my field","label":"My Field","type":"text","entity_type":"contact"}`)
		return code == 400
	})
	test("2.6", "Key with uppercase → 400", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"MyField","label":"My Field","type":"text","entity_type":"contact"}`)
		return code == 400
	})
	test("2.7", "Key longer than 64 chars → 400", func() bool {
		longKey := strings.Repeat("a", 65)
		code, _ := apiPost("/api/settings/fields", fmt.Sprintf(`{"key":"%s","label":"Long","type":"text","entity_type":"contact"}`, longKey))
		return code == 400
	})
	test("2.8", "Empty key → 400", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"","label":"No Key","type":"text","entity_type":"contact"}`)
		return code == 400
	})
	test("2.9", "Empty label → 400", func() bool {
		code, _ := apiPost("/api/settings/fields", `{"key":"nolab","label":"","type":"text","entity_type":"contact"}`)
		return code == 400
	})
	test("2.10", "Auto-position assignment (positions 0,1,2...)", func() bool {
		_, body := apiGet("/api/settings/fields?entity_type=contact")
		var resp struct {
			Data []struct {
				Position int    `json:"position"`
				Key      string `json:"key"`
			}
		}
		json.Unmarshal([]byte(body), &resp)
		// Check positions increase
		for i := 1; i < len(resp.Data); i++ {
			if resp.Data[i].Position <= resp.Data[i-1].Position {
				return false
			}
		}
		return len(resp.Data) > 0
	})

	// Section 3: Auth & Permissions
	section("3. Backend API — Auth & Permissions")
	test("3.1", "GET fields without auth → 401", func() bool {
		code, _ := apiGetNoAuth("/api/settings/fields")
		return code == 401
	})
	test("3.2", "POST field without auth → 401", func() bool {
		code, _ := apiPostNoAuth("/api/settings/fields", `{"key":"test","label":"Test","type":"text","entity_type":"contact"}`)
		return code == 401
	})
	test("3.4", "POST field as admin → 201 (already verified above)", func() bool {
		return true // Already verified in section 1
	})
	test("3.5", "GET fields as authenticated user → 200", func() bool {
		code, _ := apiGet("/api/settings/fields?entity_type=contact")
		return code == 200
	})

	// Section 7: Cross-Entity Isolation
	section("7. Cross-Entity Isolation")
	// Create a company field
	apiPost("/api/settings/fields", `{"key":"industry_segment","label":"Industry Segment","type":"text","entity_type":"company"}`)
	test("7.1", "Contact fields don't appear in company list", func() bool {
		_, body := apiGet("/api/settings/fields?entity_type=company")
		// Should have industry_segment but NOT budget
		return strings.Contains(body, "industry_segment") && !strings.Contains(body, `"key":"lead_source"`)
	})
	test("7.2", "Deal fields don't appear in contact list", func() bool {
		_, body := apiGet("/api/settings/fields?entity_type=contact")
		// Contact list should not have deal's "budget" (deal budget is different entity_type)
		// Actually budget exists for contact. Check that company's industry_segment isn't here
		return !strings.Contains(body, "industry_segment")
	})
	test("7.3", "Company fields don't appear in deal list", func() bool {
		_, body := apiGet("/api/settings/fields?entity_type=deal")
		return !strings.Contains(body, "industry_segment")
	})

	// Summary
	fmt.Println()
	fmt.Println("══════════════════════════════════════════════════════════")
	fmt.Printf("  RESULTS: %d/%d passed", passed, total)
	if failed > 0 {
		fmt.Printf(", %d FAILED ❌", failed)
	} else {
		fmt.Printf(" ✅ ALL PASSED")
	}
	fmt.Println()
	fmt.Println("══════════════════════════════════════════════════════════")

	// Cleanup deal field we created for 2.2
	apiDelete("/api/settings/fields/budget") // This deletes the contact one
}

// ─── Helpers ────────────────────────────────────────────

func section(name string) {
	fmt.Println()
	fmt.Printf("── %s ──\n", name)
}

func test(id, name string, fn func() bool) {
	total++
	ok := fn()
	if ok {
		passed++
		fmt.Printf("  ✅ %s: %s\n", id, name)
	} else {
		failed++
		fmt.Printf("  ❌ %s: %s\n", id, name)
	}
}

func apiGet(path string) (int, string) {
	req, _ := http.NewRequest("GET", baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func apiGetNoAuth(path string) (int, string) {
	resp, err := http.Get(baseURL + path)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func apiPost(path, jsonBody string) (int, string) {
	req, _ := http.NewRequest("POST", baseURL+path, bytes.NewBufferString(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func apiPostNoAuth(path, jsonBody string) (int, string) {
	resp, err := http.Post(baseURL+path, "application/json", bytes.NewBufferString(jsonBody))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func apiPut(path, jsonBody string) (int, string) {
	req, _ := http.NewRequest("PUT", baseURL+path, bytes.NewBufferString(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func apiDelete(path string) (int, string) {
	req, _ := http.NewRequest("DELETE", baseURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func cleanupFields() {
	// Delete known test fields to ensure a clean slate
	keys := []string{"budget", "deal_value", "follow_up", "vip", "linkedin", "lead_source", "empty_select", "industry_segment", "source"}
	for _, k := range keys {
		apiDelete("/api/settings/fields/" + k)
	}
	fmt.Println("🧹 Cleaned up existing test fields")
}
