package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Notifications (A6)
// ============================================================
//
// An in-app notification delivered to one member's inbox (the header bell). It is
// a platform concern — not automation-owned — because it's produced from several
// sources (automation `notify_user` actions first, more later) and consumed
// app-wide. Rows are hard-deleted after 90 days (no soft-delete): a stale
// notification carries no audit value, so a periodic sweep keeps the table small
// and the partial unread index tight.
//
// Delivery is two-legged: NotificationUseCase.Create inserts the row AND publishes
// it on the recipient's PER-USER SSE channel (sse:<orgID>:<userID>) so the bell
// updates in real time. A per-user channel (not the org-wide sse:<orgID>) is
// mandatory — an org-wide publish would leak every member's notification payloads
// to every other member's open SSE stream.

type Notification struct {
	ID     uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID  uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	UserID uuid.UUID `gorm:"type:uuid;not null" json:"user_id"` // recipient
	// Type groups notifications for iconography/filtering (e.g. "automation").
	Type  string `gorm:"size:50;not null;default:'automation'" json:"type"`
	Title string `gorm:"size:255;not null" json:"title"`
	Body  string `gorm:"not null;default:''" json:"body"`
	// Link is an optional in-app deep link (e.g. "/deals/<id>") the inbox row
	// navigates to on click. Stored as a relative path.
	Link string `gorm:"size:1024;not null;default:''" json:"link"`
	// EntityType/EntityID optionally tie the notification to a record (object slug
	// + id) so the UI can render a contextual chip even without a Link.
	EntityType string     `gorm:"size:64;not null;default:''" json:"entity_type,omitempty"`
	EntityID   *uuid.UUID `gorm:"type:uuid" json:"entity_id,omitempty"`
	// ReadAt is nil until the recipient reads it; the partial unread index keys off it.
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func (Notification) TableName() string { return "notifications" }

// NotificationCreateInput is the create payload. The recipient (UserID) is
// resolved by the caller (an automation executor, a future in-app source), not
// from request context — a notification is always addressed to a specific member.
type NotificationCreateInput struct {
	OrgID      uuid.UUID
	UserID     uuid.UUID
	Type       string
	Title      string
	Body       string
	Link       string
	EntityType string
	EntityID   *uuid.UUID
}

// NotificationListInput is the inbox query: newest-first keyset pagination with an
// optional unread-only filter. Cursor is opaque (see EncodeNotificationCursor).
type NotificationListInput struct {
	Limit      int
	Cursor     string
	UnreadOnly bool
}

// NotificationPage is one page of the inbox plus the forward cursor and the
// current unread count (so the bell badge stays authoritative on every fetch).
type NotificationPage struct {
	Notifications []Notification `json:"notifications"`
	NextCursor    string         `json:"next_cursor,omitempty"`
	UnreadCount   int64          `json:"unread_count"`
}

// UserNotificationChannel is the per-user SSE Redis channel. Shared by the
// publisher (NotificationUseCase.Create) and the subscriber (events.go Stream) so
// the channel name can never drift between them — the same discipline as
// SessionCacheKey. Org-wide events keep using OrgNotificationChannel.
func UserNotificationChannel(orgID, userID uuid.UUID) string {
	return "sse:" + orgID.String() + ":" + userID.String()
}

// OrgNotificationChannel is the org-wide SSE channel (existing AI/voice job
// events). Named here so the two channel formats live side by side.
func OrgNotificationChannel(orgID uuid.UUID) string {
	return "sse:" + orgID.String()
}

// ============================================================
// Ports
// ============================================================

// NotificationRepository persists notifications and serves the per-user inbox.
// Every query is scoped to (orgID, userID) so a member can only ever see their own.
type NotificationRepository interface {
	Create(ctx context.Context, n *Notification) error
	// List returns one newest-first page for a recipient plus the next cursor
	// (empty when the page is the last). UnreadOnly filters to read_at IS NULL.
	List(ctx context.Context, orgID, userID uuid.UUID, in NotificationListInput) ([]Notification, string, error)
	UnreadCount(ctx context.Context, orgID, userID uuid.UUID) (int64, error)
	// MarkRead stamps read_at on one of the recipient's notifications (idempotent;
	// a no-op if already read or not theirs).
	MarkRead(ctx context.Context, orgID, userID, id uuid.UUID) error
	// MarkAllRead stamps read_at on every unread notification for the recipient and
	// returns the number affected.
	MarkAllRead(ctx context.Context, orgID, userID uuid.UUID) (int64, error)
	// DeleteOlderThan hard-deletes notifications created before cutoff across ALL
	// orgs (the 90-day sweep) and returns the number removed.
	DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// NotificationUseCase is the service the REST inbox and automation executors use.
// Create is the only write that also fans out over SSE; the read/mark operations
// are plain repository passthroughs scoped to the caller.
type NotificationUseCase interface {
	// Create inserts the notification and publishes it on the recipient's per-user
	// SSE channel. The insert is authoritative; a publish failure is logged, not
	// returned (the row is still in the inbox on next fetch).
	Create(ctx context.Context, in NotificationCreateInput) (*Notification, error)
	List(ctx context.Context, orgID, userID uuid.UUID, in NotificationListInput) (*NotificationPage, error)
	UnreadCount(ctx context.Context, orgID, userID uuid.UUID) (int64, error)
	MarkRead(ctx context.Context, orgID, userID, id uuid.UUID) error
	MarkAllRead(ctx context.Context, orgID, userID uuid.UUID) (int64, error)
	// SweepOld hard-deletes notifications older than the retention window (90 days).
	SweepOld(ctx context.Context) (int64, error)
}
