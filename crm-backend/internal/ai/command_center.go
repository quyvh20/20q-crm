package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CommandEvent represents a single SSE event from the command center.
type CommandEvent struct {
	Type    string          `json:"type"` // thinking | tool_result | response | confirm | navigate | form | error | done
	Message string          `json:"message,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Done    bool            `json:"done,omitempty"`
}

// CommandRequest carries everything needed for one conversational turn.
type CommandRequest struct {
	SessionID      uuid.UUID        `json:"session_id"`
	UserMessage    string           `json:"message"`
	History        []HistoryMessage `json:"history,omitempty"`
	UserRole       string           `json:"role"`
	Workspaces     []WorkspaceInfo  `json:"workspaces,omitempty"` // all workspaces the user belongs to
	Confirmed      bool             `json:"confirmed,omitempty"`
	ConfirmedTool  string           `json:"confirmed_tool,omitempty"`
	ConfirmedArgs  json.RawMessage  `json:"confirmed_args,omitempty"`
}

// WorkspaceInfo is a compact workspace descriptor for AI context.
type WorkspaceInfo struct {
	OrgName string `json:"org_name"`
	Role    string `json:"role"`
}

// HistoryMessage is a compact representation of a past turn.
type HistoryMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"` // trimmed text
}

// CommandCenter orchestrates agentic AI with tool calling.
type CommandCenter struct {
	gateway          *AIGateway
	knowledgeBuilder *KnowledgeBuilder
	contactRepo      domain.ContactRepository
	dealRepo         domain.DealRepository
	taskRepo         domain.TaskRepository
	activityRepo     domain.ActivityRepository
	sessionRepo      domain.ChatSessionRepository
	customObjUC      domain.CustomObjectUseCase
	sessionCtx       *SessionContextCache
	logger           *zap.Logger
}

func NewCommandCenter(
	gateway *AIGateway,
	knowledgeBuilder *KnowledgeBuilder,
	contactRepo domain.ContactRepository,
	dealRepo domain.DealRepository,
	taskRepo domain.TaskRepository,
	activityRepo domain.ActivityRepository,
	sessionRepo domain.ChatSessionRepository,
	customObjUC domain.CustomObjectUseCase,
	logger *zap.Logger,
) *CommandCenter {
	return &CommandCenter{
		gateway:          gateway,
		knowledgeBuilder: knowledgeBuilder,
		contactRepo:      contactRepo,
		dealRepo:         dealRepo,
		taskRepo:         taskRepo,
		activityRepo:     activityRepo,
		sessionRepo:      sessionRepo,
		customObjUC:      customObjUC,
		sessionCtx:       NewSessionContextCache(),
		logger:           logger,
	}
}

