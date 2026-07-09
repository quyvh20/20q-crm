package automation

import (
	"encoding/json"
	"testing"

	"gorm.io/datatypes"
)

func mustDRJSON(v any) datatypes.JSON {
	b, _ := json.Marshal(v)
	return datatypes.JSON(b)
}

// These exercise the A3.5 dry-run walker (evaluateDryRun/dryWalkSteps) directly with
// a hand-built EvalContext — no DB — so branch selection, skip propagation, top-level
// gating, and param interpolation are all covered without Docker.

func drAction(id, atype string, params map[string]any) StepSpec {
	return StepSpec{Type: "action", ID: id, Action: &ActionSpec{ID: id, Type: atype, Params: params}}
}
func drDelay(id string, sec int) StepSpec {
	return StepSpec{Type: "delay", ID: id, Delay: &DelayParams{DurationSec: sec}}
}
func drCond(id string, cond ConditionGroup, yes, no []StepSpec) StepSpec {
	return StepSpec{Type: "condition", ID: id, Condition: &cond, YesSteps: yes, NoSteps: no}
}
func byStepID(steps []TestRunStep) map[string]TestRunStep {
	m := make(map[string]TestRunStep, len(steps))
	for _, s := range steps {
		m[s.StepID] = s
	}
	return m
}

func TestDryRun_LinearAllRun(t *testing.T) {
	ctx := EvalContext{Contact: map[string]any{"first_name": "Ada"}}
	steps := []StepSpec{
		drAction("a1", "send_email", map[string]any{"subject": "Hi {{contact.first_name}}"}),
		drDelay("d1", 3600),
		drAction("a2", "create_task", map[string]any{"title": "Follow up"}),
	}
	res := evaluateDryRun(nil, steps, ctx)

	if !res.ConditionResult {
		t.Fatalf("no top-level conditions should gate open, got condition_result=false")
	}
	m := byStepID(res.Steps)
	if len(res.Steps) != 3 {
		t.Fatalf("want 3 step results, got %d", len(res.Steps))
	}
	if m["a1"].Status != "run" || m["d1"].Status != "run" || m["a2"].Status != "run" {
		t.Fatalf("all steps should run: %+v", m)
	}
	if got := m["a1"].ResolvedParams["subject"]; got != "Hi Ada" {
		t.Errorf("subject interpolation: want %q, got %q", "Hi Ada", got)
	}
	if m["a1"].ActionType != "send_email" {
		t.Errorf("action_type: want send_email, got %q", m["a1"].ActionType)
	}
	if m["d1"].DelaySec != 3600 {
		t.Errorf("delay_sec: want 3600, got %d", m["d1"].DelaySec)
	}
}

func TestDryRun_ConditionYesBranchSkipsNoAndRejoins(t *testing.T) {
	ctx := EvalContext{Contact: map[string]any{"tier": "gold"}}
	cond := ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "gold"}
	steps := []StepSpec{
		drCond("c1", cond,
			[]StepSpec{drAction("y1", "send_email", nil)},
			[]StepSpec{drAction("n1", "create_task", nil)}),
		drAction("after", "assign_user", nil), // sibling after the condition rejoins
	}
	res := evaluateDryRun(nil, steps, ctx)
	m := byStepID(res.Steps)

	if m["c1"].Status != "run" || m["c1"].Branch != "yes" || m["c1"].ConditionResult == nil || !*m["c1"].ConditionResult {
		t.Fatalf("condition should run and take yes: %+v", m["c1"])
	}
	if m["y1"].Status != "run" {
		t.Errorf("yes branch should run, got %q", m["y1"].Status)
	}
	if m["n1"].Status != "skip" || m["n1"].Reason != "branch not taken" {
		t.Errorf("no branch should skip (branch not taken), got %q/%q", m["n1"].Status, m["n1"].Reason)
	}
	if m["after"].Status != "run" {
		t.Errorf("sibling after condition should run (branches rejoin), got %q", m["after"].Status)
	}
}

func TestDryRun_ConditionNoBranch(t *testing.T) {
	ctx := EvalContext{Contact: map[string]any{"tier": "silver"}}
	cond := ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "gold"}
	steps := []StepSpec{
		drCond("c1", cond,
			[]StepSpec{drAction("y1", "send_email", nil)},
			[]StepSpec{drAction("n1", "create_task", nil)}),
	}
	res := evaluateDryRun(nil, steps, ctx)
	m := byStepID(res.Steps)

	if m["c1"].Branch != "no" || *m["c1"].ConditionResult {
		t.Fatalf("condition should take no branch: %+v", m["c1"])
	}
	if m["n1"].Status != "run" {
		t.Errorf("no branch should run, got %q", m["n1"].Status)
	}
	if m["y1"].Status != "skip" {
		t.Errorf("yes branch should skip, got %q", m["y1"].Status)
	}
}

func TestDryRun_TopLevelConditionsFailSkipsEverything(t *testing.T) {
	ctx := EvalContext{Contact: map[string]any{"tier": "silver"}}
	topConds := &ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "gold"}
	steps := []StepSpec{
		drAction("a1", "send_email", nil),
		drCond("c1", ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "silver"},
			[]StepSpec{drAction("y1", "create_task", nil)}, nil),
	}
	res := evaluateDryRun(topConds, steps, ctx)

	if res.ConditionResult {
		t.Fatalf("top-level conditions should fail")
	}
	for _, s := range res.Steps {
		if s.Status != "skip" || s.Reason != "workflow conditions not met" {
			t.Errorf("step %s should skip (workflow conditions not met), got %q/%q", s.StepID, s.Status, s.Reason)
		}
	}
	// The nested condition and its child are present and skipped too.
	m := byStepID(res.Steps)
	if _, ok := m["y1"]; !ok {
		t.Errorf("nested branch step y1 should still be reported (as skip)")
	}
}

