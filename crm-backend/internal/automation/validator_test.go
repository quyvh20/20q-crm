package automation

import (
	"encoding/json"
	"testing"
)

func TestValidateWorkflowPayload_ValidFull(t *testing.T) {
	trigger := `{"type":"contact_created"}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"{{contact.email}}"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateWorkflowPayload_EmptyTrigger(t *testing.T) {
	result := ValidateWorkflowPayload(nil, nil, []byte(`[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`))
	if result.Valid {
		t.Fatal("expected invalid for nil trigger")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "trigger" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected trigger error")
	}
}

func TestValidateWorkflowPayload_EmptyActions(t *testing.T) {
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, nil)
	if result.Valid {
		t.Fatal("expected invalid for nil actions")
	}
}

func TestValidateWorkflowPayload_EmptyActionsArray(t *testing.T) {
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(`[]`))
	if result.Valid {
		t.Fatal("expected invalid for empty actions array")
	}
}

func TestValidateWorkflowPayload_InvalidTriggerJSON(t *testing.T) {
	result := ValidateWorkflowPayload([]byte(`{bad json`), nil, []byte(`[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`))
	if result.Valid {
		t.Fatal("expected invalid for bad trigger JSON")
	}
}

func TestValidateWorkflowPayload_UnknownTriggerType(t *testing.T) {
	result := ValidateWorkflowPayload([]byte(`{"type":"unknown_trigger"}`), nil, []byte(`[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`))
	if result.Valid {
		t.Fatal("expected invalid for unknown trigger type")
	}
}

func TestValidateWorkflowPayload_AllTriggerTypes(t *testing.T) {
	types := []string{"contact_created", "contact_updated"}
	for _, tt := range types {
		trigger, _ := json.Marshal(TriggerSpec{Type: tt})
		actions := []byte(`[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`)
		result := ValidateWorkflowPayload(trigger, nil, actions)
		if !result.Valid {
			t.Fatalf("trigger type %s: expected valid, got errors: %+v", tt, result.Errors)
		}
	}
}

func TestValidateWorkflowPayload_DealStageChanged_RequiresParams(t *testing.T) {
	trigger := `{"type":"deal_stage_changed"}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — deal_stage_changed requires params")
	}
}

func TestValidateWorkflowPayload_DealStageChanged_RequiresToStage(t *testing.T) {
	trigger := `{"type":"deal_stage_changed","params":{"other":"value"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — deal_stage_changed requires to_stage")
	}
}

func TestValidateWorkflowPayload_DealStageChanged_Valid(t *testing.T) {
	trigger := `{"type":"deal_stage_changed","params":{"to_stage":"won"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateWorkflowPayload_NoActivityDays_RequiresParams(t *testing.T) {
	trigger := `{"type":"no_activity_days"}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — no_activity_days requires params")
	}
}

func TestValidateWorkflowPayload_NoActivityDays_RequiresDays(t *testing.T) {
	trigger := `{"type":"no_activity_days","params":{"entity":"contact"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — no_activity_days requires days")
	}
}

func TestValidateWorkflowPayload_NoActivityDays_RequiresEntity(t *testing.T) {
	trigger := `{"type":"no_activity_days","params":{"days":7}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — no_activity_days requires entity")
	}
}

func TestValidateWorkflowPayload_NoActivityDays_InvalidEntity(t *testing.T) {
	trigger := `{"type":"no_activity_days","params":{"days":7,"entity":"invoice"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — entity must be contact or deal")
	}
}

func TestValidateWorkflowPayload_NoActivityDays_Valid(t *testing.T) {
	trigger := `{"type":"no_activity_days","params":{"days":7,"entity":"contact"}}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(trigger), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

// --- Action validation ---

func TestValidateActions_UnknownType(t *testing.T) {
	actions := `[{"type":"unknown_action","id":"a1","params":{}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid for unknown action type")
	}
}

func TestValidateActions_DuplicateIDs(t *testing.T) {
	actions := `[{"type":"send_email","id":"dup","params":{"to":"x@test.com"}},{"type":"delay","id":"dup","params":{"duration_sec":60}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid for duplicate action IDs")
	}
}

func TestValidateActions_EmptyID(t *testing.T) {
	actions := `[{"type":"send_email","id":"","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid for empty action ID")
	}
}

func TestValidateActions_SendEmail_RequiresTo(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"subject":"hi"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — send_email requires 'to'")
	}
}

func TestValidateActions_CreateTask_RequiresTitle(t *testing.T) {
	actions := `[{"type":"create_task","id":"a1","params":{"priority":"high"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — create_task requires 'title'")
	}
}

func TestValidateActions_AssignUser_RequiresEntityAndStrategy(t *testing.T) {
	actions := `[{"type":"assign_user","id":"a1","params":{}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — assign_user requires entity and strategy")
	}
}

func TestValidateActions_AssignUser_InvalidStrategy(t *testing.T) {
	actions := `[{"type":"assign_user","id":"a1","params":{"entity":"contact","strategy":"random"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — invalid strategy")
	}
}

func TestValidateActions_AssignUser_SpecificRequiresUserID(t *testing.T) {
	actions := `[{"type":"assign_user","id":"a1","params":{"entity":"contact","strategy":"specific"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — specific strategy requires user_id")
	}
}

func TestValidateActions_AssignUser_SpecificValid(t *testing.T) {
	actions := `[{"type":"assign_user","id":"a1","params":{"entity":"contact","strategy":"specific","user_id":"abc-123"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_SendWebhook_RequiresURL(t *testing.T) {
	actions := `[{"type":"send_webhook","id":"a1","params":{"method":"POST"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — send_webhook requires 'url'")
	}
}

func TestValidateActions_Delay_RequiresDurationSec(t *testing.T) {
	actions := `[{"type":"delay","id":"a1","params":{}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — delay requires 'duration_sec'")
	}
}

func TestValidateActions_Delay_ZeroDuration(t *testing.T) {
	actions := `[{"type":"delay","id":"a1","params":{"duration_sec":0}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — duration_sec=0 must be rejected")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.duration_sec" && e.Message == "duration_sec must be a positive integer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'positive integer' error, got: %+v", result.Errors)
	}
}

func TestValidateActions_Delay_NegativeDuration(t *testing.T) {
	actions := `[{"type":"delay","id":"a1","params":{"duration_sec":-10}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — negative duration_sec must be rejected")
	}
}

func TestValidateActions_Delay_ExceedsMax(t *testing.T) {
	// 2592001 seconds = 30 days + 1 second
	actions := `[{"type":"delay","id":"a1","params":{"duration_sec":2592001}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — duration_sec exceeds 30-day max")
	}
	found := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.duration_sec" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duration_sec error, got: %+v", result.Errors)
	}
}

func TestValidateActions_Delay_ExactlyAtMax(t *testing.T) {
	// 2592000 seconds = exactly 30 days — should be valid
	actions := `[{"type":"delay","id":"a1","params":{"duration_sec":2592000}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid at exactly 30 days, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_Delay_ValidDuration(t *testing.T) {
	// 3600 seconds = 1 hour
	actions := `[{"type":"delay","id":"a1","params":{"duration_sec":3600}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid for 3600s, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_AllTypesValid(t *testing.T) {
	actions := `[
		{"type":"send_email","id":"a1","params":{"to":"x@test.com"}},
		{"type":"create_task","id":"a2","params":{"title":"t"}},
		{"type":"assign_user","id":"a3","params":{"entity":"contact","strategy":"round_robin"}},
		{"type":"send_webhook","id":"a4","params":{"url":"https://x.com"}},
		{"type":"delay","id":"a5","params":{"duration_sec":60}}
	]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_InvalidJSON(t *testing.T) {
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(`{not json`))
	if result.Valid {
		t.Fatal("expected invalid for bad actions JSON")
	}
}

// --- Condition validation ---

func TestValidateConditions_ValidSimple(t *testing.T) {
	cond := `{"op":"AND","rules":[{"field":"contact.email","operator":"eq","value":"x@y.com"}]}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), []byte(cond), []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateConditions_InvalidJSON(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), []byte(`{bad`), []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid for bad condition JSON")
	}
}

func TestValidateConditions_NullIsSkipped(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), []byte(`null`), []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid when conditions=null, got errors: %+v", result.Errors)
	}
}

func TestValidateConditions_DepthExceeded(t *testing.T) {
	// depth 4 — exceeds max of 3
	cond := `{"op":"AND","rules":[{"op":"OR","rules":[{"op":"AND","rules":[{"op":"OR","rules":[{"field":"x","operator":"eq","value":"y"}]}]}]}]}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), []byte(cond), []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — condition depth exceeds 3")
	}
}

func TestValidateConditions_EmptyField(t *testing.T) {
	cond := `{"op":"AND","rules":[{"field":"","operator":"eq","value":"x"}]}`
	actions := `[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), []byte(cond), []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid for empty field in condition rule")
	}
}

// --- Template warnings ---

func TestValidateActions_TemplateWarnings(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"{{unknown_root.field}}"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if len(result.Warnings) == 0 {
		t.Fatal("expected template warning for unknown root")
	}
}

func TestValidateActions_ValidTemplateNoWarning(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"{{contact.email}}"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings, got: %+v", result.Warnings)
	}
}

// --- Email validation ---

func TestValidateActions_SendEmail_InvalidTo(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"notanemail"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — 'notanemail' is not a valid email")
	}
}

func TestValidateActions_SendEmail_ValidToEmail(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"user@example.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_SendEmail_ValidToTemplate(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"{{contact.email}}"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_SendEmail_InvalidCC(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"a@b.com","cc":"bad, also-bad"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid — CC contains invalid email addresses")
	}
	// Should have errors for both invalid addresses
	foundCC := false
	for _, e := range result.Errors {
		if e.Field == "actions[0].params.cc" {
			foundCC = true
		}
	}
	if !foundCC {
		t.Fatal("expected CC validation error")
	}
}

func TestValidateActions_SendEmail_ValidCC(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"a@b.com","cc":"x@y.com, z@w.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestValidateActions_SendEmail_CCWithTemplate(t *testing.T) {
	actions := `[{"type":"send_email","id":"a1","params":{"to":"a@b.com","cc":"{{contact.email}}, manager@co.com"}}]`
	result := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
}

func TestIsEmailOrTemplate(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"user@example.com", true},
		{"a@b.co", true},
		{"{{contact.email}}", true},
		{"notanemail", false},
		{"@missing.com", false},
		{"user@", false},
		{"user@.com", false},
		{"user@com.", false},
		{"", false},
	}
	for _, c := range cases {
		got := isEmailOrTemplate(c.input)
		if got != c.want {
			t.Errorf("isEmailOrTemplate(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
