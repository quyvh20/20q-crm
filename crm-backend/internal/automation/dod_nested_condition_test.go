package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════════
// Definition of Done: 2-Level Nested Condition Workflow
//
// Tree structure:
//   root[0]: action "notify_start" (send_email)
//   root[1]: condition "outer_cond" — contact.tags contains vip
//     yes[0]: action "vip_greet" (send_email)
//     yes[1]: condition "inner_cond" — deal.value > 10000
//       yes[0]: action "high_value_task" (create_task)
//       yes[1]: action "high_value_email" (send_email)
//       no[0]:  action "low_value_task" (create_task)
//     no[0]: action "non_vip_task" (create_task)
//   root[2]: action "final_cleanup" (send_email)
//
// Step paths:
//   0           → notify_start
//   1           → outer_cond (condition)
//   1|yes|0     → vip_greet
//   1|yes|1     → inner_cond (condition)
//   1|yes|1|yes|0 → high_value_task
//   1|yes|1|yes|1 → high_value_email
//   1|yes|1|no|0  → low_value_task
//   1|no|0     → non_vip_task
//   2           → final_cleanup
// ═══════════════════════════════════════════════════════════════════

func buildDoDStepTree() []StepSpec {
	return []StepSpec{
		{
			Type: "action",
			ID:   "notify_start",
			Action: &ActionSpec{
				Type:   "send_email",
				ID:     "notify_start",
				Params: map[string]any{"to": "admin@co.com", "subject": "Workflow started"},
			},
		},
		{
			Type: "condition",
			ID:   "outer_cond",
			Condition: &ConditionGroup{
				Op: "AND",
				Rules: []ConditionRule{
					{Field: "contact.tags", Operator: "contains", Value: "vip"},
				},
			},
			YesSteps: []StepSpec{
				{
					Type: "action",
					ID:   "vip_greet",
					Action: &ActionSpec{
						Type:   "send_email",
						ID:     "vip_greet",
						Params: map[string]any{"to": "{{contact.email}}", "subject": "Welcome VIP"},
					},
				},
				{
					Type: "condition",
					ID:   "inner_cond",
					Condition: &ConditionGroup{
						Op: "AND",
						Rules: []ConditionRule{
							{Field: "deal.value", Operator: "gt", Value: float64(10000)},
						},
					},
					YesSteps: []StepSpec{
						{
							Type: "action",
							ID:   "high_value_task",
							Action: &ActionSpec{
								Type:   "create_task",
								ID:     "high_value_task",
								Params: map[string]any{"title": "Follow up high-value VIP"},
							},
						},
						{
							Type: "action",
							ID:   "high_value_email",
							Action: &ActionSpec{
								Type:   "send_email",
								ID:     "high_value_email",
								Params: map[string]any{"to": "sales@co.com", "subject": "High-value deal alert"},
							},
						},
					},
					NoSteps: []StepSpec{
						{
							Type: "action",
							ID:   "low_value_task",
							Action: &ActionSpec{
								Type:   "create_task",
								ID:     "low_value_task",
								Params: map[string]any{"title": "Standard VIP follow-up"},
							},
						},
					},
				},
			},
			NoSteps: []StepSpec{
				{
					Type: "action",
					ID:   "non_vip_task",
					Action: &ActionSpec{
						Type:   "create_task",
						ID:     "non_vip_task",
						Params: map[string]any{"title": "Non-VIP onboarding"},
					},
				},
			},
		},
		{
			Type: "action",
			ID:   "final_cleanup",
			Action: &ActionSpec{
				Type:   "send_email",
				ID:     "final_cleanup",
				Params: map[string]any{"to": "admin@co.com", "subject": "Workflow complete"},
			},
		},
	}
}

// ═══════════════════════════════════════════════════════════════════
// 1. Save / Reload Round-Trip — tree integrity
// ═══════════════════════════════════════════════════════════════════

