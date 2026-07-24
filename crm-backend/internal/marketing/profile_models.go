package marketing

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// OrgMarketingProfile is the per-org marketing sender identity + compliance data
// (M3). One row per org (org_id is the primary key). It carries the CAN-SPAM
// physical postal address rendered in every marketing footer, the from-identity a
// marketing send uses, and marketing_paused — the org-level circuit-breaker target
// M4 sets when the complaint rate crosses its threshold (transactional keeps
// flowing regardless). A marketing send is BLOCKED unless a complete profile exists
// and marketing is not paused (SenderProfileSendable).
type OrgMarketingProfile struct {
	OrgID                 uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	FromName              string    `gorm:"type:varchar(160);not null;default:''" json:"from_name"`
	ReplyTo               string    `gorm:"type:varchar(320);not null;default:''" json:"reply_to"`
	PhysicalPostalAddress string    `gorm:"column:physical_postal_address;type:text;not null;default:''" json:"physical_postal_address"`
	// MarketingPaused is the M4 breaker's pause target. Set on this table (not a
	// per-campaign column that does not exist until M7) so M4 is independently
	// deployable. Admin can resume via the dedicated resume action; the profile PUT
	// never changes it (so saving the address can't accidentally un-pause a breaker).
	MarketingPaused bool      `gorm:"column:marketing_paused;type:boolean;not null;default:false" json:"marketing_paused"`
	CreatedAt       time.Time `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt       time.Time `gorm:"not null;default:now()" json:"updated_at"`
}

// TableName pins the table name.
func (OrgMarketingProfile) TableName() string { return "org_marketing_profile" }

// SenderProfileSendable reports whether the org's sender profile currently permits
// a MARKETING send, with a stable reason when not. The gate: a profile row must
// exist with a non-empty physical postal address AND from-name, and marketing must
// not be paused. This is the "block send if the postal address / from identity is
// absent" control (plan Guardrails 4 + 7). A nil profile is never sendable.
func SenderProfileSendable(p *OrgMarketingProfile) (bool, string) {
	if p == nil {
		return false, "no_profile"
	}
	if p.MarketingPaused {
		return false, "marketing_paused"
	}
	if strings.TrimSpace(p.PhysicalPostalAddress) == "" {
		return false, "no_postal_address"
	}
	if strings.TrimSpace(p.FromName) == "" {
		return false, "no_from_name"
	}
	return true, ""
}

// MarketingTopic is an optional per-org subscription category (M3). Its
// opt_in_default is IMMUTABLE after creation (Resend's topics behave the same and
// flipping a default silently re-mails or silently drops people). Topics have no
// soft-delete, so the inline UNIQUE(org_id, name) is a correct table constraint (a
// partial unique would be needed only if rows were soft-deletable).
type MarketingTopic struct {
	ID           uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID        uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	Name         string    `gorm:"type:varchar(160);not null" json:"name"`
	Description  string    `gorm:"type:varchar(500);not null;default:''" json:"description"`
	OptInDefault bool      `gorm:"column:opt_in_default;type:boolean;not null;default:false" json:"opt_in_default"`
	CreatedAt    time.Time `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt    time.Time `gorm:"not null;default:now()" json:"updated_at"`
}

// TableName pins the table name.
func (MarketingTopic) TableName() string { return "marketing_topics" }
