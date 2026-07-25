package domain

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// M7 — bulk send engine. Campaign statuses (the claim query reads these to know
// which rosters are drainable, and pause/cancel flip them).
const (
	CampaignStatusDraft     = "draft"
	CampaignStatusScheduled = "scheduled"
	// CampaignStatusSnapshotting is the brief, exclusive launch-in-progress state: one
	// Launch atomically claims draft/scheduled → snapshotting, builds the roster while
	// the send worker (which claims only 'sending') cannot see it, then flips to
	// 'sending'. It makes Launch serializable — a second concurrent Launch loses the
	// claim and 409s instead of wiping a half-built roster.
	CampaignStatusSnapshotting = "snapshotting"
	CampaignStatusSending      = "sending"
	CampaignStatusPaused       = "paused"
	CampaignStatusSent         = "sent"
	CampaignStatusCanceled     = "canceled"
)

// Send lanes: single = one /emails per recipient with a per-recipient idempotency
// key (exactly-once across retries); batch = /emails/batch (opt-in, carries a
// bounded double-send window — see the M7 plan). Default single.
const (
	CampaignLaneSingle = "single"
	CampaignLaneBatch  = "batch"
)

// Recipient roster statuses (the roster is the sole durable authority for send
// state / dedup / progress / resume).
const (
	RecipientStatusPending    = "pending"    // claimable
	RecipientStatusProcessing = "processing" // claimed, in-flight
	RecipientStatusSent       = "sent"
	RecipientStatusFailed     = "failed"     // permanent 4xx / max attempts
	RecipientStatusSuppressed = "suppressed" // dropped at claim by the live M1 check
	RecipientStatusSkipped    = "skipped"    // e.g. campaign canceled before send
)

// Recipient-lock modes: snapshot the roster when the campaign is scheduled/launched
// (default — a stable audience), vs. determine at send (re-resolve — not built in
// M7.1, reserved).
const (
	RecipientLockOnSchedule = "lock_on_schedule"
	RecipientLockAtSend     = "at_send"
)

// Campaign is a bulk marketing send: an audience (segments minus exclusions) + M6
// content + an M3 topic, sent from an M2 verified domain under every M1/M4 guardrail.
type Campaign struct {
	ID                uuid.UUID      `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID             uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name              string         `gorm:"type:varchar(200);not null" json:"name"`
	ContentID         *uuid.UUID     `gorm:"type:uuid" json:"content_id,omitempty"`
	SegmentIDs        datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"segment_ids"`
	ExcludeSegmentIDs datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"exclude_segment_ids"`
	SendingDomainID   *uuid.UUID     `gorm:"type:uuid" json:"sending_domain_id,omitempty"`
	TopicID           *uuid.UUID     `gorm:"type:uuid" json:"topic_id,omitempty"`
	Status            string         `gorm:"type:varchar(16);not null;default:'draft'" json:"status"`
	SendLane          string         `gorm:"type:varchar(16);not null;default:'single'" json:"send_lane"`
	ScheduledAt       *time.Time     `json:"scheduled_at,omitempty"`
	RecipientLockMode string         `gorm:"type:varchar(24);not null;default:'lock_on_schedule'" json:"recipient_lock_mode"`
	SnapshotCounts    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"snapshot_counts"`
	FeedbackID        *string        `gorm:"type:varchar(128)" json:"feedback_id,omitempty"`
	CreatedBy         *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt         time.Time      `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt         time.Time      `gorm:"not null;default:now()" json:"updated_at"`
	StartedAt         *time.Time     `json:"started_at,omitempty"`
	FinishedAt        *time.Time     `json:"finished_at,omitempty"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName pins the table name.
func (Campaign) TableName() string { return "marketing_campaigns" }

// CampaignRecipient is one roster row. Surrogate PK (an imported email may have no
// contact); the real dedupe is UNIQUE(campaign_id, email_normalized).
type CampaignRecipient struct {
	ID                uuid.UUID  `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	CampaignID        uuid.UUID  `gorm:"type:uuid;not null;index" json:"campaign_id"`
	OrgID             uuid.UUID  `gorm:"type:uuid;not null" json:"org_id"`
	ContactID         *uuid.UUID `gorm:"type:uuid" json:"contact_id,omitempty"`
	EmailNormalized   string     `gorm:"type:varchar(320);not null" json:"email_normalized"`
	Variant           *string    `gorm:"type:varchar(32)" json:"variant,omitempty"`
	Status            string     `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	Attempts          int        `gorm:"type:int;not null;default:0" json:"attempts"`
	NextAttemptAt     *time.Time `json:"next_attempt_at,omitempty"`
	ScheduledFor      *time.Time `json:"scheduled_for,omitempty"`
	LockedAt          *time.Time `json:"locked_at,omitempty"`
	ProviderMessageID *string    `gorm:"type:varchar(128)" json:"provider_message_id,omitempty"`
	IdempotencyKey    *string    `gorm:"type:varchar(160)" json:"idempotency_key,omitempty"`
	// DispatchedAt is stamped (once) just before the send is handed to Resend. The
	// reaper reads it: a stranded 'processing' row dispatched within Resend's 24h
	// idempotency window may be safely re-pent (Resend dedupes the replay); one
	// dispatched longer ago is failed instead of re-sent, so a long pause can't
	// duplicate-deliver past the window.
	DispatchedAt      *time.Time `json:"dispatched_at,omitempty"`
	Error             *string    `gorm:"type:text" json:"error,omitempty"`
	CreatedAt         time.Time  `gorm:"not null;default:now()" json:"created_at"`
	ProcessedAt       *time.Time `json:"processed_at,omitempty"`
}

// TableName pins the table name.
func (CampaignRecipient) TableName() string { return "marketing_campaign_recipients" }

// CampaignInput is the create/update request body.
type CampaignInput struct {
	Name              string      `json:"name"`
	ContentID         *uuid.UUID  `json:"content_id"`
	SegmentIDs        []uuid.UUID `json:"segment_ids"`
	ExcludeSegmentIDs []uuid.UUID `json:"exclude_segment_ids"`
	TopicID           *uuid.UUID  `json:"topic_id"`
	SendLane          string      `json:"send_lane"`
	ScheduledAt       *time.Time  `json:"scheduled_at"`
	RecipientLockMode string      `json:"recipient_lock_mode"`
}

// LaunchCheck is one row of the pre-send checklist; a campaign may launch only when
// every check is OK. Detail carries the human-readable reason when not OK.
type LaunchCheck struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// LaunchReadiness is the whole checklist plus the derived all-green flag and the
// current estimated audience size.
type LaunchReadiness struct {
	Ready         bool          `json:"ready"`
	Checks        []LaunchCheck `json:"checks"`
	AudienceCount int           `json:"audience_count"`
}

// SnapshotResult reports the outcome of materializing the roster.
type SnapshotResult struct {
	Total int `json:"total"`
}

// ResolvedSegment is a segment reduced to what the audience compiler needs: its
// type and (for dynamic) its parsed filter AST.
type ResolvedSegment struct {
	ID     uuid.UUID
	Type   string // static | dynamic
	Filter SegmentFilter
}

// AudienceQuery is a fully-parameterized `SELECT contact_id, email_normalized` over
// the deduped union(includes) EXCEPT union(excludes) of segments, org-scoped and
// callerless (org-wide). M7 wraps it in an INSERT…SELECT (roster snapshot) or a
// COUNT (live estimate). The SelectSQL never interpolates values (M5 discipline).
type AudienceQuery struct {
	SelectSQL string
	Args      []any
}
