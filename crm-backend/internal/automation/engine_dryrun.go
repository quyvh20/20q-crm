package automation

import (
	"encoding/json"
	"fmt"

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
// then walks the steps tree.
func (e *Engine) DryRun(orgID uuid.UUID, wf *Workflow, triggerContext map[string]any) TestRunResponse {
	ctxJSON, _ := json.Marshal(triggerContext)
	run := &WorkflowRun{OrgID: orgID, TriggerContext: datatypes.JSON(ctxJSON)}
	return dryRunWorkflow(wf, e.buildEvalContext(run))
}

// dryRunWorkflow is the pure (no DB) core of a dry run. It mirrors the real driver
// (processRun in engine.go), and the top-level wf.Conditions are IGNORED — nothing in
// the engine has ever evaluated wf.Conditions for a steps workflow, and since R5
// deploy 1 removed the flat-actions branch there is no other kind of workflow. A row
// migrated from the pre-A1 era can still carry a stale top-level conditions group
// alongside its steps; honouring it here would invert run/skip against what the
// engine actually does. Per-step condition nodes are always evaluated by the walker.
func dryRunWorkflow(wf *Workflow, evalCtx EvalContext) TestRunResponse {
	return evaluateDryRun(nil, stepsForDryRun(wf), evalCtx)
}

// stepsForDryRun returns the workflow's steps tree, or nil when it has none (which
// previews as a workflow that does nothing — the same thing processRun now treats as
// a failure).
func stepsForDryRun(wf *Workflow) []StepSpec {
	if len(wf.Steps) > 0 && string(wf.Steps) != "null" {
		var steps []StepSpec
		if json.Unmarshal(wf.Steps, &steps) == nil {
			return steps
		}
	}
	return nil
}

// evaluateDryRun is the pure core (no DB): it applies the top-level condition gate
// and walks the tree. Exposed at package level so it can be unit-tested with a
// hand-built EvalContext.
//
// The only production caller (dryRunWorkflow) passes nil conditions and always will:
// no workflow has a top-level gate any more, since the one execution path that
// evaluated wf.Conditions was the flat-actions branch of processRun. The parameter
// stays because it is what defines ConditionResult and the "workflow conditions not
// met" skip reason in the response contract — but do NOT wire wf.Conditions back into
// it without changing processRun to match, or the preview will disagree with the run.
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

		case "split":
			// A dry run has no run id, so the real hash decision can't be
			// previewed — follow the A branch and say so in the reason.
			s := TestRunStep{StepID: step.ID, Type: "split"}
			percent := 50
			if step.Split != nil {
				percent = step.Split.PercentA
			}
			if reached {
				s.Status = "run"
				s.Branch = "yes"
				s.Reason = fmt.Sprintf("%d%% of runs take A, %d%% take B — preview follows A", percent, 100-percent)
				*out = append(*out, s)
				dryWalkSteps(step.YesSteps, evalCtx, true, "branch not taken", out)
				dryWalkSteps(step.NoSteps, evalCtx, false, "branch not taken", out)
			} else {
				s.Status = "skip"
				s.Reason = skipReason
				*out = append(*out, s)
				dryWalkSteps(step.YesSteps, evalCtx, false, skipReason, out)
				dryWalkSteps(step.NoSteps, evalCtx, false, skipReason, out)
			}

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

// toInt coerces a JSON-decoded number (float64) or int to int. Used by the validator
// to read numeric action params out of the untyped params map.
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
