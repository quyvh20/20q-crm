package automation

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ActionCount is derived from the steps tree — the only source since R5 deploy 1
// removed the flat Actions fallback ToWorkflowResponse used to fall back to.
func TestToWorkflowResponse_ActionCount(t *testing.T) {
	stepsJSON, _ := json.Marshal([]StepSpec{
		{Type: "action", ID: "a1", Action: &ActionSpec{Type: "send_email", ID: "a1", Params: map[string]any{"to": "x"}}},
		{Type: "delay", ID: "a2", Delay: &DelayParams{DurationSec: 60}},
	})

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Steps:     datatypes.JSON(stepsJSON),
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponse(wf)
	if resp.ActionCount != 2 {
		t.Fatalf("expected ActionCount=2, got %d", resp.ActionCount)
	}
}

func TestToWorkflowResponse_NilSteps(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Steps:     nil,
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponse(wf)
	if resp.ActionCount != 0 {
		t.Fatalf("expected ActionCount=0 for nil steps, got %d", resp.ActionCount)
	}
}

func TestToWorkflowResponse_EmptyStepsArray(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Steps:     datatypes.JSON([]byte(`[]`)),
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponse(wf)
	if resp.ActionCount != 0 {
		t.Fatalf("expected ActionCount=0 for empty array, got %d", resp.ActionCount)
	}
}

func TestToWorkflowResponse_InvalidStepsJSON(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Steps:     datatypes.JSON([]byte(`{not an array}`)),
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponse(wf)
	if resp.ActionCount != 0 {
		t.Fatalf("expected ActionCount=0 for invalid JSON, got %d", resp.ActionCount)
	}
}

func TestToWorkflowResponseWithRun(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Steps:     datatypes.JSON([]byte(`[{"type":"action","id":"a1","action":{"type":"send_email","id":"a1","params":{"to":"x"}}}]`)),
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	status := "completed"
	runAt := "2026-04-23T10:00:00Z"
	resp := ToWorkflowResponseWithRun(wf, &status, &runAt)

	if resp.LastRunStatus == nil || *resp.LastRunStatus != "completed" {
		t.Fatal("expected LastRunStatus=completed")
	}
	if resp.LastRunAt == nil || *resp.LastRunAt != "2026-04-23T10:00:00Z" {
		t.Fatal("expected LastRunAt set")
	}
	if resp.ActionCount != 1 {
		t.Fatalf("expected ActionCount=1, got %d", resp.ActionCount)
	}
}

func TestToWorkflowResponseWithRun_NilStatus(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Steps:     datatypes.JSON([]byte(`[]`)),
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponseWithRun(wf, nil, nil)
	if resp.LastRunStatus != nil {
		t.Fatal("expected nil LastRunStatus")
	}
	if resp.LastRunAt != nil {
		t.Fatal("expected nil LastRunAt")
	}
}

func TestToWorkflowResponse_FieldMapping(t *testing.T) {
	id := uuid.New()
	orgID := uuid.New()
	createdBy := uuid.New()
	now := time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC)

	wf := &Workflow{
		ID:          id,
		OrgID:       orgID,
		Name:        "My Workflow",
		Description: "A description",
		IsActive:    true,
		Version:     3,
		CreatedBy:   createdBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	resp := ToWorkflowResponse(wf)
	if resp.ID != id {
		t.Fatal("ID mismatch")
	}
	if resp.OrgID != orgID {
		t.Fatal("OrgID mismatch")
	}
	if resp.Name != "My Workflow" {
		t.Fatal("Name mismatch")
	}
	if resp.Description != "A description" {
		t.Fatal("Description mismatch")
	}
	if !resp.IsActive {
		t.Fatal("IsActive should be true")
	}
	if resp.Version != 3 {
		t.Fatal("Version mismatch")
	}
	if resp.CreatedAt != "2026-04-23T10:00:00Z" {
		t.Fatalf("CreatedAt format mismatch: %s", resp.CreatedAt)
	}
}