// Execute processes a user message and returns a channel of SSE events.
func (cc *CommandCenter) Execute(
	ctx context.Context,
	orgID, userID uuid.UUID,
	req CommandRequest,
) (<-chan CommandEvent, error) {
	// Budget check before opening stream
	if cc.gateway.Budget != nil {
		if err := cc.gateway.Budget.Check(ctx, orgID, TaskCommandCenter, 5000); err != nil {
			return nil, err
		}
	}

	events := make(chan CommandEvent, 100)

	go func() {
		defer close(events)

		// ── Persist user message asynchronously ──────────────────────────────
		if cc.sessionRepo != nil && req.SessionID != uuid.Nil {
			go cc.sessionRepo.AppendMessage(context.Background(), &domain.ChatMessage{
				SessionID: req.SessionID,
				Role:      "user",
				Content:   req.UserMessage,
			})
		}

		// ── Handle confirmed write actions ───────────────────────────────────
		if req.Confirmed && req.ConfirmedTool != "" {
			events <- CommandEvent{Type: "thinking", Message: "Executing action…"}
			var params map[string]interface{}
			json.Unmarshal(req.ConfirmedArgs, &params)
			call := ToolCall{Name: req.ConfirmedTool, Params: req.ConfirmedArgs}
			result := cc.executeTool(ctx, orgID, userID, req.UserRole, call)
			events <- CommandEvent{Type: "tool_result", Tool: req.ConfirmedTool, Data: result}

			// Final summary
			summaryMsg := []Message{
				{Role: "user", Content: fmt.Sprintf("I confirmed the action '%s'. Here is the result: %s. Summarize what happened in one sentence.", req.ConfirmedTool, string(result))},
			}
			resp, _ := cc.gateway.Complete(ctx, orgID, userID, TaskCommandCenter, summaryMsg)
			replyContent := resp.Content
			if replyContent == "" {
				replyContent = "Done ✓ Action completed successfully."
			}
			events <- CommandEvent{Type: "response", Message: replyContent, Done: true}
			events <- CommandEvent{Type: "done", Done: true}
			cc.persistAssistant(req.SessionID, replyContent)
			return
		}

		// ── Intent Router: fast-path for common actions (no AI needed) ────────
		// Skip intent router if message has contextual references (e.g., "this contact",
		// "for them") or complex parameters — these need AI reasoning to resolve.
		needsContext := needsAIReasoning(req.UserMessage)
		intentName := ""
		if !needsContext {
			intentName = MatchIntent(req.UserMessage)
		}
		if intentName != "" {
			cc.logger.Info("intent_router.matched",
				zap.String("intent", intentName),
				zap.String("message", req.UserMessage),
			)
			result := cc.ExecuteIntent(ctx, intentName, orgID, userID, req.UserRole, req.UserMessage)
			if result != nil {
				for _, ev := range result.Events {
					events <- ev
				}
				events <- CommandEvent{Type: "response", Message: result.Text, Done: true}
				events <- CommandEvent{Type: "done", Done: true}
				cc.persistAssistant(req.SessionID, result.Text)
				// Push structured context to session cache for AI follow-ups
				if result.ContextSummary != "" {
					cc.sessionCtx.Push(req.SessionID, intentName, result.ContextSummary)
				}
				return
			}
		}
		if needsContext {
			cc.logger.Info("intent_router.skipped_for_ai",
				zap.String("message", req.UserMessage),
			)
		}

		// ── Emit thinking event so the user gets real-time feedback ─────────
		events <- CommandEvent{Type: "thinking", Message: "Analyzing your request…"}

		// ── Build role-scoped system prompt ──────────────────────────────────
		sysPrompt := cc.buildRolePrompt(ctx, orgID, req)

		// Inject session context from intent router results (deals, contacts shown earlier)
		if sessionContext := cc.sessionCtx.BuildContextPrompt(req.SessionID); sessionContext != "" {
			sysPrompt += sessionContext
		}


		// ── Build messages array (system + history + new user message) ───────
		messages := []Message{{Role: "system", Content: sysPrompt}}
		for _, h := range req.History {
			messages = append(messages, Message{Role: h.Role, Content: h.Content})
		}
		messages = append(messages, Message{Role: "user", Content: req.UserMessage})

		// ── Get allowed tools for this role (with dynamic custom fields) ────
		contactFields, _ := cc.knowledgeBuilder.settingsUC.GetFieldDefs(ctx, orgID, "contact")
		dealFields, _ := cc.knowledgeBuilder.settingsUC.GetFieldDefs(ctx, orgID, "deal")
		tools := AllowedToolsWithSchema(req.UserRole, contactFields, dealFields)

		// ── First AI call with tools ─────────────────────────────────────────
		events <- CommandEvent{Type: "thinking", Message: "Thinking…"}
		response, err := cc.gateway.CompleteWithTools(ctx, orgID, userID, TaskCommandCenter, messages, tools)
		if err != nil {
			cc.logger.Error("command_center.ai_call_failed", zap.Error(err))
			events <- CommandEvent{Type: "response", Message: "⏳ I'm still processing — please try again in a few seconds.", Done: true}
			events <- CommandEvent{Type: "done", Done: true}
			return
		}
		cc.logger.Info("command_center.tool_select",
			zap.String("org_id", orgID.String()),
			zap.String("role", req.UserRole),
			zap.Int("tools_requested", len(response.ToolCalls)),
			zap.Int("input_tokens", response.InputTokens),
			zap.Int("output_tokens", response.OutputTokens),
			zap.String("stop_reason", response.StopReason),
		)

		// ── No tool calls — pure text response ───────────────────────────────
		if len(response.ToolCalls) == 0 {
			events <- CommandEvent{Type: "response", Message: response.Content, Done: true}
			events <- CommandEvent{Type: "done", Done: true}
			cc.persistAssistant(req.SessionID, response.Content)
			return
		}

		// ── Separate safe reads from writes that need confirmation ────────────
		// create_deal / create_contact / create_object_record → form events (not DB writes, handled below)
		writeTools := map[string]bool{
			"update_deal":             true,
			"create_task":             true,
			"log_activity":            true,
			"create_contact":          true,
			"create_deal":             true,
			"create_object_record":    true,
			"update_object_record":    true,
		}
		var readCalls, writeCalls []ToolCall
		for _, tc := range response.ToolCalls {
			if writeTools[tc.Name] {
				writeCalls = append(writeCalls, tc)
			} else {
				readCalls = append(readCalls, tc)
			}
		}

		// Special: navigate_to — emit navigate event immediately, then an AI text acknowledgment
		for i, tc := range readCalls {
			if tc.Name == "navigate_to" {
				var p map[string]interface{}
				json.Unmarshal(tc.Params, &p)
				navData, _ := json.Marshal(p)
				events <- CommandEvent{Type: "navigate", Data: navData}
				// Emit a short text confirmation so the user sees it before page changes
				label, _ := p["label"].(string)
				if label == "" {
					label, _ = p["path"].(string)
				}
				events <- CommandEvent{Type: "response", Message: fmt.Sprintf("Sure! Taking you to **%s** now. 🔗", label), Done: false}
				// Remove from readCalls so it's not double-executed
				readCalls = append(readCalls[:i], readCalls[i+1:]...)
				break
			}
		}

		// Special: create_contact / create_deal / create_object_record — emit form events immediately (no DB call)
		// Process ALL form tools, not just the first one
		var remainingWrites []ToolCall
		for _, tc := range writeCalls {
			if tc.Name == "create_contact" {
				var p map[string]interface{}
				json.Unmarshal(tc.Params, &p)
				cfMap := extractCustomFieldParams(p)
				formData, _ := json.Marshal(map[string]any{
					"form_type":             "contact",
					"prefill_name":          p["prefill_name"],
					"prefill_email":         p["prefill_email"],
					"prefill_phone":         p["prefill_phone"],
					"prefill_custom_fields": cfMap,
				})
				events <- CommandEvent{Type: "form", Data: formData}
				continue // don't add to remainingWrites
			}
			if tc.Name == "create_deal" {
				var p map[string]interface{}
				json.Unmarshal(tc.Params, &p)
				cfMap := extractCustomFieldParams(p)
				formData, _ := json.Marshal(map[string]any{
					"form_type":             "deal",
					"prefill_title":         p["prefill_title"],
					"prefill_value":         p["prefill_value"],
					"prefill_contact_id":    p["contact_id"],
					"prefill_contact_name":  p["contact_name"],
					"prefill_custom_fields": cfMap,
				})
				events <- CommandEvent{Type: "form", Data: formData}
				continue // don't add to remainingWrites
			}
			if tc.Name == "create_object_record" {
				var p map[string]interface{}
				json.Unmarshal(tc.Params, &p)
				slug, _ := p["object_slug"].(string)
				displayName, _ := p["display_name"].(string)
				fields, _ := p["fields"].(map[string]interface{})
				formData, _ := json.Marshal(map[string]any{
					"form_type":       "custom_object",
					"object_slug":     slug,
					"prefill_display_name": displayName,
					"prefill_fields":  fields,
				})
				events <- CommandEvent{Type: "form", Data: formData}
				continue // don't add to remainingWrites
			}
			remainingWrites = append(remainingWrites, tc)
		}
		writeCalls = remainingWrites

		// Execute read-only tool calls in parallel
		if len(readCalls) > 0 {
			events <- CommandEvent{Type: "thinking", Message: "Searching CRM data…"}
		}
		type toolResult struct {
			name   string
			output json.RawMessage
		}
		readResults := make([]toolResult, len(readCalls))
		var wg sync.WaitGroup
		for i, tc := range readCalls {
			wg.Add(1)
			go func(idx int, call ToolCall) {
				defer wg.Done()
				out := cc.executeTool(ctx, orgID, userID, req.UserRole, call)
				readResults[idx] = toolResult{call.Name, out}
				events <- CommandEvent{Type: "tool_result", Tool: call.Name, Data: truncateToolResult(out)}
			}(i, tc)
		}
		wg.Wait()

		// Push tool results to session context for follow-up memory
		for _, r := range readResults {
			if r.name != "" && len(r.output) > 0 {
				// Store compact version (max 500 chars)
				summary := fmt.Sprintf("AI tool %s returned: %s", r.name, string(r.output))
				if len(summary) > 500 {
					summary = summary[:500] + "..."
				}
				cc.sessionCtx.Push(req.SessionID, "ai_tool_"+r.name, summary)
			}
		}

		// For writes — emit confirm events instead of executing
		for _, tc := range writeCalls {
			summary := cc.describeWrite(tc)
			confirmData, _ := json.Marshal(map[string]any{
				"tool":    tc.Name,
				"args":    tc.Params,
				"summary": summary,
			})
			events <- CommandEvent{Type: "confirm", Data: confirmData}
		}

		// If writes are pending, skip the expensive summary AI call.
		// The confirm banner describes the action; we only need a brief search summary.
		if len(writeCalls) > 0 {
			// Build a lightweight summary from read results if any
			if len(readCalls) > 0 {
				var summaryParts []string
				for _, r := range readResults {
					// Extract key info from tool results
					var parsed map[string]interface{}
					if err := json.Unmarshal(r.output, &parsed); err == nil {
						if count, ok := parsed["count"].(float64); ok {
							objType, _ := parsed["object_type"].(string)
							if objType == "" {
								objType = r.name
							}
							if records, ok := parsed["records"].([]interface{}); ok && int(count) > 0 && len(records) > 0 {
								// Get first record name
								if first, ok := records[0].(map[string]interface{}); ok {
									name := ""
									if n, ok := first["name"].(string); ok {
										name = n
									} else if n, ok := first["display_name"].(string); ok {
										name = n
									}
									if name != "" {
										summaryParts = append(summaryParts, fmt.Sprintf("Found **%s** (%s).", name, objType))
									} else {
										summaryParts = append(summaryParts, fmt.Sprintf("Found %d %s record(s).", int(count), objType))
									}
								}
							}
						}
					}
				}
				if len(summaryParts) > 0 {
					summaryText := strings.Join(summaryParts, " ")
					events <- CommandEvent{Type: "response", Message: summaryText, Done: false}
				}
			}
			events <- CommandEvent{Type: "done", Done: true}
			return
		}

		// No writes — pure read flow. Ask AI to summarize tool results with rich markdown.
		// Build final message list for AI summary
		messages = append(messages, Message{
			Role:      "assistant",
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		})
		for i, tc := range readCalls {
			messages = append(messages, Message{
				Role:      "tool",
				ToolUseID: tc.ID,
				Content:   string(readResults[i].output),
			})
		}

		// CRITICAL MEMORY TRICK: We force the AI to embed the UUID in markdown links.
		// That way, the UUID is saved in the chat history text, allowing the AI to recall it for follow-up actions like "Update the second one"!
		const summaryDirective = "Summarize the results above clearly. Use rich markdown (tables, lists, bold text). IMPORTANT: When listing records (deals, contacts, tasks), you MUST embed their UUID invisibly using markdown empty links, e.g., `[Deal Name](#uuid)`. You must do this so you can remember their IDs for follow-up actions."
		messages = append(messages, Message{Role: "user", Content: summaryDirective})

		events <- CommandEvent{Type: "thinking", Message: "Preparing your answer…"}
		finalResp, err := cc.gateway.Complete(ctx, orgID, userID, TaskCommandCenter, messages)
		if err != nil {
			cc.logger.Error("command_center.summary_failed", zap.Error(err))
			events <- CommandEvent{Type: "done", Done: true}
			return
		}

		cc.logger.Info("command_center.complete",
			zap.String("org_id", orgID.String()),
			zap.String("role", req.UserRole),
			zap.Int("tools_called", len(readCalls)+len(writeCalls)),
			zap.Int("input_tokens", finalResp.InputTokens),
			zap.Int("output_tokens", finalResp.OutputTokens),
			zap.Int("cached_tokens", finalResp.CachedInputTokens),
			zap.String("stop_reason", finalResp.StopReason),
		)

		replyContent := finalResp.Content
		events <- CommandEvent{Type: "response", Message: replyContent, Done: true}
		events <- CommandEvent{Type: "done", Done: true}
		cc.persistAssistant(req.SessionID, replyContent)
	}()

	return events, nil
}