func TestDryRun_NestedCondition(t *testing.T) {
	ctx := EvalContext{Deal: map[string]any{"value": float64(5000)}}
	// c1: deal.value gte 1000 → yes. Inside yes: c2: deal.value gte 10000 → no.
	c2 := drCond("c2",
		ConditionGroup{Field: "deal.value", Operator: "gte", Value: float64(10000)},
		[]StepSpec{drAction("yy", "send_email", nil)},
		[]StepSpec{drAction("yn", "create_task", nil)})
	steps := []StepSpec{
		drCond("c1",
			ConditionGroup{Field: "deal.value", Operator: "gte", Value: float64(1000)},
			[]StepSpec{c2},
			[]StepSpec{drAction("n1", "assign_user", nil)}),
	}
	res := evaluateDryRun(nil, steps, ctx)
	m := byStepID(res.Steps)

	if m["c1"].Branch != "yes" {
		t.Fatalf("c1 should take yes, got %q", m["c1"].Branch)
	}
	if m["c2"].Status != "run" || m["c2"].Branch != "no" {
		t.Fatalf("c2 should run and take no, got %q/%q", m["c2"].Status, m["c2"].Branch)
	}
	if m["yn"].Status != "run" {
		t.Errorf("yn should run, got %q", m["yn"].Status)
	}
	if m["yy"].Status != "skip" {
		t.Errorf("yy should skip (branch not taken), got %q", m["yy"].Status)
	}
	if m["n1"].Status != "skip" {
		t.Errorf("n1 (c1 no-branch) should skip, got %q", m["n1"].Status)
	}
}

func TestDryRun_NonStringParamsPassThrough(t *testing.T) {
	ctx := EvalContext{Contact: map[string]any{"first_name": "Ada"}}
	steps := []StepSpec{
		drAction("a1", "create_task", map[string]any{
			"title":       "Call {{contact.first_name}}",
			"due_in_days": float64(3),
			"updates":     []any{map[string]any{"field": "contact.tier", "op": "set", "value": "gold"}},
		}),
	}
	res := evaluateDryRun(nil, steps, ctx)
	rp := byStepID(res.Steps)["a1"].ResolvedParams

	if rp["title"] != "Call Ada" {
		t.Errorf("string param should interpolate, got %v", rp["title"])
	}
	if rp["due_in_days"] != float64(3) {
		t.Errorf("numeric param should pass through, got %v", rp["due_in_days"])
	}
	if _, ok := rp["updates"].([]any); !ok {
		t.Errorf("nested array param should pass through unchanged, got %T", rp["updates"])
	}
}

func TestDryRun_EmptyTopLevelConditionsPass(t *testing.T) {
	res := evaluateDryRun(&ConditionGroup{}, []StepSpec{drAction("a1", "send_email", nil)}, EvalContext{})
	if !res.ConditionResult {
		t.Errorf("an empty condition group should gate open")
	}
	if byStepID(res.Steps)["a1"].Status != "run" {
		t.Errorf("step should run when conditions are empty")
	}
}

// A steps-based workflow must IGNORE its top-level wf.Conditions, mirroring the real
// engine (processRun returns after the steps block, before the legacy condition
// check). A migrated workflow can carry both a steps tree and a stale top-level
// group; the dry run must not gate the steps on it.
func TestDryRunWorkflow_StepsIgnoreTopLevelConditions(t *testing.T) {
	wf := &Workflow{
		Steps:      mustDRJSON([]StepSpec{drAction("a1", "send_email", map[string]any{"subject": "hi"})}),
		Conditions: mustDRJSON(ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "gold"}),
	}
	ctx := EvalContext{Contact: map[string]any{"tier": "silver"}} // would FAIL the top-level group
	res := dryRunWorkflow(wf, ctx)

	if !res.ConditionResult {
		t.Errorf("steps workflow should report condition_result=true (top-level conditions ignored)")
	}
	if byStepID(res.Steps)["a1"].Status != "run" {
		t.Errorf("steps workflow should run its steps regardless of the stale top-level conditions")
	}
}

// The legacy flat-actions path DOES honor top-level conditions (engine.go:589), so a
// no-steps workflow whose group fails skips everything.
func TestDryRunWorkflow_LegacyActionsHonorTopLevelConditions(t *testing.T) {
	wf := &Workflow{
		Actions:    mustDRJSON([]ActionSpec{{ID: "a1", Type: "send_email", Params: map[string]any{"subject": "hi"}}}),
		Conditions: mustDRJSON(ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "gold"}),
	}
	ctx := EvalContext{Contact: map[string]any{"tier": "silver"}}
	res := dryRunWorkflow(wf, ctx)

	if res.ConditionResult {
		t.Errorf("legacy actions workflow should honor top-level conditions (fail here)")
	}
	s := byStepID(res.Steps)["a1"]
	if s.Status != "skip" || s.Reason != "workflow conditions not met" {
		t.Errorf("legacy step should skip with reason 'workflow conditions not met', got %q/%q", s.Status, s.Reason)
	}
}
