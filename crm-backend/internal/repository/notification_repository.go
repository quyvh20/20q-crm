package repository

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// notificationRepository persists in-app notifications. Every read/mark is scoped
// to (org_id, user_id) so a member can only ever touch their own inbox.
type notificationRepository struct {
	db *gorm.DB
}

func NewNotificationRepository(db *gorm.DB) domain.NotificationRepository {
	return &notificationRepository{db: db}
}

func (r *notificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	return r.db.WithContext(ctx).Create(n).Error
}

// List returns one newest-first page for a recipient. Pagination is keyset on the
// (created_at, id) tuple — stable under concurrent inserts (unlike OFFSET) and
// tie-safe when many notifications land in the same instant.
func (r *notificationRepository) List(ctx context.Context, orgID, userID uuid.UUID, in domain.NotificationListInput) ([]domain.Notification, string, error) {
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	q := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("org_id = ? AND user_id = ?", orgID, userID)
	if in.UnreadOnly {
		q = q.Where("read_at IS NULL")
	}
	if in.Cursor != "" {
		if createdAt, id, ok := decodeNotificationCursor(in.Cursor); ok {
			// Row-value comparison walks strictly past the last row of the previous
			// page in the same (created_at DESC, id DESC) order.
			q = q.Where("(created_at, id) < (?, ?)", createdAt, id)
		}
	}

	var rows []domain.Notification
	// Fetch one extra to detect whether a further page exists without a count.
	if err := q.Order("created_at DESC, id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, "", err
	}

	next := ""
	if len(rows) > limit {
		last := rows[limit-1]
		next = encodeNotificationCursor(last.CreatedAt, last.ID)
		rows = rows[:limit]
	}
	return rows, next, nil
}

func (r *notificationRepository) UnreadCount(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("org_id = ? AND user_id = ? AND read_at IS NULL", orgID, userID).
		Count(&n).Error
	return n, err
}

func (r *notificationRepository) MarkRead(ctx context.Context, orgID, userID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("org_id = ? AND user_id = ? AND id = ? AND read_at IS NULL", orgID, userID, id).
		Update("read_at", time.Now()).Error
}

func (r *notificationRepository) MarkAllRead(ctx context.Context, orgID, userID uuid.UUID) (int64, error) {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("org_id = ? AND user_id = ? AND read_at IS NULL", orgID, userID).
		Update("read_at", time.Now())
	return res.RowsAffected, res.Error
}

func (r *notificationRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&domain.Notification{})
	return res.RowsAffected, res.Error
}

// encodeNotificationCursor renders an opaque forward cursor from the last row's
// (created_at, id). base64 of "<unixNano>|<uuid>" — decoded back into the tuple
// comparison above.
func encodeNotificationCursor(createdAt time.Time, id uuid.UUID) string {
	raw := strconv.FormatInt(createdAt.UnixNano(), 10) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeNotificationCursor reverses encodeNotificationCursor. ok=false on any
// malformed cursor, which the list query treats as "no cursor" (page 1) rather
// than erroring — a bad cursor can never leak another user's rows since the
// org/user scope is applied independently.
func decodeNotificationCursor(cursor string) (time.Time, uuid.UUID, bool) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, false
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.Unix(0, nanos), id, true
}
