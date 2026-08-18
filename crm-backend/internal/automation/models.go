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
	// THE `actions` COLUMN IS ABSENT FROM THIS STRUCT AND FROM THE TABLE (R5 complete).
	//
	// Steps is the ONLY representation of what a workflow does. Deploy 1 removed the Go
	// field (from the model, the API DTOs, the validator, the executor); deploy 2
	// dropped the underlying `actions` column itself on both this table and
	// automation_workflow_versions, once the FLAT_ACTIONS_TEARDOWN_GATE read CLEAR
	// against prod (verified directly before the drop shipped) — see
	// Repository.dropLegacyActionsColumn.
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
	// The `actions` column is absent from this struct AND the table too — see the note
	// on Workflow.
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

// RunIdempotencyClaim is a PERMANENT record that a (workflow_id, idempotency_key) pair has
// been used. It exists because the run row is not a durable ledger: PruneCompletedRuns
// hard-deletes terminal runs 90 days after they finish (run_pruner.go), and that row was
// the ONLY artifact backing the marketing sequence feeder's promise that a contact enrolls
// into a given sequence at most once, forever (marketing/sequence_feeder.go). Deleting it
// released the dedupe, so a segment re-enrolled after the window re-mailed the entire drip
// to every contact who had already completed it.
//
// Only keys whose dedupe must outlive their run write here — today just the M8 sequence
// feeder, through EnrollContact. Ordinary runs stay out, including the enroll_records
// action, whose key is scoped to its SOURCE RUN (enrollIdempotencyKey) and is therefore
// meaningless once that run is pruned; claiming those would grow this table by one row per
// run and recreate the unbounded growth the pruner exists to prevent.
//
// NO PRUNER MAY TOUCH THIS TABLE. Its cost is one narrow row per (sequence, contact) that
// lives forever, which is the irreducible price of a "forever" guarantee — and a fraction
// of the run row plus N action logs whose deletion it makes safe.
//
// The unique index is declared as a GORM tag rather than a fire-and-forget Exec like the
// other automation indexes: it IS the guarantee, so its creation error must surface through
// AutoMigrate's checked return instead of being swallowed.
type RunIdempotencyClaim struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	OrgID          uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	WorkflowID     uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_wf_idemp_claims_wf_key" json:"workflow_id"`
	IdempotencyKey string    `gorm:"size:100;not null;uniqueIndex:idx_wf_idemp_claims_wf_key" json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
}

func (RunIdempotencyClaim) TableName() string { return "automation_run_idempotency_claims" }

// EventWait is one parked wait-for-event step: "run R, at step path P, is
// waiting for contact C to trigger EventType (optionally only for CampaignID)".
//
// It exists because the resume lookup cannot be driven off the run row. A run's
// trigger_context->>'entity_id' is the DEAL id for deal-triggered runs and the
// record id for custom-object runs — the deal→contact hydration is in-memory
// only and never persisted — so the SUBJECT of the wait is stamped here at park
// time instead. It also gives the per-webhook-event lookup a real index, which a
// jsonb predicate on automation_workflow_runs would not have.
//
// The row is authoritative for "is this wait still open": ClaimEventWaits flips
// SatisfiedAt in one status-guarded UPDATE ... RETURNING, so concurrent webhook
// drains and app instances cannot both resume the same run.
//
// ExpiresAt mirrors the run's wake_at (the timeout deadline). The run is ALWAYS
// parked with that deadline, so a wait whose event never arrives is still woken
// by the ordinary clock sweep — nothing here can leak a permanently parked run.
type EventWait struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	OrgID       uuid.UUID  `gorm:"type:uuid;not null;index" json:"org_id"`
	RunID       uuid.UUID  `gorm:"type:uuid;not null;uniqueIndex:idx_wf_event_waits_run_step" json:"run_id"`
	WorkflowID  uuid.UUID  `gorm:"type:uuid;not null" json:"workflow_id"`
	StepPath    string     `gorm:"size:255;not null;uniqueIndex:idx_wf_event_waits_run_step" json:"step_path"`
	EventType   string     `gorm:"size:50;not null" json:"event_type"`
	ContactID   uuid.UUID  `gorm:"type:uuid;not null" json:"contact_id"`
	CampaignID  *uuid.UUID `gorm:"type:uuid" json:"campaign_id,omitempty"`
	ExpiresAt   time.Time  `gorm:"not null" json:"expires_at"`
	SatisfiedAt *time.Time `json:"satisfied_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (EventWait) TableName() string { return "automation_event_waits" }

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
	OrgID uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	Token string    `gorm:"size:64;uniqueIndex;not null" json:"token"`
	// Secret holds the HMAC signing secret ENVELOPE-SEALED at rest, or plaintext on
	// a deployment with no INTEGRATION_ENC_KEY and on rows written before sealing
	// shipped. Never read it directly — go through Handler.openWebhookSecret, which
	// handles both shapes.
	//
	// type:text, not size:128, and that is load-bearing rather than tidiness: a
	// sealed 64-character secret is 212 characters, so the old varchar(128) would
	// have errored on every write the moment sealing turned on.
	Secret    string    `gorm:"type:text;not null" json:"-"`
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
	// Wait-until-EVENT (A9): park until the run's contact interacts with a
	// campaign email, or until TimeoutSec elapses — whichever comes first. The
	// step always completes; its output carries `happened`, so a following
	// If/Else can branch on {{actions.<step id>.happened}}.
	WaitEvent  string `json:"wait_event,omitempty"`  // "" | email_opened | email_clicked
	TimeoutSec int    `json:"timeout_sec,omitempty"` // required with WaitEvent
	CampaignID string `json:"campaign_id,omitempty"` // optional: only this campaign counts
}

