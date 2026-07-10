package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/ai"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ai_draft.go implements the A7 AI copilot: POST /api/workflows/ai/draft turns a
// natural-language description into a normalized, VALIDATED workflow draft JSON.
// It NEVER saves — the client applies the draft through the same zod validation as
// a manual edit (the canvas is the preview). It reuses the AI gateway's tool-loop
// (draft_workflow + get_workflow_schema) and the existing workflow validator.

// draftAICaller is the narrow slice of the AI gateway the copilot needs. *ai.AIGateway
// satisfies it; a fake drives the loop in unit tests.
type draftAICaller interface {
	CompleteWithTools(ctx context.Context, orgID, userID uuid.UUID, task ai.AITask, messages []ai.Message, tools []ai.Tool) (ai.AIResponse, error)
}

// SetDraftAI wires the AI gateway for the copilot endpoint (A7). Called once at
// startup; until set, DraftWorkflow returns 503.
func (h *Handler) SetDraftAI(g draftAICaller) { h.draftAI = g }

// maxDraftIterations bounds the copilot tool loop: enough for a get_workflow_schema
// round-trip then a draft_workflow call, with slack, but never unbounded.
const maxDraftIterations = 4

// draftTimeout hard-bounds the WHOLE draft (every model call + tool iteration +
// retry inside the gateway). The copilot is interactive, so it must fail as clean
// JSON well before any client (45s) or reverse-proxy timeout — otherwise a slow
// model turns into an HTML gateway-timeout page the client can't parse. Kept under
// the frontend's abort so the browser receives our error rather than aborting first.
const draftTimeout = 28 * time.Second

// WorkflowDraft is the normalized draft the copilot returns. The shapes mirror the
// save payload (trigger/conditions/steps) so the client can apply it directly.
type WorkflowDraft struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Trigger     json.RawMessage `json:"trigger"`
	Conditions  json.RawMessage `json:"conditions,omitempty"`
	Steps       json.RawMessage `json:"steps"`
}

type draftWorkflowRequest struct {
	Prompt string `json:"prompt"`
	// CurrentWorkflow, when present, is the workflow the user is editing (the builder
	// sends its working copy). The copilot then applies the instruction AGAINST it
	// instead of drafting from scratch — so "add a step" edits rather than replaces.
	// Empty ⇒ create-from-scratch (the default). Passed through to the model as-is.
	CurrentWorkflow json.RawMessage `json:"current_workflow,omitempty"`
}

// DraftWorkflow handles POST /api/workflows/ai/draft.
func (h *Handler) DraftWorkflow(c *gin.Context) {
	orgID, userID := h.getContext(c)
	if orgID == uuid.Nil {
		return
	}
	if h.draftAI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI copilot is not configured"})
		return
	}
	var req draftWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
		return
	}

	// Bound the whole draft so it can never hang past the client/proxy timeout and
	// surface as an HTML gateway-timeout page — on expiry generateDraft returns a
	// normal error and we reply with clean JSON (the client then shows its fallback).
	ctx, cancel := context.WithTimeout(c.Request.Context(), draftTimeout)
	defer cancel()

	draft, validation, err := h.generateDraft(ctx, orgID, userID, req.Prompt, req.CurrentWorkflow)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"draft": draft, "validation": validation}})
}

