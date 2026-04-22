package automation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TaskExecutor creates tasks in the CRM.
type TaskExecutor struct {
	db *gorm.DB
}

// NewTaskExecutor creates a new task executor.
func NewTaskExecutor(db *gorm.DB) *TaskExecutor {
	return &TaskExecutor{db: db}
}

// Execute creates a task based on the action params.
func (e *TaskExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	title := getStringParam(action.Params, "title", evalCtx)
	if title == "" {
		return nil, fmt.Errorf("create_task: title is required")
	}

	priority := getStringParam(action.Params, "priority", evalCtx)
	if priority == "" {
		priority = "medium"
	}

	dueInDays := getIntParam(action.Params, "due_in_days")

	// Resolve assignee from context field path
	var assigneeID *uuid.UUID
	assigneeField := getStringParam(action.Params, "assignee_field", evalCtx)
	if assigneeField != "" {
		// The field itself might be a template path like "contact.owner_id"
		resolved := resolvePath(assigneeField, evalCtx)
		if resolved != nil {
			if idStr, ok := resolved.(string); ok {
				if uid, err := uuid.Parse(idStr); err == nil {
					assigneeID = &uid
				}
			}
		}
	}

	// Resolve contact ID from trigger context
	var contactID *uuid.UUID
	if cID, ok := evalCtx.Contact["id"]; ok {
		if idStr, ok := cID.(string); ok {
			if uid, err := uuid.Parse(idStr); err == nil {
				contactID = &uid
			}
		}
	}

	// Resolve deal ID from trigger context
	var dealID *uuid.UUID
	if dID, ok := evalCtx.Deal["id"]; ok {
		if idStr, ok := dID.(string); ok {
			if uid, err := uuid.Parse(idStr); err == nil {
				dealID = &uid
			}
		}
	}

	var dueAt *time.Time
	if dueInDays > 0 {
		t := time.Now().Add(time.Duration(dueInDays) * 24 * time.Hour)
		dueAt = &t
	}

	taskID := uuid.New()

	// Insert task directly into the tasks table (matches existing CRM schema)
	err := e.db.WithContext(ctx).Exec(
		`INSERT INTO tasks (id, org_id, title, contact_id, deal_id, assigned_to, due_at, priority, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		taskID, run.OrgID, title, contactID, dealID, assigneeID, dueAt, priority,
	).Error

	if err != nil {
		return nil, fmt.Errorf("create_task: %w", err)
	}

	slog.Info("automation: task created",
		"task_id", taskID.String(),
		"workflow_run_id", run.ID.String(),
	)

	return map[string]any{
		"task_id":  taskID.String(),
		"title":    title,
		"priority": priority,
	}, nil
}
