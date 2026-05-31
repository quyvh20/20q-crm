package automation

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/quick"
)

// validTriggerJSONLA is a known-valid trigger so trigger validation never adds
// noise to the log_activity param-error assertions in this file.
const validTriggerJSONLA = `{"type":"contact_created"}`

// pbtIterations is the minimum number of generated cases per property.
const pbtIterations = 200

// ─────────────────────────────────────────────────────────────────────────
// Shared generators
// ─────────────────────────────────────────────────────────────────────────

func laRandString(r *rand.Rand, n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _-"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(chars[r.Intn(len(chars))])
	}
	return sb.String()
}

// genValidActivityType returns one of the four user-selectable types.
func genValidActivityType(r *rand.Rand) string {
	types := []string{"call", "meeting", "note", "email"}
	return types[r.Intn(len(types))]
}

// genValidTitle returns a non-empty, non-whitespace title that may contain
// {{template}} content and surrounding text.
func genValidTitle(r *rand.Rand) string {
	base := []string{
		"Logged a call",
		"Call with {{contact.first_name}}",
		"Meeting re {{deal.title}}",
		"Note",
		"Follow up {{contact.email}} tomorrow",
		"{{contact.first_name}} {{contact.last_name}}",
	}
	s := base[r.Intn(len(base))]
	if r.Intn(2) == 0 {
		s = s + " " + laRandString(r, 1+r.Intn(8))
	}
	if strings.TrimSpace(s) == "" {
		s = "title"
	}
	return s
}

// genInvalidActivityType returns (present, value) for an activity_type that the
// validator must reject: absent, non-string, empty, whitespace-only, or near-miss.
func genInvalidActivityType(r *rand.Rand) (bool, any) {
	switch r.Intn(6) {
	case 0:
		return false, nil // absent
	case 1:
		return true, "" // empty string
	case 2: // whitespace-only
		ws := []string{" ", "   ", "\t", "\n", " \t \n ", "\r\n"}
		return true, ws[r.Intn(len(ws))]
	case 3: // non-string
		vals := []any{42, true, 3.14, []any{"call"}, map[string]any{"x": 1}}
		return true, vals[r.Intn(len(vals))]
	case 4: // near-miss strings (wrong case, plural, system-managed type, etc.)
		near := []string{"Call", "calls", "stage_change", "Meeting", "NOTE", "emails", "e-mail", "CALL", "meetings", "call "}
		return true, near[r.Intn(len(near))]
	default: // random string not exactly one of the valid types
		s := laRandString(r, 1+r.Intn(10))
		for validActivityTypes[s] {
			s = laRandString(r, 1+r.Intn(10))
		}
		return true, s
	}
}

// genInvalidTitle returns (present, value) for a title that the validator must
// reject: absent, non-string, empty, or whitespace-only.
func genInvalidTitle(r *rand.Rand) (bool, any) {
	switch r.Intn(5) {
	case 0:
		return false, nil // absent
	case 1:
		return true, "" // empty
	case 2: // whitespace-only
		ws := []string{" ", "   ", "\t", "\n", "\t\n  ", "     ", "\r\n", "\v", "\f", " \t "}
		return true, ws[r.Intn(len(ws))]
	case 3: // non-string
		vals := []any{0, 123, true, false, 9.9, []any{"x"}, map[string]any{"a": 1}}
		return true, vals[r.Intn(len(vals))]
	default: // more whitespace variants to weight this category
		ws := []string{"\r", "\v", "\f", " \t ", "\n\n\n"}
		return true, ws[r.Intn(len(ws))]
	}
}

func genAnyActivityType(r *rand.Rand) (bool, any) {
	if r.Intn(2) == 0 {
		return true, genValidActivityType(r)
	}
	return genInvalidActivityType(r)
}

func genAnyTitle(r *rand.Rand) (bool, any) {
	if r.Intn(2) == 0 {
		return true, genValidTitle(r)
	}
	return genInvalidTitle(r)
}

// ─────────────────────────────────────────────────────────────────────────
// Shared assertion helpers
// ─────────────────────────────────────────────────────────────────────────

func laHasErrorWithFieldSuffix(result *ValidationResult, suffix string) bool {
	for _, e := range result.Errors {
		if strings.HasSuffix(e.Field, suffix) {
			return true
		}
	}
	return false
}

func laHasErrorWithFieldPrefix(result *ValidationResult, prefix string) bool {
	for _, e := range result.Errors {
		if strings.HasPrefix(e.Field, prefix) {
			return true
		}
	}
	return false
}

func laHasExactErrorField(result *ValidationResult, field string) bool {
	for _, e := range result.Errors {
		if e.Field == field {
			return true
		}
	}
	return false
}