// buildRolePrompt creates a concise, role-scoped system prompt.
func (cc *CommandCenter) buildRolePrompt(ctx context.Context, orgID uuid.UUID, req CommandRequest) string {
	// Try to get company KB for context
	kbSection := ""
	if cc.knowledgeBuilder != nil {
		if p, err := cc.knowledgeBuilder.BuildSystemPrompt(ctx, orgID); err == nil && p != "" {
			kbSection = "\n\n" + p
		}
	}

	var roleInstructions string
	switch req.UserRole {
	case "owner", "admin":
		roleInstructions = `FULL ACCESS: org-wide analytics, all deals, all contacts, team performance.
- Present org-level summaries by default; drill into individuals only when asked.`
	case "manager":
		roleInstructions = `TEAM SCOPE: all deals and contacts in the org for coaching.
- Provide pipeline health, rep performance, stale-deal insights.
- Cannot change roles or access billing/admin settings.`
	case "sales_rep":
		roleInstructions = `OWN RECORDS ONLY: deals and contacts owned by the current user — no one else's.
- If asked for team/org-wide data politely decline: "I can only see your own records."
- For "summarize my work" or "my leads" → call search_deals + search_contacts with OwnerUserID already applied server-side.`
	case "viewer":
		roleInstructions = `READ-ONLY: search and view data only.
- ANY write request → respond: "You have viewer access and cannot make changes."
- Do NOT call: create_task, update_deal, log_activity, create_contact, create_deal.`
	default:
		roleInstructions = `Restricted read-only CRM access.`
	}

	// Multi-workspace context switching hint
	workspaceHint := ""
	if len(req.Workspaces) > 1 {
		var wsNames []string
		for _, w := range req.Workspaces {
			if w.Role != req.UserRole {
				wsNames = append(wsNames, fmt.Sprintf("%s (as %s)", w.OrgName, w.Role))
			}
		}
		if len(wsNames) > 0 {
			workspaceHint = fmt.Sprintf("\nMULTI-WORKSPACE: user also belongs to %s — suggest workspace switcher (top nav) if they ask about a different org.",
				strings.Join(wsNames, ", "))
		}
	}

	today := time.Now().Format("Mon Jan 2, 2006")

	return fmt.Sprintf(`You are a CRM assistant built into Guerrilla CRM. Today: %s. Role: **%s**.
%s%s

CORE RULES (MUST follow every reply):
1. CRM-ONLY: You are strictly a CRM work assistant. You handle deals, contacts, tasks, activities, analytics, emails, and pipeline management. REFUSE any request that is not related to CRM work — including code generation, general knowledge, homework, creative writing, or chitchat. Reply with: "I'm your CRM assistant — I can help with deals, contacts, tasks, and analytics. What would you like to do?"
2. CRM TOOLS: You have secure access to CRM data via tools. Call tools when the user asks for their pipeline, contacts, or metrics.
3. EXECUTE, DON'T REDIRECT: If a task involves CRM data, call the tool directly. NEVER say "navigate to the Deals page" as an alternative to doing it yourself.
4. PROACTIVE: For queries like "filter leads" or "top contacts" — call the tool immediately.
5. CONCISE: Keep responses short and action-oriented. No fluff, no filler paragraphs. Use tables for lists, bullets for single records. Save tokens.
6. WRITE SAFETY: For destructive actions (update_deal, create_task, log_activity), show a confirmation banner first. EXCEPTION: create_contact, create_deal, and create_object_record ALWAYS call the tool directly — the inline form IS the user's confirmation step. NEVER show a text confirmation table for contact/deal/custom object creation. Just call the tool immediately with all extracted data.
7. BULK CREATION: When the user asks to create MULTIPLE records (e.g. "create 3 tickets: X, Y, Z"), you MUST:
   a) Call the create tool ONCE PER RECORD in the SAME response (multiple parallel tool calls).
   b) Extract EVERY detail the user provides for EACH record — name, priority, status, fields — and pass them as tool parameters. DO NOT use generic names like "Ticket 1" when the user specified actual names.
   c) Example: "Create 3 tickets: 1. Server Crash - Critical, 2. Login Bug - High, 3. Typo - Low" → you MUST call create_object_record THREE times with display_name="Server Crash" fields={"priority":"Critical"}, display_name="Login Bug" fields={"priority":"High"}, display_name="Typo" fields={"priority":"Low"}.
   d) Only use sequential names ("Ticket 1", "Ticket 2") if the user gives NO specific details at all.
8. LANGUAGE: Reply in the same language the user writes in.

TOOL USAGE GUIDE:

search_contacts — Search contacts by name, email, or company. Use sort_by="name" for alphabetical, sort_by="created_at" for recent. Limit 5-10 for summaries, up to 15 for full lists. Never fabricate data.

search_deals — Pipeline queries. Use query to find a specific deal by name/title (e.g. query="ABC Noon"). Filter by stage_name, status (active/won/lost), min_value, days_inactive. Use sort_by="value" desc for "biggest deals", sort_by="probability" for "most likely to close". When user asks about a SPECIFIC deal by name, ALWAYS pass the deal name as query. Format as table with Title, Value, Stage, Probability columns.

get_analytics — Aggregated metrics: "revenue", "pipeline", "performance" (managers+), "forecast" (managers+). Sales reps see own data only.

navigate_to — Navigate browser to CRM page. Paths: /deals, /contacts, /tasks, /settings. Use only when user explicitly asks to "go to" a page.

create_task — Create follow-up task. Requires title. Optional: priority (low/medium/high), due_days, deal_id, contact_id.

update_deal — Update deal status/stage/probability/note. Always include deal_title. Status changes auto-set ClosedAt and probability.

log_activity — Log call/email/meeting/note against contact or deal. Requires type and title.

compose_email — Draft email for a contact. Requires contact_id and instruction.

create_contact — Inline form for new contact. You MUST extract and pass ALL available information from the user's message as tool parameters. This includes base fields (prefill_name, prefill_email, prefill_phone) AND any custom field parameters (cf_*). Do NOT show a text table — call the tool immediately.

create_deal — Inline form for new deal. Extract title/value and ALL custom field parameters (cf_*). CRITICAL: If the user references a contact, resolve contact_id and contact_name from SESSION CONTEXT. Do NOT show a text table — call the tool immediately.

search_objects — Universal object search. Works for ANY object type (base or custom). Pass the object_slug from the CRM SCHEMA section. Use query to filter by name. Example: search_objects(object_slug="ticket", query="billing issue"). ALWAYS check the CRM SCHEMA to find the correct slug.

create_object_record — Inline form for new custom object records. Works like create_contact / create_deal: call the tool immediately with all extracted data. The inline form IS the user's confirmation step. Pass object_slug, display_name, and fields (key-value pairs matching the schema). NEVER show a text confirmation table — call the tool immediately. Example: create_object_record(object_slug="ticket", display_name="Billing Issue #123", fields={"subject": "Cannot process payment", "priority": "high"}).

update_object_record — Update ANY record (contact, deal, or custom object). REQUIRES the record's UUID.
  If the user references a record by name, FIRST call search_objects to find its ID. Once you have the UUID, IMMEDIATELY call update_object_record — do NOT ask the user for confirmation in text. The system shows a confirmation banner automatically.
  For contacts: display_name splits into first/last name, fields supports first_name, last_name, email, phone.
  For custom objects: pass display_name and/or fields to change.

SESSION CONTEXT AWARENESS:
- You have access to session context showing records previously created or viewed in this conversation.
- When the user says "this contact", "that deal", "for them" — look in the session context to find the referenced record and use its UUID.
- When creating a deal linked to a contact, always pass both contact_id and contact_name.
- When the user mentions a name from the conversation history, find its UUID from the session context.

FORMATTING:
- Tables for multi-record results. Bullets for single records. One sentence for confirmations.
- Embed UUIDs as [Title](#uuid) for follow-up reference.%s`,
		today, req.UserRole, roleInstructions, workspaceHint, kbSection)
}

