package usecase

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeNotificationRepo is an in-memory NotificationRepository for pure usecase
// tests (no DB, no Redis). Only the behavior the usecase relies on is modeled.
type fakeNotificationRepo struct {
	created []*domain.Notification
	unread  int64
}

func (f *fakeNotificationRepo) Create(_ context.Context, n *domain.Notification) error {
	n.ID = uuid.New()
	n.CreatedAt = time.Now()
	f.created = append(f.created, n)
	f.unread++
	return nil
}
func (f *fakeNotificationRepo) List(context.Context, uuid.UUID, uuid.UUID, domain.NotificationListInput) ([]domain.Notification, string, error) {
	return nil, "", nil
}
func (f *fakeNotificationRepo) UnreadCount(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return f.unread, nil
}
func (f *fakeNotificationRepo) MarkRead(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeNotificationRepo) MarkAllRead(context.Context, uuid.UUID, uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeNotificationRepo) DeleteOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func TestNotificationUseCase_Create_DefaultsAndNilRedisSafe(t *testing.T) {
	repo := &fakeNotificationRepo{}
	// nil redis client → publish is skipped, Create must still succeed.
	uc := NewNotificationUseCase(repo, nil)

	orgID, userID := uuid.New(), uuid.New()
	n, err := uc.Create(context.Background(), domain.NotificationCreateInput{
		OrgID:  orgID,
		UserID: userID,
		Title:  "Deal won",
		// Type deliberately empty → defaults to "automation".
	})
	require.NoError(t, err)
	require.NotNil(t, n)
	require.Equal(t, "automation", n.Type)
	require.Equal(t, orgID, n.OrgID)
	require.Equal(t, userID, n.UserID)
	require.Len(t, repo.created, 1)
}

func TestNotificationUseCase_Create_RequiresOrgAndUser(t *testing.T) {
	uc := NewNotificationUseCase(&fakeNotificationRepo{}, nil)

	_, err := uc.Create(context.Background(), domain.NotificationCreateInput{UserID: uuid.New(), Title: "x"})
	require.Error(t, err, "missing org must be rejected")

	_, err = uc.Create(context.Background(), domain.NotificationCreateInput{OrgID: uuid.New(), Title: "x"})
	require.Error(t, err, "missing recipient must be rejected")
}

func TestNotificationUseCase_List_WrapsEmptyAndUnread(t *testing.T) {
	repo := &fakeNotificationRepo{unread: 3}
	uc := NewNotificationUseCase(repo, nil)

	page, err := uc.List(context.Background(), uuid.New(), uuid.New(), domain.NotificationListInput{})
	require.NoError(t, err)
	require.NotNil(t, page)
	require.NotNil(t, page.Notifications, "nil rows must be normalized to an empty slice")
	require.Len(t, page.Notifications, 0)
	require.EqualValues(t, 3, page.UnreadCount)
}