// generateDraft runs the copilot tool loop and returns a normalized + validated
// draft. The draft is returned even when validation surfaces issues (the client's
// zod pass + the canvas are the final gate), so the caller can show and fix it.
func (h *Handler) generateDraft(ctx context.Context, orgID, userID uuid.UUID, prompt string, current json.RawMessage) (*WorkflowDraft, *ValidationResult, error) {
	schema := h.buildSchema(ctx, orgID)
	// When editing an existing workflow, give the model the current workflow so it
	// applies the instruction against it (edit) rather than drafting from scratch
	// (replace). The client's canvas + Keep/Undo remain the final review gate.
	userContent := prompt
	if trimmed := strings.TrimSpace(string(current)); trimmed != "" && trimmed != "null" {
		userContent = "The user is EDITING this existing workflow (JSON):\n" + trimmed +
			"\n\nApply the following change and call draft_workflow with the COMPLETE updated workflow, " +
			"preserving every trigger/step/condition the user did not ask to change:\n" + prompt
	}
	messages := []ai.Message{
		{Role: "system", Content: buildDraftSystemPrompt(schema)},
		{Role: "user", Content: userContent},
	}
	tools := draftTools()

	schemaJSON, _ := json.Marshal(schema)

	for i := 0; i < maxDraftIterations; i++ {
		resp, err := h.draftAI.CompleteWithTools(ctx, orgID, userID, ai.TaskCommandCenter, messages, tools)
		if err != nil {
			return nil, nil, fmt.Errorf("the AI copilot is unavailable right now — please try again")
		}
		if len(resp.ToolCalls) == 0 {
			// The model answered with prose instead of drafting. Surface a short hint
			// rather than a mystery failure.
			msg := strings.TrimSpace(resp.Content)
			if msg == "" {
				msg = "the assistant didn't produce a workflow — try describing the trigger and the steps"
			}
			return nil, nil, fmt.Errorf("%s", msg)
		}

		messages = append(messages, ai.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
		for _, tc := range resp.ToolCalls {
			switch tc.Name {
			case "draft_workflow":
				return finalizeDraft(tc.Params)
			case "get_workflow_schema":
				messages = append(messages, ai.Message{Role: "tool", ToolUseID: tc.ID, Content: string(schemaJSON)})
			default:
				messages = append(messages, ai.Message{Role: "tool", ToolUseID: tc.ID, Content: `{"error":"unknown tool"}`})
			}
		}
	}
	return nil, nil, fmt.Errorf("the assistant couldn't finish a draft — try simplifying the request")
}

// finalizeDraft parses the model's draft_workflow arguments into a normalized draft,
// assigns any missing step/action ids, and validates it with the real workflow
// validator. Returns the draft plus the validation result.
func finalizeDraft(raw json.RawMessage) (*WorkflowDraft, *ValidationResult, error) {
	var args struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Trigger     json.RawMessage `json:"trigger"`
		Conditions  json.RawMessage `json:"conditions"`
		Steps       json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, nil, fmt.Errorf("the assistant returned a malformed draft")
	}
	if len(args.Trigger) == 0 || string(args.Trigger) == "null" {
		return nil, nil, fmt.Errorf("the assistant's draft has no trigger — try naming the event that starts it")
	}

	// Normalize step ids so the draft validates and the client can apply it as-is.
	var steps []StepSpec
	if len(args.Steps) > 0 && string(args.Steps) != "null" {
		if err := json.Unmarshal(args.Steps, &steps); err != nil {
			return nil, nil, fmt.Errorf("the assistant's steps were malformed")
		}
	}
	counter := 0
	normalizeStepIDs(steps, &counter)
	normalizedSteps, _ := json.Marshal(steps)

	draft := &WorkflowDraft{
		Name:        strings.TrimSpace(args.Name),
		Description: strings.TrimSpace(args.Description),
		Trigger:     args.Trigger,
		Conditions:  args.Conditions,
		Steps:       normalizedSteps,
	}
	if draft.Name == "" {
		draft.Name = "Untitled workflow"
	}

	validation := ValidateWorkflowPayload(draft.Trigger, draft.Conditions, nil, draft.Steps)
	return draft, validation, nil
}

// normalizeStepIDs assigns a stable unique id to every step (and mirrors it onto
// the step's action) that the model left blank, recursing through condition
// branches. The engine + validator require a unique id per step and action.id ==
// step.id, so a draft with missing/duplicate ids would otherwise fail to apply.
func normalizeStepIDs(steps []StepSpec, counter *int) {
	for i := range steps {
		*counter++
		if strings.TrimSpace(steps[i].ID) == "" {
			steps[i].ID = fmt.Sprintf("ai_%d", *counter)
		}
		if steps[i].Type == "action" && steps[i].Action != nil {
			steps[i].Action.ID = steps[i].ID
		}
		normalizeStepIDs(steps[i].YesSteps, counter)
		normalizeStepIDs(steps[i].NoSteps, counter)
	}
}

// draftTools are the two workflow-scoped tools the copilot may call.
func draftTools() []ai.Tool {
	return []ai.Tool{
		{
			Name: "get_workflow_schema",
			Desc: "Return the organization's automation schema: objects and their fields, pipeline stages, tags, and members (with ids). Call this if you need exact field paths, stage names, or user ids before drafting.",
			Params: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
		},
		{
			Name: "draft_workflow",
			Desc: "Produce the finished workflow draft. Call this exactly once when you have enough detail. The draft is shown to the user for review — it is never saved automatically.",
			Params: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "a short, human title for the workflow"},
					"description": map[string]any{"type": "string", "description": "optional one-line summary"},
					"trigger": map[string]any{
						"type":        "object",
						"description": "the event that starts the workflow: { type, params }",
					},
					"conditions": map[string]any{
						"type":        "object",
						"description": "optional top-level condition group; omit or null if none",
					},
					"steps": map[string]any{
						"type":        "array",
						"description": "ordered steps: each { type: action|condition|delay, ... }. action → { type, params }. condition → { condition:{op,rules}, yes_steps:[], no_steps:[] }. delay → { delay:{ duration_sec } }.",
						"items":       map[string]any{"type": "object"},
					},
				},
				"required": []string{"name", "trigger", "steps"},
			},
		},
	}
}

