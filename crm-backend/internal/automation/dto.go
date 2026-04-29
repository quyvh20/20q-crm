package automation

import (
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// --- Request DTOs ---

// CreateWorkflowRequest is the request body for creating a workflow.
type CreateWorkflowRequest struct {
	Name        string          `json:"name" binding:"required,min=1,max=200"`
	Description string          `json:"description" binding:"max=1000"`
	Trigger     datatypes.JSON  `json:"trigger" binding:"required"`
	Conditions  datatypes.JSON  `json:"conditions"`
	Actions     datatypes.JSON  `json:"actions" binding:"required"`
}

// UpdateWorkflowRequest is the request body for updating a workflow.
type UpdateWorkflowRequest struct {
	Name        *string         `json:"name" binding:"omitempty,min=1,max=200"`
	Description *string         `json:"description" binding:"omitempty,max=1000"`
	Trigger     datatypes.JSON  `json:"trigger"`
	Conditions  datatypes.JSON  `json:"conditions"`
	Actions     datatypes.JSON  `json:"actions"`
}

// TestRunRequest is the request body for a dry-run.
type TestRunRequest struct {
	Context map[string]any `json:"context" binding:"required"`
}

// --- Response DTOs ---

// WorkflowResponse is the response for a single workflow.
type WorkflowResponse struct {
	ID            uuid.UUID      `json:"id"`
	OrgID         uuid.UUID      `json:"org_id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	IsActive      bool           `json:"is_active"`
	Trigger       datatypes.JSON `json:"trigger"`
	Conditions    datatypes.JSON `json:"conditions"`
	Actions       datatypes.JSON `json:"actions"`
	ActionCount   int            `json:"action_count"`
	Version       int            `json:"version"`
	CreatedBy     uuid.UUID      `json:"created_by"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
	LastRunStatus *string        `json:"last_run_status"`
	LastRunAt     *string        `json:"last_run_at"`
}

// WorkflowListResponse is the response for listing workflows.
type WorkflowListResponse struct {
	Workflows []WorkflowResponse `json:"workflows"`
	Total     int64              `json:"total"`
	Page      int                `json:"page"`
	Size      int                `json:"size"`
}

// WorkflowRunResponse is the response for a single run.
type WorkflowRunResponse struct {
	ID               uuid.UUID      `json:"id"`
	WorkflowID       uuid.UUID      `json:"workflow_id"`
	WorkflowVersion  int            `json:"workflow_version"`
	OrgID            uuid.UUID      `json:"org_id"`
	Status           string         `json:"status"`
	TriggerContext   datatypes.JSON `json:"trigger_context"`
	CurrentActionIdx int            `json:"current_action_idx"`
	CompletedActions datatypes.JSON `json:"completed_actions"`
	LastError        string         `json:"last_error,omitempty"`
	RetryCount       int            `json:"retry_count"`
	StartedAt        *string        `json:"started_at,omitempty"`
	FinishedAt       *string        `json:"finished_at,omitempty"`
	CreatedAt        string         `json:"created_at"`
}

// RunDetailResponse includes run + action logs.
type RunDetailResponse struct {
	Run        WorkflowRunResponse     `json:"run"`
	ActionLogs []ActionLogResponse     `json:"action_logs"`
}

// ActionLogResponse is the response for a single action log entry.
type ActionLogResponse struct {
	ID         uuid.UUID      `json:"id"`
	RunID      uuid.UUID      `json:"run_id"`
	ActionIdx  int            `json:"action_idx"`
	ActionType string         `json:"action_type"`
	Status     string         `json:"status"`
	Input      datatypes.JSON `json:"input,omitempty"`
	Output     datatypes.JSON `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	AttemptNo  int            `json:"attempt_no"`
	DurationMs int64          `json:"duration_ms"`
	CreatedAt  string         `json:"created_at"`
}

// TestRunResponse is the response for a dry-run.
type TestRunResponse struct {
	ConditionResult bool             `json:"condition_result"`
	Actions         []TestRunAction  `json:"actions"`
}

// TestRunAction shows resolved params for each action (no side effects).
type TestRunAction struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	ResolvedParams map[string]any `json:"resolved_params"`
}

// ErrorResponse is the standard error response.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody contains error details.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details []ValidationError `json:"details,omitempty"`
}

// WebhookInboundResponse is returned from webhook ingestion.
type WebhookInboundResponse struct {
	Status    string `json:"status"`
	ContactID string `json:"contact_id"`
}

// --- Conversion helpers ---

// ToWorkflowResponse converts a Workflow model to a response DTO.
func ToWorkflowResponse(wf *Workflow) WorkflowResponse {
	// Count actions from JSON array
	var actionCount int
	if wf.Actions != nil {
		var actions []json.RawMessage
		if err := json.Unmarshal(wf.Actions, &actions); err == nil {
			actionCount = len(actions)
		}
	}

	return WorkflowResponse{
		ID:          wf.ID,
		OrgID:       wf.OrgID,
		Name:        wf.Name,
		Description: wf.Description,
		IsActive:    wf.IsActive,
		Trigger:     wf.Trigger,
		Conditions:  wf.Conditions,
		Actions:     wf.Actions,
		ActionCount: actionCount,
		Version:     wf.Version,
		CreatedBy:   wf.CreatedBy,
		CreatedAt:   wf.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   wf.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// ToWorkflowResponseWithRun converts a Workflow model to a response DTO with last run info.
func ToWorkflowResponseWithRun(wf *Workflow, lastRunStatus *string, lastRunAt *string) WorkflowResponse {
	resp := ToWorkflowResponse(wf)
	resp.LastRunStatus = lastRunStatus
	resp.LastRunAt = lastRunAt
	return resp
}

// ToRunResponse converts a WorkflowRun model to a response DTO.
func ToRunResponse(run *WorkflowRun) WorkflowRunResponse {
	resp := WorkflowRunResponse{
		ID:               run.ID,
		WorkflowID:       run.WorkflowID,
		WorkflowVersion:  run.WorkflowVersion,
		OrgID:            run.OrgID,
		Status:           run.Status,
		TriggerContext:   run.TriggerContext,
		CurrentActionIdx: run.CurrentActionIdx,
		CompletedActions: run.CompletedActions,
		LastError:        run.LastError,
		RetryCount:       run.RetryCount,
		CreatedAt:        run.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if run.StartedAt != nil {
		s := run.StartedAt.Format("2006-01-02T15:04:05Z")
		resp.StartedAt = &s
	}
	if run.FinishedAt != nil {
		s := run.FinishedAt.Format("2006-01-02T15:04:05Z")
		resp.FinishedAt = &s
	}
	return resp
}

// ToActionLogResponse converts a WorkflowActionLog model to a response DTO.
func ToActionLogResponse(log *WorkflowActionLog) ActionLogResponse {
	return ActionLogResponse{
		ID:         log.ID,
		RunID:      log.RunID,
		ActionIdx:  log.ActionIdx,
		ActionType: log.ActionType,
		Status:     log.Status,
		Input:      log.Input,
		Output:     log.Output,
		Error:      log.Error,
		AttemptNo:  log.AttemptNo,
		DurationMs: log.DurationMs,
		CreatedAt:  log.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// --- Schema DTOs (for workflow builder field pickers) ---

// SchemaField describes a single field available for conditions / template variables.
type SchemaField struct {
	Path       string   `json:"path"`                  // e.g. "contact.email"
	Label      string   `json:"label"`                 // e.g. "Email"
	Type       string   `json:"type"`                  // string, number, boolean, array, select, date
	PickerType string   `json:"picker_type,omitempty"` // tag, stage, user — tells UI which picker to render
	Options    []string `json:"options,omitempty"`      // for select-type custom fields
}

// SchemaEntity groups fields under an entity category (Contact, Deal, etc.).
type SchemaEntity struct {
	Key    string        `json:"key"`    // "contact", "deal", "trigger", or custom object slug
	Label  string        `json:"label"`  // "Contact", "Deal", ...
	Icon   string        `json:"icon"`   // emoji
	Fields []SchemaField `json:"fields"`
}

// SchemaStage represents a pipeline stage for stage pickers.
type SchemaStage struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Order int    `json:"order"`
}

// SchemaTag represents an org tag for tag pickers.
type SchemaTag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// SchemaUser represents an org member for user pickers.
type SchemaUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// SchemaResponse is the response for GET /api/workflows/schema.
type SchemaResponse struct {
	Entities      []SchemaEntity `json:"entities"`
	CustomObjects []SchemaEntity `json:"custom_objects"`
	Stages        []SchemaStage  `json:"stages"`
	Tags          []SchemaTag    `json:"tags"`
	Users         []SchemaUser   `json:"users"`
}