// TestToWorkflowResponse_DerivedActionsForCachedBundles covers the ONE reason
// WorkflowResponse still has an `actions` field after deploy 1 removed every other
// trace of the flat list: a browser tab holding the PRE-DEPLOY bundle.
//
// That bundle's store does `actions: changed ? flattenSteps(steps) : (wf.actions || [])`,
// its zod schema requires actions.min(1), handleSave early-returns when validate() fails
// and nothing renders store.errors. Emit no actions and such a tab loads workflows fine,
// shows their steps fine, and then SILENTLY REFUSES TO SAVE, with no toast, no error and
// no crash to explain it. `crm-frontend/public/_headers` sets no Cache-Control, so a
// reload fixes it — but a CRM tab left open all day never reloads.
//
// So the assertions here are the old bundle's requirements, not ours:
//
//	non-empty for any workflow with steps  → its actions.min(1) save guard passes
//	`[]` and never null for a steps-less one → no `.find` / `.map` on null
//	derived from steps, never from the column → correct with the column already empty
//
// Delete this test WITH the field in deploy 2, once no cached bundle can still be live.
func TestToWorkflowResponse_DerivedActionsForCachedBundles(t *testing.T) {
	// An If/Else workflow: the only action lives INSIDE a branch. FlattenStepsToActions
	// recurses, so it still reaches the old bundle — a non-recursive derivation would
	// hand it actions=[] and wedge saving on exactly the workflows that have branches.
	stepsJSON, err := json.Marshal([]StepSpec{
		{
			Type: "condition",
			ID:   "c1",
			YesSteps: []StepSpec{
				{Type: "action", ID: "a1", Action: &ActionSpec{Type: "send_email", ID: "a1", Params: map[string]any{"to": "x@test.com"}}},
			},
			NoSteps: []StepSpec{
				{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: 60}},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal steps: %v", err)
	}

	wf := &Workflow{
		ID: uuid.New(), OrgID: uuid.New(), Name: "branchy",
		Steps: datatypes.JSON(stepsJSON), Version: 1, CreatedBy: uuid.New(),
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponse(wf)
	if len(resp.Actions) != 2 {
		t.Fatalf("expected the branch action AND the branch delay to be flattened, got %d: %+v",
			len(resp.Actions), resp.Actions)
	}
	if resp.Actions[0].ID != "a1" || resp.Actions[0].Type != "send_email" {
		t.Fatalf("expected the nested send_email first, got %+v", resp.Actions[0])
	}
	if resp.Actions[1].ID != "d1" || resp.Actions[1].Type != ActionDelay {
		t.Fatalf("expected the nested delay synthesised as an action, got %+v", resp.Actions[1])
	}
	if resp.ActionCount != len(resp.Actions) {
		t.Fatalf("action_count (%d) and the derived actions (%d) disagree about the same tree",
			resp.ActionCount, len(resp.Actions))
	}

	// The wire shape, not just the Go value: the field has to be named `actions`, and it
	// must never marshal as null (a null array is how this frontend white-screens).
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(raw), `"actions":[{`) {
		t.Fatalf("response must carry a non-empty `actions` array for cached bundles: %s", raw)
	}

	t.Run("a steps-less workflow emits [] rather than null", func(t *testing.T) {
		empty := ToWorkflowResponse(&Workflow{
			ID: uuid.New(), OrgID: uuid.New(), Name: "no steps",
			Version: 1, CreatedBy: uuid.New(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		if empty.Actions == nil {
			t.Fatal("Actions must be a non-nil empty slice; nil marshals to JSON null")
		}
		if len(empty.Actions) != 0 {
			t.Fatalf("expected no derived actions, got %+v", empty.Actions)
		}
		raw, err := json.Marshal(empty)
		if err != nil {
			t.Fatalf("marshal response: %v", err)
		}
		if !strings.Contains(string(raw), `"actions":[]`) {
			t.Fatalf("expected `\"actions\":[]` on the wire, got %s", raw)
		}
	})

	t.Run("the derivation reads steps, never a stored column", func(t *testing.T) {
		// There is no Actions field on the model to set — that is the point — so the
		// proof is that a workflow whose steps say one thing produces exactly that,
		// with the database's `actions` column (whatever it holds) never consulted.
		one, err := json.Marshal([]StepSpec{{
			Type: "action", ID: "solo",
			Action: &ActionSpec{Type: "create_task", ID: "solo", Params: map[string]any{"title": "t"}},
		}})
		if err != nil {
			t.Fatalf("marshal steps: %v", err)
		}
		got := ToWorkflowResponse(&Workflow{
			ID: uuid.New(), OrgID: uuid.New(), Name: "solo",
			Steps: datatypes.JSON(one), Version: 1, CreatedBy: uuid.New(),
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
		if len(got.Actions) != 1 || got.Actions[0].Type != "create_task" {
			t.Fatalf("derived actions must mirror the steps tree, got %+v", got.Actions)
		}
	})
}

func TestToRunResponse_Basic(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Minute)
	finished := now

	run := &WorkflowRun{
		ID:               uuid.New(),
		WorkflowID:       uuid.New(),
		WorkflowVersion:  2,
		OrgID:            uuid.New(),
		Status:           "completed",
		TriggerContext:   datatypes.JSON([]byte(`{"type":"contact_created"}`)),
		CurrentActionIdx: 3,
		LastError:        "",
		RetryCount:       0,
		StartedAt:        &started,
		FinishedAt:       &finished,
		CreatedAt:        now,
	}

	resp := ToRunResponse(run)
	if resp.Status != "completed" {
		t.Fatalf("expected status=completed, got %s", resp.Status)
	}
	if resp.StartedAt == nil {
		t.Fatal("expected StartedAt to be set")
	}
	if resp.FinishedAt == nil {
		t.Fatal("expected FinishedAt to be set")
	}
	if resp.WorkflowVersion != 2 {
		t.Fatalf("expected WorkflowVersion=2, got %d", resp.WorkflowVersion)
	}
}

func TestToRunResponse_NilTimes(t *testing.T) {
	run := &WorkflowRun{
		ID:        uuid.New(),
		Status:    "pending",
		CreatedAt: time.Now(),
	}

	resp := ToRunResponse(run)
	if resp.StartedAt != nil {
		t.Fatal("expected nil StartedAt")
	}
	if resp.FinishedAt != nil {
		t.Fatal("expected nil FinishedAt")
	}
}

func TestToActionLogResponse(t *testing.T) {
	now := time.Now()
	log := &WorkflowActionLog{
		ID:         uuid.New(),
		RunID:      uuid.New(),
		ActionIdx:  0,
		ActionType: "send_email",
		Status:     "success",
		Input:      datatypes.JSON([]byte(`{"to":"x@y.com"}`)),
		Output:     datatypes.JSON([]byte(`{"message_id":"abc"}`)),
		Error:      "",
		AttemptNo:  1,
		DurationMs: 142,
		CreatedAt:  now,
	}

	resp := ToActionLogResponse(log)
	if resp.ActionType != "send_email" {
		t.Fatal("ActionType mismatch")
	}
	if resp.DurationMs != 142 {
		t.Fatalf("expected DurationMs=142, got %d", resp.DurationMs)
	}
	if resp.AttemptNo != 1 {
		t.Fatalf("expected AttemptNo=1, got %d", resp.AttemptNo)
	}
}

func TestTableNames(t *testing.T) {
	if (Workflow{}).TableName() != "automation_workflows" {
		t.Fatal("Workflow table name wrong")
	}
	if (WorkflowVersion{}).TableName() != "automation_workflow_versions" {
		t.Fatal("WorkflowVersion table name wrong")
	}
	if (WorkflowRun{}).TableName() != "automation_workflow_runs" {
		t.Fatal("WorkflowRun table name wrong")
	}
	if (WorkflowActionLog{}).TableName() != "automation_workflow_action_logs" {
		t.Fatal("WorkflowActionLog table name wrong")
	}
	if (WorkflowOrgToken{}).TableName() != "automation_workflow_org_tokens" {
		t.Fatal("WorkflowOrgToken table name wrong")
	}
}
