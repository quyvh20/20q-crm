package automation

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// run_now_test.go holds example-based (table-driven) unit tests for the pure-logic
// core in run_now.go. These complement the property-based tests
// (run_now_*_pbt_test.go) by pinning down concrete edge cases and the exact
// Trigger_Context key/shape for one contact and one deal example.
//
// Helpers here are intentionally given file-unique names (prefixed runNowUnit*) so
// they do not collide with package-level declarations in the sibling PBT test files.

// runNowUnitFixedUUID is a known-valid UUID string reused across the classification
// happy-path cases below.
const runNowUnitFixedContactUUID = "11111111-1111-1111-1111-111111111111"
const runNowUnitFixedDealUUID = "22222222-2222-2222-2222-222222222222"

// TestClassifyRunNowRequest_Errors asserts that classifyRunNowRequest returns the
// expected distinct sentinel errors for the both-present, neither-present, and
// invalid-UUID cases (Requirements 2.3, 2.4, 2.5).
func TestClassifyRunNowRequest_Errors(t *testing.T) {
	tests := []struct {
		name        string
		req         RunNowRequest
		wantErr     error
		wantErrText string // substring expected to identify the offending field for invalid-UUID
	}{
		{
			name:    "both ids present is rejected",
			req:     RunNowRequest{ContactID: runNowUnitFixedContactUUID, DealID: runNowUnitFixedDealUUID},
			wantErr: ErrRunNowBothIDs,
		},
		{
			name:    "neither id present is rejected",
			req:     RunNowRequest{},
			wantErr: ErrRunNowNoIDs,
		},
		{
			name:        "invalid contact_id UUID is rejected",
			req:         RunNowRequest{ContactID: "not-a-uuid"},
			wantErr:     ErrRunNowInvalidUUID,
			wantErrText: "contact_id",
		},
		{
			name:        "invalid deal_id UUID is rejected",
			req:         RunNowRequest{DealID: "also-not-a-uuid"},
			wantErr:     ErrRunNowInvalidUUID,
			wantErrText: "deal_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, id, err := classifyRunNowRequest(tt.req)
			if err == nil {
				t.Fatalf("expected error, got nil (kind=%q, id=%s)", kind, id)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected errors.Is(err, %v), got %v", tt.wantErr, err)
			}
			if kind != "" {
				t.Fatalf("expected empty kind on error, got %q", kind)
			}
			if id != uuid.Nil {
				t.Fatalf("expected uuid.Nil on error, got %s", id)
			}
			if tt.wantErrText != "" && !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("expected error message to name %q, got %q", tt.wantErrText, err.Error())
			}
		})
	}
}

// TestClassifyRunNowRequest_HappyPaths asserts the valid contact-only and deal-only
// cases return the right kind and parsed UUID (Requirements 2.1, 2.2 — covered here
// as the success counterpart to the error cases above).
func TestClassifyRunNowRequest_HappyPaths(t *testing.T) {
	tests := []struct {
		name     string
		req      RunNowRequest
		wantKind string
		wantID   string
	}{
		{
			name:     "contact only is contact-targeted",
			req:      RunNowRequest{ContactID: runNowUnitFixedContactUUID},
			wantKind: "contact",
			wantID:   runNowUnitFixedContactUUID,
		},
		{
			name:     "deal only is deal-targeted",
			req:      RunNowRequest{DealID: runNowUnitFixedDealUUID},
			wantKind: "deal",
			wantID:   runNowUnitFixedDealUUID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, id, err := classifyRunNowRequest(tt.req)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if kind != tt.wantKind {
				t.Fatalf("expected kind=%q, got %q", tt.wantKind, kind)
			}
			wantUUID := uuid.MustParse(tt.wantID)
			if id != wantUUID {
				t.Fatalf("expected id=%s, got %s", wantUUID, id)
			}
		})
	}
}

// TestEntityKindForTrigger asserts the supported trigger types map to the correct
// entity kind and that unsupported trigger types return "" (Requirements 4.1, 4.2,
// and the unsupported branch of 4.3).
func TestEntityKindForTrigger(t *testing.T) {
	tests := []struct {
		name        string
		triggerType string
		want        string
	}{
		{"contact_created maps to contact", TriggerContactCreated, "contact"},
		{"contact_updated maps to contact", TriggerContactUpdated, "contact"},
		{"webhook_inbound maps to contact", TriggerWebhookInbound, "contact"},
		{"deal_stage_changed maps to deal", TriggerDealStageChanged, "deal"},
		{"no_activity_days is unsupported", TriggerNoActivityDays, ""},
		{"random invalid string is unsupported", "totally_made_up_trigger", ""},
		{"empty string is unsupported", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entityKindForTrigger(tt.triggerType)
			if got != tt.want {
				t.Fatalf("entityKindForTrigger(%q) = %q, want %q", tt.triggerType, got, tt.want)
			}
		})
	}
}

