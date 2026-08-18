package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type taskUseCase struct {
	taskRepo domain.TaskRepository
	notifyUC domain.NotificationUseCase
	logger   *slog.Logger
	// emit fires task_created/task_updated/task_deleted. Wired post-construction
	// from main.go (the automation engine doesn't exist yet when this usecase is
	// built) — nil until then, and nil-checked at every call site so a boot
	// ordering slip degrades to "no task triggers fire" rather than a panic.
	emit domain.RecordEventEmitter
}

// NewTaskUseCase builds the task usecase. A nil logger falls back to
// slog.Default(), which main.go points at the JSON handler — same convention
// as the lead-scoring usecase, whose wiring also runs before the shared
// logger exists.
func NewTaskUseCase(taskRepo domain.TaskRepository, notifyUC domain.NotificationUseCase, logger *slog.Logger) domain.TaskUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &taskUseCase{taskRepo: taskRepo, notifyUC: notifyUC, logger: logger}
}

func (uc *taskUseCase) SetEventEmitter(fn domain.RecordEventEmitter) {
	uc.emit = fn
}

// dueReminderScanLimit caps one scan pass (no-silent-caps doctrine: a scan
// that hits it logs how many it left for next tick rather than pretending it
// caught up).
const dueReminderScanLimit = 500

func (uc *taskUseCase) RunDueReminders(ctx context.Context, lookahead time.Duration) (int, error) {
	now := time.Now()
	// The claim stamps last_reminded_at as it returns the rows, so every task
	// below is already spoken for: no other instance will notify it, and a
	// failure here costs one window rather than looping forever.
	tasks, err := uc.taskRepo.ClaimDueForReminder(ctx, now, lookahead, dueReminderScanLimit)
	if err != nil {
		return 0, err
	}
	if len(tasks) == dueReminderScanLimit {
		uc.logger.Warn("tasks: due-reminder scan hit its limit; the remainder waits for the next tick",
			"limit", dueReminderScanLimit)
	}
	sent := 0
	for _, t := range tasks {
		// The reminder goes to whoever can act on the task: the assignee, else
		// the creator (an unassigned task the creator left for themselves).
		// Neither present means nobody to notify — the claim already stamped
		// it, so it simply won't be re-read.
		recipient := t.AssignedTo
		if recipient == nil {
			recipient = t.CreatedBy
		}
		if recipient == nil {
			continue
		}

		link := ""
		if t.DealID != nil {
			link = "/deals/" + t.DealID.String()
		} else if t.ContactID != nil {
			link = "/objects/contact/" + t.ContactID.String()
		}
		body := t.Title
		if t.DueAt != nil && t.DueAt.Before(now) {
			body = t.Title + " (overdue)"
		}

		n, err := uc.notifyUC.Create(ctx, domain.NotificationCreateInput{
			OrgID:      t.OrgID,
			UserID:     *recipient,
			Type:       "task_reminder",
			Title:      "Task due",
			Body:       body,
			Link:       link,
			EntityType: "task",
			EntityID:   &t.ID,
		})
		if err != nil {
			// Log rather than retry: the task is already claimed, and a
			// recipient that always fails (a deleted user, say) would
			// otherwise be re-attempted on every tick for the life of the row.
			uc.logger.Warn("tasks: due reminder could not be delivered",
				"error", err, "task_id", t.ID.String(), "user_id", recipient.String())
			continue
		}
		// Create returns (nil, nil) when the recipient's preferences route the
		// notification to no surface at all (mute-all, or every channel off).
		// Counting those as sent would make the scanner's log lie.
		if n != nil {
			sent++
		}
	}
	return sent, nil
}

func (uc *taskUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.TaskFilter) (domain.TaskListResult, error) {
	return uc.taskRepo.List(ctx, orgID, f)
}

