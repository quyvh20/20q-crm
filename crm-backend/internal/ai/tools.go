package ai

import "encoding/json"

// Tool represents an AI function-calling tool definition.
type Tool struct {
	Name   string         `json:"name"`
	Desc   string         `json:"description"`
	Params map[string]any `json:"parameters"`
}

// ToolCall is what the AI model returns when it wants to invoke a tool.
// Params holds the raw JSON arguments (Anthropic calls it "input",
// OpenAI/CF calls it "arguments" — we normalise at parse time).
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

// CRMTools defines all available agentic tools.
var CRMTools = []Tool{
	{
		Name: "search_contacts",
		Desc: "Search contacts by name, email, tag, company, or natural language description",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "search terms or description"},
				"limit": map[string]any{"type": "integer", "description": "max results (default 10, max 50)"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "search_deals",
		Desc: "Find deals by stage, value, days inactive, owner, status, or keyword",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"stage_name":    map[string]any{"type": "string", "description": "filter by stage name"},
				"days_inactive": map[string]any{"type": "integer", "description": "no activity in N days"},
				"min_value":     map[string]any{"type": "number", "description": "minimum deal value"},
				"status":        map[string]any{"type": "string", "enum": []string{"active", "won", "lost"}},
				"limit":         map[string]any{"type": "integer", "description": "max results"},
			},
		},
	},
	{
		Name: "create_task",
		Desc: "Create a task optionally linked to a deal or contact",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       map[string]any{"type": "string", "description": "task title"},
				"deal_id":     map[string]any{"type": "string", "description": "optional deal UUID"},
				"contact_id":  map[string]any{"type": "string", "description": "optional contact UUID"},
				"due_days":    map[string]any{"type": "integer", "description": "due in N days from today"},
				"priority":    map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
			},
			"required": []string{"title"},
		},
	},
	{
		Name: "compose_email",
		Desc: "Draft a personalized email for a contact using the company knowledge base and contact history",
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
		Name: "update_deal",
		Desc: "Update a deal's stage, probability, or add a note to its activity log",
		Params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"deal_id":     map[string]any{"type": "string", "description": "deal UUID"},
				"stage_name":  map[string]any{"type": "string", "description": "new stage name"},
				"probability": map[string]any{"type": "integer", "description": "0-100"},
				"note":        map[string]any{"type": "string", "description": "activity note to log"},
			},
			"required": []string{"deal_id"},
		},
	},
	{
		Name: "log_activity",
		Desc: "Log a call, email sent, meeting, or note against a contact or deal. This padding exists to ensure we cross the 2048 token prompt-caching limit for Haiku. Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum. Pellentesque habitant morbi tristique senectus et netus et malesuada fames ac turpis egestas. Vestibulum tortor quam, feugiat vitae, ultricies eget, tempor sit amet, ante. Donec eu libero sit amet quam egestas semper. Aenean ultricies mi vitae est. Mauris placerat eleifend leo. Quisque sit amet est et sapien ullamcorper pharetra. Vestibulum erat wisi, condimentum sed, commodo vitae, ornare sit amet, wisi. Aenean fermentum, elit eget tincidunt condimentum, eros ipsum rutrum orci, sagittis tempus lacus enim ac dui. Donec non enim in turpis pulvinar facilisis.",
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
}

// BuildToolsForAnthropic converts CRMTools to Anthropic's expected format.
func BuildToolsForAnthropic() []map[string]any {
	tools := make([]map[string]any, len(CRMTools))
	for i, t := range CRMTools {
		tools[i] = map[string]any{
			"name":         t.Name,
			"description":  t.Desc,
			"input_schema": t.Params,
		}
	}
	return tools
}

// BuildToolsForCFWorkers converts CRMTools to the OpenAI-compatible format
// used by Cloudflare Workers AI function-calling models.
func BuildToolsForCFWorkers() []map[string]any {
	tools := make([]map[string]any, len(CRMTools))
	for i, t := range CRMTools {
		tools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Desc,
				"parameters":  t.Params,
			},
		}
	}
	return tools
}