func TestDoD_SaveReload_TreeIntact(t *testing.T) {
	steps := buildDoDStepTree()

	// Serialize to JSON (simulates JSONB save to DB)
	data, err := json.Marshal(steps)
	require.NoError(t, err)

	// Deserialize (simulates loading from DB)
	var loaded []StepSpec
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	// Root level: 3 steps
	require.Len(t, loaded, 3)
	assert.Equal(t, "notify_start", loaded[0].ID)
	assert.Equal(t, "action", loaded[0].Type)
	assert.Equal(t, "outer_cond", loaded[1].ID)
	assert.Equal(t, "condition", loaded[1].Type)
	assert.Equal(t, "final_cleanup", loaded[2].ID)

	// Outer condition
	outer := loaded[1]
	assert.Equal(t, "AND", outer.Condition.Op)
	assert.Len(t, outer.Condition.Rules, 1)
	assert.Equal(t, "contact.tags", outer.Condition.Rules[0].Field)
	assert.Equal(t, "contains", outer.Condition.Rules[0].Operator)
	assert.Equal(t, "vip", outer.Condition.Rules[0].Value)

	// Outer yes branch: 2 steps (vip_greet + inner_cond)
	require.Len(t, outer.YesSteps, 2)
	assert.Equal(t, "vip_greet", outer.YesSteps[0].ID)
	assert.Equal(t, "inner_cond", outer.YesSteps[1].ID)
	assert.Equal(t, "condition", outer.YesSteps[1].Type)

	// Outer no branch: 1 step
	require.Len(t, outer.NoSteps, 1)
	assert.Equal(t, "non_vip_task", outer.NoSteps[0].ID)

	// Inner condition
	inner := outer.YesSteps[1]
	assert.Equal(t, "AND", inner.Condition.Op)
	assert.Equal(t, "deal.value", inner.Condition.Rules[0].Field)
	assert.Equal(t, "gt", inner.Condition.Rules[0].Operator)

	// Inner yes branch: 2 steps
	require.Len(t, inner.YesSteps, 2)
	assert.Equal(t, "high_value_task", inner.YesSteps[0].ID)
	assert.Equal(t, "high_value_email", inner.YesSteps[1].ID)

	// Inner no branch: 1 step
	require.Len(t, inner.NoSteps, 1)
	assert.Equal(t, "low_value_task", inner.NoSteps[0].ID)
}

// ═══════════════════════════════════════════════════════════════════
// 2. Execution — VIP contact + $50k deal → nested yes path
// ═══════════════════════════════════════════════════════════════════

func TestDoD_Execute_VipHighValue_NestedYesPath(t *testing.T) {
	steps := buildDoDStepTree()

	// Track which action IDs were executed
	emailExec := &fakeExecutor{output: map[string]any{"sent": true}}
	taskExec := &fakeExecutor{output: map[string]any{"task_id": "t1"}}

	engine := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  emailExec,
			"create_task": taskExec,
		},
	}

	run := &WorkflowRun{ID: uuid.New()}
	completedSteps := make(map[string]bool)
	evalCtx := EvalContext{
		Contact: map[string]any{
			"email": "vip@corp.com",
			"tags":  []any{"vip", "enterprise"},
		},
		Deal: map[string]any{
			"value": float64(50000),
		},
		Actions: make(map[string]any),
	}

	// Execute the tree (no repo needed — we skip action log persistence)
	engine.repo = nil
	completed, err := engine.executeStepsRecursive(context.Background(), steps, run, completedSteps, &evalCtx, "", "")
	require.NoError(t, err)
	assert.True(t, completed)

	// Expected execution order:
	// 0: notify_start (email)
	// 1: outer_cond → tags contains "vip" → YES
	// 1|yes|0: vip_greet (email)
	// 1|yes|1: inner_cond → deal.value > 10000 → YES
	// 1|yes|1|yes|0: high_value_task (task)
	// 1|yes|1|yes|1: high_value_email (email)
	// 2: final_cleanup (email)

	assert.Equal(t, []string{"notify_start", "vip_greet", "high_value_email", "final_cleanup"}, emailExec.calls)
	assert.Equal(t, []string{"high_value_task"}, taskExec.calls)

	// Verify step paths are tracked
	assert.True(t, completedSteps["0"])             // notify_start
	assert.True(t, completedSteps["1|yes|0"])       // vip_greet
	assert.True(t, completedSteps["1|yes|1|yes|0"]) // high_value_task
	assert.True(t, completedSteps["1|yes|1|yes|1"]) // high_value_email
	assert.True(t, completedSteps["2"])             // final_cleanup

	// Verify NO path steps were NOT executed
	assert.False(t, completedSteps["1|no|0"])       // non_vip_task
	assert.False(t, completedSteps["1|yes|1|no|0"]) // low_value_task

	// Verify evalCtx.Actions has outputs for each step
	assert.NotNil(t, evalCtx.Actions["notify_start"])
	assert.NotNil(t, evalCtx.Actions["vip_greet"])
	assert.NotNil(t, evalCtx.Actions["high_value_task"])
	assert.NotNil(t, evalCtx.Actions["high_value_email"])
	assert.NotNil(t, evalCtx.Actions["final_cleanup"])
}

