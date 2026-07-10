package usecase

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// notificationRetention is how long a notification lives before the sweep hard-
// deletes it. In-app notifications carry no audit value once stale, so 90 days
// keeps the table small and the partial unread index tight.
const notificationRetention = 90 * 24 * time.Hour

// notificationUseCase inserts notifications and fans them out over SSE. The insert
// is authoritative; the publish is best-effort (a missed publish just means the
// bell updates on the next poll/fetch instead of instantly).
type notificationUseCase struct {
	repo  domain.NotificationRepository
	redis *redis.Client // nil-safe: without Redis, Create simply skips the live push
}

func NewNotificationUseCase(repo domain.NotificationRepository, redisClient *redis.Client) domain.NotificationUseCase {
	return &notificationUseCase{repo: repo, redis: redisClient}
}

func (u *notificationUseCase) Create(ctx context.Context, in domain.NotificationCreateInput) (*domain.Notification, error) {
	if in.OrgID == uuid.Nil || in.UserID == uuid.Nil {
		return nil, domain.NewAppError(400, "notification requires org and recipient")
	}
	typ := in.Type
	if typ == "" {
		typ = "automation"
	}
	n := &domain.Notification{
		OrgID:      in.OrgID,
		UserID:     in.UserID,
		Type:       typ,
		Title:      in.Title,
		Body:       in.Body,
		Link:       in.Link,
		EntityType: in.EntityType,
		EntityID:   in.EntityID,
	}
	if err := u.repo.Create(ctx, n); err != nil {
		return nil, err
	}
	u.publish(ctx, n)
	return n, nil
}

// publish pushes the new notification onto the recipient's per-user SSE channel
// with a fresh unread count so the header bell can update the list and badge in
// one message. Best-effort and nil-safe — a publish error never fails the insert.
func (u *notificationUseCase) publish(ctx context.Context, n *domain.Notification) {
	if u.redis == nil {
		return
	}
	unread, err := u.repo.UnreadCount(ctx, n.OrgID, n.UserID)
	if err != nil {
		slog.Warn("notification: unread count for publish failed", "error", err)
	}
	payload, err := json.Marshal(map[string]any{
		"type":         "notification",
		"notification": n,
		"unread_count": unread,
	})
	if err != nil {
		slog.Warn("notification: marshal for publish failed", "error", err)
		return
	}
	channel := domain.UserNotificationChannel(n.OrgID, n.UserID)
	if err := u.redis.Publish(ctx, channel, payload).Err(); err != nil {
		slog.Warn("notification: SSE publish failed", "channel", channel, "error", err)
	}
}

func (u *notificationUseCase) List(ctx context.Context, orgID, userID uuid.UUID, in domain.NotificationListInput) (*domain.NotificationPage, error) {
	rows, next, err := u.repo.List(ctx, orgID, userID, in)
	if err != nil {
		return nil, err
	}
	unread, err := u.repo.UnreadCount(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []domain.Notification{}
	}
	return &domain.NotificationPage{Notifications: rows, NextCursor: next, UnreadCount: unread}, nil
}

func (u *notificationUseCase) UnreadCount(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	return u.repo.UnreadCount(ctx, orgID, userID)
}

func (u *notificationUseCase) MarkRead(ctx context.Context, orgID, userID, id uuid.UUID) error {
	return u.repo.MarkRead(ctx, orgID, userID, id)
}

func (u *notificationUseCase) MarkAllRead(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	return u.repo.MarkAllRead(ctx, orgID, userID)
}

func (u *notificationUseCase) SweepOld(ctx context.Context) (int64, error) {
	return u.repo.DeleteOlderThan(ctx, time.Now().Add(-notificationRetention))
}
