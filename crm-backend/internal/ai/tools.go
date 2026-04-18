package ai

import "encoding/json"

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
		Desc: "Find and list deals. Supports sorting by value/probability for 'top N' queries. Returns empty_message when no results.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
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
		Desc: "Show an inline form so the user can create a new contact. Use this whenever the user asks to add or create a contact.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prefill_name":  map[string]any{"type": "string", "description": "pre-fill the name field"},
				"prefill_email": map[string]any{"type": "string", "description": "pre-fill the email field"},
			},
		},
	},
	{
		Name: "create_deal",
		Desc: "Show an inline form so the user can create a new deal. Use this whenever the user asks to add, create, or log a new deal. Do NOT redirect to any page.",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prefill_title": map[string]any{"type": "string", "description": "pre-fill the deal title"},
				"prefill_value": map[string]any{"type": "number", "description": "pre-fill the deal value amount"},
			},
		},
	},
}

// readOnlyTools — only safe read tools for viewers.
var readOnlyToolNames = map[string]bool{
	"search_contacts": true,
	"search_deals":    true,
	"get_analytics":   true,
	"navigate_to":     true,
}

// AllowedTools returns the subset of tools a given role may call.
func AllowedTools(role string) []Tool {
	switch role {
	case "viewer":
		var tools []Tool
		for _, t := range allCRMTools {
			if readOnlyToolNames[t.Name] {
				tools = append(tools, t)
			}
		}
		return tools
	default:
		// owner, admin, manager, sales_rep all get full set
		return allCRMTools
	}
}

// CRMTools is kept for backward-compat with any existing callers.
var CRMTools = allCRMTools

// BuildToolsForAnthropic converts tools to Anthropic's expected format.
func BuildToolsForAnthropic() []map[string]any {
	return buildAnthropicTools(allCRMTools)
}

func buildAnthropicTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, len(tools))
	for i, t := range tools {
		out[i] = map[string]any{
			"name":         t.Name,
			"description":  t.Desc,
			"input_schema": t.Params,
		}
	}
	return out
}

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
