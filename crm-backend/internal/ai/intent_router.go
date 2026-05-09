package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// ============================================================
// Intent Router — fast keyword-based action dispatcher
// ============================================================
//
// The intent router pattern-matches user messages to predefined CRM actions
// and executes them directly via code — no AI model call needed.
// This saves tokens, eliminates hallucination, and responds in < 200ms.
//
// If no intent matches, the caller falls back to full AI reasoning.

// IntentResult holds the output of an intent execution.
type IntentResult struct {
	Text   string         // Markdown response text
	Events []CommandEvent // Extra SSE events (navigate, form, etc.)
}

// intent is a single matchable action.
type intent struct {
	name     string
	patterns []string // lowercased keyword phrases
}

// intentRegistry defines all routable intents.
// Order matters — first match wins.
var intentRegistry = []intent{
	// ── Navigation ──
	{name: "nav_deals", patterns: []string{"go to deals", "open deals", "navigate to deals", "take me to deals"}},
	{name: "nav_contacts", patterns: []string{"go to contacts", "open contacts", "navigate to contacts", "take me to contacts"}},
	{name: "nav_tasks", patterns: []string{"go to tasks", "open tasks", "navigate to tasks"}},
	{name: "nav_settings", patterns: []string{"go to settings", "open settings", "navigate to settings"}},
	{name: "nav_analytics", patterns: []string{"go to analytics", "open analytics", "navigate to analytics", "go to dashboard"}},

	// ── Forms ──
	{name: "create_contact", patterns: []string{"create contact", "new contact", "add contact", "add a contact", "create a contact"}},
	{name: "create_deal", patterns: []string{"create deal", "new deal", "add deal", "add a deal", "create a deal"}},

	// ── Data queries ──
	{name: "search_deals", patterns: []string{
		"my deals", "show deals", "show my deals", "list deals", "list my deals",
		"show pipeline", "my pipeline", "show my pipeline", "pipeline overview",
		"active deals", "show active deals",
	}},
	{name: "top_deals", patterns: []string{
		"top deals", "biggest deals", "largest deals", "top 5 deals", "top 10 deals",
		"highest value deals", "best deals",
	}},
	{name: "search_contacts", patterns: []string{
		"my contacts", "show contacts", "show my contacts", "list contacts", "list my contacts",
		"all contacts",
	}},
	{name: "my_tasks", patterns: []string{
		"my tasks", "show tasks", "show my tasks", "list tasks", "pending tasks",
		"open tasks", "todo", "to-do",
	}},

	// ── Analytics ──
	{name: "analytics_pipeline", patterns: []string{
		"pipeline stats", "pipeline summary", "pipeline analytics", "pipeline health",
		"deal stats", "deal summary",
	}},
	{name: "analytics_revenue", patterns: []string{
		"revenue", "total revenue", "revenue summary", "show revenue",
		"how much revenue", "sales revenue",
	}},

	// ── Help ──
	{name: "help", patterns: []string{
		"help", "what can you do", "what do you do", "commands", "show commands",
	}},
}

// MatchIntent checks if the user's message matches a predefined intent.
// Returns the intent name or "" if no match.
func MatchIntent(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, it := range intentRegistry {
		for _, pattern := range it.patterns {
			if strings.Contains(lower, pattern) {
				return it.name
			}
		}
	}
	return ""
}

