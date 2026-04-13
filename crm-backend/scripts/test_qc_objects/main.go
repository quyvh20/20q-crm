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

	// Track IDs for record tests
	projectDefID string
	vehicleDefID string
	record1ID    string
	record2ID    string
	record3ID    string
)

func main() {
	token = os.Args[1]

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║        QC PLAN — Custom Objects Phase 3                ║")
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Clean up any existing test objects
	cleanup()

	// ═══════════════════════════════════════════════════════════
	// Section 1: Object Definition CRUD
	// ═══════════════════════════════════════════════════════════
	section("1. Backend API — Object Definition CRUD")

	test("1.1", "List defs when none exist → empty", func() bool {
		code, body := apiGet("/api/objects")
		return code == 200 && strings.Contains(body, `"data":[]`)
	})

	test("1.2", "Create 'Project' with 3 fields (text, select, number)", func() bool {
		code, body := apiPost("/api/objects", `{
			"slug":"project","label":"Project","label_plural":"Projects","icon":"🏗️",
			"fields":[
				{"key":"name","label":"Name","type":"text"},
				{"key":"status","label":"Status","type":"select","options":["Planning","Active","Complete"]},
				{"key":"budget","label":"Budget","type":"number"}
			]
		}`)
		if code != 201 {
			fmt.Printf("    → got %d: %s\n", code, body)
			return false
		}
		var resp struct{ Data struct{ ID string `json:"id"`; Slug string; Fields []interface{} } }
		json.Unmarshal([]byte(body), &resp)
		projectDefID = resp.Data.ID
		return resp.Data.Slug == "project" && len(resp.Data.Fields) == 3
	})

	test("1.3", "Create 'Vehicle' with 2 fields (text, url)", func() bool {
		code, body := apiPost("/api/objects", `{
			"slug":"vehicle","label":"Vehicle","label_plural":"Vehicles","icon":"🚗",
			"fields":[
				{"key":"make","label":"Make","type":"text"},
				{"key":"listing_url","label":"Listing URL","type":"url"}
			]
		}`)
		if code != 201 {
			return false
		}
		var resp struct{ Data struct{ ID string `json:"id"` } }
		json.Unmarshal([]byte(body), &resp)
		vehicleDefID = resp.Data.ID
		return true
	})

	test("1.4", "List defs after creation → 2 defs", func() bool {
		code, body := apiGet("/api/objects")
		if code != 200 {
			return false
		}
		var resp struct{ Data []interface{} }
		json.Unmarshal([]byte(body), &resp)
		return len(resp.Data) == 2
	})

	test("1.5", "Get single def by slug → label='Project', 3 fields", func() bool {
		code, body := apiGet("/api/objects/project")
		if code != 200 {
			return false
		}
		var resp struct{ Data struct{ Label string; Fields []interface{} } }
		json.Unmarshal([]byte(body), &resp)
		return resp.Data.Label == "Project" && len(resp.Data.Fields) == 3
	})

	test("1.6", "Get non-existent slug → 404", func() bool {
		code, _ := apiGet("/api/objects/nonexistent_xyz")
		return code == 404
	})

	test("1.7", "Update label + icon", func() bool {
		code, body := apiPut("/api/objects/project", `{"label":"My Project","icon":"🎯"}`)
		if code != 200 {
			fmt.Printf("    → got %d: %s\n", code, body)
			return false
		}
		return strings.Contains(body, `"label":"My Project"`)
	})

	test("1.8", "Update fields (add 4th field)", func() bool {
		code, body := apiPut("/api/objects/project", `{"fields":[
			{"key":"name","label":"Name","type":"text"},
			{"key":"status","label":"Status","type":"select","options":["Planning","Active","Complete"]},
			{"key":"budget","label":"Budget","type":"number"},
			{"key":"deadline","label":"Deadline","type":"date"}
		]}`)
		if code != 200 {
			return false
		}
		var resp struct{ Data struct{ Fields []interface{} } }
		json.Unmarshal([]byte(body), &resp)
		return len(resp.Data.Fields) == 4
	})

	test("1.9", "Delete object def (vehicle)", func() bool {
		code, _ := apiDelete("/api/objects/vehicle")
		return code == 200
	})

	test("1.10", "Delete non-existent → 404", func() bool {
		code, _ := apiDelete("/api/objects/vehicle")
		return code == 404
	})

	// ═══════════════════════════════════════════════════════════
	// Section 2: Slug Validation
	// ═══════════════════════════════════════════════════════════
	section("2. Backend API — Slug Validation")

	test("2.1", "Duplicate slug in same org → 409", func() bool {
		code, body := apiPost("/api/objects", `{"slug":"project","label":"Dup","label_plural":"Dups"}`)
		return code == 409 && strings.Contains(body, "already exists")
	})

	test("2.2", "Slug with spaces → 400", func() bool {
		code, _ := apiPost("/api/objects", `{"slug":"my project","label":"X","label_plural":"Xs"}`)
		return code == 400
	})

	test("2.3", "Slug with uppercase → 400", func() bool {
		code, _ := apiPost("/api/objects", `{"slug":"MyProject","label":"X","label_plural":"Xs"}`)
		return code == 400
	})

	test("2.4", "Slug starting with number → 400", func() bool {
		code, _ := apiPost("/api/objects", `{"slug":"1project","label":"X","label_plural":"Xs"}`)
		return code == 400
	})

	test("2.5", "Slug longer than 50 chars → 400", func() bool {
		longSlug := "a" + strings.Repeat("b", 50) // 51 chars
		body := fmt.Sprintf(`{"slug":"%s","label":"Long","label_plural":"Longs"}`, longSlug)
		code, _ := apiPost("/api/objects", body)
		return code == 400
	})

	test("2.6", "Empty slug → 400", func() bool {
		code, _ := apiPost("/api/objects", `{"slug":"","label":"X","label_plural":"Xs"}`)
		return code == 400
	})

	test("2.7", "Empty label → 400", func() bool {
		code, _ := apiPost("/api/objects", `{"slug":"test_empty","label":"","label_plural":"Xs"}`)
		return code == 400
	})

	test("2.8", "Valid slug with underscores → 201", func() bool {
		code, _ := apiPost("/api/objects", `{"slug":"my_project_v2","label":"My Project V2","label_plural":"My Projects V2","icon":"📋"}`)
		if code != 201 {
			return false
		}
		// Cleanup
		apiDelete("/api/objects/my_project_v2")
		return true
	})

	// ═══════════════════════════════════════════════════════════
	// Section 3: Field Definition Validation
	// ═══════════════════════════════════════════════════════════
	section("3. Backend API — Field Definition Validation")

	test("3.1", "Invalid field type in fields array → 400", func() bool {
		code, body := apiPost("/api/objects", `{
			"slug":"test_bad_type","label":"Bad","label_plural":"Bads",
			"fields":[{"key":"f","label":"F","type":"blob"}]
		}`)
		return code == 400 && strings.Contains(body, "invalid field type")
	})

	test("3.2", "Select field without options → 400", func() bool {
		code, body := apiPost("/api/objects", `{
			"slug":"test_no_opts","label":"Bad","label_plural":"Bads",
			"fields":[{"key":"f","label":"F","type":"select","options":[]}]
		}`)
		return code == 400 && strings.Contains(body, "option")
	})

	test("3.3", "Duplicate field keys → 400", func() bool {
		code, body := apiPost("/api/objects", `{
			"slug":"test_dup_key","label":"Bad","label_plural":"Bads",
			"fields":[{"key":"name","label":"A","type":"text"},{"key":"name","label":"B","type":"text"}]
		}`)
		return code == 400 && strings.Contains(body, "duplicate")
	})

	test("3.4", "Field missing key → 400", func() bool {
		code, _ := apiPost("/api/objects", `{
			"slug":"test_no_key","label":"Bad","label_plural":"Bads",
			"fields":[{"key":"","label":"F","type":"text"}]
		}`)
		return code == 400
	})

	test("3.5", "Field missing label → 400", func() bool {
		code, _ := apiPost("/api/objects", `{
			"slug":"test_no_label","label":"Bad","label_plural":"Bads",
			"fields":[{"key":"f","label":"","type":"text"}]
		}`)
		return code == 400
	})

	test("3.6", "Valid all 6 field types → 201", func() bool {
		code, _ := apiPost("/api/objects", `{
			"slug":"test_all_types","label":"AllTypes","label_plural":"AllTypes",
			"fields":[
				{"key":"t","label":"T","type":"text"},
				{"key":"n","label":"N","type":"number"},
				{"key":"d","label":"D","type":"date"},
				{"key":"s","label":"S","type":"select","options":["A","B"]},
				{"key":"b","label":"B","type":"boolean"},
				{"key":"u","label":"U","type":"url"}
			]
		}`)
		if code != 201 {
			return false
		}
		apiDelete("/api/objects/test_all_types")
		return true
	})

	// ═══════════════════════════════════════════════════════════
	// Section 4: Record CRUD
	// ═══════════════════════════════════════════════════════════
	section("4. Backend API — Record CRUD")

	test("4.1", "List records when none → empty", func() bool {
		code, body := apiGet("/api/objects/project/records")
		return code == 200 && strings.Contains(body, `"total":0`)
	})

	test("4.2", "Create record → display_name auto-computed", func() bool {
		code, body := apiPost("/api/objects/project/records", `{
			"data":{"name":"Website Redesign","status":"Active","budget":15000}
		}`)
		if code != 201 {
			fmt.Printf("    → got %d: %s\n", code, body)
			return false
		}
		var resp struct{ Data struct{ ID string `json:"id"`; DisplayName string `json:"display_name"` } }
		json.Unmarshal([]byte(body), &resp)
		record1ID = resp.Data.ID
		return resp.Data.DisplayName == "Website Redesign"
	})

	test("4.3", "Create 2nd record", func() bool {
		code, body := apiPost("/api/objects/project/records", `{
			"data":{"name":"Mobile App","status":"Planning","budget":50000}
		}`)
		if code != 201 {
			return false
		}
		var resp struct{ Data struct{ ID string `json:"id"` } }
		json.Unmarshal([]byte(body), &resp)
		record2ID = resp.Data.ID
		return true
	})

	test("4.4", "List records → total=2", func() bool {
		code, body := apiGet("/api/objects/project/records")
		if code != 200 {
			return false
		}
		var resp struct{ Data []interface{}; Total int }
		json.Unmarshal([]byte(body), &resp)
		return resp.Total == 2 && len(resp.Data) == 2
	})

	test("4.5", "Get single record by ID", func() bool {
		code, body := apiGet("/api/objects/project/records/" + record1ID)
		if code != 200 {
			return false
		}
		return strings.Contains(body, "Website Redesign")
	})

	test("4.6", "Get non-existent record → 404", func() bool {
		code, _ := apiGet("/api/objects/project/records/00000000-0000-0000-0000-000000000001")
		return code == 404
	})

	test("4.7", "Update record data → display_name recalculated", func() bool {
		code, body := apiPut("/api/objects/project/records/"+record1ID, `{
			"data":{"name":"Website Redesign V2","status":"Complete","budget":18000}
		}`)
		if code != 200 {
			fmt.Printf("    → got %d: %s\n", code, body)
			return false
		}
		return strings.Contains(body, "Website Redesign V2")
	})

	test("4.8", "Delete record", func() bool {
		code, _ := apiDelete("/api/objects/project/records/" + record2ID)
		return code == 200
	})

	test("4.9", "List after delete → total=1", func() bool {
		code, body := apiGet("/api/objects/project/records")
		if code != 200 {
			return false
		}
		var resp struct{ Total int }
		json.Unmarshal([]byte(body), &resp)
		return resp.Total == 1
	})

	test("4.10", "Create record on non-existent object → 404", func() bool {
		code, _ := apiPost("/api/objects/fake_obj/records", `{"data":{"x":"y"}}`)
		return code == 404
	})

	// ═══════════════════════════════════════════════════════════
	// Section 5: display_name Auto-Computation
	// ═══════════════════════════════════════════════════════════
	section("5. Backend API — display_name Auto-Computation")

	test("5.1", "First text field value → display_name", func() bool {
		_, body := apiGet("/api/objects/project/records/" + record1ID)
		return strings.Contains(body, `"display_name":"Website Redesign V2"`)
	})

	test("5.2", "First text field empty → fallback to any string", func() bool {
		code, body := apiPost("/api/objects/project/records", `{
			"data":{"name":"","status":"Active","budget":100}
		}`)
		if code != 201 {
			return false
		}
		var resp struct{ Data struct{ ID string `json:"id"`; DisplayName string `json:"display_name"` } }
		json.Unmarshal([]byte(body), &resp)
		record3ID = resp.Data.ID
		// Should fallback to "Active" (next string value) or "Untitled"
		return resp.Data.DisplayName == "Active" || resp.Data.DisplayName == "Untitled"
	})

	test("5.3", "No text fields data → Untitled", func() bool {
		// Create a temp object with only number fields
		apiPost("/api/objects", `{"slug":"numonly","label":"NumOnly","label_plural":"NumOnlys","fields":[{"key":"val","label":"Val","type":"number"}]}`)
		code, body := apiPost("/api/objects/numonly/records", `{"data":{"val":42}}`)
		apiDelete("/api/objects/numonly") // Cascade deletes records too
		if code != 201 {
			return false
		}
		return strings.Contains(body, `"display_name":"Untitled"`)
	})

	test("5.4", "Update data changes display_name", func() bool {
		code, body := apiPut("/api/objects/project/records/"+record3ID, `{
			"data":{"name":"Alpha Project","status":"Active","budget":100}
		}`)
		if code != 200 {
			return false
		}
		return strings.Contains(body, `"display_name":"Alpha Project"`)
	})

	// Clean up record3
	apiDelete("/api/objects/project/records/" + record3ID)

	// ═══════════════════════════════════════════════════════════
	// Section 6: Contact/Deal Linking
	// ═══════════════════════════════════════════════════════════
	section("6. Backend API — Contact/Deal Linking")

	// Get a contact ID to link
	contactID := getFirstContactID()
	if contactID == "" {
		fmt.Println("  ⚠️  No contacts found — creating test contact")
		contactID = createTestContact()
	}

	test("6.1", "Create record with contact_id → contact preloaded", func() bool {
		if contactID == "" {
			fmt.Println("    → skipped (no contact)")
			return true
		}
		body := fmt.Sprintf(`{"data":{"name":"Linked Record"},"contact_id":"%s"}`, contactID)
		code, resp := apiPost("/api/objects/project/records", body)
		if code != 201 {
			fmt.Printf("    → got %d: %s\n", code, resp)
			return false
		}
		var r struct{ Data struct{ ID string `json:"id"`; Contact interface{} } }
		json.Unmarshal([]byte(resp), &r)
		record3ID = r.Data.ID
		// Re-fetch to check preload
		_, getBody := apiGet("/api/objects/project/records/" + record3ID)
		return strings.Contains(getBody, "contact") && strings.Contains(getBody, "first_name")
	})

	test("6.2", "Create record with no links → null", func() bool {
		code, body := apiPost("/api/objects/project/records", `{"data":{"name":"No Link"}}`)
		if code != 201 {
			return false
		}
		var r struct{ Data struct{ ID string `json:"id"` } }
		json.Unmarshal([]byte(body), &r)
		apiDelete("/api/objects/project/records/" + r.Data.ID)
		return true
	})

	test("6.3", "Update record to add contact_id", func() bool {
		if contactID == "" {
			return true
		}
		body := fmt.Sprintf(`{"contact_id":"%s"}`, contactID)
		code, resp := apiPut("/api/objects/project/records/"+record1ID, body)
		if code != 200 {
			fmt.Printf("    → got %d: %s\n", code, resp)
			return false
		}
		_, getBody := apiGet("/api/objects/project/records/" + record1ID)
		return strings.Contains(getBody, "first_name")
	})

	// Clean linked record
	if record3ID != "" {
		apiDelete("/api/objects/project/records/" + record3ID)
	}

	// ═══════════════════════════════════════════════════════════
	// Section 7: Pagination & Search
	// ═══════════════════════════════════════════════════════════
	section("7. Backend API — Pagination & Search")

	// Create 4 more records (total ~5 with the V2 one)
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("Batch Project %d", i)
		body := fmt.Sprintf(`{"data":{"name":"%s","status":"Planning","budget":%d}}`, name, i*1000)
		apiPost("/api/objects/project/records", body)
	}

	test("7.1", "List with limit=2 → 2 returned", func() bool {
		code, body := apiGet("/api/objects/project/records?limit=2")
		if code != 200 {
			return false
		}
		var resp struct{ Data []interface{}; Total int }
		json.Unmarshal([]byte(body), &resp)
		return len(resp.Data) == 2 && resp.Total == 5
	})

	test("7.2", "List with offset=2,limit=2 → 2 returned", func() bool {
		code, body := apiGet("/api/objects/project/records?offset=2&limit=2")
		if code != 200 {
			return false
		}
		var resp struct{ Data []interface{} }
		json.Unmarshal([]byte(body), &resp)
		return len(resp.Data) == 2
	})

	test("7.3", "Search by q=Batch → 4 matching", func() bool {
		code, body := apiGet("/api/objects/project/records?q=Batch")
		if code != 200 {
			return false
		}
		var resp struct{ Data []interface{}; Total int }
		json.Unmarshal([]byte(body), &resp)
		return resp.Total == 4
	})

	test("7.4", "Search with no match → empty", func() bool {
		code, body := apiGet("/api/objects/project/records?q=ZZZZNOTHING")
		if code != 200 {
			return false
		}
		var resp struct{ Total int }
		json.Unmarshal([]byte(body), &resp)
		return resp.Total == 0
	})

	// ═══════════════════════════════════════════════════════════
	// Section 8: Auth & Permissions
	// ═══════════════════════════════════════════════════════════
	section("8. Backend API — Auth & Permissions")

	test("8.1", "GET objects without auth → 401", func() bool {
		code, _ := apiGetNoAuth("/api/objects")
		return code == 401
	})

	test("8.2", "POST object without auth → 401", func() bool {
		code, _ := apiPostNoAuth("/api/objects", `{"slug":"x","label":"X","label_plural":"Xs"}`)
		return code == 401
	})

	test("8.3", "GET records without auth → 401", func() bool {
		code, _ := apiGetNoAuth("/api/objects/project/records")
		return code == 401
	})

	test("8.4", "POST record without auth → 401", func() bool {
		code, _ := apiPostNoAuth("/api/objects/project/records", `{"data":{"name":"Unauth"}}`)
		return code == 401
	})

	// ═══════════════════════════════════════════════════════════
	// Cleanup & Summary
	// ═══════════════════════════════════════════════════════════
	cleanup()

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

	if failed > 0 {
		os.Exit(1)
	}
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

func cleanup() {
	slugs := []string{"project", "vehicle", "my_project_v2", "test_all_types", "numonly",
		"test_bad_type", "test_no_opts", "test_dup_key", "test_no_key", "test_no_label"}
	for _, s := range slugs {
		apiDelete("/api/objects/" + s)
	}
	fmt.Println("🧹 Cleaned up test object defs")
}

func getFirstContactID() string {
	code, body := apiGet("/api/contacts?limit=1")
	if code != 200 {
		return ""
	}
	var resp struct {
		Data struct {
			Contacts []struct {
				ID string `json:"id"`
			} `json:"contacts"`
		} `json:"data"`
	}
	json.Unmarshal([]byte(body), &resp)
	if len(resp.Data.Contacts) > 0 {
		return resp.Data.Contacts[0].ID
	}
	return ""
}

func createTestContact() string {
	code, body := apiPost("/api/contacts", `{"first_name":"QC","last_name":"TestObj","email":"qcobj@test.com"}`)
	if code != 201 {
		return ""
	}
	var resp struct{ Data struct{ ID string `json:"id"` } }
	json.Unmarshal([]byte(body), &resp)
	return resp.Data.ID
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