// describeWrite returns a human-readable confirm summary for the banner.
func (cc *CommandCenter) describeWrite(tc ToolCall) string {
	var p map[string]interface{}
	json.Unmarshal(tc.Params, &p)
	switch tc.Name {
	case "update_deal":
		title := ""
		if t, ok := p["deal_title"].(string); ok {
			title = " **" + t + "**"
		}
		if status, ok := p["status"].(string); ok {
			switch status {
			case "won":
				return fmt.Sprintf("Mark deal%s as **Won** ✅", title)
			case "lost":
				return fmt.Sprintf("Mark deal%s as **Lost** ❌", title)
			case "active":
				return fmt.Sprintf("Reopen deal%s as active", title)
			}
		}
		if stage, ok := p["stage_name"].(string); ok && stage != "" {
			return fmt.Sprintf("Move deal%s to stage **%s**", title, stage)
		}
		if prob, ok := p["probability"].(float64); ok {
			return fmt.Sprintf("Update deal%s probability to **%d%%**", title, int(prob))
		}
		return fmt.Sprintf("Update deal%s", title)
	case "create_task":
		return fmt.Sprintf("Create task: **%v**", p["title"])
	case "log_activity":
		return fmt.Sprintf("Log %v activity: **%v**", p["type"], p["title"])
	case "create_contact":
		return fmt.Sprintf("Create new contact: **%v**", p["prefill_name"])
	case "create_object_record":
		slug, _ := p["object_slug"].(string)
		name, _ := p["display_name"].(string)
		if name == "" {
			name = "new record"
		}
		return fmt.Sprintf("Create %s: **%s**", slug, name)
	case "update_object_record":
		slug, _ := p["object_slug"].(string)
		// Build a human-readable description of what's changing
		var changes []string
		if newName, ok := p["display_name"].(string); ok && newName != "" {
			changes = append(changes, fmt.Sprintf("name → **%s**", newName))
		}
		if fields, ok := p["fields"].(map[string]interface{}); ok {
			for k, v := range fields {
				changes = append(changes, fmt.Sprintf("%s → **%v**", k, v))
			}
		}
		if len(changes) > 0 {
			return fmt.Sprintf("Update %s — %s", slug, strings.Join(changes, ", "))
		}
		return fmt.Sprintf("Update %s record", slug)
	}
	return tc.Name
}