func (uc *taskUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateTaskInput) (*domain.Task, error) {
	task := domain.Task{
		OrgID:      orgID,
		Title:      input.Title,
		DealID:     input.DealID,
		ContactID:  input.ContactID,
		AssignedTo: input.AssignedTo,
		Priority:   input.Priority,
	}

	if task.Priority == "" {
		task.Priority = "medium"
	}

	task.Status = input.Status
	if task.Status == "" {
		task.Status = domain.TaskStatusOpen
	} else if !domain.TaskStatusValues[task.Status] {
		return nil, domain.NewAppError(http.StatusBadRequest, fmt.Sprintf("invalid status %q", task.Status))
	}
	if task.Status == domain.TaskStatusCompleted {
		now := time.Now()
		task.CompletedAt = &now
	}

	if input.DueAt != nil && *input.DueAt != "" {
		t, err := time.Parse(time.RFC3339, *input.DueAt)
		if err != nil {
			return nil, fmt.Errorf("invalid due_at format: %w", err)
		}
		task.DueAt = &t
	}

	if err := uc.taskRepo.Create(ctx, &task); err != nil {
		return nil, err
	}
	uc.fireTaskEvent(ctx, orgID, "task_created", &task, nil)
	return &task, nil
}

func (uc *taskUseCase) Update(ctx context.Context, orgID uuid.UUID, id uuid.UUID, input domain.UpdateTaskInput) (*domain.Task, error) {
	task, err := uc.taskRepo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		// nil from the scoped GetByID means "not found or not visible to this
		// caller" — a row-scoped user editing another rep's task lands here (404),
		// not a silent success.
		return nil, domain.ErrTaskNotFound
	}
	before := taskAutomationMap(task)
	oldStatus := task.Status

	if input.Title != nil {
		task.Title = *input.Title
	}
	if input.AssignedTo != nil {
		task.AssignedTo = input.AssignedTo
	}
	if input.Priority != nil {
		task.Priority = *input.Priority
	}
	if input.DueAt != nil {
		if *input.DueAt == "" {
			task.DueAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, *input.DueAt)
			if err != nil {
				return nil, fmt.Errorf("invalid due_at format: %w", err)
			}
			task.DueAt = &t
		}
	}

	// Status is the richer control and wins over Completed when a caller sends
	// both — see the doc comment on UpdateTaskInput. Either path keeps
	// CompletedAt in sync (domain.Task.Status's doc comment on why), so
	// task_updated's changed_fields always includes "status" when it moves,
	// regardless of which shape the caller used to move it.
	switch {
	case input.Status != nil:
		if !domain.TaskStatusValues[*input.Status] {
			return nil, domain.NewAppError(http.StatusBadRequest, fmt.Sprintf("invalid status %q", *input.Status))
		}
		uc.setTaskStatus(task, *input.Status)
	case input.Completed != nil:
		if *input.Completed {
			uc.setTaskStatus(task, domain.TaskStatusCompleted)
		} else {
			// Unchecking has never meant "resume progress" — before Status
			// existed it was the ONLY way to reopen a task, and it always reset
			// to not-started. Keep that exact behavior.
			uc.setTaskStatus(task, domain.TaskStatusOpen)
		}
	}

	if err := uc.taskRepo.Update(ctx, task); err != nil {
		return nil, err
	}
	uc.fireTaskEvent(ctx, orgID, "task_updated", task, before)
	// task_status_changed fires ADDITIONALLY, not instead of, task_updated — see
	// TriggerTaskStatusChanged's doc comment. Both are legitimate to fire from one
	// call because, unlike deals (whose stage move and field edits are separate
	// UI flows), Task.Update always takes every possible change in one input.
	if task.Status != oldStatus {
		uc.fireTaskStatusChanged(ctx, orgID, task, oldStatus)
	}
	return task, nil
}

// fireTaskStatusChanged emits TriggerTaskStatusChanged with old_status/new_status
// at the payload's top level — the exact shape fireStageChanged uses for
// old_stage_id/new_stage_id (record_service_system.go), which the trigger's
// to_status/from_status params are validated and matched against.
func (uc *taskUseCase) fireTaskStatusChanged(ctx context.Context, orgID uuid.UUID, task *domain.Task, oldStatus string) {
	if uc.emit == nil {
		return
	}
	payload := map[string]any{
		"entity_id":  task.ID.String(),
		"task":       taskAutomationMap(task),
		"old_status": oldStatus,
		"new_status": task.Status,
		"trigger":    map[string]any{"type": "task_status_changed", "source": domain.WriteSourceFromContext(ctx)},
	}
	go uc.emit(context.Background(), orgID, "task_status_changed", payload)
}

