package marketing

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Merge-scope roots a campaign may hydrate (B4). Default = contact + org + campaign;
// company is the only optionally-declared extra (one-hop from contact.company_id).
// deal / custom-object are deferred (no deterministic per-recipient resolver yet).
const (
	ScopeContact  = "contact"
	ScopeOrg      = "org"
	ScopeCampaign = "campaign"
	ScopeCompany  = "company"
)

// DefaultMergeScope is the safe default set of roots.
func DefaultMergeScope() []string { return []string{ScopeContact, ScopeOrg, ScopeCampaign} }

var validMergeRoots = map[string]bool{
	ScopeContact: true, ScopeOrg: true, ScopeCampaign: true, ScopeCompany: true,
}

// CampaignContent is one authored marketing email: the block document (edit source),
// the compiled email-safe HTML (send source, still carrying {{merge}} tokens resolved
// per-recipient at send), a derived plain-text alternative, and the declared merge
// scope. A NEW table — NOT an ALTER of automation_email_templates (GORM ALTER fails
// silently on prod). Soft-deletable.
type CampaignContent struct {
	ID                uuid.UUID      `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID             uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name              string         `gorm:"type:varchar(160);not null" json:"name"`
	Subject           string         `gorm:"type:varchar(998);not null;default:''" json:"subject"`
	Preheader         string         `gorm:"type:varchar(255);not null;default:''" json:"preheader"`
	// BodyJSON is the block/document model (the edit source). BodyHTMLCompiled is the
	// email-safe nested-table HTML produced by the mjml compiler (the send source) —
	// it still contains {{path|fallback}} tokens, resolved per recipient at send.
	BodyJSON          datatypes.JSON `gorm:"type:jsonb;not null;default:'{\"blocks\":[]}'" json:"body_json"`
	BodyHTMLCompiled  string         `gorm:"type:text;not null;default:''" json:"body_html_compiled"`
	PlainText         string         `gorm:"type:text;not null;default:''" json:"plain_text"`
	MergeScope        datatypes.JSON `gorm:"type:jsonb;not null;default:'[\"contact\",\"org\",\"campaign\"]'" json:"merge_scope"`
	CompiledSizeBytes int            `gorm:"type:int;not null;default:0" json:"compiled_size_bytes"`
	CompiledAt        *time.Time     `json:"compiled_at,omitempty"`
	CreatedBy         *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt         time.Time      `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt         time.Time      `gorm:"not null;default:now()" json:"updated_at"`
	DeletedAt         gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName pins the table name.
func (CampaignContent) TableName() string { return "marketing_campaign_content" }

// ── Block document model ─────────────────────────────────────────────────────

// Block types the composer + compiler support (M6). A constrained set keeps the
// MJML output correct (bulletproof buttons, ghost-table columns) without an
// open-ended node zoo.
const (
	BlockText    = "text"
	BlockHeading = "heading"
	BlockButton  = "button"
	BlockImage   = "image"
	BlockDivider = "divider"
	BlockSpacer  = "spacer"
	BlockColumns = "columns"
)

// Block is one node in the document. Text-bearing blocks (text/heading) carry HTML
// authored via the reused TipTap surface, embedding {{merge}} tokens. Other blocks
// are plain typed config. Columns hold up to a few sub-blocks per column.
type Block struct {
	ID    string  `json:"id"`
	Type  string  `json:"type"`
	Text  string  `json:"text,omitempty"`  // text/heading: serialized HTML w/ {{}} tokens
	Level int     `json:"level,omitempty"` // heading: 1-3
	Align string  `json:"align,omitempty"` // left|center|right
	Label string  `json:"label,omitempty"` // button label
	Href  string  `json:"href,omitempty"`  // button/image link
	Src   string  `json:"src,omitempty"`   // image src
	Alt   string  `json:"alt,omitempty"`   // image alt
	Height int    `json:"height,omitempty"` // spacer px
	// Columns: each entry is one column's ordered sub-blocks (1 level deep only).
	Columns [][]Block `json:"columns,omitempty"`
}

// BlockDocument is the persisted body_json shape.
type BlockDocument struct {
	Blocks []Block `json:"blocks"`
}
