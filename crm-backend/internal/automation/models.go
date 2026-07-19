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
	// Actions is the DEPRECATED flat action list. Kept for rollback compatibility.
	// All new workflows use Steps (recursive tree). The frontend always writes both.
	// Target removal: 2026-09-01 (3 months after Steps GA).
	// Before removal: run migration to verify all workflows have Steps populated.
	Actions     datatypes.JSON `gorm:"type:jsonb;not null" json:"actions"`
	Steps       datatypes.JSON `gorm:"type:jsonb" json:"steps,omitempty"`
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
	// Actions is DEPRECATED — see Workflow.Actions for details.
	Actions    datatypes.JSON `gorm:"type:jsonb;not null" json:"actions"`
	Steps      datatypes.JSON `gorm:"type:jsonb" json:"steps,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

func (WorkflowVersion) TableName() string { return "automation_workflow_versions" }

// WorkflowRun tracks a single execution of a workflow.
type WorkflowRun struct {
	ID               uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	WorkflowID       uuid.UUID      `gorm:"type:uuid;not null;index" json:"workflow_id"`
	WorkflowVersion  int            `gorm:"not null" json:"workflow_version"`
	OrgID            uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Status           string         `gorm:"size:20;not null;index" json:"status"` // pending|running|waiting|completed|failed|skipped
	TriggerContext   datatypes.JSON `gorm:"type:jsonb;not null" json:"trigger_context"`
	CurrentActionIdx int            `gorm:"not null;default:0" json:"current_action_idx"`
	CompletedActions datatypes.JSON `gorm:"type:jsonb" json:"completed_actions"`
	LastError        string         `gorm:"type:text" json:"last_error,omitempty"`
	RetryCount       int            `gorm:"not null;default:0" json:"retry_count"`
	NextRetryAt      *time.Time     `gorm:"index" json:"next_retry_at,omitempty"`
	// WakeAt is the absolute deadline of an in-flight delay step. Only set while
	// Status == StatusWaiting; the retry sweeper flips due waiting runs back to
	// pending, so a restart never loses elapsed delay time.
	WakeAt           *time.Time     `gorm:"index" json:"wake_at,omitempty"`
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
	ActionPath string         `gorm:"size:255;index" json:"action_path"`
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

// AutomationTimer is a durable, absolute-time firing for time-based triggers (A4).
// A `schedule` timer holds the next cron occurrence; a `date_field` timer holds a
// materialized "N days before/after <record>.<date field>" moment. The scanner cron
// claims due pending timers (FOR UPDATE SKIP LOCKED) and fires the workflow.
//
// DedupeKey is unique per (workflow_id) and encodes the occurrence identity — for a
// schedule it embeds the fire time, for a date_field it embeds the source date value
// — so re-arming, event-driven materialization, and reconciliation all converge on
// one pending row per occurrence, and firing uses it as the run idempotency key
// (second dedup layer on top of the atomic 'fired' claim).
type AutomationTimer struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	WorkflowID uuid.UUID      `gorm:"type:uuid;not null;index" json:"workflow_id"`
	OrgID      uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Kind       string         `gorm:"size:20;not null" json:"kind"`   // schedule|date_field
	Status     string         `gorm:"size:20;not null;default:'pending'" json:"status"` // pending|fired|cancelled
	FireAt     time.Time      `gorm:"not null" json:"fire_at"`
	DedupeKey  string         `gorm:"size:200;not null" json:"dedupe_key"`
	Payload    datatypes.JSON `gorm:"type:jsonb" json:"payload,omitempty"`
	FiredAt    *time.Time     `json:"fired_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

func (AutomationTimer) TableName() string { return "automation_timers" }

