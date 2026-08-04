package automation

import (
	"encoding/json"
	"testing"

	"gorm.io/datatypes"
)

// steps_fixtures_test.go holds the shared "express this as a steps tree" helpers that
// R5 deploy 1 made necessary.
//
// WHY THEY EXIST. Before the teardown, a great many tests seeded a workflow (or a
// validator payload) as a FLAT action list, because that was the shortest way to say
// "this workflow does A then B". The flat list is gone: it is no longer a column the
// model writes, no longer a request field, no longer an execution path, and no longer
// a validator entry point. Those tests were not testing the flat list, though — they
// were testing retry/resume, timers, suppression, activity logging, deal-stage writes,
// parameter validation. Rather than delete them with the feature, they were rewritten
// through these helpers to say exactly the same thing in the one shape that survives.
//
// A flat action list and a list of single-action steps are behaviourally identical for
// everything below the entry point: validateActionParams is shared verbatim, and
// executeAction is reached either way. The repo proved that equivalence itself before
// the rewrite — validator_log_activity_test.go carried a property test asserting flat
// and steps produced identical errors modulo the path prefix (`actions[0].params…` vs
// `steps[0].action.params…`). That test has been retired because there is no longer a
// second path to compare against, but it is the reason this rewrite is mechanical
// rather than a re-derivation.

// actionStep wraps one action as an "action" step, mirroring the id onto the step as
// the validator requires (step.id must equal action.id).
func actionStep(a ActionSpec) StepSpec {
	act := a
	return StepSpec{Type: "action", ID: act.ID, Action: &act}
}

// actionSteps wraps a flat action list as an equivalent linear steps tree.
func actionSteps(actions ...ActionSpec) []StepSpec {
	out := make([]StepSpec, 0, len(actions))
	for _, a := range actions {
		out = append(out, actionStep(a))
	}
	return out
}

// actionStepsJSON is actionSteps, marshalled — the usual form for a Workflow.Steps or
// WorkflowVersion.Steps fixture.
func actionStepsJSON(t *testing.T, actions ...ActionSpec) datatypes.JSON {
	t.Helper()
	raw, err := json.Marshal(actionSteps(actions...))
	if err != nil {
		t.Fatalf("marshal steps fixture: %v", err)
	}
	return datatypes.JSON(raw)
}

// stepsFromActionsJSON re-expresses a raw flat-actions JSON array as a raw steps JSON
// array. For the validator tests, which are written against JSON text so the params
// survive a real round-trip (a []string literal arrives as []any, a 60.5 as float64) —
// exactly as it would from an HTTP body.
func stepsFromActionsJSON(t *testing.T, actionsJSON []byte) []byte {
	t.Helper()
	var actions []ActionSpec
	if err := json.Unmarshal(actionsJSON, &actions); err != nil {
		t.Fatalf("fixture actions are not a JSON array of actions: %v (%s)", err, actionsJSON)
	}
	raw, err := json.Marshal(actionSteps(actions...))
	if err != nil {
		t.Fatalf("marshal steps fixture: %v", err)
	}
	return raw
}

// validateSingleActionParams runs the LIVE validator entry point over a flat action
// list by wrapping it as steps first. Errors come back at `steps[i].action.params.…`
// rather than the retired `actions[i].params.…`.
func validateSingleActionParams(t *testing.T, actions []ActionSpec, result *ValidationResult) {
	t.Helper()
	raw, err := json.Marshal(actions)
	if err != nil {
		t.Fatalf("marshal actions fixture: %v", err)
	}
	validateSteps(stepsFromActionsJSON(t, raw), result)
}

// stepActionPath is where the steps validator reports what the flat validator used to
// report at "actions[0]".
const stepActionPath = "steps[0].action"

// completedStepKeys reads a run's completed_actions in the shape the STEPS executor
// writes it: a JSON array of strings holding each finished step's id AND its structural
// path (syncRunCompleted). The flat executor wrote integer indices into the same column,
// which is why GetCompletedActionIndices cannot read a modern run — see its doc comment.
func completedStepKeys(t *testing.T, run *WorkflowRun) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if len(run.CompletedActions) == 0 {
		return out
	}
	var keys []string
	if err := json.Unmarshal(run.CompletedActions, &keys); err != nil {
		t.Fatalf("completed_actions is not a JSON string array (%s): %v", run.CompletedActions, err)
	}
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// logStatusesByPath groups a run's action logs by their STRUCTURAL PATH, which is how
// the steps executor identifies a step ("0", "1", "0.yes.0", …).
//
// This is the replacement for keying on ActionIdx, and it is not cosmetic: the steps
// executor leaves action_idx at its zero value on every row, so grouping a steps run's
// logs by index silently collapses every step into bucket 0. Resume state is derived
// from these same rows — processRun rebuilds "which steps are done" from the SUCCESS
// logs — so they are also the only durable record of progress now that the flat path's
// completed_actions / current_action_idx columns have no writer.
func logStatusesByPath(logs []WorkflowActionLog) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, l := range logs {
		if out[l.ActionPath] == nil {
			out[l.ActionPath] = map[string]int{}
		}
		out[l.ActionPath][l.Status]++
	}
	return out
}