// buildDraftSystemPrompt describes the drafting task, the workflow JSON shape, the
// valid trigger/action vocabulary, and a compact live schema so the model uses real
// field paths, stage names, and user ids.
func buildDraftSystemPrompt(schema *SchemaResponse) string {
	var b strings.Builder
	b.WriteString("You are a workflow builder for a CRM. Turn the user's request into ONE automation workflow and return it by calling the draft_workflow tool. Do not save anything; the user reviews the draft.\n\n")

	b.WriteString("WORKFLOW SHAPE:\n")
	b.WriteString("- trigger: { \"type\": <trigger type>, \"params\": {...} }\n")
	b.WriteString("- steps: an ordered array. Each step is one of:\n")
	b.WriteString("  - action:    { \"type\": \"action\", \"action\": { \"type\": <action type>, \"params\": {...} } }\n")
	b.WriteString("  - condition: { \"type\": \"condition\", \"condition\": { \"op\": \"AND\"|\"OR\", \"rules\": [ { \"field\": <path>, \"operator\": <op>, \"value\": <v> } ] }, \"yes_steps\": [...], \"no_steps\": [...] }\n")
	b.WriteString("  - delay:     { \"type\": \"delay\", \"delay\": { \"duration_sec\": <seconds> } }\n")
	b.WriteString("You may omit step ids; they are assigned for you.\n\n")

	b.WriteString("TRIGGER TYPES: contact_created, contact_updated, deal_stage_changed (params.to_stage = a stage id), company_updated, <custom_slug>_created/_updated, schedule (params.cron + params.timezone), date_field (params.object, params.field, params.offset_days, params.at_time).\n")
	b.WriteString("ACTION TYPES: send_email {to, subject, body_html}, create_task {title, priority, due_in_days, assignee_field}, assign_user {entity, strategy, user_id}, update_record {updates:[{field,op,value}]}, create_record {object, fields:[{field,value}]}, notify_user {recipient:'owner_field'|'specific', title, body}, find_records {object, filters}, enroll_records {workflow_id, object}, ai_generate {prompt, max_tokens}, send_webhook {url, method}, log_activity {activity_type, title}.\n")
	b.WriteString("CONDITION OPERATORS: eq, neq, gt, gte, lt, lte, contains, not_contains, in, not_in, is_empty, is_not_empty, starts_with, ends_with.\n")
	b.WriteString("Field paths look like \"contact.email\", \"deal.value\", \"deal.stage_id\". Use interpolation like {{contact.first_name}} in text params.\n\n")

	b.WriteString(compactSchema(schema))

	b.WriteString("\nEXAMPLE — \"when a deal moves to Won, email the owner\":\n")
	b.WriteString(`{"name":"Notify owner on Won","trigger":{"type":"deal_stage_changed","params":{"to_stage":"<won stage id>"}},"steps":[{"type":"action","action":{"type":"notify_user","params":{"recipient":"owner_field","title":"Deal won: {{deal.title}}"}}}]}`)
	b.WriteString("\n")
	return b.String()
}

// compactSchema renders a token-frugal summary of the org's objects/fields, stages,
// and members so the model can pick real paths and ids without a tool round-trip.
func compactSchema(schema *SchemaResponse) string {
	if schema == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("AVAILABLE OBJECTS & FIELDS:\n")
	writeEntity := func(e SchemaEntity) {
		paths := make([]string, 0, len(e.Fields))
		for _, f := range e.Fields {
			paths = append(paths, f.Path)
		}
		b.WriteString("- " + e.Key + ": " + strings.Join(paths, ", ") + "\n")
	}
	for _, e := range schema.Entities {
		writeEntity(e)
	}
	for _, e := range schema.CustomObjects {
		writeEntity(e)
	}
	if len(schema.Stages) > 0 {
		parts := make([]string, 0, len(schema.Stages))
		for _, s := range schema.Stages {
			parts = append(parts, fmt.Sprintf("%s=%s", s.Name, s.ID))
		}
		b.WriteString("PIPELINE STAGES (name=id): " + strings.Join(parts, ", ") + "\n")
	}
	if len(schema.Users) > 0 {
		parts := make([]string, 0, len(schema.Users))
		for _, u := range schema.Users {
			parts = append(parts, fmt.Sprintf("%s=%s", u.Name, u.ID))
		}
		b.WriteString("MEMBERS (name=id): " + strings.Join(parts, ", ") + "\n")
	}
	return b.String()
}
