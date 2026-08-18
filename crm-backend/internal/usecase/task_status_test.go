package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// task_status_test.go covers the task status field and its automation triggers
// (task_created/task_updated/task_deleted): status↔completed_at stays in sync no
// matter which API shape a caller uses, task_updated carries changed_fields so
// watch_field='status' actually works, and Task.Status is validated at the one
// place it can change.

// --- fake repo (in-memory, supports GetByID for Update) --------------------

type fakeStatusTaskRepo struct {
	store map[uuid.UUID]*domain.Task
}

func newFakeStatusTaskRepo() *fakeStatusTaskRepo {
	return &fakeStatusTaskRepo{store: map[uuid.UUID]*domain.Task{}}
}
func (f *fakeStatusTaskRepo) List(context.Context, uuid.UUID, domain.TaskFilter) (domain.TaskListResult, error) {
	return domain.TaskListResult{}, nil
}
func (f *fakeStatusTaskRepo) GetByID(_ context.Context, _, id uuid.UUID) (*domain.Task, error) {
	t, ok := f.store[id]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}
func (f *fakeStatusTaskRepo) Create(_ context.Context, t *domain.Task) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	cp := *t
	f.store[t.ID] = &cp
	return nil
}
func (f *fakeStatusTaskRepo) Update(_ context.Context, t *domain.Task) error {
	cp := *t
	f.store[t.ID] = &cp
	return nil
}
func (f *fakeStatusTaskRepo) SoftDelete(_ context.Context, _, id uuid.UUID) error {
	delete(f.store, id)
	return nil
}
func (f *fakeStatusTaskRepo) ClaimDueForReminder(context.Context, time.Time, time.Duration, int) ([]domain.Task, error) {
	return nil, nil
}

// firedEvent is one call the fake emitter recorded.
type firedEvent struct {
	eventType string
	payload   map[string]any
}

// awaitTaskEvents runs act and collects every event fired within the window —
// a status change fires task_updated AND task_status_changed from two separate
// goroutines (see taskUseCase.Update), so callers that only expect one, like
// record_service_events_test.go's single-event awaitEvent, would race between
// which arrives and panic closing an already-closed channel. This drains for a
// short quiet period instead of stopping at the first event.
func awaitTaskEvents(t *testing.T, uc domain.TaskUseCase, act func() error) []firedEvent {
	t.Helper()
	events := make(chan firedEvent, 8)
	uc.SetEventEmitter(func(_ context.Context, _ uuid.UUID, eventType string, payload map[string]any) {
		events <- firedEvent{eventType: eventType, payload: payload}
	})
	if err := act(); err != nil {
		t.Fatalf("action: %v", err)
	}
	var got []firedEvent
	for {
		select {
		case e := <-events:
			got = append(got, e)
		case <-time.After(200 * time.Millisecond):
			return got
		}
	}
}

// awaitTaskEvent is awaitTaskEvents for the common single-event case: it fails
// the test if the event never fires, or if more than one type fires (a caller
// expecting exactly one event that starts seeing two is exactly the class of
// bug this file's TestTaskUpdate_StatusChangeFiresBothEvents pins down instead).
func awaitTaskEvent(t *testing.T, uc domain.TaskUseCase, act func() error) (string, map[string]any) {
	t.Helper()
	got := awaitTaskEvents(t, uc, act)
	require.Len(t, got, 1, "%+v", got)
	return got[0].eventType, got[0].payload
}

// eventOfType finds the one event of the given type, failing the test if it is
// absent or duplicated.
func eventOfType(t *testing.T, events []firedEvent, eventType string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, e := range events {
		if e.eventType == eventType {
			found = append(found, e.payload)
		}
	}
	require.Len(t, found, 1, "expected exactly one %s among %+v", eventType, events)
	return found[0]
}

func newStatusTestUC() (domain.TaskUseCase, *fakeStatusTaskRepo) {
	repo := newFakeStatusTaskRepo()
	return NewTaskUseCase(repo, nil, nil), repo
}

// --- Create: status default + validation ------------------------------------