// paramErrorSuffixes returns the sorted set of "<suffix>|<message>" pairs for
// every error whose field starts with prefix, with the prefix stripped. This
// lets flat-array and steps-tree errors be compared independent of their
// structural prefix.
func paramErrorSuffixes(result *ValidationResult, prefix string) []string {
	var out []string
	for _, e := range result.Errors {
		if strings.HasPrefix(e.Field, prefix) {
			out = append(out, strings.TrimPrefix(e.Field, prefix)+"|"+e.Message)
		}
	}
	sort.Strings(out)
	return out
}

// validateLogActivityFlat builds a single-action flat workflow payload around
// the given params and runs the real validator.
func validateLogActivityFlat(t *testing.T, params map[string]any) *ValidationResult {
	t.Helper()
	actionsJSON, err := json.Marshal([]ActionSpec{{Type: ActionLogActivity, ID: "a1", Params: params}})
	if err != nil {
		t.Fatalf("marshal actions: %v", err)
	}
	return ValidateWorkflowPayload([]byte(validTriggerJSONLA), nil, actionsJSON)
}

// ─────────────────────────────────────────────────────────────────────────
// Property 1 — invalid or missing activity_type is rejected
// ─────────────────────────────────────────────────────────────────────────

type prop1Input struct {
	Present bool
	AT      any
	Title   string
}

func (prop1Input) Generate(r *rand.Rand, _ int) reflect.Value {
	present, value := genInvalidActivityType(r)
	return reflect.ValueOf(prop1Input{Present: present, AT: value, Title: genValidTitle(r)})
}