// persistAssistant appends the assistant reply to the DB in the background.
func (cc *CommandCenter) persistAssistant(sessionID uuid.UUID, content string) {
	if cc.sessionRepo == nil || sessionID == uuid.Nil {
		return
	}
	go cc.sessionRepo.AppendMessage(context.Background(), &domain.ChatMessage{
		SessionID: sessionID,
		Role:      "assistant",
		Content:   content,
	})
}

// truncateToolResult caps tool result JSON at 2KB for token efficiency.
func truncateToolResult(raw json.RawMessage) json.RawMessage {
	const maxBytes = 2048
	if len(raw) <= maxBytes {
		return raw
	}
	return json.RawMessage(fmt.Sprintf(`{"truncated":true,"preview":%q}`, string(raw[:maxBytes])))
}

// executeTool runs a single tool call with role-aware scoping.
// role is passed so reads for sales_rep are automatically narrowed to userID.
func (cc *CommandCenter) executeTool(ctx context.Context, orgID, userID uuid.UUID, role string, call ToolCall) json.RawMessage {
	var params map[string]interface{}
	json.Unmarshal(call.Params, &params)

	switch call.Name {
	case "search_contacts":
		return cc.toolSearchContacts(ctx, orgID, userID, role, params)
	case "search_deals":
		return cc.toolSearchDeals(ctx, orgID, userID, role, params)
	case "get_analytics":
		return cc.toolGetAnalytics(ctx, orgID, userID, role, params)
	case "create_task":
		return cc.toolCreateTask(ctx, orgID, userID, params)
	case "compose_email":
		return cc.toolComposeEmail(params)
	case "update_deal":
		return cc.toolUpdateDeal(ctx, orgID, userID, role, params)
	case "log_activity":
		return cc.toolLogActivity(ctx, orgID, userID, params)
	case "navigate_to":
		out, _ := json.Marshal(map[string]any{"navigated": true})
		return out
	case "create_contact":
		// Form tools — return prefill data for the frontend to render the form
		out, _ := json.Marshal(map[string]any{"form_type": "contact", "prefill_name": params["prefill_name"], "prefill_email": params["prefill_email"]})
		return out
	case "create_deal":
		out, _ := json.Marshal(map[string]any{"form_type": "deal", "prefill_title": params["prefill_title"], "prefill_value": params["prefill_value"]})
		return out
	case "search_objects":
		return cc.toolSearchObjects(ctx, orgID, params)
	case "create_object_record":
		return cc.toolCreateObjectRecord(ctx, orgID, userID, params)
	case "update_object_record":
		return cc.toolUpdateObjectRecord(ctx, orgID, params)
	default:
		out, _ := json.Marshal(map[string]any{"error": "unknown tool: " + call.Name})
		return out
	}
}

// ─── Tool implementations ─────────────────────────────────────────────────────