// TestBuildRunNowTriggerContext_ContactShape asserts the exact key/shape of the
// Trigger_Context for a concrete contact example: entity_id equals the contact id,
// the contact is present under the "contact" key with its own nested "id",
// trigger.type equals the workflow trigger type, trigger.source == "run_now", there
// is no _internal_update marker, and no deal-only keys are present
// (Requirements 5.1, 5.2, 5.3, 5.4, 5.6).
func TestBuildRunNowTriggerContext_ContactShape(t *testing.T) {
	contactID := runNowUnitFixedContactUUID
	contact := map[string]any{
		"id":         contactID,
		"first_name": "Ada",
		"last_name":  "Lovelace",
		"email":      "ada@example.com",
	}

	ctx := buildRunNowTriggerContext("contact", TriggerContactCreated, contact)

	// entity_id == contact id
	if got := ctx["entity_id"]; got != contactID {
		t.Fatalf("entity_id = %v, want %v", got, contactID)
	}

	// contact present under "contact" key, with its own nested id
	gotContact, ok := ctx["contact"].(map[string]any)
	if !ok {
		t.Fatalf("expected ctx[\"contact\"] to be map[string]any, got %T", ctx["contact"])
	}
	if gotContact["id"] != contactID {
		t.Fatalf("contact.id = %v, want %v", gotContact["id"], contactID)
	}
	if gotContact["email"] != "ada@example.com" {
		t.Fatalf("contact.email = %v, want ada@example.com", gotContact["email"])
	}

	// trigger object: type + source
	trigger, ok := ctx["trigger"].(map[string]any)
	if !ok {
		t.Fatalf("expected ctx[\"trigger\"] to be map[string]any, got %T", ctx["trigger"])
	}
	if trigger["type"] != TriggerContactCreated {
		t.Fatalf("trigger.type = %v, want %v", trigger["type"], TriggerContactCreated)
	}
	if trigger["source"] != "run_now" {
		t.Fatalf("trigger.source = %v, want run_now", trigger["source"])
	}

	// no internal-update marker
	if _, present := ctx["_internal_update"]; present {
		t.Fatal("expected no _internal_update key in contact trigger context")
	}

	// deal-only keys must not appear for a contact context
	if _, present := ctx["deal"]; present {
		t.Fatal("did not expect a deal key in a contact trigger context")
	}
	if _, present := ctx["new_stage_id"]; present {
		t.Fatal("did not expect new_stage_id in a contact trigger context")
	}
	if _, present := ctx["old_stage_id"]; present {
		t.Fatal("did not expect old_stage_id in a contact trigger context")
	}
}

// TestBuildRunNowTriggerContext_DealShape asserts the exact key/shape of the
// Trigger_Context for a concrete deal example: entity_id equals the deal id, the deal
// is present under the "deal" key with its own nested "id", trigger.type ==
// deal_stage_changed, trigger.source == "run_now", new_stage_id equals the deal's
// stage_id, old_stage_id == "", and there is no _internal_update marker
// (Requirements 5.2, 5.3, 5.4, 5.5, 5.6).
func TestBuildRunNowTriggerContext_DealShape(t *testing.T) {
	dealID := runNowUnitFixedDealUUID
	stageID := "33333333-3333-3333-3333-333333333333"
	deal := map[string]any{
		"id":       dealID,
		"title":    "Big Deal",
		"value":    1000,
		"stage_id": stageID,
	}

	ctx := buildRunNowTriggerContext("deal", TriggerDealStageChanged, deal)

	// entity_id == deal id
	if got := ctx["entity_id"]; got != dealID {
		t.Fatalf("entity_id = %v, want %v", got, dealID)
	}

	// deal present under "deal" key, with its own nested id
	gotDeal, ok := ctx["deal"].(map[string]any)
	if !ok {
		t.Fatalf("expected ctx[\"deal\"] to be map[string]any, got %T", ctx["deal"])
	}
	if gotDeal["id"] != dealID {
		t.Fatalf("deal.id = %v, want %v", gotDeal["id"], dealID)
	}
	if gotDeal["title"] != "Big Deal" {
		t.Fatalf("deal.title = %v, want Big Deal", gotDeal["title"])
	}

	// trigger object: type + source
	trigger, ok := ctx["trigger"].(map[string]any)
	if !ok {
		t.Fatalf("expected ctx[\"trigger\"] to be map[string]any, got %T", ctx["trigger"])
	}
	if trigger["type"] != TriggerDealStageChanged {
		t.Fatalf("trigger.type = %v, want %v", trigger["type"], TriggerDealStageChanged)
	}
	if trigger["source"] != "run_now" {
		t.Fatalf("trigger.source = %v, want run_now", trigger["source"])
	}

	// new_stage_id == deal's current stage_id; old_stage_id == ""
	if ctx["new_stage_id"] != stageID {
		t.Fatalf("new_stage_id = %v, want %v", ctx["new_stage_id"], stageID)
	}
	if ctx["old_stage_id"] != "" {
		t.Fatalf("old_stage_id = %v, want empty string", ctx["old_stage_id"])
	}

	// no internal-update marker
	if _, present := ctx["_internal_update"]; present {
		t.Fatal("expected no _internal_update key in deal trigger context")
	}

	// contact key must not appear for a deal context
	if _, present := ctx["contact"]; present {
		t.Fatal("did not expect a contact key in a deal trigger context")
	}
}
