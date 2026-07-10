package ai

import (
	"crm-backend/internal/domain"
	"encoding/json"
)

// Tool represents an AI function-calling tool definition.
type Tool struct {
	Name   string         `json:"name"`
	Desc   string         `json:"description"`
	Params map[string]any `json:"parameters"`
}

// ToolCall is what the AI model returns when it wants to invoke a tool.
type ToolCall struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Params json.RawMessage `json:"input"` // normalised to this field internally
}

// ToolResult is sent back to the model after executing a tool.
type ToolResult struct {
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// ─── Tool definitions ────────────────────────────────────────────────────────

var allCRMTools = []Tool{
	{
		Name: "search_contacts",
		Desc: "Search and list contacts. Use sort_by='name' for alphabetical or 'created_at' for newest. Returns empty_message when no results found.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "search terms, name, email, or company"},
				"limit":      map[string]any{"type": "integer", "description": "max results (default 10, max 15)"},
				"sort_by":    map[string]any{"type": "string", "enum": []string{"created_at", "name"}, "description": "field to sort by"},
				"sort_order": map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "description": "asc or desc (default desc)"},
			},
			"required": []string{},
		},
	},
	{
		Name: "search_deals",
		Desc: "Find and list deals by name, stage, value, or status. Use query to search for specific deals by title. Returns empty_message when no results.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":          map[string]any{"type": "string", "description": "search by deal title or keyword"},
				"stage_name":    map[string]any{"type": "string", "description": "filter by pipeline stage name"},
				"days_inactive": map[string]any{"type": "integer", "description": "return deals with no activity in N days"},
				"min_value":     map[string]any{"type": "number", "description": "only deals worth at least this amount"},
				"status":        map[string]any{"type": "string", "enum": []string{"active", "won", "lost"}, "description": "deal status filter"},
				"sort_by":       map[string]any{"type": "string", "enum": []string{"value", "probability", "created_at", "title"}, "description": "field to sort by — use 'value' for largest deals"},
				"sort_order":    map[string]any{"type": "string", "enum": []string{"asc", "desc"}, "description": "asc or desc (default desc)"},
				"limit":         map[string]any{"type": "integer", "description": "max results (default 10, max 15)"},
			},
		},
	},
	{
		Name: "get_analytics",
		Desc: "Get revenue, pipeline health, or sales performance data",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"metric": map[string]any{"type": "string", "enum": []string{"revenue", "pipeline", "performance", "forecast"}},
				"period": map[string]any{"type": "string", "enum": []string{"this_month", "last_month", "this_quarter", "ytd"}},
			},
			"required": []string{"metric"},
		},
	},
	{
		Name: "navigate_to",
		Desc: "Navigate the user's browser to a specific CRM page (deals, contacts, settings, etc.)",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string", "description": "URL path, e.g. /deals or /contacts"},
				"label": map[string]any{"type": "string", "description": "human-readable destination, e.g. 'the Deals page'"},
			},
			"required": []string{"path", "label"},
		},
	},
	{
		Name: "create_task",
		Desc: "Create a task optionally linked to a deal or contact",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":      map[string]any{"type": "string", "description": "task title"},
				"deal_id":    map[string]any{"type": "string", "description": "optional deal UUID"},
				"contact_id": map[string]any{"type": "string", "description": "optional contact UUID"},
				"due_days":   map[string]any{"type": "integer", "description": "due in N days from today"},
				"priority":   map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
			},
			"required": []string{"title"},
		},
	},
	{
		Name: "update_deal",
		Desc: "Update a deal's status (won/lost/active), stage, probability, or add a note. Always include deal_title for a human-readable confirmation message.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id":     map[string]any{"type": "string", "description": "deal UUID (required)"},
				"deal_title":  map[string]any{"type": "string", "description": "deal title for the confirmation banner — always include if known"},
				"status":      map[string]any{"type": "string", "enum": []string{"won", "lost", "active"}, "description": "set deal outcome: 'won', 'lost', or reopen as 'active'"},
				"stage_name":  map[string]any{"type": "string", "description": "move to this stage name"},
				"probability": map[string]any{"type": "integer", "description": "win probability 0-100"},
				"note":        map[string]any{"type": "string", "description": "activity note to log alongside the update"},
			},
			"required": []string{"deal_id"},
		},
	},
	{
		Name: "log_activity",
		Desc: "Log a call, email sent, meeting, or note against a contact or deal",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type":       map[string]any{"type": "string", "enum": []string{"call", "email", "meeting", "note"}},
				"title":      map[string]any{"type": "string", "description": "activity title"},
				"deal_id":    map[string]any{"type": "string", "description": "optional deal UUID"},
				"contact_id": map[string]any{"type": "string", "description": "optional contact UUID"},
				"body":       map[string]any{"type": "string", "description": "optional body text"},
			},
			"required": []string{"type", "title"},
		},
	},
	{
		Name: "compose_email",
		Desc: "Draft a personalized email for a contact",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"contact_id":  map[string]any{"type": "string", "description": "contact UUID"},
				"instruction": map[string]any{"type": "string", "description": "what the email should accomplish"},
				"tone":        map[string]any{"type": "string", "enum": []string{"professional", "friendly", "urgent"}},
			},
			"required": []string{"contact_id", "instruction"},
		},
	},
	{
		Name: "create_contact",
		Desc: "Show an inline form so the user can create a new contact. Use this whenever the user asks to add or create a contact. You MUST extract and pre-fill ALL fields you can from the user's message.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prefill_name":  map[string]any{"type": "string", "description": "Extract the person's full name"},
				"prefill_email": map[string]any{"type": "string", "description": "Extract the email address"},
				"prefill_phone": map[string]any{"type": "string", "description": "Extract the phone number"},
			},
		},
	},
	{
		Name: "create_deal",
		Desc: "Show an inline form so the user can create a new deal. You MUST extract and pre-fill ALL fields. If the user mentions a contact, pass their contact_id and contact_name from the session context.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prefill_title":   map[string]any{"type": "string", "description": "A brief professional title for the deal"},
				"prefill_value":   map[string]any{"type": "number", "description": "The deal's monetary value as a number"},
				"contact_id":      map[string]any{"type": "string", "description": "UUID of the contact to link this deal to"},
				"contact_name":    map[string]any{"type": "string", "description": "display name of the linked contact"},
			},
		},
	},
	{
		Name: "search_objects",
		Desc: "Search any object type (base or custom) by its slug. Use the CRM SCHEMA to find the correct object_slug. Returns matching records with their field data.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"object_slug": map[string]any{"type": "string", "description": "the slug of the object type from the CRM schema (e.g. 'ticket', 'product', 'contact', 'deal')"},
				"query":       map[string]any{"type": "string", "description": "search term to filter records by display name or field values"},
				"limit":       map[string]any{"type": "integer", "description": "max results (default 10, max 20)"},
			},
			"required": []string{"object_slug"},
		},
	},
	{
		Name: "create_object_record",
		Desc: "Create a new record for any custom object type. Use the CRM SCHEMA to know which fields are available and required. Pass field values as key-value pairs in the 'fields' parameter.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"object_slug":  map[string]any{"type": "string", "description": "the slug of the custom object type from the CRM schema"},
				"display_name": map[string]any{"type": "string", "description": "a human-readable name for this record"},
				"fields":       map[string]any{"type": "object", "description": "key-value pairs matching the field keys from the CRM schema"},
			},
			"required": []string{"object_slug", "display_name"},
		},
	},
	{
		Name: "update_object_record",
		Desc: "Update any record (contact, deal, or custom object). Use search_objects first to find the record's ID. For contacts: display_name becomes first+last name, fields supports first_name, last_name, email, phone. For custom objects: pass display_name and/or fields.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"object_slug":  map[string]any{"type": "string", "description": "the slug of the custom object type from the CRM schema"},
				"record_id":    map[string]any{"type": "string", "description": "UUID of the record to update (from search_objects results)"},
				"display_name": map[string]any{"type": "string", "description": "new display name for the record (optional, only if renaming)"},
				"fields":       map[string]any{"type": "object", "description": "key-value pairs of fields to update — only include fields that should change"},
			},
			"required": []string{"object_slug", "record_id"},
		},
	},
	{
		Name: "create_workflow",
		Desc: "Open the automation builder to create a new workflow (automation) from a plain-language description. Use whenever the user asks to build, set up, or automate something (e.g. 'when a deal is won, notify the owner and create a follow-up task'). The described automation is drafted onto the builder canvas for the user to review and Save — it is NOT saved or activated automatically. Pass the user's full request as `description`, including the trigger and the steps.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"description": map[string]any{"type": "string", "description": "the automation to build, in plain language — include the trigger and the steps, e.g. 'when a deal moves to Won, wait 2 days then email the owner'"},
			},
			"required": []string{"description"},
		},
	},
	{
		Name: "update_workflow",
		Desc: "Open an EXISTING workflow in the automation builder with requested changes drafted for review. Requires the workflow's id (workflow_id) — only use it when you actually know the id (the user shared a workflow link, or it appears in the conversation/session context). If you don't know the id, ask the user which workflow or use create_workflow instead. The changes are drafted on the canvas for the user to review and Save — nothing is saved automatically.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"workflow_id": map[string]any{"type": "string", "description": "UUID of the workflow to edit"},
				"instruction": map[string]any{"type": "string", "description": "the change to make, in plain language, e.g. 'also send a Slack notification when it fires'"},
			},
			"required": []string{"workflow_id", "instruction"},
		},
	},
}