// setTaskStatus is the ONE place Task.Status changes, so CompletedAt can never
// drift from it: entering TaskStatusCompleted stamps it (once — a second
// "complete" call must not slide an already-completed task's timestamp
// forward), leaving it clears it.
func (uc *taskUseCase) setTaskStatus(task *domain.Task, status string) {
	task.Status = status
	if status == domain.TaskStatusCompleted {
		if task.CompletedAt == nil {
			now := time.Now()
			task.CompletedAt = &now
		}
		return
	}
	task.CompletedAt = nil
}

func (uc *taskUseCase) Delete(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	// Snapshot before delete so a task_deleted workflow can condition on the
	// task's fields — mirrors contact_deleted/deal_deleted. A load failure (the
	// row is already gone, or a race) still deletes and fires a minimal payload
	// rather than skipping the trigger.
	snap, _ := uc.taskRepo.GetByID(ctx, orgID, id)
	if err := uc.taskRepo.SoftDelete(ctx, orgID, id); err != nil {
		return err
	}
	m := map[string]any{"id": id.String()}
	if snap != nil {
		m = taskAutomationMap(snap)
	}
	if uc.emit != nil {
		go uc.emit(context.Background(), orgID, "task_deleted", map[string]any{
			"entity_id": id.String(),
			"task":      m,
			"trigger":   map[string]any{"type": "task_deleted", "source": domain.WriteSourceFromContext(ctx)},
		})
	}
	return nil
}

// fireTaskEvent fires task_created/task_updated in fireLifecycleEvent's exact
// payload shape ({entity_id, task: record, trigger}) — Task isn't a
// RecordService object (see report_objects.go), so this is hand-wired rather
// than routed through it. Fire-and-forget on context.Background() so a
// cancelled request can't kill the async run, same reasoning as
// fireLifecycleEvent. before is nil for a create; when non-nil its diff against
// the fresh map becomes changed_fields, which is what lets a workflow's
// watch_field='status' actually filter task_updated triggers — see
// computeTaskChangedFields.
func (uc *taskUseCase) fireTaskEvent(ctx context.Context, orgID uuid.UUID, eventType string, task *domain.Task, before map[string]any) {
	if uc.emit == nil {
		return
	}
	after := taskAutomationMap(task)
	payload := map[string]any{
		"entity_id": task.ID.String(),
		"task":      after,
		"trigger":   map[string]any{"type": eventType, "source": domain.WriteSourceFromContext(ctx)},
	}
	if before != nil {
		if changed := computeTaskChangedFields(before, after); len(changed) > 0 {
			payload["changed_fields"] = changed
		}
	}
	go uc.emit(context.Background(), orgID, eventType, payload)
}

// taskAutomationMap is the event-payload shape for a task — mirrors
// contactAutomationMap/dealAutomationMap (usecase/record_service_system.go).
// Dates serialize as RFC3339 so {{task.due_at}} and a date_field trigger on
// task.due_at both parse it the same way conditions/dealAutomationMap's dates
// already do.
func taskAutomationMap(t *domain.Task) map[string]any {
	m := map[string]any{
		"id":       t.ID.String(),
		"title":    t.Title,
		"priority": t.Priority,
		"status":   t.Status,
	}
	if t.DealID != nil {
		m["deal_id"] = t.DealID.String()
	}
	if t.ContactID != nil {
		m["contact_id"] = t.ContactID.String()
	}
	if t.AssignedTo != nil {
		m["assigned_to"] = t.AssignedTo.String()
	}
	if t.DueAt != nil {
		m["due_at"] = t.DueAt.Format(time.RFC3339)
	}
	if t.CompletedAt != nil {
		m["completed_at"] = t.CompletedAt.Format(time.RFC3339)
	}
	return m
}

// computeTaskChangedFields compares old and new task maps and returns the
// changed field paths as "task.<key>", the exact shape
// payloadContainsChangedField (internal/automation/engine.go) reads for
// watch_field — mirrors contact_handler.go's computeChangedFields.
func computeTaskChangedFields(oldMap, newMap map[string]any) []string {
	if oldMap == nil || newMap == nil {
		return nil
	}
	var changed []string
	for key, newVal := range newMap {
		oldVal, exists := oldMap[key]
		if !exists || fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			changed = append(changed, "task."+key)
		}
	}
	for key := range oldMap {
		if _, exists := newMap[key]; !exists {
			changed = append(changed, "task."+key)
		}
	}
	return changed
}