func TestTaskCreate_DefaultsStatusToOpen(t *testing.T) {
	uc, _ := newStatusTestUC()
	task, err := uc.Create(context.Background(), uuid.New(), domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusOpen, task.Status)
	assert.Nil(t, task.CompletedAt)
}

func TestTaskCreate_AcceptsAnExplicitValidStatus(t *testing.T) {
	uc, _ := newStatusTestUC()
	task, err := uc.Create(context.Background(), uuid.New(), domain.CreateTaskInput{Title: "t", Status: domain.TaskStatusInProgress})
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusInProgress, task.Status)
	assert.Nil(t, task.CompletedAt, "in_progress is not completion")
}

func TestTaskCreate_CreatingDirectlyIntoCompletedStampsCompletedAt(t *testing.T) {
	uc, _ := newStatusTestUC()
	task, err := uc.Create(context.Background(), uuid.New(), domain.CreateTaskInput{Title: "t", Status: domain.TaskStatusCompleted})
	require.NoError(t, err)
	require.NotNil(t, task.CompletedAt)
}

func TestTaskCreate_RejectsAnUnknownStatus(t *testing.T) {
	uc, _ := newStatusTestUC()
	_, err := uc.Create(context.Background(), uuid.New(), domain.CreateTaskInput{Title: "t", Status: "archived"})
	require.Error(t, err)
	assertBadRequest(t, err)
}

// assertBadRequest pins handleAppError's contract (delivery/http/auth_handler.go):
// it renders a 4xx with the real message ONLY for an error that type-asserts to
// *domain.AppError — a bare fmt.Errorf falls into the "unexpected error" branch and
// ships a generic 500, discarding the message and logging a well-formed client
// input error as a server fault. A caller that treats 5xx as retryable would retry
// a request that can never succeed.
func assertBadRequest(t *testing.T, err error) {
	t.Helper()
	appErr, ok := err.(*domain.AppError)
	require.True(t, ok, "must be a *domain.AppError so handleAppError renders 400 with the real message, not a 500 with a discarded one — got %T: %v", err, err)
	assert.Equal(t, 400, appErr.Code)
}

// --- Update: status ↔ completed_at stay in sync no matter the API shape -----

func TestTaskUpdate_StatusCompletedStampsCompletedAt(t *testing.T) {
	uc, repo := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	status := domain.TaskStatusCompleted
	updated, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &status})
	require.NoError(t, err)
	require.NotNil(t, updated.CompletedAt)
	assert.Equal(t, domain.TaskStatusCompleted, repo.store[task.ID].Status)
}

func TestTaskUpdate_LeavingCompletedClearsCompletedAt(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t", Status: domain.TaskStatusCompleted})
	require.NoError(t, err)
	require.NotNil(t, task.CompletedAt)

	status := domain.TaskStatusInProgress
	updated, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &status})
	require.NoError(t, err)
	assert.Nil(t, updated.CompletedAt, "reopening (even to in_progress, not just open) must clear completion")
}

func TestTaskUpdate_CompletingTwiceDoesNotSlideTheTimestamp(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t", Status: domain.TaskStatusCompleted})
	require.NoError(t, err)
	first := *task.CompletedAt

	time.Sleep(2 * time.Millisecond)
	status := domain.TaskStatusCompleted
	updated, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &status})
	require.NoError(t, err)
	assert.True(t, updated.CompletedAt.Equal(first), "a second 'complete' must not move an already-stamped completed_at")
}

func TestTaskUpdate_LegacyCompletedBoolStillWorks(t *testing.T) {
	// The checkbox UI predates Status and still sends this shape.
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	yes := true
	updated, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Completed: &yes})
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusCompleted, updated.Status)
	require.NotNil(t, updated.CompletedAt)

	no := false
	updated, err = uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Completed: &no})
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusOpen, updated.Status, "unchecking has always meant 'reset to not-started', never 'resume in_progress'")
	assert.Nil(t, updated.CompletedAt)
}

func TestTaskUpdate_StatusWinsWhenBothStatusAndCompletedAreSent(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	status := domain.TaskStatusInProgress
	yes := true
	updated, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &status, Completed: &yes})
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusInProgress, updated.Status, "the richer control describes intent more precisely than the boolean it grew out of")
	assert.Nil(t, updated.CompletedAt)
}

