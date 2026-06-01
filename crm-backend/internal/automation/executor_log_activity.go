package automation

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ActivityExecutor logs an activity (call/meeting/note/email) against the
// contact and/or deal that triggered the workflow.
type ActivityExecutor struct {
	db *gorm.DB
}

// NewActivityExecutor creates a new activity executor.
func NewActivityExecutor(db *gorm.DB) *ActivityExecutor {
	return &ActivityExecutor{db: db}
}

// Execute inserts an activity row based on the action params. All failures are
// non-retryable (a plain error), matching TaskExecutor.
func (e *ActivityExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	// 1. Resolve + validate activity_type (Req 9.4)
	activityType := getStringParam(action.Params, "activity_type", evalCtx)
	if !validActivityTypes[activityType] {
		return nil, fmt.Errorf("log_activity: invalid activity type %q (must be call, meeting, note, or email)", activityType)
	}

	// 2. Resolve + validate title (Req 9.1)
	title := strings.TrimSpace(getStringParam(action.Params, "title", evalCtx))
	if title == "" {
		return nil, fmt.Errorf("log_activity: title is required")
	}

	// 3. Resolve body; empty/whitespace => NULL (Req 9.3)
	var bodyPtr *string
	if body := strings.TrimSpace(getStringParam(action.Params, "body", evalCtx)); body != "" {
		bodyPtr = &body
	}

	// 4. Resolve contact/deal IDs (Req 4). Invalid/missing => nil column.
	contactID := parseEntityID(evalCtx.Contact)
	dealID := parseEntityID(evalCtx.Deal)
	if contactID == nil && dealID == nil {
		return nil, fmt.Errorf("log_activity: no valid contact or deal identifier in trigger context")
	}

	// 5. Insert exactly one row; occurred_at/created_at = DB NOW(); user_id = NULL.
	// RETURNING created_at reads back the DB-generated server timestamp so it can be
	// surfaced in the output map (no second round-trip, no clock skew with Go's time).
	activityID := uuid.New()
	var createdAt time.Time
	err := e.db.WithContext(ctx).Raw(
		`INSERT INTO activities (id, org_id, type, contact_id, deal_id, user_id, title, body, occurred_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NULL, ?, ?, NOW(), NOW(), NOW())
		 RETURNING created_at`,
		activityID, run.OrgID, activityType, contactID, dealID, title, bodyPtr,
	).Scan(&createdAt).Error
	if err != nil {
		return nil, fmt.Errorf("log_activity: %w", err) // non-retryable (Req 9.2)
	}

	slog.Info("automation: activity logged",
		"activity_id", activityID.String(),
		"activity_type", activityType,
		"workflow_run_id", run.ID.String(),
	)

	// 6. Output map (Req 3.8). nil ids serialize as JSON null. created_at is the
	// DB server timestamp (RFC3339) so later steps can reference actions.<id>.created_at.
	return map[string]any{
		"activity_id":   activityID.String(),
		"activity_type": activityType,
		"contact_id":    uuidOrNil(contactID),
		"deal_id":       uuidOrNil(dealID),
		"created_at":    createdAt.Format(time.RFC3339),
	}, nil
}

// parseEntityID extracts a UUID from evalCtx.Contact["id"] / evalCtx.Deal["id"].
// Returns nil when absent, non-string, or not a valid UUID (Req 4.4).
func parseEntityID(entity map[string]any) *uuid.UUID {
	raw, ok := entity["id"]
	if !ok {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

// uuidOrNil returns nil for a nil UUID pointer or the string form otherwise,
// so output-map ids serialize as JSON null when absent.
func uuidOrNil(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
