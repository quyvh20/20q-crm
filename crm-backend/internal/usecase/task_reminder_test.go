package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// task_reminder_test.go covers the due-reminder scan, which shipped with R8.1
// carrying no tests at all. The three behaviours pinned here are the ones that
// break silently: who the reminder is addressed to, that a delivery failure
// neither stops the scan nor re-queues the task forever, and that the returned
// count means "actually reached someone".

// --- fakes ---------------------------------------------------------------

type fakeReminderTaskRepo struct {
	claimed     []domain.Task
	claimErr    error
	claimCalls  int
	gotNow      time.Time
	gotLookahed time.Duration
	gotLimit    int
}

func (f *fakeReminderTaskRepo) ClaimDueForReminder(_ context.Context, now time.Time, lookahead time.Duration, limit int) ([]domain.Task, error) {
	f.claimCalls++
	f.gotNow, f.gotLookahed, f.gotLimit = now, lookahead, limit
	return f.claimed, f.claimErr
}
func (f *fakeReminderTaskRepo) List(context.Context, uuid.UUID, domain.TaskFilter) (domain.TaskListResult, error) {
	return domain.TaskListResult{}, nil
}
func (f *fakeReminderTaskRepo) GetByID(context.Context, uuid.UUID, uuid.UUID) (*domain.Task, error) {
	return nil, nil
}
func (f *fakeReminderTaskRepo) Create(context.Context, *domain.Task) error             { return nil }
func (f *fakeReminderTaskRepo) Update(context.Context, *domain.Task) error             { return nil }
func (f *fakeReminderTaskRepo) SoftDelete(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type sentNotification struct {
	userID uuid.UUID
	body   string
	link   string
}

type fakeNotifier struct {
	sent []sentNotification
	// per-call behaviour, indexed by call number: nil result = "delivered to no
	// surface" (the mute-all path), error = delivery failure.
	results []*domain.Notification
	errs    []error
}

func (f *fakeNotifier) Create(_ context.Context, in domain.NotificationCreateInput) (*domain.Notification, error) {
	i := len(f.sent)
	f.sent = append(f.sent, sentNotification{userID: in.UserID, body: in.Body, link: in.Link})
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.results) {
		return f.results[i], nil
	}
	return &domain.Notification{}, nil
}

func (f *fakeNotifier) List(context.Context, uuid.UUID, uuid.UUID, domain.NotificationListInput) (*domain.NotificationPage, error) {
	return nil, nil
}
func (f *fakeNotifier) UnreadCount(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeNotifier) MarkRead(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (f *fakeNotifier) MarkAllRead(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeNotifier) SweepOld(context.Context) (int64, error) { return 0, nil }
func (f *fakeNotifier) GetPreferences(context.Context, uuid.UUID, uuid.UUID) (*domain.NotificationPreferenceView, error) {
	return nil, nil
}
func (f *fakeNotifier) UpdatePreferences(context.Context, uuid.UUID, uuid.UUID, domain.NotificationPreferenceUpdate) (*domain.NotificationPreferenceView, error) {
	return nil, nil
}
func (f *fakeNotifier) RunDailyDigest(context.Context) (int, error) { return 0, nil }

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }

// --- tests ---------------------------------------------------------------

func TestRunDueReminders_AddressesAssigneeElseCreator(t *testing.T) {
	assignee, creator := uuid.New(), uuid.New()
	past := time.Now().Add(-time.Hour)
	repo := &fakeReminderTaskRepo{claimed: []domain.Task{
		{ID: uuid.New(), OrgID: uuid.New(), Title: "assigned", DueAt: &past, AssignedTo: ptrUUID(assignee), CreatedBy: ptrUUID(creator)},
		{ID: uuid.New(), OrgID: uuid.New(), Title: "self-made", DueAt: &past, CreatedBy: ptrUUID(creator)},
		{ID: uuid.New(), OrgID: uuid.New(), Title: "orphan", DueAt: &past}, // nobody to tell
	}}
	notifier := &fakeNotifier{}

	sent, err := NewTaskUseCase(repo, notifier, nil).RunDueReminders(context.Background(), 15*time.Minute)

	require.NoError(t, err)
	assert.Equal(t, 2, sent, "the orphan task has no recipient and must not count")
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, assignee, notifier.sent[0].userID, "assignee wins over creator")
	assert.Equal(t, creator, notifier.sent[1].userID, "creator is the fallback")
}

