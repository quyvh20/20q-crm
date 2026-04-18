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
			events <- CommandEvent{Type: "thinking", Message: "Executing confirmed action..."}
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

		// ── Build role-scoped system prompt ──────────────────────────────────
		sysPrompt := cc.buildRolePrompt(ctx, orgID, req)

		events <- CommandEvent{Type: "thinking", Message: "Analyzing your request..."}

		// ── Build messages array (system + history + new user message) ───────
		messages := []Message{{Role: "system", Content: sysPrompt}}
		for _, h := range req.History {
			messages = append(messages, Message{Role: h.Role, Content: h.Content})
		}
		messages = append(messages, Message{Role: "user", Content: req.UserMessage})

		// ── Get allowed tools for this role ──────────────────────────────────
		tools := AllowedTools(req.UserRole)

		// ── First AI call with tools ─────────────────────────────────────────
		response, err := cc.gateway.CompleteWithTools(ctx, orgID, userID, TaskCommandCenter, messages, tools)
		if err != nil {
			events <- CommandEvent{Type: "error", Message: fmt.Sprintf("AI error: %v", err)}
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
		// create_deal / create_contact → form events (not DB writes, handled below)
		writeTools := map[string]bool{
			"update_deal":    true,
			"create_task":    true,
			"log_activity":   true,
			"create_contact": true,
			"create_deal":    true,
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

		// Special: create_contact / create_deal — emit form event immediately (no DB call)
		for i, tc := range writeCalls {
			if tc.Name == "create_contact" {
				var p map[string]interface{}
				json.Unmarshal(tc.Params, &p)
				formData, _ := json.Marshal(map[string]any{
					"form_type":     "contact",
					"prefill_name":  p["prefill_name"],
					"prefill_email": p["prefill_email"],
				})
				events <- CommandEvent{Type: "form", Data: formData}
				writeCalls = append(writeCalls[:i], writeCalls[i+1:]...)
				break
			}
			if tc.Name == "create_deal" {
				var p map[string]interface{}
				json.Unmarshal(tc.Params, &p)
				formData, _ := json.Marshal(map[string]any{
					"form_type":     "deal",
					"prefill_title": p["prefill_title"],
					"prefill_value": p["prefill_value"],
				})
				events <- CommandEvent{Type: "form", Data: formData}
				writeCalls = append(writeCalls[:i], writeCalls[i+1:]...)
				break
			}
		}

		// Execute read-only tool calls in parallel
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

		// Ask AI to summarize tool results — use rich markdown but keep confirmations brief
		const summaryDirective = "Summarize the results above clearly. Use rich markdown (tables, lists, bold text) if helpful. Provide insights, code, or analysis if the user asked."
		const confirmDirective = "Acknowledge what you found and state what action is pending user confirmation. Keep this confirmation concise."
		summaryContent := summaryDirective
		if len(writeCalls) > 0 {
			summaryContent = confirmDirective
		}
		// Only add the summary user message when there are actual read results to summarize
		if len(readCalls) > 0 || len(writeCalls) > 0 {
			messages = append(messages, Message{Role: "user", Content: summaryContent})
		}

		finalResp, err := cc.gateway.Complete(ctx, orgID, userID, TaskCommandCenter, messages)
		if err != nil {
			events <- CommandEvent{Type: "error", Message: fmt.Sprintf("AI error: %v", err)}
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

	return fmt.Sprintf(`You are a highly capable AI assistant integrated directly into the CRM. Today: %s. Role: **%s**.
%s%s

CORE RULES (MUST follow every reply):
1. GENERAL CAPABILITY: You can answer general knowledge questions, write code, draft text, teach concepts, or provide advice. You are not limited to CRM topics.
2. CRM TOOLS: You have secure access to CRM data via tools. Call tools when the user asks for their pipeline, contacts, or metrics.
3. EXECUTE, DON'T REDIRECT: If a task involves CRM data, call the tool directly. NEVER say "navigate to the Deals page" as an alternative to doing it yourself.
4. PROACTIVE: For queries like "filter leads" or "top contacts" — call the tool immediately.
5. FORMATTING: Use rich markdown everywhere. Use code blocks with language tags, format data in tables (especially tool results), and use bold/italics for readability. Do not output raw JSON text to the user.
6. WRITE SAFETY: Never execute create/update/delete without user confirmation. Show confirmation banner first.
7. LANGUAGE: Reply in the same language the user writes in.%s`,
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