// toolSearchContacts: sales_rep sees only their own contacts; others see all.
func (cc *CommandCenter) toolSearchContacts(ctx context.Context, orgID, userID uuid.UUID, role string, params map[string]interface{}) json.RawMessage {
	query, _ := params["query"].(string)
	limit := 10
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 15 {
			limit = 15
		}
	}
	sortBy, _ := params["sort_by"].(string)
	sortOrder, _ := params["sort_order"].(string)

	filter := domain.ContactFilter{
		Q:         query,
		Limit:     limit,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	if role == "sales_rep" {
		filter.OwnerUserID = &userID
	}

	contacts, _, err := cc.contactRepo.List(ctx, orgID, filter)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	// Empty state: give the AI a human-readable message to relay
	if len(contacts) == 0 {
		msg := "You currently have no contacts assigned to you."
		if role != "sales_rep" {
			msg = "No contacts found matching your search criteria."
			if query == "" {
				msg = "There are no contacts in the system yet."
			}
		}
		out, _ := json.Marshal(map[string]any{"count": 0, "contacts": []any{}, "empty_message": msg})
		return out
	}

	simplified := make([]map[string]interface{}, len(contacts))
	for i, c := range contacts {
		m := map[string]interface{}{
			"id": c.ID, "name": strings.TrimSpace(c.FirstName + " " + c.LastName),
		}
		if c.Email != nil {
			m["email"] = *c.Email
		}
		if c.Phone != nil {
			m["phone"] = *c.Phone
		}
		if c.Company != nil {
			m["company"] = c.Company.Name
		}
		simplified[i] = m
	}
	out, _ := json.Marshal(map[string]any{"count": len(contacts), "contacts": simplified})
	return out
}

// toolSearchDeals: sales_rep sees only their own deals; others see all org deals.
func (cc *CommandCenter) toolSearchDeals(ctx context.Context, orgID, userID uuid.UUID, role string, params map[string]interface{}) json.RawMessage {
	query, _ := params["query"].(string)
	limit := 10
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 15 {
			limit = 15
		}
	}

	// Read sort params — AI will set sort_by="value" for "top N largest deals"
	sortBy, _ := params["sort_by"].(string)
	sortOrder, _ := params["sort_order"].(string)

	filter := domain.DealFilter{
		Q:         query,
		Limit:     limit,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}
	if role == "sales_rep" {
		filter.OwnerUserID = &userID
	}

	deals, _, err := cc.dealRepo.List(ctx, orgID, filter)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	// Empty state: give the AI a human-readable message to relay
	if len(deals) == 0 {
		msg := "You currently have no deals assigned to you."
		if role != "sales_rep" {
			msg = "No deals found in the pipeline matching your criteria."
		}
		out, _ := json.Marshal(map[string]any{"count": 0, "deals": []any{}, "empty_message": msg})
		return out
	}

	simplified := make([]map[string]interface{}, len(deals))
	for i, d := range deals {
		m := map[string]interface{}{
			"id":          d.ID,
			"title":       d.Title,
			"value":       d.Value,
			"probability": d.Probability,
			"is_won":      d.IsWon,
			"is_lost":     d.IsLost,
		}
		if d.Stage != nil {
			m["stage"] = d.Stage.Name
		}
		if d.Contact != nil {
			m["contact"] = strings.TrimSpace(d.Contact.FirstName + " " + d.Contact.LastName)
		}
		if d.Owner != nil {
			m["owner"] = strings.TrimSpace(d.Owner.FirstName + " " + d.Owner.LastName)
		}
		simplified[i] = m
	}
	out, _ := json.Marshal(map[string]any{"count": len(deals), "sorted_by": sortBy, "deals": simplified})
	return out
}

func (cc *CommandCenter) toolCreateTask(ctx context.Context, orgID, userID uuid.UUID, params map[string]interface{}) json.RawMessage {
	title, _ := params["title"].(string)
	priority := "medium"
	if p, ok := params["priority"].(string); ok {
		priority = p
	}
	dueDays := 1
	if d, ok := params["due_days"].(float64); ok {
		dueDays = int(d)
	}

	dueAt := time.Now().AddDate(0, 0, dueDays)
	task := &domain.Task{
		OrgID:      orgID,
		Title:      title,
		Priority:   priority,
		DueAt:      &dueAt,
		AssignedTo: &userID,
	}

	if dealStr, ok := params["deal_id"].(string); ok {
		if id, err := uuid.Parse(dealStr); err == nil {
			task.DealID = &id
		}
	}
	if contactStr, ok := params["contact_id"].(string); ok {
		if id, err := uuid.Parse(contactStr); err == nil {
			task.ContactID = &id
		}
	}

	if err := cc.taskRepo.Create(ctx, task); err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{
		"created": true, "task_id": task.ID, "title": task.Title,
		"due_at": dueAt.Format("2006-01-02"),
	})
	return out
}

func (cc *CommandCenter) toolComposeEmail(_ map[string]interface{}) json.RawMessage {
	out, _ := json.Marshal(map[string]any{"status": "ready_for_composition"})
	return out
}

// toolUpdateDeal: handles status (won/lost/active), stage, probability, and notes.
// Verifies ownership if role is sales_rep.
func (cc *CommandCenter) toolUpdateDeal(ctx context.Context, orgID, userID uuid.UUID, role string, params map[string]interface{}) json.RawMessage {
	dealStr, _ := params["deal_id"].(string)
	dealID, err := uuid.Parse(dealStr)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": "invalid deal_id"})
		return out
	}

	deal, err := cc.dealRepo.GetByID(ctx, orgID, dealID)
	if err != nil || deal == nil {
		out, _ := json.Marshal(map[string]any{"error": "deal not found"})
		return out
	}

	// ← RBAC guard: sales_rep can only edit their own deals
	if role == "sales_rep" && deal.OwnerUserID != nil && *deal.OwnerUserID != userID {
		out, _ := json.Marshal(map[string]any{"error": "access denied: you can only update deals you own"})
		return out
	}

	// Apply status change (won / lost / active)
	if status, ok := params["status"].(string); ok {
		now := time.Now()
		switch strings.ToLower(status) {
		case "won":
			deal.IsWon = true
			deal.IsLost = false
			deal.ClosedAt = &now
			deal.Probability = 100
		case "lost":
			deal.IsLost = true
			deal.IsWon = false
			deal.ClosedAt = &now
			deal.Probability = 0
		case "active":
			deal.IsWon = false
			deal.IsLost = false
			deal.ClosedAt = nil
		}
	}

	// Apply probability
	if prob, ok := params["probability"].(float64); ok {
		deal.Probability = int(prob)
	}

	if err := cc.dealRepo.Update(ctx, deal); err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	// Log optional note as activity
	if note, ok := params["note"].(string); ok && note != "" {
		title := "AI Assistant: Deal Update"
		_ = cc.activityRepo.Create(ctx, &domain.Activity{
			OrgID:      orgID,
			Type:       "note",
			DealID:     &dealID,
			UserID:     &userID,
			Title:      &title,
			Body:       &note,
			OccurredAt: time.Now(),
		})
	}

	out, _ := json.Marshal(map[string]any{
		"updated":  true,
		"deal_id":  dealID,
		"title":    deal.Title,
		"is_won":   deal.IsWon,
		"is_lost":  deal.IsLost,
	})
	return out
}