// workflowToolNames — the automation-authoring tools, gated by the workflows.manage
// capability (independent of the records read/write split, since a user can manage
// workflows without records.write). They hand off to the builder's AI copilot via a
// navigate event rather than writing anything directly.
var workflowToolNames = map[string]bool{
	"create_workflow": true,
	"update_workflow": true,
}

// readOnlyTools — only safe read tools for viewers.
var readOnlyToolNames = map[string]bool{
	"search_contacts": true,
	"search_deals":    true,
	"search_objects":  true,
	"get_analytics":   true,
	"navigate_to":     true,
}

// AllowedToolsWithSchema returns the tools available to the caller, with
// org-specific custom fields injected as first-class parameters into
// create_contact and create_deal (they appear identically to base fields, so the
// model treats them with equal priority). readOnly drops the write tools, leaving
// the read-only set — P7 derives readOnly from the caller's capabilities + OLS
// (records.write / object create-edit) instead of the old role-name switch, so a
// custom role is gated by what it can actually do. canManageWorkflows gates the
// automation-authoring tools (create_workflow/update_workflow) on the
// workflows.manage capability, independent of the records read/write split.
func AllowedToolsWithSchema(readOnly, canManageWorkflows bool, contactFields []domain.CustomFieldDef, dealFields []domain.CustomFieldDef) []Tool {
	var tools []Tool
	for _, t := range allCRMTools {
		switch {
		case workflowToolNames[t.Name]:
			if !canManageWorkflows {
				continue // workflows.manage gates these, not the read/write split
			}
		case readOnly && !readOnlyToolNames[t.Name]:
			continue // read-only caller: drop record write tools
		}
		// Deep-copy so we don't mutate the global slice
		tools = append(tools, Tool{Name: t.Name, Desc: t.Desc, Params: deepCopyMap(t.Params)})
	}

	// Inject custom fields into create_contact and create_deal
	for i := range tools {
		switch tools[i].Name {
		case "create_contact":
			injectCustomFieldParams(&tools[i], contactFields)
		case "create_deal":
			injectCustomFieldParams(&tools[i], dealFields)
		}
	}
	return tools
}

