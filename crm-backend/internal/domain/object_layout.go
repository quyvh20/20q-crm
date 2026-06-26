package domain

// ============================================================
// P8 — Per-role detail layouts
// ============================================================
//
// An admin can author one or more named layouts for an object's detail page.
// Each layout is an ordered list of sections; each section has a label, a
// 1-or-2-column grid, and an ordered list of field slots. Multiple layouts
// may be assigned to different roles; unassigned roles fall back to the
// is_default layout, then to a synthesized field-order view (today's behaviour).
//
// Layout is presentation only. FLS (field_permissions) remains the security
// boundary: hidden fields are stripped before the schema response leaves the
// server, so they never appear in a layout section even if the admin added them.
// "Absent from layout" is never treated as access control (plan §5.3).

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// LayoutField is one field slot within a LayoutSection.
// Width controls horizontal span in a 2-column section:
//   - "full"  — spans both columns
//   - "half"  — occupies one column (default)
//
// Ignored for 1-column sections.
type LayoutField struct {
	Key   string `json:"key"`
	Width string `json:"width,omitempty"` // "full" | "half"
}

// LayoutSection is one collapsible panel in an object's detail layout.
// Columns is 1 (stacked) or 2 (side-by-side grid). FLS-hidden fields are
// stripped server-side before the section reaches the frontend, so the
// renderer never has to enforce access — it just renders what it gets.
type LayoutSection struct {
	ID      string        `json:"id"`
	Label   string        `json:"label"`
	Columns int           `json:"columns"` // 1 or 2
	Fields  []LayoutField `json:"fields"`
}

// ObjectLayout is a named detail-page layout for one object. The layout JSONB
// column stores an ordered []LayoutSection (marshalled/unmarshalled explicitly
// rather than via GORM hooks, to keep the conversion visible and testable).
type ObjectLayout struct {
	ID         uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID      uuid.UUID      `gorm:"type:uuid;not null"                               json:"org_id"`
	ObjectSlug string         `gorm:"size:100;not null"                                json:"object_slug"`
	Name       string         `gorm:"size:255;not null"                                json:"name"`
	// RawLayout holds the raw JSONB bytes as stored. Callers use Sections after
	// calling UnmarshalSections; they must call MarshalSections before writing.
	RawLayout JSON            `gorm:"column:layout;type:jsonb;not null;default:'[]'"   json:"-"`
	// Sections is the decoded view of RawLayout. Populated on every read by the
	// repository; must be set before any write.
	Sections  []LayoutSection `gorm:"-"                                                json:"layout"`
	IsDefault bool            `gorm:"not null;default:false"                           json:"is_default"`
	CreatedAt time.Time       `                                                         json:"created_at"`
	UpdatedAt time.Time       `                                                         json:"updated_at"`
	DeletedAt gorm.DeletedAt  `gorm:"index"                                            json:"-"`
}

func (ObjectLayout) TableName() string { return "object_layouts" }

// MarshalSections encodes Sections → RawLayout before any DB write.
func (l *ObjectLayout) MarshalSections() error {
	if l.Sections == nil {
		l.Sections = []LayoutSection{}
	}
	raw, err := json.Marshal(l.Sections)
	if err != nil {
		return err
	}
	l.RawLayout = raw
	return nil
}

// UnmarshalSections decodes RawLayout → Sections after any DB read.
func (l *ObjectLayout) UnmarshalSections() error {
	if len(l.RawLayout) == 0 {
		l.Sections = []LayoutSection{}
		return nil
	}
	return json.Unmarshal(l.RawLayout, &l.Sections)
}

// ObjectLayoutRole routes one org role to one layout for one (org, object).
// The unique index uix_object_layout_roles_one_per_role on (org_id, object_slug,
// role_id) ensures exactly one layout per role per object — no ambiguity.
type ObjectLayoutRole struct {
	ID         uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID      uuid.UUID `gorm:"type:uuid;not null"                               json:"org_id"`
	LayoutID   uuid.UUID `gorm:"type:uuid;not null"                               json:"layout_id"`
	ObjectSlug string    `gorm:"size:100;not null"                                json:"object_slug"`
	RoleID     uuid.UUID `gorm:"type:uuid;not null"                               json:"role_id"`
}

func (ObjectLayoutRole) TableName() string { return "object_layout_roles" }

// ============================================================
// DTOs
// ============================================================

