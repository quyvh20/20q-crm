package automation

import (
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// --- Request DTOs ---

// CreateWorkflowRequest is the request body for creating a workflow.
//
// There is no `actions` field: the flat action list was removed in R5 deploy 1 and a
// non-empty `steps` tree is now REQUIRED (see ValidateWorkflowPayload). An
// actions-only body is not silently ignored — it is rejected with a 400 naming
// `steps`, because silently accepting one would create a workflow that runs nothing.
//
// The asymmetry with WorkflowResponse.Actions (which still exists, derived) is
// deliberate and temporary: the API must stop ACCEPTING a flat list immediately, but it
// keeps EMITTING one for one more deploy so cached bundles can still save. Read the
// field comment there before deleting either half.
type CreateWorkflowRequest struct {
	Name        string          `json:"name" binding:"required,min=1,max=200"`
	Description string          `json:"description" binding:"max=1000"`
	Trigger     datatypes.JSON  `json:"trigger" binding:"required"`
	Conditions  datatypes.JSON  `json:"conditions"`
	Steps       datatypes.JSON  `json:"steps"`
}

// UpdateWorkflowRequest is the request body for updating a workflow. See
// CreateWorkflowRequest on the absent `actions` field.
type UpdateWorkflowRequest struct {
	Name        *string         `json:"name" binding:"omitempty,min=1,max=200"`
	Description *string         `json:"description" binding:"omitempty,max=1000"`
	Trigger     datatypes.JSON  `json:"trigger"`
	Conditions  datatypes.JSON  `json:"conditions"`
	Steps       datatypes.JSON  `json:"steps"`
}

// TestRunRequest is the request body for a dry-run (A3.5). Prefer a sample entity
// (contact_id / deal_id) — the server loads it and builds a realistic eval context
// exactly like Run Now — so conditions and templates resolve against real data.
// Context is a raw override for tests/advanced callers when no entity is supplied.
type TestRunRequest struct {
	ContactID string         `json:"contact_id"`
	DealID    string         `json:"deal_id"`
	Context   map[string]any `json:"context"`
}

// RunNowRequest is the request body for POST /api/workflows/:id/run.
// Exactly one of ContactID / DealID must be a non-empty, valid UUID string.
// Plain string fields (no binding:"required") let the handler distinguish the
// both-present, neither-present, and invalid-UUID cases and emit precise 400 errors.
type RunNowRequest struct {
	ContactID string `json:"contact_id"`
	DealID    string `json:"deal_id"`
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
	Steps         datatypes.JSON `json:"steps,omitempty"`
	// Actions is a COMPATIBILITY SHIM FOR CACHED FRONTEND BUNDLES. REMOVE IN R5 DEPLOY 2,
	// with the column, the gate and this comment — not before.
	//
	// It is NOT the deprecated column. Nothing reads `actions` from the database any
	// more; this is FlattenStepsToActions(steps), derived on the way out, so it is
	// correct on a database where the column is empty and it disappears cleanly when
	// this field goes.
	//
	// WHY IT HAS TO SURVIVE ONE MORE DEPLOY. A browser tab that loaded the pre-deploy-1
	// bundle and was never reloaded (a CRM tab left open all day — `public/_headers`
	// sets no Cache-Control, so only a reload revalidates) keeps its own copy of the
	// builder store, which does `actions: changed ? flattenSteps(steps) : (wf.actions || [])`.
	// Drop the field and that store loads every workflow with actions = [], which does
	// not crash — it does something quieter and worse:
	//
	//   - its zod schema requires actions.min(1), handleSave early-returns on a failed
	//     validate(), and nothing renders store.errors, so SAVE SILENTLY DOES NOTHING,
	//     with no toast and no error text anywhere near the button;
	//   - ActionConfig resolves the selected node with actions.find(...), gets undefined
	//     and renders a blank config panel for every action and delay node;
	//   - the sequences page's drip picker lists nothing.
	//
	// Emitting the derived list keeps those tabs fully working until they reload: the
	// old bundle's SAVE payload is already steps-only (buildSavePayload), so its copy of
	// `actions` never had to survive a round trip — it only has to be non-empty for the
	// save to be attempted at all.
	//
	// The REQUEST DTOs are a separate question and are already right: CreateWorkflowRequest
	// and UpdateWorkflowRequest have no `actions` field, so the API cannot be talked into
	// accepting a flat list even by a client that sends one.
	Actions       []ActionSpec   `json:"actions"`
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
	WakeAt           *string        `json:"wake_at,omitempty"`
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
	ActionPath string         `json:"action_path,omitempty"`
	ActionType string         `json:"action_type"`
	Status     string         `json:"status"`
	Input      datatypes.JSON `json:"input,omitempty"`
	Output     datatypes.JSON `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	AttemptNo  int            `json:"attempt_no"`
	DurationMs int64          `json:"duration_ms"`
	CreatedAt  string         `json:"created_at"`
}

// TestRunResponse is the response for a dry-run (A3.5): the top-level condition
// gate plus one entry per step in the tree (pre-order), keyed by step id so the
// builder can overlay run/skip status and resolved params directly onto canvas nodes.
type TestRunResponse struct {
	ConditionResult bool          `json:"condition_result"`
	Steps           []TestRunStep `json:"steps"`
}

// TestRunStep is the dry-run outcome for a single step (no side effects).
type TestRunStep struct {
	StepID string `json:"step_id"`
	Type   string `json:"type"` // "action" | "condition" | "delay"
	// Status is "run" (on the taken path) or "skip" (untaken branch, or the whole
	// workflow gated off by failing top-level conditions).
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"` // why a step was skipped
	// Action-only: the resolved (interpolated) params it would run with.
	ActionType     string         `json:"action_type,omitempty"`
	ResolvedParams map[string]any `json:"resolved_params,omitempty"`
	// Condition-only: the evaluated result and which branch ("yes"/"no") is taken.
	ConditionResult *bool  `json:"condition_result,omitempty"`
	Branch          string `json:"branch,omitempty"`
	// Delay-only: the wait duration.
	DelaySec int `json:"delay_sec,omitempty"`
}

// RunNowResponse is the success body (HTTP 201) for POST /api/workflows/:id/run.
type RunNowResponse struct {
	ID     uuid.UUID `json:"id"`     // created Workflow_Run id
	Status string    `json:"status"` // run status, e.g. "pending"
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

// WebhookTokenResponse is returned by GET /api/webhooks/token. It powers the
// builder's inbound-webhook setup panel (P17). The secret is MASKED (last 4 chars
// only) — the full secret is exposed solely by the regenerate endpoint. The route
// is guarded by the workflows.manage capability (requireCap(CapWorkflowsManage)).
type WebhookTokenResponse struct {
	Token        string `json:"token"`         // org token embedded in the inbound URL path
	SecretMasked string `json:"secret_masked"` // signing secret, masked for display
	URL          string `json:"url"`           // absolute URL external systems POST to
}

// WebhookSecretResponse is returned by POST /api/webhooks/regenerate-secret. It
// carries the FULL, freshly-rotated signing secret exactly once — the caller must
// capture it immediately, since subsequent GETs return only the masked form.
// Rotating invalidates the previous secret (old X-Signature values stop verifying).
type WebhookSecretResponse struct {
	Token  string `json:"token"`  // unchanged by rotation; inbound URL stays stable
	Secret string `json:"secret"` // full HMAC-SHA256 secret — shown exactly once
	URL    string `json:"url"`    // absolute URL external systems POST to
}

// WebhookSecretRevealResponse is returned by POST /api/webhooks/reveal-secret. It
// carries the org's current full signing secret for on-demand reveal/copy in the
// setup UI (the listing GET only returns the masked form). It does not rotate.
type WebhookSecretRevealResponse struct {
	Secret string `json:"secret"` // full HMAC-SHA256 secret (unchanged)
}

// --- Email template DTOs (A5) ---

// CreateEmailTemplateRequest is the body for POST /api/workflows/email-templates.
// BodyJSON (the TipTap doc) is optional — a hand-edited template can omit it.
type CreateEmailTemplateRequest struct {
	Name       string         `json:"name" binding:"required,min=1,max=200"`
	Subject    string         `json:"subject" binding:"max=500"`
	BodyHTML   string         `json:"body_html"`
	BodyJSON   datatypes.JSON `json:"body_json"`
	ObjectSlug string         `json:"object_slug" binding:"max=100"`
}

// UpdateEmailTemplateRequest is the body for PUT /api/workflows/email-templates/:id.
// Pointer fields make each mutation optional (a nil field is left unchanged).
type UpdateEmailTemplateRequest struct {
	Name       *string        `json:"name" binding:"omitempty,min=1,max=200"`
	Subject    *string        `json:"subject" binding:"omitempty,max=500"`
	BodyHTML   *string        `json:"body_html"`
	BodyJSON   datatypes.JSON `json:"body_json"`
	ObjectSlug *string        `json:"object_slug" binding:"omitempty,max=100"`
}

// EmailTemplateResponse is the API shape for a single template.
type EmailTemplateResponse struct {
	ID         uuid.UUID      `json:"id"`
	OrgID      uuid.UUID      `json:"org_id"`
	Name       string         `json:"name"`
	Subject    string         `json:"subject"`
	BodyHTML   string         `json:"body_html"`
	BodyJSON   datatypes.JSON `json:"body_json,omitempty"`
	ObjectSlug string         `json:"object_slug"`
	CreatedBy  uuid.UUID      `json:"created_by"`
	UpdatedBy  uuid.UUID      `json:"updated_by"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
}

// EmailTemplateListResponse is the API shape for GET /api/workflows/email-templates.
type EmailTemplateListResponse struct {
	Templates []EmailTemplateResponse `json:"templates"`
	Total     int                     `json:"total"`
}

// TestSendEmailTemplateResponse confirms a test-send and echoes the recipient.
type TestSendEmailTemplateResponse struct {
	Status string `json:"status"`
	To     string `json:"to"`
}

// ToEmailTemplateResponse converts a model to its response DTO.
func ToEmailTemplateResponse(t *EmailTemplate) EmailTemplateResponse {
	return EmailTemplateResponse{
		ID:         t.ID,
		OrgID:      t.OrgID,
		Name:       t.Name,
		Subject:    t.Subject,
		BodyHTML:   t.BodyHTML,
		BodyJSON:   t.BodyJSON,
		ObjectSlug: t.ObjectSlug,
		CreatedBy:  t.CreatedBy,
		UpdatedBy:  t.UpdatedBy,
		CreatedAt:  t.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:  t.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

// --- Conversion helpers ---

// ToWorkflowResponse converts a Workflow model to a response DTO.
func ToWorkflowResponse(wf *Workflow) WorkflowResponse {
	// The steps tree is the only source of BOTH the action count and the derived
	// `actions` compatibility list. There is no flat-Actions fallback any more: a
	// workflow with no steps genuinely executes nothing, so reporting 0 (and an empty
	// list) is the truth rather than a degraded reading.
	var steps []StepSpec
	if len(wf.Steps) > 0 && string(wf.Steps) != "null" {
		if err := json.Unmarshal(wf.Steps, &steps); err != nil {
			steps = nil
		}
	}

	// Derived, never read from the deprecated column — see the field comment on
	// WorkflowResponse.Actions, and delete both in deploy 2. FlattenStepsToActions
	// recurses into If/Else branches, so an action buried in a branch still reaches an
	// old cached bundle, which is what its actions.min(1) save guard needs.
	//
	// Non-nil on purpose: a nil slice marshals to JSON `null`, and a null array is how
	// this frontend white-screens on `.map` / `.find`.
	actions := FlattenStepsToActions(steps)
	if actions == nil {
		actions = []ActionSpec{}
	}

	return WorkflowResponse{
		ID:          wf.ID,
		OrgID:       wf.OrgID,
		Name:        wf.Name,
		Description: wf.Description,
		IsActive:    wf.IsActive,
		Trigger:     wf.Trigger,
		Conditions:  wf.Conditions,
		Steps:       wf.Steps,
		Actions:     actions,
		ActionCount: countStepsList(steps),
		Version:     wf.Version,
		CreatedBy:   wf.CreatedBy,
		CreatedAt:   wf.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:   wf.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}
}

func countStepsList(steps []StepSpec) int {
	count := 0
	for _, s := range steps {
		if s.Type == "action" || s.Type == "delay" {
			count++
		} else if s.Type == "condition" {
			count += countStepsList(s.YesSteps)
			count += countStepsList(s.NoSteps)
		}
	}
	return count
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
	if run.WakeAt != nil {
		s := run.WakeAt.Format("2006-01-02T15:04:05Z")
		resp.WakeAt = &s
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
		ActionPath: log.ActionPath,
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