func TestRunDueReminders_CountsOnlyWhatReachedASurface(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	user := uuid.New()
	repo := &fakeReminderTaskRepo{claimed: []domain.Task{
		{ID: uuid.New(), Title: "muted recipient", DueAt: &past, AssignedTo: ptrUUID(user)},
		{ID: uuid.New(), Title: "real delivery", DueAt: &past, AssignedTo: ptrUUID(user)},
	}}
	// First call mimics the mute-all path: no error, no row.
	notifier := &fakeNotifier{results: []*domain.Notification{nil, {}}}

	sent, err := NewTaskUseCase(repo, notifier, nil).RunDueReminders(context.Background(), time.Minute)

	require.NoError(t, err)
	assert.Equal(t, 1, sent, "a notification routed to no surface is not a reminder sent")
	assert.Len(t, notifier.sent, 2, "both were still attempted")
}

func TestRunDueReminders_DeliveryFailureIsSkippedNotRetried(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	user := uuid.New()
	repo := &fakeReminderTaskRepo{claimed: []domain.Task{
		{ID: uuid.New(), Title: "bad recipient", DueAt: &past, AssignedTo: ptrUUID(user)},
		{ID: uuid.New(), Title: "fine", DueAt: &past, AssignedTo: ptrUUID(user)},
	}}
	notifier := &fakeNotifier{errs: []error{errors.New("user was deleted"), nil}}

	sent, err := NewTaskUseCase(repo, notifier, nil).RunDueReminders(context.Background(), time.Minute)

	// The failure must not abort the pass, must not surface as a scan error, and
	// must not leave the task unclaimed — the claim already stamped it, so the
	// scanner cannot pick it up again until the next window.
	require.NoError(t, err)
	assert.Equal(t, 1, sent)
	assert.Len(t, notifier.sent, 2, "the scan continued past the failure")
	assert.Equal(t, 1, repo.claimCalls, "one claim per pass — no re-scan of the failed task")
}

func TestRunDueReminders_MarksOverdueAndLinksToTheRecord(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	future := time.Now().Add(10 * time.Minute)
	user, dealID, contactID := uuid.New(), uuid.New(), uuid.New()
	repo := &fakeReminderTaskRepo{claimed: []domain.Task{
		{ID: uuid.New(), Title: "Call back", DueAt: &past, AssignedTo: ptrUUID(user), DealID: ptrUUID(dealID)},
		{ID: uuid.New(), Title: "Send quote", DueAt: &future, AssignedTo: ptrUUID(user), ContactID: ptrUUID(contactID)},
	}}
	notifier := &fakeNotifier{}

	_, err := NewTaskUseCase(repo, notifier, nil).RunDueReminders(context.Background(), 15*time.Minute)

	require.NoError(t, err)
	require.Len(t, notifier.sent, 2)
	assert.Equal(t, "Call back (overdue)", notifier.sent[0].body)
	assert.Equal(t, "/deals/"+dealID.String(), notifier.sent[0].link)
	assert.Equal(t, "Send quote", notifier.sent[1].body, "not yet due — no overdue marker")
	assert.Equal(t, "/objects/contact/"+contactID.String(), notifier.sent[1].link)
}

func TestRunDueReminders_PassesLookaheadAndScanLimit(t *testing.T) {
	repo := &fakeReminderTaskRepo{}
	_, err := NewTaskUseCase(repo, &fakeNotifier{}, nil).RunDueReminders(context.Background(), 42*time.Minute)

	require.NoError(t, err)
	assert.Equal(t, 42*time.Minute, repo.gotLookahed)
	assert.Equal(t, dueReminderScanLimit, repo.gotLimit)
	assert.WithinDuration(t, time.Now(), repo.gotNow, 5*time.Second, "one clock for tick and query")
}

func TestRunDueReminders_ClaimFailurePropagates(t *testing.T) {
	repo := &fakeReminderTaskRepo{claimErr: errors.New("db down")}
	notifier := &fakeNotifier{}

	sent, err := NewTaskUseCase(repo, notifier, nil).RunDueReminders(context.Background(), time.Minute)

	require.Error(t, err)
	assert.Zero(t, sent)
	assert.Empty(t, notifier.sent)
}