// LayoutWithRoles is the full admin view of one layout: the layout itself plus
// the UUIDs of the roles it is currently assigned to.
type LayoutWithRoles struct {
	ObjectLayout
	RoleIDs []uuid.UUID `json:"role_ids"`
}

// CreateLayoutInput is the payload for POST /api/registry/objects/:slug/layouts.
type CreateLayoutInput struct {
	Name      string          `json:"name"      binding:"required"`
	Sections  []LayoutSection `json:"layout"`
	IsDefault bool            `json:"is_default"`
	RoleIDs   []uuid.UUID     `json:"role_ids"`
}

// UpdateLayoutInput is the payload for PATCH /api/registry/objects/:slug/layouts/:id.
// Nil pointers are not updated, so callers can patch one attribute at a time.
type UpdateLayoutInput struct {
	Name      *string          `json:"name"`
	Sections  *[]LayoutSection `json:"layout"`
	IsDefault *bool            `json:"is_default"`
}

// ============================================================
// Ports
// ============================================================

// ObjectLayoutRepository persists layouts and their role assignments.
type ObjectLayoutRepository interface {
	// LoadOrgLayouts returns all non-deleted layouts for the org, grouped by
	// object_slug, with Sections already decoded. Used to warm the per-org cache.
	LoadOrgLayouts(ctx context.Context, orgID uuid.UUID) (map[string][]ObjectLayout, error)
	// LoadOrgLayoutRoleMap returns slug → roleName → layoutID for the org in one
	// JOIN query, so the usecase cache resolves roles without extra DB round-trips.
	LoadOrgLayoutRoleMap(ctx context.Context, orgID uuid.UUID) (map[string]map[string]uuid.UUID, error)

	// GetLayout returns one layout (with Sections decoded) owned by the org, or nil.
	GetLayout(ctx context.Context, orgID, id uuid.UUID) (*ObjectLayout, error)
	// ListLayouts returns all non-deleted layouts for an object, ordered by
	// creation time, with Sections decoded.
	ListLayouts(ctx context.Context, orgID uuid.UUID, slug string) ([]ObjectLayout, error)
	// CreateLayout inserts a new layout. If IsDefault is true it clears any
	// existing default for the same (org, slug) in the same transaction.
	CreateLayout(ctx context.Context, layout *ObjectLayout) error
	// UpdateLayout saves edits. If IsDefault is now true it clears other defaults
	// for the same (org, slug) in the same transaction.
	UpdateLayout(ctx context.Context, layout *ObjectLayout) error
	// DeleteLayout soft-deletes a layout (sets deleted_at).
	DeleteLayout(ctx context.Context, orgID, id uuid.UUID) error

	// SetLayoutRoles replaces the role-assignment rows for a layout.
	// Existing assignments not in roleIDs are deleted; new ones are inserted.
	SetLayoutRoles(ctx context.Context, orgID uuid.UUID, layoutID uuid.UUID, slug string, roleIDs []uuid.UUID) error
	// ListLayoutRoleIDs returns the role UUIDs currently assigned to a layout.
	ListLayoutRoleIDs(ctx context.Context, orgID, layoutID uuid.UUID) ([]uuid.UUID, error)
}

// ObjectLayoutUseCase is the per-request resolver and the admin CRUD surface.
type ObjectLayoutUseCase interface {
	// ResolveLayout returns the effective layout sections for a caller with
	// FLS-hidden keys already stripped out of every section. Returns nil when no
	// layout is configured — the renderer falls back to flat field order.
	// Resolver precedence: role-assigned → is_default → nil.
	// Results are served from a per-org cache (60-second TTL, busted on any write).
	ResolveLayout(ctx context.Context, orgID uuid.UUID, slug, callerRole string, hiddenKeys map[string]bool) ([]LayoutSection, error)

	// Admin CRUD (all admin-only at the HTTP layer):
	ListLayouts(ctx context.Context, orgID uuid.UUID, slug string) ([]LayoutWithRoles, error)
	GetLayout(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*LayoutWithRoles, error)
	CreateLayout(ctx context.Context, orgID uuid.UUID, slug string, in CreateLayoutInput) (*LayoutWithRoles, error)
	UpdateLayout(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, in UpdateLayoutInput) (*ObjectLayout, error)
	DeleteLayout(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error
	SetLayoutRoles(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, roleIDs []uuid.UUID) error

	// Invalidate drops the per-org cache so the next request loads fresh data.
	Invalidate(orgID uuid.UUID)
}
