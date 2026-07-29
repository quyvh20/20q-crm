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
	// Folder is a flat organizational label ("" = unfiled). Folders exist
	// implicitly as the distinct set of values — no separate table.
	Folder            string         `gorm:"type:varchar(80);not null;default:''" json:"folder"`
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
	// BlockHTML is an author-supplied raw HTML snippet (Klaviyo-style custom
	// block). Content rides in Text; it is sanitized with the WIDE email policy
	// (tables/inline styles kept, scripts and structural MJML stripped) and
	// emitted through mj-raw.
	BlockHTML = "html"
	// BlockSocial renders mj-social icon links (network names gated to MJML's
	// built-in icon set). BlockVideo is the email-safe video pattern: thumbnail
	// image + watch button, both linking to Href. BlockQuote is a styled
	// testimonial (Text = rich HTML, Label = attribution). BlockMenu is a
	// horizontal nav of Items links.
	BlockSocial = "social"
	BlockVideo  = "video"
	BlockQuote  = "quote"
	BlockMenu   = "menu"
	// BlockProduct is a product card (image + title + description + price +
	// button). The builder fills it from a CRM record, but the stored content is
	// a self-contained snapshot — sent emails never chase live record data.
	BlockProduct = "product"
)

// SocialLink is one mj-social entry. Network must be in socialNetworks.
type SocialLink struct {
	Network string `json:"network"`
	Href    string `json:"href"`
}

// LinkItem is one menu entry.
type LinkItem struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// BlockCondition is one show-only-if rule. Field is a merge path (same catalog
// as merge tags: contact.*, company.*, org.name, ...); Op is gated to
// condOps at save time.
type BlockCondition struct {
	Field string `json:"field"`
	Op    string `json:"op"` // exists | not_exists | eq | neq | contains
	Value string `json:"value,omitempty"`
}

// FooterStyle customizes the always-present compliance footer. The unsubscribe
// link and postal address ALWAYS render (CAN-SPAM/GDPR — the compile sentinel
// enforces it); authors control the look and can add their own text above.
type FooterStyle struct {
	Bg    string `json:"bg,omitempty"`    // footer section background (#hex)
	Color string `json:"color,omitempty"` // footer text color (#hex)
	Text  string `json:"text,omitempty"`  // optional HTML shown above the compliance lines
}

// Block is one node in the document. Text-bearing blocks (text/heading) carry HTML
// authored via the reused TipTap surface, embedding {{merge}} tokens. Other blocks
// are plain typed config. Columns hold up to a few sub-blocks per column.
type Block struct {
	ID    string  `json:"id"`
	Type  string  `json:"type"`
	Text  string  `json:"text,omitempty"`  // text/heading/quote: serialized HTML w/ {{}} tokens; html: raw snippet
	Level int     `json:"level,omitempty"` // heading: 1-3
	Align string  `json:"align,omitempty"` // left|center|right (text/heading/button/image/social)
	Label string  `json:"label,omitempty"` // button label; video watch-button label; quote attribution
	Href  string  `json:"href,omitempty"`  // button/image/video link
	Src   string  `json:"src,omitempty"`   // image/video-thumbnail src
	Alt   string  `json:"alt,omitempty"`   // image alt
	Height int    `json:"height,omitempty"` // spacer px
	Bg    string  `json:"bg,omitempty"`    // root blocks: section background (#hex)
	Title string  `json:"title,omitempty"` // product: name line
	Price string  `json:"price,omitempty"` // product: price line (free text — currency their way)

	// Per-block styling (all optional; zero value = compiler default):
	Color     string `json:"color,omitempty"`     // text/heading text; divider line; quote accent (#hex)
	Size      int    `json:"size,omitempty"`      // text/heading font px; social icon px
	Thickness int    `json:"thickness,omitempty"` // divider px
	Radius    int    `json:"radius,omitempty"`    // button/image border-radius px
	WidthPx   int    `json:"width_px,omitempty"`  // image width px
	PadY      *int   `json:"pad_y,omitempty"`     // section vertical padding override (0 allowed — pointer keeps 0 distinct from unset)
	BtnBg     string `json:"btn_bg,omitempty"`    // button background (#hex)
	BtnColor  string `json:"btn_color,omitempty"` // button text color (#hex)

	// Device visibility (Brevo-style "Content visibility"). Implemented with
	// media-query classes: clients without media-query support (old Outlook)
	// show everything — the industry-standard caveat.
	HideMobile  bool `json:"hide_mobile,omitempty"`
	HideDesktop bool `json:"hide_desktop,omitempty"`

	// Cond shows the block only to recipients matching the rule — evaluated
	// per recipient at RENDER time (the compiler embeds a marker; see
	// ApplyConditions). Root blocks only.
	Cond *BlockCondition `json:"cond,omitempty"`

	Social []SocialLink `json:"social,omitempty"` // social block entries
	Items  []LinkItem   `json:"items,omitempty"`  // menu block entries

	// Columns: each entry is one column's ordered sub-blocks (1 level deep only).
	Columns [][]Block `json:"columns,omitempty"`
}

// BlockDocument is the persisted body_json shape.
type BlockDocument struct {
	Blocks []Block `json:"blocks"`
	// BodyBg colors the mj-body (the full-width backdrop AND, since sections
	// default transparent, the content area behind unset-bg blocks).
	BodyBg string `json:"body_bg,omitempty"`
	// Global email styles (Klaviyo-style "Styles" panel).
	Width      int          `json:"width,omitempty"`       // content width px (480-800; default 600)
	FontFamily string       `json:"font_family,omitempty"` // key into fontStacks (allowlisted)
	TextColor  string       `json:"text_color,omitempty"`  // default mj-text color (#hex)
	LinkColor  string       `json:"link_color,omitempty"`  // <a> color (#hex)
	Footer     *FooterStyle `json:"footer,omitempty"`      // compliance-footer styling
}
