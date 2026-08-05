package usecase

import (
	"context"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type taskUseCase struct {
	taskRepo    domain.TaskRepository
	notifyUC    domain.NotificationUseCase
}

func NewTaskUseCase(taskRepo domain.TaskRepository, notifyUC domain.NotificationUseCase) domain.TaskUseCase {
	return &taskUseCase{taskRepo: taskRepo, notifyUC: notifyUC}
}

// dueReminderScanLimit caps one scan pass (no-silent-caps doctrine: a scan
// that hits it logs how many it left for next tick rather than pretending it
// caught up).
const dueReminderScanLimit = 500

func (uc *taskUseCase) RunDueReminders(ctx context.Context, lookahead time.Duration) (int, error) {
	now := time.Now()
	tasks, err := uc.taskRepo.DueForReminder(ctx, now, lookahead, dueReminderScanLimit)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, t := range tasks {
		// The reminder goes to whoever can act on the task: the assignee, else
		// the creator (an unassigned task the creator left for themselves).
		// Neither present means nobody to notify — mark it reminded anyway so
		// the scanner doesn't re-read it forever.
		recipient := t.AssignedTo
		if recipient == nil {
			recipient = t.CreatedBy
		}
		if recipient != nil {
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
			if _, err := uc.notifyUC.Create(ctx, domain.NotificationCreateInput{
				OrgID:      t.OrgID,
				UserID:     *recipient,
				Type:       "task_reminder",
				Title:      "Task due",
				Body:       body,
				Link:       link,
				EntityType: "task",
				EntityID:   &t.ID,
			}); err != nil {
				continue
			}
			sent++
		}
		_ = uc.taskRepo.MarkReminded(ctx, t.ID, now)
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
	if input.Completed != nil {
		if *input.Completed {
			now := time.Now()
			task.CompletedAt = &now
		} else {
			task.CompletedAt = nil
		}
	}

	if err := uc.taskRepo.Update(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (uc *taskUseCase) Delete(ctx context.Context, orgID uuid.UUID, id uuid.UUID) error {
	return uc.taskRepo.SoftDelete(ctx, orgID, id)
}
