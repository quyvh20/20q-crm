package automation

import (
	"context"
	"fmt"
	"log/slog"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// maxNotifyPerRun caps how many in-app notifications a single workflow run may
// create via notify_user. A defense-in-depth guard against a misauthored loop (or
// a future re-enrollment path) flooding an inbox — not a security control, so it
// fails open if the count query itself errors.
const maxNotifyPerRun = 50

// NotificationCreator is the narrow port the notify_user executor writes through —
// satisfied by the platform NotificationUseCase (which inserts the row AND pushes
// it over the recipient's per-user SSE channel). Kept minimal so the automation
// package doesn't depend on the usecase layer (mirrors the P8 authorizer port).
type NotificationCreator interface {
	Create(ctx context.Context, in domain.NotificationCreateInput) (*domain.Notification, error)
}

// NotifyUserExecutor delivers an in-app notification to a member's inbox. Unlike
// the record-writing executors it enforces no OLS/FLS — a notification is a
// message, not a CRM record — but it honors a per-run cap and only ever addresses
// a resolved user id.
type NotifyUserExecutor struct {
	db       *gorm.DB
	notifier NotificationCreator
}

// NewNotifyUserExecutor builds the executor. db is used only for the per-run cap
// count (nil disables the cap, e.g. unit tests); notifier performs the insert +
// SSE fan-out.
func NewNotifyUserExecutor(db *gorm.DB, notifier NotificationCreator) *NotifyUserExecutor {
	return &NotifyUserExecutor{db: db, notifier: notifier}
}

func (e *NotifyUserExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	if e.notifier == nil {
		// No notifier wired (misconfiguration) — permanent, so the run fails clearly
		// rather than retrying forever against a dependency that will never appear.
		return nil, fmt.Errorf("notify_user: notification service is not configured")
	}

	title := getStringParam(action.Params, "title", evalCtx)
	if title == "" {
		return nil, fmt.Errorf("notify_user: title is required")
	}
	body := getStringParam(action.Params, "body", evalCtx)

	recipientID, err := e.resolveRecipient(action, evalCtx)
	if err != nil {
		return nil, err // permanent: a config/data problem, retrying won't help
	}

	// Contextual anchor + default deep link from the trigger record (deal preferred,
	// then contact). An author-supplied link overrides the derived one.
	entityType, entityID := triggerEntityRef(evalCtx)
	link := getStringParam(action.Params, "link", evalCtx)
	if link == "" {
		link = defaultEntityLink(entityType, entityID)
	}

	// Per-run cap (fail-open on query error — the cap is a guard, not a gate).
	if e.db != nil {
		var count int64
		if err := e.db.WithContext(ctx).
			Model(&WorkflowActionLog{}).
			Where("run_id = ? AND action_type = ? AND status = ?", run.ID, ActionNotifyUser, LogStatusSuccess).
			Count(&count).Error; err == nil && count >= maxNotifyPerRun {
			return nil, fmt.Errorf("notify_user: per-run notification limit of %d reached", maxNotifyPerRun)
		}
	}

	in := domain.NotificationCreateInput{
		OrgID:      run.OrgID,
		UserID:     recipientID,
		Type:       "automation",
		Title:      title,
		Body:       body,
		Link:       link,
		EntityType: entityType,
	}
	if entityID != uuid.Nil {
		id := entityID
		in.EntityID = &id
	}

	n, err := e.notifier.Create(ctx, in)
	if err != nil {
		// A create failure is almost always a transient DB issue → retryable.
		return nil, NewRetryableError(fmt.Errorf("notify_user: %w", err))
	}
	if n == nil {
		// The recipient's notification preferences suppressed the in-app row (muted,
		// or the "automation" channel turned off). That's a delivered-as-configured
		// outcome, not a failure — the action succeeds so the run continues (U5).
		slog.Info("automation: notification suppressed by recipient preferences",
			"recipient_id", recipientID.String(),
			"workflow_run_id", run.ID.String(),
		)
		return map[string]any{
			"user_id":    recipientID.String(),
			"suppressed": true,
		}, nil
	}

	slog.Info("automation: notification sent",
		"notification_id", n.ID.String(),
		"recipient_id", recipientID.String(),
		"workflow_run_id", run.ID.String(),
	)

	return map[string]any{
		"notification_id": n.ID.String(),
		"user_id":         recipientID.String(),
	}, nil
}

// resolveRecipient turns the action's recipient config into a concrete user id.
//   - "specific": params.user_id is a user uuid (may be a template).
//   - "owner_field" (default when unset): params.owner_field is a context path
//     resolving to a user id (e.g. "deal.owner_user_id"); if it resolves empty it
//     falls back to the trigger record's owner (deal, then contact) so a plain
//     "notify the owner" needs no exact path.
func (e *NotifyUserExecutor) resolveRecipient(action ActionSpec, evalCtx EvalContext) (uuid.UUID, error) {
	mode := getStringParam(action.Params, "recipient", evalCtx)

	if mode == "specific" {
		idStr := getStringParam(action.Params, "user_id", evalCtx)
		if idStr == "" {
			return uuid.Nil, fmt.Errorf("notify_user: user_id is required for a specific recipient")
		}
		uid, err := uuid.Parse(idStr)
		if err != nil {
			return uuid.Nil, fmt.Errorf("notify_user: invalid user_id %q", idStr)
		}
		return uid, nil
	}

	// owner_field (default): resolve the configured path, else fall back to the
	// trigger record's owner.
	var resolved any
	if field := getStringParam(action.Params, "owner_field", evalCtx); field != "" {
		resolved = resolvePath(field, evalCtx)
	}
	idStr := stringifyOwner(resolved)
	if idStr == "" {
		if v, ok := evalCtx.Deal["owner_user_id"]; ok {
			idStr = stringifyOwner(v)
		}
		if idStr == "" {
			if v, ok := evalCtx.Contact["owner_user_id"]; ok {
				idStr = stringifyOwner(v)
			}
		}
	}
	if idStr == "" {
		return uuid.Nil, fmt.Errorf("notify_user: could not resolve a record owner to notify (the record has no owner)")
	}
	uid, err := uuid.Parse(idStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("notify_user: resolved owner is not a valid user id %q", idStr)
	}
	return uid, nil
}

// stringifyOwner coerces a resolved owner value to a non-empty id string, treating
// nil / empty as "unresolved".
func stringifyOwner(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	s := fmt.Sprintf("%v", v)
	if s == "<nil>" {
		return ""
	}
	return s
}

// triggerEntityRef returns the primary trigger record's (object slug, id) for the
// notification's contextual anchor — deal preferred over contact. Returns
// ("", uuid.Nil) when neither is present (e.g. a schedule-triggered run).
func triggerEntityRef(evalCtx EvalContext) (string, uuid.UUID) {
	if id, ok := parseCtxID(evalCtx.Deal); ok {
		return "deal", id
	}
	if id, ok := parseCtxID(evalCtx.Contact); ok {
		return "contact", id
	}
	return "", uuid.Nil
}

func parseCtxID(m map[string]any) (uuid.UUID, bool) {
	if m == nil {
		return uuid.Nil, false
	}
	raw, ok := m["id"]
	if !ok {
		return uuid.Nil, false
	}
	s, ok := raw.(string)
	if !ok {
		s = fmt.Sprintf("%v", raw)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// defaultEntityLink builds the in-app deep link for a trigger entity when the
// author didn't supply one. Only the two entities with detail pages today.
func defaultEntityLink(entityType string, id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	switch entityType {
	case "deal":
		return "/deals/" + id.String()
	case "contact":
		return "/contacts/" + id.String()
	default:
		return ""
	}
}
