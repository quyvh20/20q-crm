package automation

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deterministic run IDs so every assertion in this file is reproducible: the
// same seed always yields the same UUID, and therefore the same split branch.
func seededRunID(i int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("split-test-run-%d", i)))
}

// ═══════════════════════════════════════════════════════════════════
// splitTakesA — determinism + distribution
// ═══════════════════════════════════════════════════════════════════

func TestSplitTakesA_Deterministic(t *testing.T) {
	run := seededRunID(1)
	first := splitTakesA(run, "split_1", 30)
	for i := 0; i < 1000; i++ {
		assert.Equal(t, first, splitTakesA(run, "split_1", 30), "same (run, step, percent) must always choose the same branch")
	}
}

func TestSplitTakesA_DifferentStepsIndependent(t *testing.T) {
	// Two splits in the same run must not be correlated by construction: at
	// least one of many step ids should disagree with split_a's choice.
	run := seededRunID(2)
	a := splitTakesA(run, "split_a", 50)
	disagree := false
	for i := 0; i < 50; i++ {
		if splitTakesA(run, fmt.Sprintf("other_%d", i), 50) != a {
			disagree = true
			break
		}
	}
	assert.True(t, disagree, "branch choices must vary across step ids")
}

func TestSplitTakesA_Distribution(t *testing.T) {
	// Fully deterministic (seeded run ids): 10k runs at 30% should land near
	// 3000 A-assignments. sha256 is uniform; ±3% absolute tolerance.
	const n = 10000
	const percent = 30
	countA := 0
	for i := 0; i < n; i++ {
		if splitTakesA(seededRunID(i), "split_dist", percent) {
			countA++
		}
	}
	assert.InDelta(t, n*percent/100, countA, n*3/100,
		"A-branch share should track percent_a (got %d/%d)", countA, n)
}

func TestSplitTakesA_Bounds(t *testing.T) {
	// percent 0 can never take A; percent 100 always does (validator forbids
	// both, but the hash must still behave sanely if handed them).
	for i := 0; i < 200; i++ {
		assert.False(t, splitTakesA(seededRunID(i), "s", 0))
		assert.True(t, splitTakesA(seededRunID(i), "s", 100))
	}
}

// ═══════════════════════════════════════════════════════════════════
// Validator
// ═══════════════════════════════════════════════════════════════════

func splitStep(id string, percent int, yes, no []StepSpec) StepSpec {
	return StepSpec{Type: "split", ID: id, Split: &SplitParams{PercentA: percent}, YesSteps: yes, NoSteps: no}
}

func splitActionStep(id string) StepSpec {
	return StepSpec{Type: "action", ID: id, Action: &ActionSpec{Type: "create_task", ID: id, Params: map[string]any{"title": "t"}}}
}

func runStepValidation(t *testing.T, steps []StepSpec) *ValidationResult {
	t.Helper()
	result := &ValidationResult{Valid: true}
	validateStepsRecursive(steps, "steps", 0, map[string]bool{}, result)
	return result
}

func TestValidator_SplitValid(t *testing.T) {
	res := runStepValidation(t, []StepSpec{
		splitStep("s1", 30, []StepSpec{splitActionStep("a1")}, []StepSpec{splitActionStep("b1")}),
	})
	assert.True(t, res.Valid, "errors: %v", res.Errors)
}

func TestValidator_SplitRequiresParams(t *testing.T) {
	res := runStepValidation(t, []StepSpec{{Type: "split", ID: "s1"}})
	require.False(t, res.Valid)
	assert.Contains(t, res.Errors[0].Message, "split property is required")
}

func TestValidator_SplitPercentBounds(t *testing.T) {
	for _, p := range []int{0, 100, -5, 250} {
		res := runStepValidation(t, []StepSpec{splitStep("s1", p, nil, nil)})
		require.False(t, res.Valid, "percent %d must be rejected", p)
		assert.Contains(t, res.Errors[0].Message, "between 1 and 99")
	}
}