// ═══════════════════════════════════════════════════════════════════
// 3. Execution — Non-VIP contact → outer no path
// ═══════════════════════════════════════════════════════════════════

func TestDoD_Execute_NonVip_OuterNoPath(t *testing.T) {
	steps := buildDoDStepTree()

	emailExec := &fakeExecutor{output: map[string]any{"sent": true}}
	taskExec := &fakeExecutor{output: map[string]any{"task_id": "t1"}}

	engine := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  emailExec,
			"create_task": taskExec,
		},
	}

	run := &WorkflowRun{ID: uuid.New()}
	completedSteps := make(map[string]bool)
	evalCtx := EvalContext{
		Contact: map[string]any{
			"email": "regular@corp.com",
			"tags":  []any{"prospect"},
		},
		Deal: map[string]any{
			"value": float64(5000),
		},
		Actions: make(map[string]any),
	}

	engine.repo = nil
	completed, err := engine.executeStepsRecursive(context.Background(), steps, run, completedSteps, &evalCtx, "", "")
	require.NoError(t, err)
	assert.True(t, completed)

	// Expected execution order:
	// 0: notify_start (email)
	// 1: outer_cond → tags contains "vip" → NO
	// 1|no|0: non_vip_task (task)
	// 2: final_cleanup (email)

	assert.Equal(t, []string{"notify_start", "final_cleanup"}, emailExec.calls)
	assert.Equal(t, []string{"non_vip_task"}, taskExec.calls)

	// Verify correct paths tracked
	assert.True(t, completedSteps["0"])      // notify_start
	assert.True(t, completedSteps["1|no|0"]) // non_vip_task
	assert.True(t, completedSteps["2"])      // final_cleanup

	// Verify yes path steps were NOT executed
	assert.False(t, completedSteps["1|yes|0"])
	assert.False(t, completedSteps["1|yes|1|yes|0"])
	assert.False(t, completedSteps["1|yes|1|yes|1"])
	assert.False(t, completedSteps["1|yes|1|no|0"])
}

// ═══════════════════════════════════════════════════════════════════
// 4. Crash Recovery — resume at 1|yes|1|yes|0 without re-executing
// ═══════════════════════════════════════════════════════════════════

