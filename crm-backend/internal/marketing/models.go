// Package marketing is the email-marketing module (plan email_marketing_plan.md).
//
// M1 builds only the foundation: the suppression & consent ledger and the
// IsSendable() pre-send chokepoint. There is deliberately NO send path here yet —
// sending is M7/M8. Every query is org-scoped by an explicit WHERE org_id = ?
// (there is no RLS/global-scope hook to lean on), and the ledger is keyed on a
// NORMALIZED email, never contact_id: contact.email is *string and
// case-sensitively unique, so several contacts can share one normalized address.
package marketing

import (
	"time"

	"github.com/google/uuid"
)

// Channel distinguishes the two kinds of send that share the send_email executor.
// It is supplied by the caller of IsSendable (Guardrail 9): a marketing send
// consults the full ledger + requires a lawful basis; a transactional send
// consults only global (scope=all) suppressions and is never blocked by an
// unsubscribe.
type Channel string

const (
	ChannelTransactional Channel = "transactional"
	ChannelMarketing     Channel = "marketing"
)

// Suppression reasons. Enum-as-string validated in-app (the schema has no PG ENUM
// type — Postgres has no CREATE TYPE ... IF NOT EXISTS, which the idempotent
// boot-guard regime needs).
const (
	ReasonHardBounce  = "hard_bounce"
	ReasonSoftBounce  = "soft_bounce"
	ReasonComplaint   = "complaint"
	ReasonUnsubscribe = "unsubscribe"
	ReasonManual      = "manual"
	ReasonGDPRErasure = "gdpr_erasure"
)

var validReasons = map[string]bool{
	ReasonHardBounce: true, ReasonSoftBounce: true, ReasonComplaint: true,
	ReasonUnsubscribe: true, ReasonManual: true, ReasonGDPRErasure: true,
}

// IsValidReason reports whether s is a known suppression reason.
func IsValidReason(s string) bool { return validReasons[s] }

// Suppression scopes. `all` blocks every send (bounce/complaint/deliverability);
// `marketing` blocks only marketing sends (unsubscribe).
const (
	ScopeAll       = "all"
	ScopeMarketing = "marketing"
)

var validScopes = map[string]bool{ScopeAll: true, ScopeMarketing: true}

// IsValidScope reports whether s is a known suppression scope.
func IsValidScope(s string) bool { return validScopes[s] }

// DefaultScopeForReason is the scope a suppression takes when the caller does not
// specify one. Bounces/complaints/erasure stop all mail; an unsubscribe stops only
// marketing; a manual add defaults to marketing (an admin manually suppressing is
// almost always honoring a do-not-market request).
func DefaultScopeForReason(reason string) string {
	switch reason {
	case ReasonHardBounce, ReasonSoftBounce, ReasonComplaint, ReasonGDPRErasure:
		return ScopeAll
	default: // unsubscribe, manual
		return ScopeMarketing
	}
}

// SoftBounceSuppressThreshold is the number of accumulated soft bounces at which a
// soft_bounce row starts blocking sends. Below it, a soft_bounce row is advisory
// (it tracks the count but does not suppress). The accumulation itself is M4's job
// (the webhook processor); IsSendable only reads the current count.
const SoftBounceSuppressThreshold = 3

// Marketing lifecycle status for an email address.
const (
	StatusSubscribed   = "subscribed"
	StatusUnsubscribed = "unsubscribed"
	StatusPending      = "pending"
	StatusCleaned      = "cleaned" // repeatedly bounced / scrubbed
)

var validStatuses = map[string]bool{
	StatusSubscribed: true, StatusUnsubscribed: true, StatusPending: true, StatusCleaned: true,
}

// IsValidStatus reports whether s is a known marketing status.
func IsValidStatus(s string) bool { return validStatuses[s] }

// Lawful bases for mailing a recipient (CAN-SPAM / CASL / GDPR). `express` and
// `double_opt_in` are affirmative opt-ins; the rest are non-express bases that
// still permit mailing (unexpired CASL implied, documented EBR/legitimate-interest).
const (
	BasisExpress                      = "express"
	BasisDoubleOptIn                  = "double_opt_in"
	BasisImpliedTransaction           = "implied_transaction"
	BasisImpliedInquiry               = "implied_inquiry"
	BasisExistingBusinessRelationship = "existing_business_relationship"
	BasisLegitimateInterest           = "legitimate_interest"
)

var validBases = map[string]bool{
	BasisExpress: true, BasisDoubleOptIn: true, BasisImpliedTransaction: true,
	BasisImpliedInquiry: true, BasisExistingBusinessRelationship: true, BasisLegitimateInterest: true,
}

// IsValidConsentBasis reports whether s is a known consent basis.
func IsValidConsentBasis(s string) bool { return validBases[s] }

