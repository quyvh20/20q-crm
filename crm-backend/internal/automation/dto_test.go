package automation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

func TestToWorkflowResponse_ActionCount(t *testing.T) {
	actions := []ActionSpec{
		{Type: "send_email", ID: "a1", Params: map[string]any{"to": "x"}},
		{Type: "delay", ID: "a2", Params: map[string]any{"duration_sec": 60}},
	}
	actionsJSON, _ := json.Marshal(actions)

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Actions:   datatypes.JSON(actionsJSON),
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

func TestToWorkflowResponse_NilActions(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Actions:   nil,
		Version:   1,
		CreatedBy: uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	resp := ToWorkflowResponse(wf)
	if resp.ActionCount != 0 {
		t.Fatalf("expected ActionCount=0 for nil actions, got %d", resp.ActionCount)
	}
}

func TestToWorkflowResponse_EmptyActionsArray(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Actions:   datatypes.JSON([]byte(`[]`)),
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

func TestToWorkflowResponse_InvalidActionsJSON(t *testing.T) {
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Name:      "Test",
		Actions:   datatypes.JSON([]byte(`{not an array}`)),
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
		Actions:   datatypes.JSON([]byte(`[{"type":"send_email","id":"a1","params":{"to":"x"}}]`)),
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
		Actions:   datatypes.JSON([]byte(`[]`)),
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