func TestTaskUpdate_RejectsAnUnknownStatus(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	bad := "archived"
	_, err = uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &bad})
	require.Error(t, err)
	assertBadRequest(t, err)
}

func TestTaskUpdate_UnknownTaskIsNotFound(t *testing.T) {
	uc, _ := newStatusTestUC()
	status := domain.TaskStatusCompleted
	_, err := uc.Update(context.Background(), uuid.New(), uuid.New(), domain.UpdateTaskInput{Status: &status})
	assert.ErrorIs(t, err, domain.ErrTaskNotFound)
}

// --- Automation events -------------------------------------------------------

func TestTaskCreate_FiresTaskCreatedInTheUniformShape(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	var taskID uuid.UUID

	eventType, payload := awaitTaskEvent(t, uc, func() error {
		task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "Follow up"})
		if task != nil {
			taskID = task.ID
		}
		return err
	})

	assert.Equal(t, "task_created", eventType)
	assert.Equal(t, taskID.String(), payload["entity_id"])
	taskMap, ok := payload["task"].(map[string]any)
	require.True(t, ok, "payload must carry the record under its own slug, matching fireLifecycleEvent's shape")
	assert.Equal(t, "Follow up", taskMap["title"])
	assert.Equal(t, domain.TaskStatusOpen, taskMap["status"])
	trigger, ok := payload["trigger"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "task_created", trigger["type"])
	assert.NotContains(t, payload, "changed_fields", "a create has no before-state to diff")
}

func TestTaskUpdate_ChangedFieldsNamesTheStatusMove(t *testing.T) {
	// This is what makes watch_field='status' actually work — engine.go's
	// payloadContainsChangedField reads exactly this key.
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	status := domain.TaskStatusCompleted
	events := awaitTaskEvents(t, uc, func() error {
		_, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &status})
		return err
	})
	payload := eventOfType(t, events, "task_updated")

	changed, ok := payload["changed_fields"].([]string)
	require.True(t, ok, "%#v", payload["changed_fields"])
	assert.Contains(t, changed, "task.status")
	assert.Contains(t, changed, "task.completed_at", "completed_at moved too — nil to a timestamp")
}

func TestTaskUpdate_StatusChangeFiresBothEvents(t *testing.T) {
	// Unlike deals (whose stage move and other field edits are separate UI
	// flows, so fireStageChanged and deal_updated are mutually exclusive per
	// write), Task.Update always takes every possible change in one input — so
	// a status move fires task_updated (the generic "something changed") AND
	// task_status_changed (the bespoke, pickable-in-the-builder trigger) from
	// the SAME write. Both must reach a listener, not just one.
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	status := domain.TaskStatusInProgress
	events := awaitTaskEvents(t, uc, func() error {
		_, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Status: &status})
		return err
	})

	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.eventType
	}
	assert.ElementsMatch(t, []string{"task_updated", "task_status_changed"}, types, "%+v", events)

	statusPayload := eventOfType(t, events, "task_status_changed")
	assert.Equal(t, domain.TaskStatusOpen, statusPayload["old_status"])
	assert.Equal(t, domain.TaskStatusInProgress, statusPayload["new_status"])
	assert.Equal(t, task.ID.String(), statusPayload["entity_id"])
	trigger, ok := statusPayload["trigger"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "task_status_changed", trigger["type"])
}

func TestTaskUpdate_NonStatusChangeDoesNotFireStatusChanged(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	title := "renamed"
	eventType, _ := awaitTaskEvent(t, uc, func() error {
		_, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Title: &title})
		return err
	})
	assert.Equal(t, "task_updated", eventType, "task_status_changed must not fire when status did not move")
}

func TestTaskUpdate_ChangedFieldsOmitsStatusWhenOnlyTitleMoved(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	title := "renamed"
	_, payload := awaitTaskEvent(t, uc, func() error {
		_, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Title: &title})
		return err
	})

	changed, _ := payload["changed_fields"].([]string)
	assert.NotContains(t, changed, "task.status")
	assert.Contains(t, changed, "task.title")
}