func TestDoD_CrashRecovery_ResumeNestedYes(t *testing.T) {
	steps := buildDoDStepTree()

	// Simulate crash: steps 0, 1|yes|0 already completed.
	// The process crashed just before executing 1|yes|1|yes|0 (high_value_task).
	// On restart, the engine loads completed action logs and populates completedSteps.
	completedSteps := map[string]bool{
		// Structural paths (new format — from action logs)
		"0":       true, // notify_start
		"1|yes|0": true, // vip_greet
		// Also track by step ID (backward compat — existing logs may have step IDs)
		"notify_start": true,
		"vip_greet":    true,
	}

	emailExec := &fakeExecutor{output: map[string]any{"sent": true}}
	taskExec := &fakeExecutor{output: map[string]any{"task_id": "t1"}}

	engine := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  emailExec,
			"create_task": taskExec,
		},
	}

	run := &WorkflowRun{ID: uuid.New()}
	evalCtx := EvalContext{
		Contact: map[string]any{
			"email": "vip@corp.com",
			"tags":  []any{"vip", "enterprise"},
		},
		Deal: map[string]any{
			"value": float64(50000),
		},
		Actions: map[string]any{
			// Outputs from previously completed steps (loaded from action logs)
			"notify_start": map[string]any{"sent": true},
			"vip_greet":    map[string]any{"sent": true},
		},
	}

	engine.repo = nil
	completed, err := engine.executeStepsRecursive(context.Background(), steps, run, completedSteps, &evalCtx, "", "")
	require.NoError(t, err)
	assert.True(t, completed)

	// CRITICAL: notify_start and vip_greet must NOT have been re-executed
	// Only new steps should appear in executor calls
	assert.NotContains(t, emailExec.calls, "notify_start", "must not re-execute notify_start")
	assert.NotContains(t, emailExec.calls, "vip_greet", "must not re-execute vip_greet")

	// Steps that SHOULD have executed after resume:
	// 1|yes|1: inner_cond → deal.value > 10000 → YES (condition eval, no executor call)
	// 1|yes|1|yes|0: high_value_task (task)
	// 1|yes|1|yes|1: high_value_email (email)
	// 2: final_cleanup (email)
	assert.Equal(t, []string{"high_value_task"}, taskExec.calls)
	assert.Equal(t, []string{"high_value_email", "final_cleanup"}, emailExec.calls)

	// Verify the newly completed paths
	assert.True(t, completedSteps["1|yes|1|yes|0"])
	assert.True(t, completedSteps["1|yes|1|yes|1"])
	assert.True(t, completedSteps["2"])

	// Verify total completed steps
	// Original 2 (×2 for ID+path) + 3 new (×2) = 10 entries
	expectedPaths := []string{
		"0", "notify_start",
		"1|yes|0", "vip_greet",
		"1|yes|1|yes|0", "high_value_task",
		"1|yes|1|yes|1", "high_value_email",
		"2", "final_cleanup",
	}
	for _, p := range expectedPaths {
		assert.True(t, completedSteps[p], "expected completed: %s", p)
	}
}

// ═══════════════════════════════════════════════════════════════════
// 5. Crash Recovery — hasAnyStepExecuted drives condition branch
//    selection on resume (not re-evaluating condition)
// ═══════════════════════════════════════════════════════════════════

func TestDoD_CrashRecovery_ConditionBranchDeterminedByCompletedSteps(t *testing.T) {
	steps := buildDoDStepTree()

	// Simulate: outer condition's YES branch was taken previously (vip_greet completed).
	// On resume, even if we pass a different contact (non-VIP), the engine must
	// still follow the YES branch because it detects completed steps inside YES.
	completedSteps := map[string]bool{
		"0":            true,
		"notify_start": true,
		"1|yes|0":      true,
		"vip_greet":    true,
	}

	emailExec := &fakeExecutor{output: map[string]any{"sent": true}}
	taskExec := &fakeExecutor{output: map[string]any{"task_id": "t1"}}

	engine := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  emailExec,
			"create_task": taskExec,
		},
	}

	run := &WorkflowRun{ID: uuid.New()}
	evalCtx := EvalContext{
		Contact: map[string]any{
			"email": "regular@corp.com",
			"tags":  []any{"prospect"}, // NOT vip! But branch was already chosen.
		},
		Deal: map[string]any{
			"value": float64(50000),
		},
		Actions: map[string]any{
			"notify_start": map[string]any{"sent": true},
			"vip_greet":    map[string]any{"sent": true},
		},
	}

	engine.repo = nil
	completed, err := engine.executeStepsRecursive(context.Background(), steps, run, completedSteps, &evalCtx, "", "")
	require.NoError(t, err)
	assert.True(t, completed)

	// CRITICAL: Despite non-VIP contact, engine must follow YES branch (because
	// hasAnyStepExecuted detects vip_greet was already executed in YES branch).
	// The inner condition IS re-evaluated (no steps completed in inner branches).

	// Inner condition: deal.value > 10000 → YES → execute high_value_task + high_value_email
	assert.Equal(t, []string{"high_value_task"}, taskExec.calls)
	assert.Equal(t, []string{"high_value_email", "final_cleanup"}, emailExec.calls)

	// non_vip_task must NOT have been executed
	assert.NotContains(t, taskExec.calls, "non_vip_task")
}