// ExecuteIntent runs a matched intent by code — no AI needed.
func (cc *CommandCenter) ExecuteIntent(
	ctx context.Context,
	intentName string,
	orgID, userID uuid.UUID,
	role, message string,
) *IntentResult {
	switch intentName {
	// ── Navigation ──
	case "nav_deals":
		return navResult("/deals", "Deals")
	case "nav_contacts":
		return navResult("/contacts", "Contacts")
	case "nav_tasks":
		return navResult("/tasks", "Tasks")
	case "nav_settings":
		return navResult("/settings", "Settings")
	case "nav_analytics":
		return navResult("/analytics", "Analytics")

	// ── Forms ──
	case "create_contact":
		return cc.intentCreateContact(message)
	case "create_deal":
		return cc.intentCreateDeal(message)

	// ── Data queries ──
	case "search_deals":
		return cc.intentSearchDeals(ctx, orgID, userID, role, 10, "created_at", "desc")
	case "top_deals":
		return cc.intentSearchDeals(ctx, orgID, userID, role, 5, "value", "desc")
	case "search_contacts":
		return cc.intentSearchContacts(ctx, orgID, userID, role)
	case "my_tasks":
		return cc.intentMyTasks(ctx, orgID, userID)

	// ── Analytics ──
	case "analytics_pipeline", "analytics_revenue":
		return cc.intentAnalytics(ctx, orgID, userID, role, intentName)

	// ── Help ──
	case "help":
		return intentHelp()

	default:
		return nil
	}
}

// ============================================================
// Intent implementations
// ============================================================

func navResult(path, label string) *IntentResult {
	navData, _ := json.Marshal(map[string]string{"path": path, "label": label})
	return &IntentResult{
		Text: fmt.Sprintf("Taking you to **%s** now 🔗", label),
		Events: []CommandEvent{
			{Type: "navigate", Data: navData},
		},
	}
}

func (cc *CommandCenter) intentCreateContact(message string) *IntentResult {
	// Try to extract name from message like "create contact John Doe"
	name := extractAfterKeyword(message, []string{"create contact ", "new contact ", "add contact "})
	formData, _ := json.Marshal(map[string]any{
		"form_type":    "contact",
		"prefill_name": name,
	})
	return &IntentResult{
		Text: "Opening the new contact form ✨",
		Events: []CommandEvent{
			{Type: "form", Data: formData},
		},
	}
}

func (cc *CommandCenter) intentCreateDeal(message string) *IntentResult {
	title := extractAfterKeyword(message, []string{"create deal ", "new deal ", "add deal "})
	formData, _ := json.Marshal(map[string]any{
		"form_type":     "deal",
		"prefill_title": title,
	})
	return &IntentResult{
		Text: "Opening the new deal form 📝",
		Events: []CommandEvent{
			{Type: "form", Data: formData},
		},
	}
}

func (cc *CommandCenter) intentSearchDeals(
	ctx context.Context,
	orgID, userID uuid.UUID,
	role string,
	limit int,
	sortBy, sortOrder string,
) *IntentResult {
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
		cc.logger.Error("intent_search_deals failed", zap.Error(err))
		return &IntentResult{Text: "⚠️ Could not load deals right now. Please try again."}
	}

	if len(deals) == 0 {
		if role == "sales_rep" {
			return &IntentResult{Text: "You don't have any deals assigned to you yet. Click **+ New Deal** to create one!"}
		}
		return &IntentResult{Text: "No deals found in the pipeline yet."}
	}

	// Format as markdown table
	var b strings.Builder
	if sortBy == "value" {
		b.WriteString(fmt.Sprintf("### 💰 Top %d Deals by Value\n\n", len(deals)))
	} else {
		scope := "Your"
		if role != "sales_rep" {
			scope = "All"
		}
		b.WriteString(fmt.Sprintf("### 📊 %s Deals (%d)\n\n", scope, len(deals)))
	}

	b.WriteString("| Deal | Value | Stage | Probability |\n")
	b.WriteString("|------|------:|-------|------------:|\n")
	for _, d := range deals {
		stage := "—"
		if d.Stage != nil {
			stage = d.Stage.Name
		}
		status := ""
		if d.IsWon {
			status = " ✅"
		} else if d.IsLost {
			status = " ❌"
		}
		// Embed UUID for follow-up reference
		b.WriteString(fmt.Sprintf("| [%s](#%s)%s | $%.0f | %s | %d%% |\n",
			d.Title, d.ID.String(), status, d.Value, stage, d.Probability))
	}

	return &IntentResult{Text: b.String()}
}

