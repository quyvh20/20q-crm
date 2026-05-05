package automation

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Workflow represents an automation workflow definition.
type Workflow struct {
	ID          uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name        string         `gorm:"size:200;not null" json:"name"`
	Description string         `gorm:"size:1000" json:"description"`
	IsActive    bool           `gorm:"not null;default:false;index" json:"is_active"`
	Trigger     datatypes.JSON `gorm:"type:jsonb;not null" json:"trigger"`
	Conditions  datatypes.JSON `gorm:"type:jsonb" json:"conditions"`
	Actions     datatypes.JSON `gorm:"type:jsonb;not null" json:"actions"`
	Version     int            `gorm:"not null;default:1" json:"version"`
	CreatedBy   uuid.UUID      `gorm:"type:uuid;not null" json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Workflow) TableName() string { return "automation_workflows" }

// WorkflowVersion stores a snapshot of a workflow at a specific version for in-flight run pinning.
type WorkflowVersion struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	WorkflowID uuid.UUID      `gorm:"type:uuid;not null;index" json:"workflow_id"`
	Version    int            `gorm:"not null" json:"version"`
	Trigger    datatypes.JSON `gorm:"type:jsonb;not null" json:"trigger"`
	Conditions datatypes.JSON `gorm:"type:jsonb" json:"conditions"`
	Actions    datatypes.JSON `gorm:"type:jsonb;not null" json:"actions"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (WorkflowVersion) TableName() string { return "automation_workflow_versions" }

// WorkflowRun tracks a single execution of a workflow.
type WorkflowRun struct {
	ID               uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	WorkflowID       uuid.UUID      `gorm:"type:uuid;not null;index" json:"workflow_id"`
	WorkflowVersion  int            `gorm:"not null" json:"workflow_version"`
	OrgID            uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Status           string         `gorm:"size:20;not null;index" json:"status"` // pending|running|completed|failed|skipped
	TriggerContext   datatypes.JSON `gorm:"type:jsonb;not null" json:"trigger_context"`
	CurrentActionIdx int            `gorm:"not null;default:0" json:"current_action_idx"`
	CompletedActions datatypes.JSON `gorm:"type:jsonb" json:"completed_actions"`
	LastError        string         `gorm:"type:text" json:"last_error,omitempty"`
	RetryCount       int            `gorm:"not null;default:0" json:"retry_count"`
	NextRetryAt      *time.Time     `gorm:"index" json:"next_retry_at,omitempty"`
	StartedAt        *time.Time     `json:"started_at,omitempty"`
	FinishedAt       *time.Time     `json:"finished_at,omitempty"`
	IdempotencyKey   string         `gorm:"size:100;not null" json:"idempotency_key"`
	RecoveryCount    int            `gorm:"not null;default:0" json:"recovery_count"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

func (WorkflowRun) TableName() string { return "automation_workflow_runs" }

// WorkflowActionLog records the result of each action step within a run.
type WorkflowActionLog struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	RunID      uuid.UUID      `gorm:"type:uuid;not null;index" json:"run_id"`
	ActionIdx  int            `gorm:"not null" json:"action_idx"`
	ActionType string         `gorm:"size:50;not null" json:"action_type"`
	Status     string         `gorm:"size:20;not null" json:"status"` // success|failed|retrying
	Input      datatypes.JSON `gorm:"type:jsonb" json:"input,omitempty"`
	Output     datatypes.JSON `gorm:"type:jsonb" json:"output,omitempty"`
	Error      string         `gorm:"type:text" json:"error,omitempty"`
	AttemptNo  int            `gorm:"not null;default:1" json:"attempt_no"`
	DurationMs int64          `json:"duration_ms"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (WorkflowActionLog) TableName() string { return "automation_workflow_action_logs" }

// WorkflowOrgToken stores per-org webhook tokens for inbound webhook authentication.
type WorkflowOrgToken struct {
	OrgID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	Token     string    `gorm:"size:64;uniqueIndex;not null" json:"token"`
	Secret    string    `gorm:"size:128;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

func (WorkflowOrgToken) TableName() string { return "automation_workflow_org_tokens" }

// --- JSON payload spec types ---

// TriggerSpec represents the trigger configuration for a workflow.
type TriggerSpec struct {
	Type   string            `json:"type"`
	Params map[string]any    `json:"params,omitempty"`
}

// ConditionGroup is a recursive condition tree supporting AND/OR with max depth 3.
type ConditionGroup struct {
	Op    string          `json:"op,omitempty"`              // "AND" | "OR"
	Rules []ConditionRule `json:"rules,omitempty"`
	// Leaf rule fields (mutually exclusive with op/rules)
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    any    `json:"value,omitempty"`
}

// ConditionRule represents either a leaf condition or a nested group.
type ConditionRule struct {
	// Leaf fields
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    any    `json:"value,omitempty"`
	// Nested group fields
	Op    string          `json:"op,omitempty"`
	Rules []ConditionRule `json:"rules,omitempty"`
}

// IsGroup returns true if this rule is a nested group (has Op set).
func (r ConditionRule) IsGroup() bool {
	return r.Op != ""
}

// ActionSpec represents a single action step in a workflow.
type ActionSpec struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Params map[string]any `json:"params,omitempty"`
}

// EvalContext holds all the data available for template interpolation and condition evaluation.
type EvalContext struct {
	Contact map[string]any `json:"contact,omitempty"`
	Deal    map[string]any `json:"deal,omitempty"`
	Trigger map[string]any `json:"trigger,omitempty"`
	Org     map[string]any `json:"org,omitempty"`
	User    map[string]any `json:"user,omitempty"`
	Actions map[string]any `json:"actions,omitempty"` // action.id -> output
}

// Valid trigger types
const (
	TriggerContactCreated   = "contact_created"
	TriggerContactUpdated   = "contact_updated"
	TriggerDealStageChanged = "deal_stage_changed"
	TriggerNoActivityDays   = "no_activity_days"
	TriggerWebhookInbound   = "webhook_inbound"
)

// Valid action types
const (
	ActionSendEmail      = "send_email"
	ActionCreateTask     = "create_task"
	ActionAssignUser     = "assign_user"
	ActionSendWebhook    = "send_webhook"
	ActionDelay          = "delay"
	ActionUpdateContact  = "update_contact"
)

// Run statuses
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
)

// Action log statuses
const (
	LogStatusSuccess  = "success"
	LogStatusFailed   = "failed"
	LogStatusRetrying = "retrying"
)

// RetryableError wraps an error to signal it can be retried.
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// NewRetryableError wraps an error as retryable.
func NewRetryableError(err error) *RetryableError {
	return &RetryableError{Err: err}
}

// isRetryable checks if an error is a RetryableError.
func isRetryable(err error) bool {
	var re *RetryableError
	if err == nil {
		return false
	}
	// Check if the error or any wrapped error is RetryableError
	for e := err; e != nil; {
		if _, ok := e.(*RetryableError); ok {
			return true
		}
		if unwrapper, ok := e.(interface{ Unwrap() error }); ok {
			e = unwrapper.Unwrap()
		} else {
			break
		}
	}
	_ = re
	return false
}

// backoff returns the retry delay for the given attempt (1-indexed).
// 30s, 2m, 10m
func backoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 30 * time.Second
	case 2:
		return 2 * time.Minute
	case 3:
		return 10 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// Valid condition operators
var ValidOperators = map[string]bool{
	"eq": true, "neq": true,
	"gt": true, "gte": true,
	"lt": true, "lte": true,
	"contains": true, "not_contains": true,
	"in": true, "not_in": true,
	"is_empty": true, "is_not_empty": true,
	"starts_with": true, "ends_with": true,
}

// Valid trigger types set
var ValidTriggerTypes = map[string]bool{
	TriggerContactCreated:   true,
	TriggerContactUpdated:   true,
	TriggerDealStageChanged: true,
	TriggerNoActivityDays:   true,
	TriggerWebhookInbound:   true,
}

// IsValidTriggerType checks if a trigger type is valid.
// Accepts built-in types from ValidTriggerTypes AND dynamic custom object
// patterns like "{slug}_created", "{slug}_updated", "{slug}_deleted", or "{slug}_any".
func IsValidTriggerType(triggerType string) bool {
	if ValidTriggerTypes[triggerType] {
		return true
	}
	// Dynamic: accept {slug}_{event} for custom objects and built-in entities
	suffixes := []string{"_created", "_updated", "_deleted", "_any"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(triggerType, suffix) {
			slug := strings.TrimSuffix(triggerType, suffix)
			// slug must be non-empty
			if slug != "" {
				return true
			}
		}
	}
	return false
}

// Valid action types set
var ValidActionTypes = map[string]bool{
	ActionSendEmail:     true,
	ActionCreateTask:    true,
	ActionAssignUser:    true,
	ActionSendWebhook:   true,
	ActionDelay:         true,
	ActionUpdateContact: true,
}