func (cc *CommandCenter) toolLogActivity(ctx context.Context, orgID, userID uuid.UUID, params map[string]interface{}) json.RawMessage {
	actType, _ := params["type"].(string)
	title, _ := params["title"].(string)

	activity := &domain.Activity{
		OrgID:      orgID,
		Type:       actType,
		UserID:     &userID,
		Title:      &title,
		OccurredAt: time.Now(),
	}

	if body, ok := params["body"].(string); ok {
		activity.Body = &body
	}
	if dealStr, ok := params["deal_id"].(string); ok {
		if id, err := uuid.Parse(dealStr); err == nil {
			activity.DealID = &id
		}
	}
	if contactStr, ok := params["contact_id"].(string); ok {
		if id, err := uuid.Parse(contactStr); err == nil {
			activity.ContactID = &id
		}
	}

	if err := cc.activityRepo.Create(ctx, activity); err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{"logged": true, "type": actType, "title": title})
	return out
}

// toolGetAnalytics: viewer gets read of pipeline; sales_rep only sees their own.
func (cc *CommandCenter) toolGetAnalytics(ctx context.Context, orgID, userID uuid.UUID, role string, params map[string]interface{}) json.RawMessage {
	metric, _ := params["metric"].(string)

	switch metric {
	case "pipeline", "revenue":
		filter := domain.DealFilter{Limit: 200}
		if role == "sales_rep" {
			filter.OwnerUserID = &userID // only their pipeline
		}
		deals, _, _ := cc.dealRepo.List(ctx, orgID, filter)
		var totalValue float64
		activeCount, wonCount := 0, 0
		for _, d := range deals {
			totalValue += d.Value
			if !d.IsWon && !d.IsLost {
				activeCount++
			}
			if d.IsWon {
				wonCount++
			}
		}
		out, _ := json.Marshal(map[string]any{
			"metric":       metric,
			"scope":        scopeLabel(role, "deals"),
			"total_value":  totalValue,
			"active_deals": activeCount,
			"won_deals":    wonCount,
			"total_deals":  len(deals),
		})
		return out

	case "forecast":
		if role == "viewer" || role == "sales_rep" {
			// Forecast is org-wide — restrict for these roles
			out, _ := json.Marshal(map[string]any{"error": "forecast analytics requires manager or above access"})
			return out
		}
		rows, _ := cc.dealRepo.Forecast(ctx, orgID)
		out, _ := json.Marshal(map[string]any{"metric": "forecast", "forecast": rows})
		return out

	default:
		out, _ := json.Marshal(map[string]any{"metric": metric, "message": "metric not yet implemented"})
		return out
	}
}

// scopeLabel returns a text description of what data scope was applied.
func scopeLabel(role, entity string) string {
	if role == "sales_rep" {
		return "your own " + entity
	}
	return "org-wide " + entity
}

// extractCustomFieldParams collects any "cf_*" prefixed parameters from an AI
// tool call response and returns them as a map[string]any keyed by the original
// field key (e.g., "cf_industry" → "industry": value).
func extractCustomFieldParams(params map[string]interface{}) map[string]any {
	cfMap := make(map[string]any)
	for k, v := range params {
		if len(k) > 3 && k[:3] == "cf_" && v != nil {
			cfMap[k[3:]] = v // strip "cf_" prefix → original field key
		}
	}
	if len(cfMap) == 0 {
		return nil
	}
	return cfMap
}

// ─── Custom Object Tools ─────────────────────────────────────────────────────

// toolSearchObjects queries any custom object by its slug.
func (cc *CommandCenter) toolSearchObjects(ctx context.Context, orgID uuid.UUID, params map[string]interface{}) json.RawMessage {
	slug, _ := params["object_slug"].(string)
	if slug == "" {
		out, _ := json.Marshal(map[string]any{"error": "object_slug is required"})
		return out
	}

	query, _ := params["query"].(string)
	limit := 10
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 20 {
			limit = 20
		}
	}

	// For base objects, delegate to existing tools
	if slug == "contact" || slug == "contacts" {
		return cc.toolSearchContacts(ctx, orgID, uuid.Nil, "owner", map[string]interface{}{"query": query, "limit": float64(limit)})
	}
	if slug == "deal" || slug == "deals" {
		return cc.toolSearchDeals(ctx, orgID, uuid.Nil, "owner", map[string]interface{}{"query": query, "limit": float64(limit)})
	}

	// Custom object search
	if cc.customObjUC == nil {
		out, _ := json.Marshal(map[string]any{"error": "custom objects not configured"})
		return out
	}

	records, total, err := cc.customObjUC.ListRecords(ctx, orgID, slug, domain.RecordFilter{
		Q:     query,
		Limit: limit,
	})
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": "Object type '" + slug + "' not found or query failed"})
		return out
	}

	if len(records) == 0 {
		out, _ := json.Marshal(map[string]any{"count": 0, "object_type": slug, "records": []any{}, "empty_message": "No " + slug + " records found matching your criteria."})
		return out
	}

	simplified := make([]map[string]interface{}, len(records))
	for i, r := range records {
		m := map[string]interface{}{
			"id":           r.ID,
			"display_name": r.DisplayName,
			"created_at":   r.CreatedAt.Format("2006-01-02"),
		}
		// Parse JSONB data fields into the result
		var data map[string]interface{}
		if err := json.Unmarshal(r.Data, &data); err == nil {
			for k, v := range data {
				m[k] = v
			}
		}
		simplified[i] = m
	}
	out, _ := json.Marshal(map[string]any{"count": len(records), "total": total, "object_type": slug, "records": simplified})
	return out
}