func (cc *CommandCenter) intentSearchContacts(
	ctx context.Context,
	orgID, userID uuid.UUID,
	role string,
) *IntentResult {
	filter := domain.ContactFilter{Limit: 10, SortBy: "created_at", SortOrder: "desc"}
	if role == "sales_rep" {
		filter.OwnerUserID = &userID
	}

	contacts, _, err := cc.contactRepo.List(ctx, orgID, filter)
	if err != nil {
		cc.logger.Error("intent_search_contacts failed", zap.Error(err))
		return &IntentResult{Text: "⚠️ Could not load contacts right now. Please try again."}
	}

	if len(contacts) == 0 {
		if role == "sales_rep" {
			return &IntentResult{Text: "You don't have any contacts assigned to you yet. Click **+ New Contact** to add one!"}
		}
		return &IntentResult{Text: "No contacts found in the system yet."}
	}

	scope := "Your"
	if role != "sales_rep" {
		scope = "All"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("### 👥 %s Contacts (%d)\n\n", scope, len(contacts)))
	b.WriteString("| Name | Email | Company |\n")
	b.WriteString("|------|-------|---------|\n")
	for _, c := range contacts {
		name := strings.TrimSpace(c.FirstName + " " + c.LastName)
		email := "—"
		if c.Email != nil {
			email = *c.Email
		}
		company := "—"
		if c.Company != nil {
			company = c.Company.Name
		}
		b.WriteString(fmt.Sprintf("| [%s](#%s) | %s | %s |\n", name, c.ID.String(), email, company))
	}

	return &IntentResult{Text: b.String()}
}

func (cc *CommandCenter) intentMyTasks(
	ctx context.Context,
	orgID, userID uuid.UUID,
) *IntentResult {
	completed := false
	filter := domain.TaskFilter{
		AssignedTo: &userID,
		Completed:  &completed,
	}

	tasks, err := cc.taskRepo.List(ctx, orgID, filter)
	if err != nil {
		cc.logger.Error("intent_my_tasks failed", zap.Error(err))
		return &IntentResult{Text: "⚠️ Could not load tasks right now. Please try again."}
	}

	if len(tasks) == 0 {
		return &IntentResult{Text: "✅ You have no pending tasks. You're all caught up!"}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("### ✅ Your Pending Tasks (%d)\n\n", len(tasks)))
	b.WriteString("| Task | Priority | Due |\n")
	b.WriteString("|------|----------|-----|\n")
	for _, t := range tasks {
		due := "—"
		if t.DueAt != nil {
			due = t.DueAt.Format("Jan 2")
		}
		priorityIcon := "⚪"
		switch t.Priority {
		case "high":
			priorityIcon = "🔴"
		case "medium":
			priorityIcon = "🟡"
		case "low":
			priorityIcon = "🟢"
		}
		b.WriteString(fmt.Sprintf("| %s | %s %s | %s |\n", t.Title, priorityIcon, t.Priority, due))
	}

	return &IntentResult{Text: b.String()}
}