// AssignCursor persists one assign_user step's place in its round-robin rotation.
//
// This row is the entire difference between real rotation and a load heuristic that
// merely resembles one: without somewhere durable to remember whose turn it was, a
// stateless picker can only infer fairness from record counts, and counts freeze the
// moment someone stops receiving work.
//
// The primary key is (org_id, workflow_id, action_id) — action_id because one
// workflow may hold several assign_user steps, each owed its own independent turn
// order. It is also the conflict target of the atomic UPSERT in nextAssignTicket.
type AssignCursor struct {
	OrgID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	WorkflowID uuid.UUID `gorm:"type:uuid;primaryKey" json:"workflow_id"`
	ActionID   string    `gorm:"size:255;primaryKey" json:"action_id"`
	Ticket     int64     `gorm:"not null;default:0" json:"ticket"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (AssignCursor) TableName() string { return "automation_assign_cursors" }

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

// DelayParams holds the typed parameters for a delay step. Two modes:
//   - fixed duration: DurationSec > 0 (capped at 30 days).
//   - wait-until (A4.4): UntilField set → resolve to an absolute wake time from a
//     record date field on the run's eval context, plus OffsetDays/AtTime/Timezone
//     (the fixed-duration 30-day cap does not apply). UntilField presence is the
//     discriminator; when set, DurationSec is ignored.
type DelayParams struct {
	DurationSec int    `json:"duration_sec"`
	UntilField  string `json:"until_field,omitempty"` // dotted path, e.g. "deal.expected_close_at"
	OffsetDays  int    `json:"offset_days,omitempty"` // negative = before, positive = after
	AtTime      string `json:"at_time,omitempty"`     // "HH:MM"; empty → 09:00
	Timezone    string `json:"timezone,omitempty"`    // IANA zone; empty → UTC
}

// IsWaitUntil reports whether the delay resolves its deadline from a date field
// rather than a fixed duration.
func (d *DelayParams) IsWaitUntil() bool {
	return d != nil && d.UntilField != ""
}

// StepSpec represents a step in a recursive workflow steps tree.
type StepSpec struct {
	Type      string          `json:"type"` // "action" | "condition" | "delay"
	ID        string          `json:"id"`
	Action    *ActionSpec     `json:"action,omitempty"`
	Condition *ConditionGroup `json:"condition,omitempty"`
	Delay     *DelayParams    `json:"delay,omitempty"`
	YesSteps  []StepSpec      `json:"yes_steps,omitempty"`
	NoSteps   []StepSpec      `json:"no_steps,omitempty"`
}


// FlattenStepsToActions derives the deprecated flat actions list from a steps
// tree: actions and delays in DFS order (branches inlined), condition nodes
// dropped. Exact Go port of the frontend's former flattenSteps (store.ts) so
// pre-A1 rows and server-derived rows are indistinguishable. Remove with the
// Actions column (overhaul A8, scheduled 2026-09-01).
func FlattenStepsToActions(steps []StepSpec) []ActionSpec {
	var result []ActionSpec
	for _, step := range steps {
		switch step.Type {
		case "action":
			if step.Action != nil {
				a := *step.Action
				if a.ID == "" {
					a.ID = step.ID
				}
				result = append(result, a)
			}
		case "delay":
			params := map[string]any{"duration_sec": 0}
			if step.Delay != nil {
				params["duration_sec"] = step.Delay.DurationSec
				if step.Delay.IsWaitUntil() {
					params["until_field"] = step.Delay.UntilField
					params["offset_days"] = step.Delay.OffsetDays
					params["at_time"] = step.Delay.AtTime
					params["timezone"] = step.Delay.Timezone
				}
			}
			result = append(result, ActionSpec{
				Type:   ActionDelay,
				ID:     step.ID,
				Params: params,
			})
		}
		result = append(result, FlattenStepsToActions(step.YesSteps)...)
		result = append(result, FlattenStepsToActions(step.NoSteps)...)
	}
	return result
}

// delayParamsFromMap converts a legacy action params map to a typed *DelayParams.
func delayParamsFromMap(m map[string]any) *DelayParams {
	if m == nil {
		return nil
	}
	d := &DelayParams{}
	switch v := m["duration_sec"].(type) {
	case float64:
		d.DurationSec = int(v)
	case int:
		d.DurationSec = v
	}
	d.UntilField, _ = m["until_field"].(string)
	switch v := m["offset_days"].(type) {
	case float64:
		d.OffsetDays = int(v)
	case int:
		d.OffsetDays = v
	}
	d.AtTime, _ = m["at_time"].(string)
	d.Timezone, _ = m["timezone"].(string)
	return d
}

// EvalContext holds all the data available for template interpolation and condition evaluation.
type EvalContext struct {
	Contact map[string]any `json:"contact,omitempty"`
	Deal    map[string]any `json:"deal,omitempty"`
	Trigger map[string]any `json:"trigger,omitempty"`
	Org     map[string]any `json:"org,omitempty"`
	User    map[string]any `json:"user,omitempty"`
	Actions map[string]any `json:"actions,omitempty"` // action.id -> output
	Extra   map[string]any `json:"extra,omitempty"`   // custom object slug -> fields map
}

// Valid trigger types
const (
	TriggerContactCreated   = "contact_created"
	TriggerContactUpdated   = "contact_updated"
	TriggerDealStageChanged = "deal_stage_changed"
	TriggerNoActivityDays   = "no_activity_days"
	TriggerWebhookInbound   = "webhook_inbound"
	// TriggerSchedule fires a workflow on a cron schedule (A4) via automation_timers.
	TriggerSchedule = "schedule"
	// TriggerDateField fires N days before/after a record's date field (A4), via
	// automation_timers materialized event-driven at the record write chokepoint.
	TriggerDateField = "date_field"
)

// Valid action types
const (
	ActionSendEmail      = "send_email"
	ActionCreateTask     = "create_task"
	ActionAssignUser     = "assign_user"
	ActionSendWebhook    = "send_webhook"
	ActionDelay          = "delay"
	ActionUpdateRecord   = "update_record"
	ActionLogActivity    = "log_activity"
	ActionNotifyUser     = "notify_user"    // A6: in-app notification to a member's inbox
	ActionCreateRecord   = "create_record"  // A6: create any object's record via RecordService
	ActionFindRecords    = "find_records"   // A6: query records into the action output
	ActionEnrollRecords  = "enroll_records" // A6: enroll matching records into a target workflow
	ActionAIGenerate     = "ai_generate"    // A7: bounded AI text generation into the action output
	ActionUpdateContact  = "update_contact" // DEPRECATED alias: kept for backward compat with saved workflows
)

// Run statuses
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusWaiting   = "waiting" // parked on a delay step until wake_at
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
)

// Action log statuses
const (
	LogStatusSuccess  = "success"
	LogStatusFailed   = "failed"
	LogStatusRetrying = "retrying"
	LogStatusWaiting  = "waiting" // delay step parked; Output carries {"wake_at": ...}
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
	TriggerSchedule:         true,
	TriggerDateField:        true,
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
	ActionUpdateRecord:  true,
	ActionLogActivity:   true,
	ActionNotifyUser:    true,
	ActionCreateRecord:  true,
	ActionFindRecords:   true,
	ActionEnrollRecords: true,
	ActionAIGenerate:    true,
	ActionUpdateContact: true, // backward compat
}

// validActivityTypes is the set of user-selectable activity types for the
// log_activity action, shared by the validator and the executor. The activities
// enum also includes "stage_change", which is system-managed and intentionally
// excluded here.
var validActivityTypes = map[string]bool{
	"call": true, "meeting": true, "note": true, "email": true,
}