// toolCreateObjectRecord creates a record for a custom object.
func (cc *CommandCenter) toolCreateObjectRecord(ctx context.Context, orgID, userID uuid.UUID, params map[string]interface{}) json.RawMessage {
	slug, _ := params["object_slug"].(string)
	if slug == "" {
		out, _ := json.Marshal(map[string]any{"error": "object_slug is required"})
		return out
	}
	displayName, _ := params["display_name"].(string)
	if displayName == "" {
		out, _ := json.Marshal(map[string]any{"error": "display_name is required"})
		return out
	}

	if cc.customObjUC == nil {
		out, _ := json.Marshal(map[string]any{"error": "custom objects not configured"})
		return out
	}

	// Build fields map from params, include display_name as a data field too
	fields := make(map[string]interface{})
	if f, ok := params["fields"].(map[string]interface{}); ok {
		fields = f
	}

	// Marshal fields map into JSON for the Data field
	dataJSON, err := json.Marshal(fields)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": "invalid fields data"})
		return out
	}

	record, err := cc.customObjUC.CreateRecord(ctx, orgID, userID, slug, domain.CreateRecordInput{
		Data: dataJSON,
	})
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": "Failed to create " + slug + " record: " + err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{
		"success":      true,
		"id":           record.ID,
		"display_name": record.DisplayName,
		"object_type":  slug,
		"message":      slug + " record '" + displayName + "' created successfully",
	})
	return out
}

// toolUpdateObjectRecord updates an existing custom object record.
// For base types (contact, deal), it delegates to the appropriate repository.
func (cc *CommandCenter) toolUpdateObjectRecord(ctx context.Context, orgID uuid.UUID, params map[string]interface{}) json.RawMessage {
	slug, _ := params["object_slug"].(string)
	if slug == "" {
		out, _ := json.Marshal(map[string]any{"error": "object_slug is required"})
		return out
	}
	recordIDStr, _ := params["record_id"].(string)
	if recordIDStr == "" {
		out, _ := json.Marshal(map[string]any{"error": "record_id is required"})
		return out
	}
	recordID, err := uuid.Parse(recordIDStr)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": "invalid record_id: " + recordIDStr})
		return out
	}

	// ── Route base types to their native repositories ─────────────────────
	if slug == "contact" || slug == "contacts" {
		return cc.toolUpdateContact(ctx, orgID, recordID, params)
	}
	if slug == "deal" || slug == "deals" {
		// Reuse existing update_deal tool by mapping params
		params["deal_id"] = recordIDStr
		if newName, ok := params["display_name"].(string); ok && newName != "" {
			params["deal_title"] = newName
		}
		return cc.toolUpdateDeal(ctx, orgID, uuid.Nil, "owner", params)
	}

	// ── Custom object update ──────────────────────────────────────────────
	if cc.customObjUC == nil {
		out, _ := json.Marshal(map[string]any{"error": "custom objects not configured"})
		return out
	}

	input := domain.UpdateRecordInput{}
	if newName, ok := params["display_name"].(string); ok && newName != "" {
		input.DisplayName = &newName
	}
	if fields, ok := params["fields"].(map[string]interface{}); ok && len(fields) > 0 {
		dataJSON, err := json.Marshal(fields)
		if err != nil {
			out, _ := json.Marshal(map[string]any{"error": "invalid fields data"})
			return out
		}
		input.Data = dataJSON
	}

	record, err := cc.customObjUC.UpdateRecord(ctx, orgID, slug, recordID, input)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": "Failed to update " + slug + " record: " + err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{
		"success":      true,
		"id":           record.ID,
		"display_name": record.DisplayName,
		"object_type":  slug,
		"message":      slug + " record updated successfully",
	})
	return out
}

// toolUpdateContact handles contact updates via the unified update_object_record tool.
func (cc *CommandCenter) toolUpdateContact(ctx context.Context, orgID, contactID uuid.UUID, params map[string]interface{}) json.RawMessage {
	contact, err := cc.contactRepo.GetByID(ctx, orgID, contactID)
	if err != nil || contact == nil {
		out, _ := json.Marshal(map[string]any{"error": "contact not found"})
		return out
	}

	// Map display_name → first/last name
	if newName, ok := params["display_name"].(string); ok && newName != "" {
		parts := strings.SplitN(newName, " ", 2)
		contact.FirstName = parts[0]
		if len(parts) > 1 {
			contact.LastName = parts[1]
		} else {
			contact.LastName = ""
		}
	}

	// Map individual fields from the "fields" parameter
	if fields, ok := params["fields"].(map[string]interface{}); ok {
		if v, ok := fields["first_name"].(string); ok {
			contact.FirstName = v
		}
		if v, ok := fields["last_name"].(string); ok {
			contact.LastName = v
		}
		if v, ok := fields["email"].(string); ok {
			contact.Email = &v
		}
		if v, ok := fields["phone"].(string); ok {
			contact.Phone = &v
		}
		// Collect remaining fields as custom_fields
		cfMap := make(map[string]interface{})
		knownKeys := map[string]bool{"first_name": true, "last_name": true, "email": true, "phone": true}
		for k, v := range fields {
			if !knownKeys[k] {
				cfMap[k] = v
			}
		}
		if len(cfMap) > 0 {
			cfJSON, _ := json.Marshal(cfMap)
			contact.CustomFields = cfJSON
		}
	}

	if err := cc.contactRepo.Update(ctx, contact); err != nil {
		out, _ := json.Marshal(map[string]any{"error": "Failed to update contact: " + err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{
		"success":    true,
		"id":         contact.ID,
		"name":       strings.TrimSpace(contact.FirstName + " " + contact.LastName),
		"object_type": "contact",
		"message":    "Contact updated successfully",
	})
	return out
}