func (cc *CommandCenter) intentAnalytics(
	ctx context.Context,
	orgID, userID uuid.UUID,
	role, intentName string,
) *IntentResult {
	filter := domain.DealFilter{Limit: 500}
	if role == "sales_rep" {
		filter.OwnerUserID = &userID
	}

	deals, _, err := cc.dealRepo.List(ctx, orgID, filter)
	if err != nil {
		cc.logger.Error("intent_analytics failed", zap.Error(err))
		return &IntentResult{Text: "⚠️ Could not load analytics right now. Please try again."}
	}

	var totalValue, wonValue, activeValue float64
	activeCount, wonCount, lostCount := 0, 0, 0
	for _, d := range deals {
		totalValue += d.Value
		if d.IsWon {
			wonCount++
			wonValue += d.Value
		} else if d.IsLost {
			lostCount++
		} else {
			activeCount++
			activeValue += d.Value
		}
	}

	scope := "Org-wide"
	if role == "sales_rep" {
		scope = "Your"
	}

	var b strings.Builder
	if intentName == "analytics_revenue" {
		b.WriteString(fmt.Sprintf("### 📈 %s Revenue Summary\n\n", scope))
		b.WriteString("| Metric | Value |\n")
		b.WriteString("|--------|------:|\n")
		b.WriteString(fmt.Sprintf("| **Won Revenue** | %s |\n", formatMoney(wonValue)))
		b.WriteString(fmt.Sprintf("| **Active Pipeline** | %s |\n", formatMoney(activeValue)))
		b.WriteString(fmt.Sprintf("| **Total Value** | %s |\n", formatMoney(totalValue)))
		b.WriteString(fmt.Sprintf("| Deals Won | %d |\n", wonCount))
		b.WriteString(fmt.Sprintf("| Deals Active | %d |\n", activeCount))
		b.WriteString(fmt.Sprintf("| Deals Lost | %d |\n", lostCount))
	} else {
		b.WriteString(fmt.Sprintf("### 🏥 %s Pipeline Health\n\n", scope))
		b.WriteString("| Metric | Value |\n")
		b.WriteString("|--------|------:|\n")
		b.WriteString(fmt.Sprintf("| **Active Deals** | %d |\n", activeCount))
		b.WriteString(fmt.Sprintf("| **Pipeline Value** | %s |\n", formatMoney(activeValue)))
		b.WriteString(fmt.Sprintf("| Won | %d (%s) |\n", wonCount, formatMoney(wonValue)))
		b.WriteString(fmt.Sprintf("| Lost | %d |\n", lostCount))
		b.WriteString(fmt.Sprintf("| **Total** | %d deals |\n", len(deals)))

		if len(deals) > 0 {
			winRate := float64(wonCount) / float64(wonCount+lostCount) * 100
			if wonCount+lostCount > 0 {
				b.WriteString(fmt.Sprintf("| **Win Rate** | %.0f%% |\n", winRate))
			}
		}
	}

	return &IntentResult{Text: b.String()}
}

func intentHelp() *IntentResult {
	return &IntentResult{
		Text: `### 🤖 What I Can Do

**Quick Actions** (instant, no AI needed):
- 📊 "Show my deals" / "Show my pipeline"
- 💰 "Top deals by value"
- 👥 "Show my contacts"
- ✅ "My tasks"
- 📈 "Revenue summary" / "Pipeline health"
- ➕ "Create contact" / "Create deal"
- 🔗 "Go to deals" / "Go to contacts"

**AI-Powered** (I'll think about it):
- "What strategy should I use for stale deals?"
- "Compare my top contacts by deal value"
- "Draft a follow-up email for John"
- "Mark the Acme deal as won"
- "Create a task to follow up with Jane next week"

Just type naturally — I'll figure it out! 💡`,
	}
}

// ============================================================
// Helpers
// ============================================================

// extractAfterKeyword extracts text after a matched keyword.
// e.g., "create contact John Doe" → "John Doe"
func extractAfterKeyword(message string, keywords []string) string {
	lower := strings.ToLower(message)
	for _, kw := range keywords {
		if idx := strings.Index(lower, kw); idx >= 0 {
			after := strings.TrimSpace(message[idx+len(kw):])
			if after != "" {
				return after
			}
		}
	}
	return ""
}

// formatMoney formats a float as $X,XXX (with comma separators).
func formatMoney(v float64) string {
	// Simple comma-separated formatting for dollar amounts
	intPart := int64(v)
	if intPart < 0 {
		return fmt.Sprintf("-$%s", formatCommas(-intPart))
	}
	return fmt.Sprintf("$%s", formatCommas(intPart))
}

func formatCommas(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}
