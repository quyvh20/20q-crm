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
	// DigestOnly marks a row that exists ONLY to feed the daily digest (U5): the
	// recipient turned the in-app (bell) channel off for this type but still wants
	// email/digest, so the row is stored but the bell (List/UnreadCount) filters it
	// out. Default false (a normal bell notification). Framed as the exception (true)
	// rather than in_app (default true) on purpose: GORM omits a zero-value field that
	// has a `default`, so an explicit in_app=false would be silently dropped to the
	// column default — whereas the true value here is always written.
	DigestOnly bool `gorm:"not null;default:false" json:"-"`
	// ReadAt is nil until the recipient reads it; the partial unread index keys off it.
	ReadAt *time.Time `json:"read_at,omitempty"`
	// DigestedAt marks that the daily-digest job has already processed this row (U5),
	// so a notification is emailed in exactly one digest even under the per-run cap /
	// bursts — the idempotency key that replaces a fragile time-window.
	DigestedAt *time.Time `json:"-"`
	CreatedAt  time.Time  `json:"created_at"`
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
	// ListPendingDigest returns a recipient's notifications that still need to be
	// considered for a daily digest (U5): unread, not-yet-digested (digested_at IS
	// NULL), created at/after `since` (a safety floor), oldest-first, capped. Includes
	// rows the recipient hid from the bell (in_app=false) — those exist ONLY to be
	// digested. digested_at (not a moving time-window) is what makes each row reach
	// exactly one digest despite the cap and bursts.
	ListPendingDigest(ctx context.Context, orgID, userID uuid.UUID, since time.Time, limit int) ([]Notification, error)
	// MarkNotificationsDigested stamps digested_at on the given rows so the digest
	// job never reconsiders them.
	MarkNotificationsDigested(ctx context.Context, ids []uuid.UUID, at time.Time) error
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

	// --- Preferences + email channel + digest (U5) ---

	// GetPreferences returns the caller's notification preferences for the workspace
	// as the preference-center payload (current settings merged with the event-type
	// catalog + per-type defaults). Never errors on "no row" — it returns defaults.
	GetPreferences(ctx context.Context, orgID, userID uuid.UUID) (*NotificationPreferenceView, error)
	// UpdatePreferences upserts the caller's preferences and returns the fresh view.
	UpdatePreferences(ctx context.Context, orgID, userID uuid.UUID, in NotificationPreferenceUpdate) (*NotificationPreferenceView, error)
	// RunDailyDigest sends each opted-in member (email_digest='daily') a single email
	// summarizing their recent unread, email-eligible notifications, at most once per
	// ~day. Returns how many digest emails were sent. Best-effort; a per-user failure
	// is logged and skipped so one bad address can't stall the batch.
	RunDailyDigest(ctx context.Context) (int, error)
}

// ============================================================
// Notification preferences (U5)
// ============================================================

// Notification email-digest modes.
const (
	DigestOff   = "off"   // email eligible notifications immediately
	DigestDaily = "daily" // batch them into one email per day
)

// NotificationEventType is a category a member can independently control per channel.
type NotificationEventType struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// NotificationEventTypes is the code-defined catalog the preference center renders a
// labeled row for. Today the only producer is automation notify_user ("automation");
// a new producer adds its type here so it becomes independently controllable. A type
// NOT in the catalog still delivers using DefaultChannelPref.
var NotificationEventTypes = []NotificationEventType{
	{Key: "automation", Label: "Workflow notifications", Description: "Alerts an automation sends you (a workflow's \"Notify user\" action)."},
	// Registering this is not cosmetic. An unregistered type still DELIVERS (using
	// DefaultChannelPref), but it renders no row in the preference centre and
	// UpdatePreferences silently discards any override for it — so a member who wanted
	// to quieten integration alerts could only do it by muting everything, taking their
	// workflow notifications with it.
	{Key: "integration_health", Label: "Lead source health", Description: "Alerts when a lead source or a connected account stops working, and when it recovers."},
}

// ChannelPref is the per-event-type toggle pair. An ABSENT override (not the zero
// value) means DefaultChannelPref applies — overrides are stored sparsely so a new
// event type works with no write.
type ChannelPref struct {
	InApp bool `json:"in_app"`
	Email bool `json:"email"`
}

