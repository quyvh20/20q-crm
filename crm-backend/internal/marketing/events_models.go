package marketing

import (
	"encoding/json"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// MarketingEmailEvent is one Resend delivery-webhook event (M4). It is an
// OWNER-LESS, org-level row (no owner_user_id) so a later Reports rowPredicateFor
// returns org-scoping only (like companies). It doubles as the durable ledger, the
// svix dedupe row, AND the async work queue (status/attempts/claimed_at), exactly
// like integrations.IntegrationEvent — one table, not two.
type MarketingEmailEvent struct {
	ID              uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID           uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	// SvixID is the webhook delivery id (svix-id header). The (org_id, svix_id) unique
	// index makes ingestion idempotent: a redelivery inserts nothing.
	SvixID          string `gorm:"column:svix_id;type:varchar(80);not null" json:"svix_id"`
	EventType       string `gorm:"type:varchar(48);not null" json:"event_type"`
	EmailNormalized string `gorm:"column:email_normalized;type:varchar(320);not null;default:''" json:"email"`
	FromDomain      string `gorm:"type:varchar(255);not null;default:''" json:"from_domain"`
	// Reason is the mapped suppression reason ("" when the event does not suppress).
	Reason string `gorm:"type:varchar(32);not null;default:''" json:"reason"`
	// BounceType is Resend's bounce classification ("permanent"/"transient"/""), used
	// to route email.bounced to the hard (scope=all) vs soft (accumulate) path.
	BounceType string `gorm:"type:varchar(24);not null;default:''" json:"bounce_type"`
	// Channel scopes the event to the sending lane for the rate denominator. Empty
	// today (no send lane); M7 stamps marketing/transactional so the breaker can scope.
	Channel    string     `gorm:"type:varchar(16);not null;default:''" json:"channel"`
	CampaignID *uuid.UUID `gorm:"type:uuid" json:"campaign_id,omitempty"`
	// Queue columns (mirror integrations.IntegrationEvent).
	Status     string         `gorm:"type:varchar(16);not null;default:'pending'" json:"status"`
	Attempts   int            `gorm:"type:int;not null;default:0" json:"attempts"`
	ClaimedAt  *time.Time     `json:"claimed_at,omitempty"`
	// datatypes.JSON (not []byte) so GORM binds it as jsonb, not bytea — a plain
	// []byte is sent as bytea and Postgres rejects it for a jsonb column (22P02).
	RawPayload datatypes.JSON `gorm:"column:raw_payload;type:jsonb;not null;default:'{}'" json:"-"`
	Error      string     `gorm:"type:text;not null;default:''" json:"error,omitempty"`
	OccurredAt *time.Time `json:"occurred_at,omitempty"`
	CreatedAt  time.Time  `gorm:"not null;default:now()" json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// TableName pins the table name.
func (MarketingEmailEvent) TableName() string { return "marketing_email_events" }

// Event queue statuses.
const (
	EventStatusPending    = "pending"
	EventStatusProcessing = "processing"
	EventStatusDone       = "done"
	EventStatusFailed     = "failed"
)

// Resend webhook event types (the strings Resend sends). Delivered/sent feed the
// rate denominator; bounced/complained/unsubscribed drive suppression.
const (
	ResendTypeSent          = "email.sent"
	ResendTypeDelivered     = "email.delivered"
	ResendTypeDeliveryDelay = "email.delivery_delayed"
	ResendTypeBounced       = "email.bounced"
	ResendTypeComplained    = "email.complained"
	ResendTypeOpened        = "email.opened"
	ResendTypeClicked       = "email.clicked"
	ResendTypeFailed        = "email.failed"
)

// resendEnvelope is the minimal shape M4 parses from a Resend webhook body — only
// the fields org-resolution + classification need. Everything else stays in raw_payload.
type resendEnvelope struct {
	Type      string          `json:"type"`
	CreatedAt string          `json:"created_at"`
	Data      resendEventData `json:"data"`
}

type resendEventData struct {
	EmailID string          `json:"email_id"`
	From    string          `json:"from"`
	To      json.RawMessage `json:"to"` // Resend sends an array of strings; tolerate a bare string
	Bounce  *resendBounce   `json:"bounce,omitempty"`
	// Some Resend bounce payloads carry the classification at the top of data rather
	// than under a nested bounce object; accept both.
	BounceType string `json:"bounce_type,omitempty"`
}

type resendBounce struct {
	Type    string `json:"type"`
	SubType string `json:"subType"`
	Message string `json:"message"`
}

// parseResendEnvelope decodes the minimal fields. A decode error is a permanent
// (unprocessable) event — the caller acks 200 and drops.
func parseResendEnvelope(raw []byte) (resendEnvelope, error) {
	var e resendEnvelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return resendEnvelope{}, err
	}
	return e, nil
}

// recipient extracts the first recipient address from data.to (an array of strings,
// or a bare string), normalized.
func (d resendEventData) recipient() string {
	raw := strings.TrimSpace(string(d.To))
	if raw == "" || raw == "null" {
		return ""
	}
	// Array form.
	var arr []string
	if json.Unmarshal(d.To, &arr) == nil && len(arr) > 0 {
		return extractEmail(arr[0])
	}
	// Bare string form.
	var s string
	if json.Unmarshal(d.To, &s) == nil {
		return extractEmail(s)
	}
	return ""
}

// fromDomain returns the lowercased domain of the message's From address.
func (d resendEventData) fromDomain() string {
	email := extractEmail(d.From)
	at := strings.LastIndexByte(email, '@')
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// extractEmail pulls the bare address out of a possibly-display-name-wrapped value
// ("Acme <noreply@acme.com>" -> "noreply@acme.com"), lowercased + trimmed.
func extractEmail(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(v); err == nil {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	// Fall back to a naive <...> strip.
	if i := strings.IndexByte(v, '<'); i >= 0 {
		if j := strings.IndexByte(v[i:], '>'); j > 0 {
			return strings.ToLower(strings.TrimSpace(v[i+1 : i+j]))
		}
	}
	return strings.ToLower(v)
}

// bounceClass returns "permanent" or "transient" for a bounce event, from the
// nested bounce.type (or the flat bounce_type). Anything not clearly Permanent is
// treated as transient (soft) — conservative: never hard-suppress on an ambiguous
// classification, only on an explicit Permanent.
func (e resendEnvelope) bounceClass() string {
	t := e.Data.BounceType
	if e.Data.Bounce != nil && e.Data.Bounce.Type != "" {
		t = e.Data.Bounce.Type
	}
	if strings.EqualFold(strings.TrimSpace(t), "Permanent") {
		return "permanent"
	}
	return "transient"
}

// suppressionAction describes what a webhook event does to the ledger.
type suppressionAction struct {
	Reason     string // "" = no suppression (delivered/opened/etc)
	SoftBounce bool   // true => accumulate soft_bounce_count instead of a direct write
}

// classify maps a Resend event to its suppression action. Delivered/sent/opened/
// clicked/delayed do not suppress (they are ledger-only, and delivered feeds the
// rate denominator).
func classify(e resendEnvelope) suppressionAction {
	switch e.Type {
	case ResendTypeComplained:
		return suppressionAction{Reason: ReasonComplaint}
	case ResendTypeBounced:
		if e.bounceClass() == "permanent" {
			return suppressionAction{Reason: ReasonHardBounce}
		}
		return suppressionAction{Reason: ReasonSoftBounce, SoftBounce: true}
	case ResendTypeFailed:
		// A terminal provider failure to a specific address behaves like a hard bounce.
		return suppressionAction{Reason: ReasonHardBounce}
	default:
		// sent/delivered/delivery_delayed/opened/clicked and any unknown type: no suppression.
		return suppressionAction{}
	}
}

// parseOccurredAt best-effort parses Resend's created_at (RFC3339). Zero time on failure.
func parseOccurredAt(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return &t
	}
	return nil
}
