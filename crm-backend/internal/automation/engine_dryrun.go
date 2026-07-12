package automation

import (
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// engine_dryrun.go implements the A3.5 dry run: a side-effect-free walk of the
// steps tree that mirrors the real executor's branch semantics
// (executeStepsWithState in engine.go) but records, per step, whether it would run
// or be skipped and — for actions — the interpolated params it would run with.
// The builder overlays this onto the canvas.

// DryRun evaluates a workflow against a sample trigger context without any side
// effects or a persisted Workflow_Run. It builds the eval context the same way a
// real run does (buildEvalContext — hydrating deal→contact and company relations),
// then walks the steps tree. Legacy actions-only workflows (no Steps) fall back to
// a linear action list so they still preview.
func (e *Engine) DryRun(orgID uuid.UUID, wf *Workflow, triggerContext map[string]any) TestRunResponse {
	ctxJSON, _ := json.Marshal(triggerContext)
	run := &WorkflowRun{OrgID: orgID, TriggerContext: datatypes.JSON(ctxJSON)}
	return dryRunWorkflow(wf, e.buildEvalContext(run))
}

// dryRunWorkflow is the pure (no DB) core of a dry run. It mirrors the real driver
// (processRun in engine.go): a steps-based workflow executes its tree and the
// top-level wf.Conditions are IGNORED — processRun returns right after the steps
// block, before the legacy top-level condition check (engine.go ~563 vs ~589), and
// nothing else in the engine evaluates wf.Conditions for steps workflows. So the
// top-level gate is applied here ONLY on the legacy flat-actions path (no steps
// tree); per-step condition nodes are always evaluated by the walker. Getting this
// wrong would invert run/skip for a migrated workflow that still carries a legacy
// top-level conditions group alongside its steps.
func dryRunWorkflow(wf *Workflow, evalCtx EvalContext) TestRunResponse {
	steps, usedStepsTree := stepsForDryRun(wf)
	var conditions *ConditionGroup
	if !usedStepsTree && len(wf.Conditions) > 0 && string(wf.Conditions) != "null" {
		var cg ConditionGroup
		if json.Unmarshal(wf.Conditions, &cg) == nil {
			conditions = &cg
		}
	}
	return evaluateDryRun(conditions, steps, evalCtx)
}

// stepsForDryRun returns the workflow's canonical steps and whether they came from
// the steps tree (true) or were derived from the deprecated flat Actions (false) for
// pre-A1 rows that never stored a tree (mirrors the frontend's loadWorkflow mapping).
// The caller uses the flag to decide whether the top-level conditions gate applies.
func stepsForDryRun(wf *Workflow) ([]StepSpec, bool) {
	if len(wf.Steps) > 0 && string(wf.Steps) != "null" {
		var steps []StepSpec
		if json.Unmarshal(wf.Steps, &steps) == nil && len(steps) > 0 {
			return steps, true
		}
	}
	var actions []ActionSpec
	if len(wf.Actions) > 0 {
		_ = json.Unmarshal(wf.Actions, &actions)
	}
	steps := make([]StepSpec, 0, len(actions))
	for _, a := range actions {
		if a.Type == ActionDelay {
			sec := 0
			if v, ok := a.Params["duration_sec"]; ok {
				sec = toInt(v)
			}
			steps = append(steps, StepSpec{Type: "delay", ID: a.ID, Delay: &DelayParams{DurationSec: sec}})
			continue
		}
		act := a
		steps = append(steps, StepSpec{Type: "action", ID: a.ID, Action: &act})
	}
	return steps, false
}

// evaluateDryRun is the pure core (no DB): it applies the top-level condition gate
// and walks the tree. Exposed at package level so it can be unit-tested with a
// hand-built EvalContext.
func evaluateDryRun(conditions *ConditionGroup, steps []StepSpec, evalCtx EvalContext) TestRunResponse {
	conditionResult := true
	if conditions != nil && (conditions.Op != "" || conditions.Field != "" || len(conditions.Rules) > 0) {
		conditionResult = EvaluateConditions(*conditions, evalCtx)
	}
	var out []TestRunStep
	dryWalkSteps(steps, evalCtx, conditionResult, "workflow conditions not met", &out)
	return TestRunResponse{ConditionResult: conditionResult, Steps: out}
}

// dryWalkSteps walks an ordered step list. `reached` is false once we've descended
// into an untaken condition branch (or the whole run is gated off), in which case
// every step in the subtree is a skip carrying skipReason. Condition steps that are
// reached evaluate their group and recurse into the taken branch as reached and the
// other branch as not-reached — the sibling after a condition stays reached, matching
// the engine (branch tails rejoin the next sibling).
func dryWalkSteps(steps []StepSpec, evalCtx EvalContext, reached bool, skipReason string, out *[]TestRunStep) {
	for _, step := range steps {
		switch step.Type {
		case "action":
			s := TestRunStep{StepID: step.ID, Type: "action", ActionType: actionTypeOf(step)}
			if reached {
				s.Status = "run"
				s.ResolvedParams = resolveDryParams(step, evalCtx)
			} else {
				s.Status = "skip"
				s.Reason = skipReason
			}
			*out = append(*out, s)

		case "delay":
			s := TestRunStep{StepID: step.ID, Type: "delay"}
			if step.Delay != nil {
				s.DelaySec = step.Delay.DurationSec
			}
			if reached {
				s.Status = "run"
			} else {
				s.Status = "skip"
				s.Reason = skipReason
			}
			*out = append(*out, s)

		case "condition":
			s := TestRunStep{StepID: step.ID, Type: "condition"}
			if reached {
				runYes := false
				if step.Condition != nil {
					runYes = EvaluateConditions(*step.Condition, evalCtx)
				}
				s.Status = "run"
				s.ConditionResult = &runYes
				if runYes {
					s.Branch = "yes"
				} else {
					s.Branch = "no"
				}
				*out = append(*out, s)
				dryWalkSteps(step.YesSteps, evalCtx, runYes, "branch not taken", out)
				dryWalkSteps(step.NoSteps, evalCtx, !runYes, "branch not taken", out)
			} else {
				s.Status = "skip"
				s.Reason = skipReason
				*out = append(*out, s)
				dryWalkSteps(step.YesSteps, evalCtx, false, skipReason, out)
				dryWalkSteps(step.NoSteps, evalCtx, false, skipReason, out)
			}
		}
	}
}

// resolveDryParams interpolates a step's params against the eval context, mirroring
// the real executor's template resolution. Interpolation recurses into nested
// structures so previews are accurate for actions whose meaningful params are nested
// (notably update_record's `updates` array, whose field/value the executor
// interpolates), not just top-level strings.
func resolveDryParams(step StepSpec, evalCtx EvalContext) map[string]any {
	if step.Action == nil {
		return nil
	}
	resolved := make(map[string]any, len(step.Action.Params))
	for k, v := range step.Action.Params {
		// Mirror the executor: send_email's body_html is an HTML context, so the
		// preview shows the same escaped output the send would produce.
		if step.Action.Type == "send_email" && k == "body_html" {
			if s, ok := v.(string); ok {
				resolved[k] = InterpolateTemplateHTML(s, evalCtx)
				continue
			}
		}
		resolved[k] = interpolateValue(v, evalCtx)
	}
	return resolved
}

// interpolateValue interpolates every string within a JSON-shaped value (strings,
// arrays, and objects), leaving other scalars unchanged.
func interpolateValue(v any, evalCtx EvalContext) any {
	switch t := v.(type) {
	case string:
		return InterpolateTemplate(t, evalCtx)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = interpolateValue(item, evalCtx)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = interpolateValue(item, evalCtx)
		}
		return out
	default:
		return v
	}
}

func actionTypeOf(step StepSpec) string {
	if step.Action != nil {
		return step.Action.Type
	}
	return ""
}

// toInt coerces a JSON-decoded number (float64) or int to int for the delay fallback.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}