// injectCustomFieldParams adds custom field definitions as explicit tool parameters.
// e.g., a custom field {Key: "industry", Label: "Industry", Type: "text"} becomes:
//   "cf_industry": {"type": "string", "description": "Industry (text). Extract this value if mentioned."}
func injectCustomFieldParams(tool *Tool, fields []domain.CustomFieldDef) {
	if len(fields) == 0 {
		return
	}
	props, ok := tool.Params["properties"].(map[string]any)
	if !ok {
		return
	}
	for _, f := range fields {
		jsonType := "string"
		switch f.Type {
		case "number":
			jsonType = "number"
		case "boolean":
			jsonType = "boolean"
		}
		reqHint := ""
		if f.Required {
			reqHint = " REQUIRED."
		}
		paramDef := map[string]any{
			"type":        jsonType,
			"description": f.Label + " (" + f.Type + ")." + reqHint + " Extract this value if the user mentions it.",
		}
		if f.Type == "select" && len(f.Options) > 0 {
			paramDef["enum"] = f.Options
		}
		props["cf_"+f.Key] = paramDef
	}
}

// deepCopyMap creates a shallow-ish copy of a map[string]any, deep-copying
// nested maps so mutations don't affect the original.
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sub, ok := v.(map[string]any); ok {
			out[k] = deepCopyMap(sub)
		} else {
			out[k] = v
		}
	}
	return out
}

// CRMTools is kept for backward-compat with any existing callers.
var CRMTools = allCRMTools


// BuildToolsForCFWorkers converts tools to the OpenAI-compatible format.
func BuildToolsForCFWorkers() []map[string]any {
	return buildCFTools(allCRMTools)
}

func buildCFTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Desc,
				"parameters":  t.Params,
			},
		}
	}
	return out
}