func TestValidator_SplitBranchesCountTowardDepth(t *testing.T) {
	// 6 nested splits exceed MaxStepTreeDepth=5 exactly like nested conditions.
	deep := splitStep("s6", 50, nil, nil)
	for i := 5; i >= 1; i-- {
		deep = splitStep(fmt.Sprintf("s%d", i), 50, []StepSpec{deep}, nil)
	}
	res := runStepValidation(t, []StepSpec{deep})
	require.False(t, res.Valid)
}

func TestValidator_SplitBranchStepsValidated(t *testing.T) {
	// A malformed action inside a split branch must be caught.
	bad := StepSpec{Type: "action", ID: "bad"} // no Action payload
	res := runStepValidation(t, []StepSpec{splitStep("s1", 50, []StepSpec{bad}, nil)})
	require.False(t, res.Valid)
}

// ═══════════════════════════════════════════════════════════════════
// FlattenStepsToActions — the generic YesSteps/NoSteps recursion must see
// into split branches (security: shouldActivate + workflowHasMarketingSend
// consume this walk; a miss here fails OPEN).
// ═══════════════════════════════════════════════════════════════════

func TestFlatten_SeesIntoSplitBranches(t *testing.T) {
	steps := []StepSpec{
		splitStep("s1", 40,
			[]StepSpec{{Type: "action", ID: "mail_a", Action: &ActionSpec{Type: "send_email", ID: "mail_a", Params: map[string]any{}}}},
			[]StepSpec{{Type: "action", ID: "hook_b", Action: &ActionSpec{Type: "send_webhook", ID: "hook_b", Params: map[string]any{}}}},
		),
	}
	flat := FlattenStepsToActions(steps)
	ids := make([]string, 0, len(flat))
	for _, a := range flat {
		ids = append(ids, a.ID)
	}
	assert.Equal(t, []string{"mail_a", "hook_b"}, ids,
		"actions hidden inside split branches must be visible to the flatten walk")
}

func TestCountSteps_SeesIntoSplitBranches(t *testing.T) {
	steps := []StepSpec{
		splitStep("s1", 50,
			[]StepSpec{splitActionStep("a1"), splitActionStep("a2")},
			[]StepSpec{splitActionStep("b1")},
		),
	}
	assert.Equal(t, 3, countStepsList(steps))
}

// ═══════════════════════════════════════════════════════════════════
// Engine walk — hash decides, pinning overrides
// ═══════════════════════════════════════════════════════════════════

func splitTree() []StepSpec {
	return []StepSpec{
		splitStep("split_1", 50,
			[]StepSpec{{Type: "action", ID: "branch_a", Action: &ActionSpec{Type: "create_task", ID: "branch_a", Params: map[string]any{}}}},
			[]StepSpec{{Type: "action", ID: "branch_b", Action: &ActionSpec{Type: "create_task", ID: "branch_b", Params: map[string]any{}}}},
		),
	}
}

// findRunFor returns a seeded run id whose hash assigns the wanted branch.
func findRunFor(t *testing.T, wantA bool) uuid.UUID {
	t.Helper()
	for i := 0; i < 1000; i++ {
		id := seededRunID(i)
		if splitTakesA(id, "split_1", 50) == wantA {
			return id
		}
	}
	t.Fatal("no seeded run id found for wanted branch")
	return uuid.Nil
}

func TestExecuteSplit_TakesHashAssignedBranch(t *testing.T) {
	for _, wantA := range []bool{true, false} {
		taskExec := &fakeExecutor{output: map[string]any{"ok": true}}
		engine := &Engine{
			ctx:       context.Background(),
			logger:    defaultTestLogger(),
			executors: map[string]ActionExecutor{"create_task": taskExec},
		}
		run := &WorkflowRun{ID: findRunFor(t, wantA)}
		evalCtx := EvalContext{Actions: map[string]any{}}

		completed, err := engine.executeStepsRecursive(context.Background(), splitTree(), run, map[string]bool{}, &evalCtx, "", "")
		require.NoError(t, err)
		assert.True(t, completed)

		want := "branch_a"
		if !wantA {
			want = "branch_b"
		}
		assert.Equal(t, []string{want}, taskExec.calls, "wantA=%v", wantA)
	}
}