// Feature: log-activity-action, Property 1: For any log_activity action whose
// activity_type is absent, non-string, empty, whitespace-only, or a string that
// is not exactly one of call/meeting/note/email, the validator marks the result
// invalid and produces an error whose field path ends with params.activity_type.
//
// **Validates: Requirements 2.1, 2.2, 2.3**
func TestLogActivity_Property1_InvalidActivityTypeRejected(t *testing.T) {
	f := func(in prop1Input) bool {
		params := map[string]any{"title": in.Title}
		if in.Present {
			params["activity_type"] = in.AT
		}
		result := validateLogActivityFlat(t, params)
		return !result.Valid && laHasErrorWithFieldSuffix(result, "params.activity_type")
	}
	cfg := &quick.Config{MaxCount: pbtIterations, Rand: rand.New(rand.NewSource(1))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 1 (invalid activity_type rejected) failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Property 2 — missing or blank title is rejected
// ─────────────────────────────────────────────────────────────────────────

type prop2Input struct {
	Present bool
	Title   any
	AT      string
}

func (prop2Input) Generate(r *rand.Rand, _ int) reflect.Value {
	present, value := genInvalidTitle(r)
	return reflect.ValueOf(prop2Input{Present: present, Title: value, AT: genValidActivityType(r)})
}

// Feature: log-activity-action, Property 2: For any log_activity action whose
// title is absent, non-string, empty, or composed only of whitespace (with a
// valid activity_type), the validator marks the result invalid and produces an
// error whose field path ends with params.title.
//
// **Validates: Requirements 2.4, 2.5**
func TestLogActivity_Property2_BlankTitleRejected(t *testing.T) {
	f := func(in prop2Input) bool {
		params := map[string]any{"activity_type": in.AT}
		if in.Present {
			params["title"] = in.Title
		}
		result := validateLogActivityFlat(t, params)
		return !result.Valid && laHasErrorWithFieldSuffix(result, "params.title")
	}
	cfg := &quick.Config{MaxCount: pbtIterations, Rand: rand.New(rand.NewSource(2))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 2 (blank title rejected) failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Property 3 — valid type and title produce no validation error
// ─────────────────────────────────────────────────────────────────────────

type prop3Input struct {
	AT    string
	Title string
}

func (prop3Input) Generate(r *rand.Rand, _ int) reflect.Value {
	return reflect.ValueOf(prop3Input{AT: genValidActivityType(r), Title: genValidTitle(r)})
}

// Feature: log-activity-action, Property 3: For any log_activity action whose
// activity_type is one of call/meeting/note/email and whose title is a
// non-empty, non-whitespace string, the validator produces no validation error
// for that action.
//
// **Validates: Requirements 2.8**
func TestLogActivity_Property3_ValidTypeAndTitleNoError(t *testing.T) {
	f := func(in prop3Input) bool {
		params := map[string]any{"activity_type": in.AT, "title": in.Title}
		result := validateLogActivityFlat(t, params)
		// No error may be reported for this action (actions[0].*), and the
		// overall result must be valid.
		return result.Valid && !laHasErrorWithFieldPrefix(result, "actions[0]")
	}
	cfg := &quick.Config{MaxCount: pbtIterations, Rand: rand.New(rand.NewSource(3))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 3 (valid type+title => no error) failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Property 4 — flat-array and steps-tree validation are equivalent
// ─────────────────────────────────────────────────────────────────────────

type prop4Input struct {
	PresentAT    bool
	AT           any
	PresentTitle bool
	Title        any
}

func (prop4Input) Generate(r *rand.Rand, _ int) reflect.Value {
	pa, av := genAnyActivityType(r)
	pt, tv := genAnyTitle(r)
	return reflect.ValueOf(prop4Input{PresentAT: pa, AT: av, PresentTitle: pt, Title: tv})
}

// Feature: log-activity-action, Property 4: For any log_activity action (valid
// or invalid), validating it inside the flat actions array and inside an
// equivalent single-step "action" steps tree produces the same number and kind
// of validation errors, with field paths differing only by their structural
// prefix (actions[0].params... vs steps[0].action.params...).
//
// **Validates: Requirements 2.6**
func TestLogActivity_Property4_FlatAndStepsEquivalent(t *testing.T) {
	f := func(in prop4Input) bool {
		params := map[string]any{}
		if in.PresentAT {
			params["activity_type"] = in.AT
		}
		if in.PresentTitle {
			params["title"] = in.Title
		}

		actionsJSON, err := json.Marshal([]ActionSpec{{Type: ActionLogActivity, ID: "a1", Params: params}})
		if err != nil {
			t.Fatalf("marshal actions: %v", err)
		}
		stepsJSON, err := json.Marshal([]StepSpec{{
			Type:   "action",
			ID:     "a1",
			Action: &ActionSpec{Type: ActionLogActivity, ID: "a1", Params: params},
		}})
		if err != nil {
			t.Fatalf("marshal steps: %v", err)
		}

		flat := ValidateWorkflowPayload([]byte(validTriggerJSONLA), nil, actionsJSON)
		// When steps are present, actions are ignored; pass "[]" for actions.
		steps := ValidateWorkflowPayload([]byte(validTriggerJSONLA), nil, []byte("[]"), stepsJSON)

		flatErrs := paramErrorSuffixes(flat, "actions[0]")
		stepErrs := paramErrorSuffixes(steps, "steps[0].action")

		if !reflect.DeepEqual(flatErrs, stepErrs) {
			t.Logf("flat/steps error mismatch for params=%#v\n  flat=%v\n  steps=%v", params, flatErrs, stepErrs)
			return false
		}
		return flat.Valid == steps.Valid
	}
	cfg := &quick.Config{MaxCount: pbtIterations, Rand: rand.New(rand.NewSource(4))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 4 (flat vs steps equivalence) failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Example + smoke tests (Requirements 1.1, 1.2, 1.3, 2.7)
// ─────────────────────────────────────────────────────────────────────────

// Req 1.1: the action type constant string value is exactly "log_activity".
func TestLogActivity_ActionConstantValue(t *testing.T) {
	if ActionLogActivity != "log_activity" {
		t.Fatalf("ActionLogActivity = %q, want %q", ActionLogActivity, "log_activity")
	}
}

// Req 1.2: "log_activity" is registered as true in ValidActionTypes.
func TestLogActivity_RegisteredInValidActionTypes(t *testing.T) {
	if !ValidActionTypes["log_activity"] {
		t.Fatal("ValidActionTypes[\"log_activity\"] = false, want true")
	}
}

// Req 1.3: a workflow with a valid log_activity action validates with no
// unknown-action-type error, and is overall Valid when it is the only action.
func TestLogActivity_ValidActionHasNoUnknownTypeError(t *testing.T) {
	actions := `[{"type":"log_activity","id":"a1","params":{"activity_type":"call","title":"Logged a call with {{contact.first_name}}"}}]`
	result := ValidateWorkflowPayload([]byte(validTriggerJSONLA), nil, []byte(actions))
	if !result.Valid {
		t.Fatalf("expected valid, got errors: %+v", result.Errors)
	}
	for _, e := range result.Errors {
		if strings.Contains(strings.ToLower(e.Message), "unknown action type") {
			t.Fatalf("unexpected unknown-action-type error: %+v", e)
		}
	}
}

// Req 2.7: a workflow with TWO invalid log_activity actions reports BOTH errors
// (validation continues across actions instead of stopping at the first).
func TestLogActivity_TwoInvalidActionsBothReported(t *testing.T) {
	actions := `[{"type":"log_activity","id":"a1","params":{}},{"type":"log_activity","id":"a2","params":{}}]`
	result := ValidateWorkflowPayload([]byte(validTriggerJSONLA), nil, []byte(actions))
	if result.Valid {
		t.Fatal("expected invalid for two misconfigured log_activity actions")
	}
	if !laHasExactErrorField(result, "actions[0].params.activity_type") {
		t.Fatalf("expected error for actions[0].params.activity_type, got: %+v", result.Errors)
	}
	if !laHasExactErrorField(result, "actions[1].params.activity_type") {
		t.Fatalf("expected error for actions[1].params.activity_type, got: %+v", result.Errors)
	}
	// Both missing-title errors should also be present for completeness.
	if !laHasExactErrorField(result, "actions[0].params.title") || !laHasExactErrorField(result, "actions[1].params.title") {
		t.Fatalf("expected title errors for both actions, got: %+v", result.Errors)
	}
}
