package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// CommandEvent represents a single SSE event from the command center.
type CommandEvent struct {
	Type    string          `json:"type"`              // thinking | planning | tool_result | response | error | done
	Message string          `json:"message,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Done    bool            `json:"done,omitempty"`
}

// CommandCenter orchestrates agentic AI with tool calling.
type CommandCenter struct {
	gateway          *AIGateway
	knowledgeBuilder *KnowledgeBuilder
	contactRepo      domain.ContactRepository
	dealRepo         domain.DealRepository
	taskRepo         domain.TaskRepository
	activityRepo     domain.ActivityRepository
	logger           *zap.Logger
}

func NewCommandCenter(
	gateway *AIGateway,
	knowledgeBuilder *KnowledgeBuilder,
	contactRepo domain.ContactRepository,
	dealRepo domain.DealRepository,
	taskRepo domain.TaskRepository,
	activityRepo domain.ActivityRepository,
	logger *zap.Logger,
) *CommandCenter {
	return &CommandCenter{
		gateway:          gateway,
		knowledgeBuilder: knowledgeBuilder,
		contactRepo:      contactRepo,
		dealRepo:         dealRepo,
		taskRepo:         taskRepo,
		activityRepo:     activityRepo,
		logger:           logger,
	}
}

// Execute processes a user command and returns a channel of events.
func (cc *CommandCenter) Execute(
	ctx context.Context,
	orgID, userID uuid.UUID,
	userMessage string,
) (<-chan CommandEvent, error) {
	events := make(chan CommandEvent, 100)

	go func() {
		defer close(events)

		// 1. Build system prompt from knowledge base
		sysPrompt := "You are an AI CRM assistant."
		if cc.knowledgeBuilder != nil {
			if p, err := cc.knowledgeBuilder.BuildSystemPrompt(ctx, orgID); err == nil && p != "" {
				sysPrompt = p
			}
		}

		sysPrompt += "\n\nYou have access to CRM tools. When the user asks you to find data, create records, or take actions, use the appropriate tools. Always explain what you're doing."

		events <- CommandEvent{Type: "thinking", Message: "Analyzing your request..."}

		messages := []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userMessage},
		}

		// 2. Call AI with tools
		response, err := cc.gateway.CompleteWithTools(ctx, orgID, userID, TaskCommandCenter, messages, CRMTools)
		if err != nil {
			events <- CommandEvent{Type: "error", Message: fmt.Sprintf("AI error: %v", err)}
			events <- CommandEvent{Type: "done", Done: true}
			return
		}

		// 3. If no tool calls, just return the response
		if len(response.ToolCalls) == 0 {
			events <- CommandEvent{Type: "response", Message: response.Content, Done: true}
			events <- CommandEvent{Type: "done", Done: true}
			return
		}

		// 4. Execute tools in parallel
		events <- CommandEvent{
			Type:    "planning",
			Message: fmt.Sprintf("Running %d actions...", len(response.ToolCalls)),
		}

		type toolResult struct {
			index  int
			name   string
			output json.RawMessage
		}
		results := make([]toolResult, len(response.ToolCalls))
		var wg sync.WaitGroup

		for i, call := range response.ToolCalls {
			wg.Add(1)
			go func(idx int, tc ToolCall) {
				defer wg.Done()
				out := cc.executeTool(ctx, orgID, userID, tc)
				results[idx] = toolResult{idx, tc.Name, out}
				events <- CommandEvent{
					Type: "tool_result",
					Tool: tc.Name,
					Data: out,
				}
			}(i, call)
		}
		wg.Wait()

		// 5. Send tool results back to AI for final response
		messages = append(messages, Message{
			Role:      "assistant",
			Content:   response.Content,
			ToolCalls: response.ToolCalls,
		})

		for i, tc := range response.ToolCalls {
			messages = append(messages, Message{
				Role:      "tool",
				ToolUseID: tc.ID,
				Content:   string(results[i].output),
			})
		}

		finalResp, err := cc.gateway.CompleteWithTools(ctx, orgID, userID, TaskCommandCenter, messages, CRMTools)
		if err != nil {
			events <- CommandEvent{Type: "error", Message: fmt.Sprintf("AI error: %v", err)}
			events <- CommandEvent{Type: "done", Done: true}
			return
		}

		events <- CommandEvent{Type: "response", Message: finalResp.Content, Done: true}
		events <- CommandEvent{Type: "done", Done: true}
	}()

	return events, nil
}

// executeTool dispatches a single tool call to the appropriate handler.
func (cc *CommandCenter) executeTool(ctx context.Context, orgID, userID uuid.UUID, call ToolCall) json.RawMessage {
	var params map[string]interface{}
	json.Unmarshal(call.Params, &params)

	switch call.Name {
	case "search_contacts":
		return cc.toolSearchContacts(ctx, orgID, params)
	case "search_deals":
		return cc.toolSearchDeals(ctx, orgID, params)
	case "create_task":
		return cc.toolCreateTask(ctx, orgID, userID, params)
	case "compose_email":
		return cc.toolComposeEmail(ctx, orgID, params)
	case "update_deal":
		return cc.toolUpdateDeal(ctx, orgID, userID, params)
	case "log_activity":
		return cc.toolLogActivity(ctx, orgID, userID, params)
	case "get_analytics":
		return cc.toolGetAnalytics(ctx, orgID, params)
	default:
		out, _ := json.Marshal(map[string]any{"error": "unknown tool: " + call.Name})
		return out
	}
}

func (cc *CommandCenter) toolSearchContacts(ctx context.Context, orgID uuid.UUID, params map[string]interface{}) json.RawMessage {
	query, _ := params["query"].(string)
	limit := 10
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
		if limit > 50 {
			limit = 50
		}
	}

	contacts, _, err := cc.contactRepo.List(ctx, orgID, domain.ContactFilter{Q: query, Limit: limit})
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	// Return simplified contact data
	simplified := make([]map[string]interface{}, len(contacts))
	for i, c := range contacts {
		m := map[string]interface{}{
			"id": c.ID, "name": c.FirstName + " " + c.LastName,
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

func (cc *CommandCenter) toolSearchDeals(ctx context.Context, orgID uuid.UUID, params map[string]interface{}) json.RawMessage {
	limit := 10
	if l, ok := params["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	deals, _, err := cc.dealRepo.List(ctx, orgID, domain.DealFilter{Limit: limit})
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	simplified := make([]map[string]interface{}, len(deals))
	for i, d := range deals {
		m := map[string]interface{}{
			"id": d.ID, "title": d.Title, "value": d.Value,
			"probability": d.Probability, "created_at": d.CreatedAt,
		}
		if d.Stage != nil {
			m["stage"] = d.Stage.Name
		}
		if d.Contact != nil {
			m["contact"] = d.Contact.FirstName + " " + d.Contact.LastName
		}
		simplified[i] = m
	}
	out, _ := json.Marshal(map[string]any{"count": len(deals), "deals": simplified})
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

	err := cc.taskRepo.Create(ctx, task)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{
		"created": true, "task_id": task.ID, "title": task.Title,
		"due_at": dueAt.Format("2006-01-02"),
	})
	return out
}

func (cc *CommandCenter) toolComposeEmail(_ context.Context, _ uuid.UUID, params map[string]interface{}) json.RawMessage {
	instruction, _ := params["instruction"].(string)
	tone := "professional"
	if t, ok := params["tone"].(string); ok {
		tone = t
	}

	// Return the instruction back — the final AI response will compose the actual email
	out, _ := json.Marshal(map[string]any{
		"instruction": instruction,
		"tone":        tone,
		"status":      "ready_for_composition",
	})
	return out
}

func (cc *CommandCenter) toolUpdateDeal(ctx context.Context, orgID, userID uuid.UUID, params map[string]interface{}) json.RawMessage {
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

	if prob, ok := params["probability"].(float64); ok {
		deal.Probability = int(prob)
	}

	if err := cc.dealRepo.Update(ctx, deal); err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	// Log a note if provided
	if note, ok := params["note"].(string); ok && note != "" {
		title := "AI Command: Deal Update"
		cc.activityRepo.Create(ctx, &domain.Activity{
			OrgID:      orgID,
			Type:       "note",
			DealID:     &dealID,
			UserID:     &userID,
			Title:      &title,
			Body:       &note,
			OccurredAt: time.Now(),
		})
	}

	out, _ := json.Marshal(map[string]any{"updated": true, "deal_id": dealID, "title": deal.Title})
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

	err := cc.activityRepo.Create(ctx, activity)
	if err != nil {
		out, _ := json.Marshal(map[string]any{"error": err.Error()})
		return out
	}

	out, _ := json.Marshal(map[string]any{"logged": true, "type": actType, "title": title})
	return out
}

func (cc *CommandCenter) toolGetAnalytics(ctx context.Context, orgID uuid.UUID, params map[string]interface{}) json.RawMessage {
	metric, _ := params["metric"].(string)

	switch metric {
	case "pipeline", "revenue":
		deals, _, _ := cc.dealRepo.List(ctx, orgID, domain.DealFilter{Limit: 100})
		totalValue := 0.0
		activeCount := 0
		wonCount := 0
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
			"total_value":  totalValue,
			"active_deals": activeCount,
			"won_deals":    wonCount,
			"total_deals":  len(deals),
		})
		return out

	case "forecast":
		rows, _ := cc.dealRepo.Forecast(ctx, orgID)
		out, _ := json.Marshal(map[string]any{
			"metric":   "forecast",
			"forecast": rows,
		})
		return out

	default:
		out, _ := json.Marshal(map[string]any{"metric": metric, "message": "metric not yet implemented"})
		return out
	}
}