// ═══════════════════════════════════════════════════════════════════
// 6. Step Path Encoding — verify tree structure maps to expected paths
// ═══════════════════════════════════════════════════════════════════

func TestDoD_StepPaths_MatchExpectedEncoding(t *testing.T) {
	// Verify that BuildStepPath produces the expected paths for the DoD tree
	cases := []struct {
		name       string
		parentPath string
		branch     string
		index      int
		expected   string
	}{
		{"root[0] notify_start", "", "", 0, "0"},
		{"root[1] outer_cond", "", "", 1, "1"},
		{"root[2] final_cleanup", "", "", 2, "2"},
		{"outer.yes[0] vip_greet", "1", "yes", 0, "1|yes|0"},
		{"outer.yes[1] inner_cond", "1", "yes", 1, "1|yes|1"},
		{"inner.yes[0] high_value_task", "1|yes|1", "yes", 0, "1|yes|1|yes|0"},
		{"inner.yes[1] high_value_email", "1|yes|1", "yes", 1, "1|yes|1|yes|1"},
		{"inner.no[0] low_value_task", "1|yes|1", "no", 0, "1|yes|1|no|0"},
		{"outer.no[0] non_vip_task", "1", "no", 0, "1|no|0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := BuildStepPath(tc.parentPath, tc.branch, tc.index)
			assert.Equal(t, tc.expected, path)

			// Verify round-trip
			segs, err := ParseStepPath(path)
			require.NoError(t, err)
			assert.Equal(t, path, FormatStepPath(segs))
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// 7. Validation — the DoD tree passes validation
// ═══════════════════════════════════════════════════════════════════

func TestDoD_Validation_TreePasses(t *testing.T) {
	steps := buildDoDStepTree()
	stepsJSON, err := json.Marshal(steps)
	require.NoError(t, err)

	trigger := `{"type":"contact_created"}`
	result := ValidateWorkflowPayload([]byte(trigger), nil, nil, stepsJSON)
	assert.True(t, result.Valid, "DoD tree should pass validation: %+v", result.Errors)
}

// ═══════════════════════════════════════════════════════════════════
// 8. VIP + low-value deal → inner NO path
// ═══════════════════════════════════════════════════════════════════

func TestDoD_Execute_VipLowValue_InnerNoPath(t *testing.T) {
	steps := buildDoDStepTree()

	emailExec := &fakeExecutor{output: map[string]any{"sent": true}}
	taskExec := &fakeExecutor{output: map[string]any{"task_id": "t1"}}

	engine := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  emailExec,
			"create_task": taskExec,
		},
	}

	run := &WorkflowRun{ID: uuid.New()}
	completedSteps := make(map[string]bool)
	evalCtx := EvalContext{
		Contact: map[string]any{
			"email": "vip@corp.com",
			"tags":  []any{"vip"},
		},
		Deal: map[string]any{
			"value": float64(5000), // Below 10000 threshold
		},
		Actions: make(map[string]any),
	}

	engine.repo = nil
	completed, err := engine.executeStepsRecursive(context.Background(), steps, run, completedSteps, &evalCtx, "", "")
	require.NoError(t, err)
	assert.True(t, completed)

	// Expected:
	// notify_start → outer YES → vip_greet → inner NO → low_value_task → final_cleanup
	assert.Equal(t, []string{"notify_start", "vip_greet", "final_cleanup"}, emailExec.calls)
	assert.Equal(t, []string{"low_value_task"}, taskExec.calls)

	// high_value_task must NOT have been executed
	assert.False(t, completedSteps["1|yes|1|yes|0"])
	assert.True(t, completedSteps["1|yes|1|no|0"])
}

// ═══════════════════════════════════════════════════════════════════
// fakeExecutor that crashes after N calls (for crash recovery simulation)
// ═══════════════════════════════════════════════════════════════════

type crashingExecutor struct {
	calls     []string
	crashAt   int
	output    any
	callCount int
}

func (c *crashingExecutor) Execute(_ context.Context, _ *WorkflowRun, action ActionSpec, _ EvalContext) (any, error) {
	c.calls = append(c.calls, action.ID)
	c.callCount++
	if c.callCount == c.crashAt {
		return nil, fmt.Errorf("simulated crash at step %s", action.ID)
	}
	return c.output, nil
}

// TestDoD_CrashAndResume_FullScenario simulates:
// 1. First run: execute 3 steps, crash on the 4th
// 2. Second run: resume with completed steps pre-populated, only run remaining steps
func TestDoD_CrashAndResume_FullScenario(t *testing.T) {
	steps := buildDoDStepTree()

	// First run: crash on the 4th action call (which would be high_value_task)
	// Execution order for VIP + $50k:
	//   call 1: notify_start
	//   call 2: vip_greet
	//   call 3: high_value_task ← crash here
	crashEmail := &crashingExecutor{output: map[string]any{"sent": true}, crashAt: 999}
	crashTask := &crashingExecutor{output: map[string]any{"task_id": "t1"}, crashAt: 1} // crash on 1st task call

	engine := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  crashEmail,
			"create_task": crashTask,
		},
	}

	run := &WorkflowRun{ID: uuid.New()}
	completedSteps := make(map[string]bool)
	evalCtx := EvalContext{
		Contact: map[string]any{"email": "vip@corp.com", "tags": []any{"vip"}},
		Deal:    map[string]any{"value": float64(50000)},
		Actions: make(map[string]any),
	}

	engine.repo = nil
	completed, err := engine.executeStepsRecursive(context.Background(), steps, run, completedSteps, &evalCtx, "", "")
	assert.Error(t, err, "should fail on crash")
	assert.False(t, completed)

	// After crash: notify_start and vip_greet completed, high_value_task failed
	assert.True(t, completedSteps["0"])
	assert.True(t, completedSteps["notify_start"])
	assert.True(t, completedSteps["1|yes|0"])
	assert.True(t, completedSteps["vip_greet"])
	// high_value_task NOT in completed (it crashed)
	assert.False(t, completedSteps["1|yes|1|yes|0"])

	// ── Second run: resume ──

	resumeEmail := &fakeExecutor{output: map[string]any{"sent": true}}
	resumeTask := &fakeExecutor{output: map[string]any{"task_id": "t2"}}

	engine2 := &Engine{
		ctx:    context.Background(),
		logger: defaultTestLogger(),
		executors: map[string]ActionExecutor{
			"send_email":  resumeEmail,
			"create_task": resumeTask,
		},
	}

	run2 := &WorkflowRun{ID: run.ID} // Same run ID
	engine2.repo = nil
	completed2, err2 := engine2.executeStepsRecursive(context.Background(), steps, run2, completedSteps, &evalCtx, "", "")
	require.NoError(t, err2)
	assert.True(t, completed2)

	// CRITICAL: Only the remaining steps should be executed
	assert.NotContains(t, resumeEmail.calls, "notify_start", "must skip already-completed")
	assert.NotContains(t, resumeEmail.calls, "vip_greet", "must skip already-completed")
	assert.Equal(t, []string{"high_value_task"}, resumeTask.calls)
	assert.Equal(t, []string{"high_value_email", "final_cleanup"}, resumeEmail.calls)

	// All paths now completed
	assert.True(t, completedSteps["1|yes|1|yes|0"])
	assert.True(t, completedSteps["1|yes|1|yes|1"])
	assert.True(t, completedSteps["2"])
}