func TestTaskUpdate_LegacyCompletedBoolAlsoProducesChangedFields(t *testing.T) {
	// The two API shapes must be indistinguishable to a watch_field='status'
	// workflow — a caller using the old checkbox endpoint must not silently fail
	// to fire triggers the new Status endpoint fires correctly.
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	yes := true
	events := awaitTaskEvents(t, uc, func() error {
		_, err := uc.Update(context.Background(), orgID, task.ID, domain.UpdateTaskInput{Completed: &yes})
		return err
	})
	payload := eventOfType(t, events, "task_updated")

	changed, _ := payload["changed_fields"].([]string)
	assert.Contains(t, changed, "task.status")

	statusPayload := eventOfType(t, events, "task_status_changed")
	assert.Equal(t, domain.TaskStatusCompleted, statusPayload["new_status"], "the legacy boolean path must ALSO reach the bespoke trigger")
}

func TestTaskDelete_FiresTaskDeletedWithASnapshot(t *testing.T) {
	uc, _ := newStatusTestUC()
	orgID := uuid.New()
	task, err := uc.Create(context.Background(), orgID, domain.CreateTaskInput{Title: "Doomed"})
	require.NoError(t, err)

	eventType, payload := awaitTaskEvent(t, uc, func() error {
		return uc.Delete(context.Background(), orgID, task.ID)
	})

	assert.Equal(t, "task_deleted", eventType)
	taskMap, ok := payload["task"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Doomed", taskMap["title"], "a task_deleted condition on the deleted record's fields needs the pre-delete snapshot")
}

func TestTaskUseCase_NoEmitterWiredIsSilentNotAPanic(t *testing.T) {
	// main.go wires SetEventEmitter after construction; a boot-ordering slip
	// (or a unit test that never calls it) must degrade to "no triggers fire",
	// never a nil-pointer panic on an ordinary Create/Update/Delete.
	repo := newFakeStatusTaskRepo()
	uc := NewTaskUseCase(repo, nil, nil)

	task, err := uc.Create(context.Background(), uuid.New(), domain.CreateTaskInput{Title: "t"})
	require.NoError(t, err)

	status := domain.TaskStatusCompleted
	_, err = uc.Update(context.Background(), task.OrgID, task.ID, domain.UpdateTaskInput{Status: &status})
	require.NoError(t, err)

	require.NoError(t, uc.Delete(context.Background(), task.OrgID, task.ID))
}

// --- taskAutomationMap / computeTaskChangedFields ---------------------------

func TestTaskAutomationMap_DatesAreRFC3339(t *testing.T) {
	due := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	task := &domain.Task{ID: uuid.New(), Title: "t", Priority: "high", Status: domain.TaskStatusOpen, DueAt: &due}
	m := taskAutomationMap(task)
	assert.Equal(t, due.Format(time.RFC3339), m["due_at"], "must parse the same way parseDateValue/date_field triggers expect")
}

func TestTaskAutomationMap_OmitsAbsentOptionalFields(t *testing.T) {
	task := &domain.Task{ID: uuid.New(), Title: "t", Priority: "medium", Status: domain.TaskStatusOpen}
	m := taskAutomationMap(task)
	assert.NotContains(t, m, "due_at")
	assert.NotContains(t, m, "contact_id")
	assert.NotContains(t, m, "deal_id")
	assert.NotContains(t, m, "assigned_to")
	assert.NotContains(t, m, "completed_at")
}

func TestComputeTaskChangedFields(t *testing.T) {
	old := map[string]any{"status": "open", "title": "a"}
	next := map[string]any{"status": "completed", "title": "a", "completed_at": "2026-09-01T00:00:00Z"}
	changed := computeTaskChangedFields(old, next)
	assert.Contains(t, changed, "task.status")
	assert.Contains(t, changed, "task.completed_at")
	assert.NotContains(t, changed, "task.title")
}

func TestComputeTaskChangedFields_NilInputsAreSafe(t *testing.T) {
	assert.Nil(t, computeTaskChangedFields(nil, map[string]any{"a": 1}))
	assert.Nil(t, computeTaskChangedFields(map[string]any{"a": 1}, nil))
}
