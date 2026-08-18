package automation

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TaskExecutor creates tasks in the CRM.
type TaskExecutor struct {
	db    *gorm.DB
	authz domain.RecordAuthorizer
	// emit fires task_created so a SECOND workflow can chain off a task this one
	// created ("workflow A creates a task" → "workflow B notifies on task
	// created"), the same way a create_record action's writes still fire their
	// own CRUD triggers. Nil-checked; nil in unit tests that don't wire it.
	emit domain.RecordEventEmitter
}

// NewTaskExecutor creates a new task executor. authz enforces the workflow
// author's read access on the linked contact/deal and audits the creation
// (P8); nil disables enforcement (unit tests). emit is the engine's own
// TriggerEvent — see the field comment on why this fires task_created too.
func NewTaskExecutor(db *gorm.DB, authz domain.RecordAuthorizer, emit domain.RecordEventEmitter) *TaskExecutor {
	return &TaskExecutor{db: db, authz: authz, emit: emit}
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

	// Run-as-creator gate (P8): attaching a task to a record the author cannot
	// read would leak its existence — enforce OLS(read) + own-scope first.
	if err := authorizeLinkedRecordRead(ctx, e.db, e.authz, run.OrgID, contactID, dealID); err != nil {
		return nil, fmt.Errorf("create_task: %w", err)
	}

	taskID := uuid.New()

	// created_by is the workflow author (P8 run-as-creator attribution), matching
	// what log_activity does for activities. It is not cosmetic: taskScope
	// (repository/task_repository.go) admits a row-scoped caller only via
	// assigned_to, created_by, or a reachable contact/deal. assignee_field,
	// contact and deal are all optional here, so without a creator a task born
	// from a schedule/company/custom-object trigger with no assignee would be
	// invisible to every row-scoped user in the org, permanently.
	var creatorID *uuid.UUID
	if caller, ok := domain.CallerFromContext(ctx); ok && caller.UserID != uuid.Nil {
		id := caller.UserID
		creatorID = &id
	}

	// Insert task directly into the tasks table (matches existing CRM schema)
	err := e.db.WithContext(ctx).Exec(
		`INSERT INTO tasks (id, org_id, title, contact_id, deal_id, assigned_to, created_by, due_at, priority, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		taskID, run.OrgID, title, contactID, dealID, assigneeID, creatorID, dueAt, priority,
	).Error

	if err != nil {
		return nil, fmt.Errorf("create_task: %w", err)
	}

	// Attribute the creation to the workflow author in the audit trail (P8).
	if e.authz != nil {
		caller, _ := domain.CallerFromContext(ctx)
		e.authz.Audit(ctx, domain.AuditEntry{
			OrgID:      run.OrgID,
			ActorID:    caller.UserID,
			ObjectSlug: "task",
			RecordID:   taskID,
			Action:     domain.ActionCreate,
			Changes:    map[string]interface{}{"title": map[string]interface{}{"new": title}},
		})
	}

	slog.Info("automation: task created",
		"task_id", taskID.String(),
		"workflow_run_id", run.ID.String(),
	)

	// Fire task_created in the same {entity_id, task, trigger} shape
	// taskAutomationMap/fireTaskEvent produce (usecase/task_usecase.go) — a
	// small duplication of that map's shape rather than importing usecase from
	// here, which this package cannot do without a cycle (usecase already
	// reaches into automation to call TriggerEvent).
	if e.emit != nil {
		m := map[string]any{"id": taskID.String(), "title": title, "priority": priority, "status": "open"}
		if contactID != nil {
			m["contact_id"] = contactID.String()
		}
		if dealID != nil {
			m["deal_id"] = dealID.String()
		}
		if assigneeID != nil {
			m["assigned_to"] = assigneeID.String()
		}
		if dueAt != nil {
			m["due_at"] = dueAt.Format(time.RFC3339)
		}
		payload := map[string]any{
			"entity_id": taskID.String(),
			"task":      m,
			"trigger":   map[string]any{"type": "task_created", "source": "automation"},
		}
		go e.emit(context.Background(), run.OrgID, "task_created", payload)
	}

	return map[string]any{
		"task_id":  taskID.String(),
		"title":    title,
		"priority": priority,
	}, nil
}