// Suppression is one do-not-mail entry, keyed on a normalized email. It is EXEMPT
// from contact-deletion tombstoning (RedactForRecord never names this table) so an
// opt-out survives contact deletion, GDPR erasure, and CSV re-import.
type Suppression struct {
	ID              uuid.UUID  `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID           uuid.UUID  `gorm:"type:uuid;not null;index" json:"org_id"`
	EmailNormalized string     `gorm:"column:email_normalized;type:varchar(320);not null" json:"email"`
	Reason          string     `gorm:"type:varchar(32);not null" json:"reason"`
	Scope           string     `gorm:"type:varchar(16);not null;default:marketing" json:"scope"`
	// TopicID scopes a marketing suppression to one topic (M3). NULL = a global
	// marketing opt-out. Pointer so NULL is representable and the dedupe index's
	// COALESCE(topic_id, zero-uuid) collapses topic-less rows to one.
	TopicID         *uuid.UUID `gorm:"type:uuid" json:"topic_id,omitempty"`
	Source          string     `gorm:"type:varchar(64);not null;default:''" json:"source"`
	SoftBounceCount int        `gorm:"type:int;not null;default:0" json:"soft_bounce_count"`
	CreatedAt       time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

// TableName pins the table name (never let GORM pluralize/guess).
func (Suppression) TableName() string { return "marketing_suppressions" }

// Suppresses reports whether this row blocks a send on the given channel. A
// soft_bounce only suppresses once its count reaches the threshold; every other
// reason suppresses immediately within its scope. Transactional sends consult only
// scope=all; marketing sends consult scope=all and scope=marketing (topic-aware).
func (s Suppression) Suppresses(channel Channel, topicID *uuid.UUID) bool {
	// A soft bounce below threshold is advisory, not suppressing.
	if s.Reason == ReasonSoftBounce && s.SoftBounceCount < SoftBounceSuppressThreshold {
		return false
	}
	switch s.Scope {
	case ScopeAll:
		return true // blocks every channel
	case ScopeMarketing:
		if channel != ChannelMarketing {
			return false // an unsubscribe never blocks transactional mail
		}
		// A topic-less marketing suppression (global opt-out) blocks all topics; a
		// topic-scoped one blocks only that topic.
		if s.TopicID == nil {
			return true
		}
		return topicID != nil && *s.TopicID == *topicID
	default:
		return false
	}
}

// ContactMarketingState is the per-email consent/lifecycle record. On GDPR erasure
// it collapses to email_normalized + marketing_status (all provenance nulled) via
// RedactMarketingStateForEmail — so the nullable provenance columns are pointers.
type ContactMarketingState struct {
	ID              uuid.UUID  `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID           uuid.UUID  `gorm:"type:uuid;not null;index" json:"org_id"`
	EmailNormalized string     `gorm:"column:email_normalized;type:varchar(320);not null" json:"email"`
	ContactID       *uuid.UUID `gorm:"type:uuid" json:"contact_id,omitempty"`
	MarketingStatus string     `gorm:"type:varchar(16);not null;default:pending" json:"marketing_status"`
	ConsentBasis    *string    `gorm:"type:varchar(40)" json:"consent_basis,omitempty"`
	ConsentSource   *string    `gorm:"type:varchar(64)" json:"consent_source,omitempty"`
	ConsentAt       *time.Time `json:"consent_at,omitempty"`
	ConsentIP       *string    `gorm:"type:varchar(64)" json:"consent_ip,omitempty"`
	Region          *string    `gorm:"type:varchar(16)" json:"region,omitempty"`
	CASLExpiresAt   *time.Time `gorm:"column:casl_expires_at" json:"casl_expires_at,omitempty"`
	DoubleOptInAt   *time.Time `gorm:"column:double_opt_in_at" json:"double_opt_in_at,omitempty"`
	CreatedAt       time.Time  `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt       time.Time  `gorm:"not null;default:now()" json:"updated_at"`
}

// TableName pins the table name.
func (ContactMarketingState) TableName() string { return "contact_marketing_state" }

// HasLawfulBasis reports whether this state permits a MARKETING send at time `now`.
// Permitted when the status is subscribed, or a still-valid non-express basis
// exists (EBR / legitimate-interest are documented standing bases; implied CASL
// bases are valid only until casl_expires_at). unsubscribed/cleaned are never
// mailable. A nil receiver (no state row) is NOT consent.
func (s *ContactMarketingState) HasLawfulBasis(now time.Time) bool {
	if s == nil {
		return false
	}
	// A hard "no" always wins, whatever the recorded basis.
	if s.MarketingStatus == StatusUnsubscribed || s.MarketingStatus == StatusCleaned {
		return false
	}
	if s.MarketingStatus == StatusSubscribed {
		return true
	}
	// status is pending (or an unknown value): mailable ONLY under a still-valid
	// NON-express standing basis. An express opt-in / double opt-in must first
	// promote the status to 'subscribed' (the branch above) — a recorded-but-not-
	// yet-subscribed express/double-opt-in row (e.g. a double opt-in whose
	// confirmation link was never clicked) is exactly what must NOT be mailed
	// (plan Guardrail 5 excludes pending/unconfirmed; double-opt-in confirmation is
	// a deferred fast-follow).
	if s.ConsentBasis == nil {
		return false
	}
	switch *s.ConsentBasis {
	case BasisExistingBusinessRelationship, BasisLegitimateInterest:
		return true
	case BasisImpliedTransaction, BasisImpliedInquiry:
		// CASL implied consent expires; null expiry means "no recorded expiry" → valid.
		return s.CASLExpiresAt == nil || s.CASLExpiresAt.After(now)
	default:
		// express / double_opt_in at a non-subscribed status, or an unknown basis.
		return false
	}
}