// Delay MODES are discriminated by presence, in this precedence order — every
// reader must use these helpers rather than testing fields directly, or a
// wait-for-event carrying a stale until_field silently degrades into a
// wait-until-date.
//
// IsWaitEvent reports whether the delay parks until an engagement event.
func (d *DelayParams) IsWaitEvent() bool {
	return d != nil && d.WaitEvent != ""
}

// IsWaitUntil reports whether the delay resolves its deadline from a date field
// rather than a fixed duration. An event wait is never a date wait.
func (d *DelayParams) IsWaitUntil() bool {
	return d != nil && !d.IsWaitEvent() && d.UntilField != ""
}

// SplitParams configures a percentage split step: PercentA percent of runs take
// the A branch (YesSteps), the rest take B (NoSteps). The branch choice is a
// deterministic hash of (run id, step id) — see splitTakesA for why it must be.
type SplitParams struct {
	PercentA int `json:"percent_a"` // 1..99
}

// StepSpec represents a step in a recursive workflow steps tree.
//
// Fork kinds (condition, split) reuse YesSteps/NoSteps as their two branches —
// for a split, YesSteps is the A branch and NoSteps is B. Reusing the arrays
// keeps every generic tree walk (flatten, id normalization, step paths with
// their yes|no tokens) working unchanged for both kinds.
type StepSpec struct {
	Type      string          `json:"type"` // "action" | "condition" | "delay" | "split"
	ID        string          `json:"id"`
	Action    *ActionSpec     `json:"action,omitempty"`
	Condition *ConditionGroup `json:"condition,omitempty"`
	Delay     *DelayParams    `json:"delay,omitempty"`
	Split     *SplitParams    `json:"split,omitempty"`
	YesSteps  []StepSpec      `json:"yes_steps,omitempty"`
	NoSteps   []StepSpec      `json:"no_steps,omitempty"`
}


// FlattenStepsToActions linearises a steps tree: actions and delays in DFS order
// (branches inlined), condition nodes dropped.
//
// THIS FUNCTION OUTLIVED THE ACTIONS COLUMN ON PURPOSE. R5 listed it for removal
// alongside the flat-Actions teardown; that was wrong. It has nothing to do with the
// deprecated column any more — it is the shared "what does this workflow actually
// do?" walk, and it has two live cross-package consumers that both make a SECURITY
// or CORRECTNESS decision from its result:
//
//  1. usecase.shouldActivate (system_template_usecase.go) refuses to auto-activate a
//     starter-template workflow if ANY flattened action fails
//     domain.IsAutoActivatableAction. Return an empty slice here and every template
//     workflow becomes auto-activatable — including ones carrying send_email or
//     send_webhook, which is how a freshly-created workspace starts emailing a
//     customer's contacts by itself. That is a fail-open regression, not a cleanup.
//     Note that domain's allow-list contains "delay": that entry is only truthful
//     because the delay branch below synthesises an ActionSpec{Type: ActionDelay}.
//     Stop emitting those and the map entry silently becomes a lie.
//  2. marketing.workflowHasMarketingSend (sequence_usecase.go) — the load-bearing
//     "is this a drip sequence?" check, which must see into If/Else branches.
//
// Exact Go port of the frontend's former flattenSteps (store.ts).
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
	// TriggerEmailOpened fires when a CAMPAIGN email is opened (arc G, plan
	// engagement_and_split_plan.md). Emitted by the marketing Resend webhook
	// processor — campaign opens only (sequence/1:1 sends are excluded, which
	// structurally prevents open→send→open trigger loops), with the analytics
	// layer's 10s machine-open (Apple MPP) filter applied. NOTE: "_opened" is
	// deliberately NOT a dynamic-wildcard suffix, so this const is what makes
	// the type valid; do not rename it to a *_updated form (that would drag it
	// into the watch_field/changed_fields filter branch).
	TriggerEmailOpened = "email_opened"
	// TriggerEmailClicked fires when a link in a CAMPAIGN email is clicked.
	// Same gating as TriggerEmailOpened (campaign-only, machine-filtered), plus
	// one exclusion of its own: a click on the unsubscribe / preference-centre
	// link never fires it. Like "_opened", "_clicked" is not a dynamic-wildcard
	// suffix, so this const is what makes the type valid.
	TriggerEmailClicked = "email_clicked"
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
	// The builder offers exactly these two for a boolean field
	// (useSchema.ts BOOLEAN_BASE) and auto-selects the first when the field type
	// changes, so every boolean condition a user can build arrives as one of
	// them. They were missing here, which made the payload a 400 and left
	// boolean fields unconditionable through the UI — including the
	// wait-for-event outcome the Wait step tells you to branch on.
	"is_true": true, "is_false": true,
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
	TriggerEmailOpened:      true,
	TriggerEmailClicked:     true,
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