// DefaultChannelPref: in-app on (the bell is the primary surface), email OFF (email
// is opt-in, so a fresh install never mails anyone until they ask).
func DefaultChannelPref() ChannelPref { return ChannelPref{InApp: true, Email: false} }

// NotificationPreference is one member's per-workspace notification settings.
type NotificationPreference struct {
	ID     uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"-"`
	OrgID  uuid.UUID `gorm:"type:uuid;not null" json:"-"`
	UserID uuid.UUID `gorm:"type:uuid;not null" json:"-"`
	// MuteAll silences every channel (no bell row, no email). Per-type overrides are
	// preserved so unmuting restores them.
	MuteAll bool `gorm:"not null;default:false" json:"mute_all"`
	// EmailDigest is DigestOff (email immediately) or DigestDaily (batch daily).
	EmailDigest string `gorm:"size:16;not null;default:'off'" json:"email_digest"`
	// Overrides maps event-type key → channel toggles. Absent keys use DefaultChannelPref.
	Overrides map[string]ChannelPref `gorm:"type:jsonb;serializer:json" json:"-"`
	// LastDigestSentAt guards the daily digest against double-sends across restarts.
	LastDigestSentAt *time.Time `json:"-"`
	CreatedAt        time.Time  `json:"-"`
	UpdatedAt        time.Time  `json:"-"`
}

func (NotificationPreference) TableName() string { return "notification_preferences" }

// Channels returns the effective channel toggles for an event type, applying the
// per-type override if present, else DefaultChannelPref.
func (p *NotificationPreference) Channels(eventType string) ChannelPref {
	if p != nil {
		if c, ok := p.Overrides[eventType]; ok {
			return c
		}
	}
	return DefaultChannelPref()
}

// NotificationTypePref is one catalog row in the preference-center payload: the
// event type's identity plus the member's effective channel toggles.
type NotificationTypePref struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description"`
	InApp       bool   `json:"in_app"`
	Email       bool   `json:"email"`
}

// NotificationPreferenceView is the preference-center payload (GET/PUT response).
type NotificationPreferenceView struct {
	MuteAll     bool                   `json:"mute_all"`
	EmailDigest string                 `json:"email_digest"`
	Types       []NotificationTypePref `json:"types"`
}

// NotificationPreferenceUpdate is the PUT payload. All fields optional: only the
// provided ones are written (a nil pointer / absent Types leaves that part as-is).
type NotificationPreferenceUpdate struct {
	MuteAll     *bool                    `json:"mute_all"`
	EmailDigest *string                  `json:"email_digest"`
	Types       []NotificationChannelSet `json:"types"`
}

// NotificationChannelSet sets one event type's channel toggles.
type NotificationChannelSet struct {
	Key   string `json:"key"`
	InApp bool   `json:"in_app"`
	Email bool   `json:"email"`
}

// NotificationDigestItem is one line in a daily digest email.
type NotificationDigestItem struct {
	Title     string
	Body      string
	Link      string // absolute URL
	CreatedAt time.Time
}

// NotificationPreferenceRepository persists per-member preferences and serves the
// daily-digest job's work list.
type NotificationPreferenceRepository interface {
	// Get returns the member's row for the workspace, or nil when none exists yet.
	Get(ctx context.Context, orgID, userID uuid.UUID) (*NotificationPreference, error)
	// Upsert inserts or updates the member's row (unique on org_id+user_id).
	Upsert(ctx context.Context, p *NotificationPreference) error
	// ListDailyDigestDue returns every email_digest='daily' preference whose last
	// digest was sent before `sentBefore` (or never), across all orgs — the digest
	// job's candidate list.
	ListDailyDigestDue(ctx context.Context, sentBefore time.Time) ([]NotificationPreference, error)
	// TryClaimDailyDigest atomically advances last_digest_sent_at to `at` for one
	// preference IFF it is still due (last_digest_sent_at IS NULL OR < sentBefore),
	// returning true only for the caller that won the claim. This compare-and-swap is
	// what keeps two concurrent digest passes (multiple instances / overlapping
	// ticks) from both emailing the same member (U5).
	TryClaimDailyDigest(ctx context.Context, id uuid.UUID, sentBefore, at time.Time) (bool, error)
}