func TestExecuteSplit_RewalkIsStable(t *testing.T) {
	// Two walks of the same run must take the same branch (the resume path
	// re-derives branch decisions from scratch — determinism is the contract).
	run := &WorkflowRun{ID: seededRunID(42)}
	var firstCalls []string
	for walk := 0; walk < 2; walk++ {
		taskExec := &fakeExecutor{output: map[string]any{"ok": true}}
		engine := &Engine{
			ctx:       context.Background(),
			logger:    defaultTestLogger(),
			executors: map[string]ActionExecutor{"create_task": taskExec},
		}
		evalCtx := EvalContext{Actions: map[string]any{}}
		_, err := engine.executeStepsRecursive(context.Background(), splitTree(), run, map[string]bool{}, &evalCtx, "", "")
		require.NoError(t, err)
		if walk == 0 {
			firstCalls = taskExec.calls
		} else {
			assert.Equal(t, firstCalls, taskExec.calls, "re-walk flipped the split branch")
		}
	}
}

func TestExecuteSplit_PinnedBranchOverridesHash(t *testing.T) {
	// A run whose hash says A, but whose B branch already has progress
	// (resume state), must stay on B — mirror of the condition pinning rule.
	runID := findRunFor(t, true)
	taskExec := &fakeExecutor{output: map[string]any{"ok": true}}
	engine := &Engine{
		ctx:       context.Background(),
		logger:    defaultTestLogger(),
		executors: map[string]ActionExecutor{"create_task": taskExec},
	}
	run := &WorkflowRun{ID: runID}
	evalCtx := EvalContext{Actions: map[string]any{}}
	completedSteps := map[string]bool{"branch_b": true} // B already committed

	completed, err := engine.executeStepsRecursive(context.Background(), splitTree(), run, completedSteps, &evalCtx, "", "")
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Empty(t, taskExec.calls, "pinned B branch is already complete; A must not run")
}

// ═══════════════════════════════════════════════════════════════════
// Resume plumbing — path indexing + started detection through splits
// ═══════════════════════════════════════════════════════════════════

func TestStepPathIndex_WalksSplitBranches(t *testing.T) {
	idx := stepPathIndex(splitTree(), "", "")
	assert.Equal(t, "split_1", idx["0"])
	assert.Equal(t, "branch_a", idx["0|yes|0"])
	assert.Equal(t, "branch_b", idx["0|no|0"])
}

func TestHasAnyStepStarted_SeesSplitBranches(t *testing.T) {
	started := map[string]bool{"branch_b": true}
	assert.True(t, hasAnyStepStarted(splitTree(), started, "", ""))
	assert.False(t, hasAnyStepStarted(splitTree(), map[string]bool{}, "", ""))
}

// ═══════════════════════════════════════════════════════════════════
// Dry run
// ═══════════════════════════════════════════════════════════════════

func TestDryRun_SplitPreviewsABranch(t *testing.T) {
	resp := evaluateDryRun(nil, splitTree(), EvalContext{})
	require.Len(t, resp.Steps, 3)

	split := resp.Steps[0]
	assert.Equal(t, "split", split.Type)
	assert.Equal(t, "run", split.Status)
	assert.Equal(t, "yes", split.Branch)
	assert.Contains(t, split.Reason, "50%")

	a := resp.Steps[1]
	assert.Equal(t, "branch_a", a.StepID)
	assert.Equal(t, "run", a.Status)

	b := resp.Steps[2]
	assert.Equal(t, "branch_b", b.StepID)
	assert.Equal(t, "skip", b.Status)
	assert.Equal(t, "branch not taken", b.Reason)
}

func TestDryRun_SplitInsideUntakenBranchSkips(t *testing.T) {
	steps := []StepSpec{
		{
			Type: "condition", ID: "gate",
			Condition: &ConditionGroup{Op: "AND", Rules: []ConditionRule{{Field: "contact.email", Operator: "is_not_empty"}}},
			YesSteps:  nil,
			NoSteps:   splitTree(),
		},
	}
	// contact.email empty → condition false → NO branch taken → split runs there.
	resp := evaluateDryRun(nil, steps, EvalContext{Contact: map[string]any{"email": ""}})
	require.Len(t, resp.Steps, 4)
	assert.Equal(t, "run", resp.Steps[1].Status, "split on the taken NO branch runs")
}
