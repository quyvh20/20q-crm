package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

type taskUseCase struct {
	taskRepo domain.TaskRepository
	notifyUC domain.NotificationUseCase
	logger   *slog.Logger
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
