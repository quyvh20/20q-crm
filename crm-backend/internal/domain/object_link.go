package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ObjectLink is one polymorphic relationship edge connecting any record to any
// record (plan §3.3, §4.3). The endpoints are addressed by (slug, id) — the same
// shape record_shares uses — so no DB foreign key constrains from_id/to_id.
// Referential integrity is app-enforced: RecordService cascade-soft-deletes every
// link touching a deleted record (R3).
//
// Tags ride on the same table for non-contact objects: relation_key='tags',
// to_slug='tag', to_id=<tag id> (D4). Contacts keep their legacy contact_tags
// store until the workflow engine cuts over in P7; RecordService hides that split
// behind one uniform tag API.
type ObjectLink struct {
	ID          uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	FromSlug    string         `gorm:"size:100;not null" json:"from_slug"`
	FromID      uuid.UUID      `gorm:"type:uuid;not null" json:"from_id"`
	ToSlug      string         `gorm:"size:100;not null" json:"to_slug"`
	ToID        uuid.UUID      `gorm:"type:uuid;not null" json:"to_id"`
	RelationKey string         `gorm:"size:100;not null" json:"relation_key"`
	CreatedBy   *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	DeletedAt   gorm.DeletedAt `json:"-"`
}

func (ObjectLink) TableName() string { return "object_links" }

// ============================================================
// Link / tag DTOs (the uniform relationship surface)
// ============================================================

// LinkInput is the create-a-relationship payload. RelationKey names the
// relationship from the source's point of view (e.g. "account", "assets").
type LinkInput struct {
	RelationKey string    `json:"relation_key" binding:"required"`
	ToSlug      string    `json:"to_slug" binding:"required"`
	ToID        uuid.UUID `json:"to_id" binding:"required"`
}

// LinkView is one resolved outgoing relationship: the edge plus the target
// record's current display title, so the UI never has to render a raw UUID.
type LinkView struct {
	ID          uuid.UUID `json:"id"`
	RelationKey string    `json:"relation_key"`
	ToSlug      string    `json:"to_slug"`
	ToID        uuid.UUID `json:"to_id"`
	ToDisplay   string    `json:"to_display"`
}

// ============================================================
// Port
// ============================================================

// LinkRepository persists relationships. It owns object_links (the universal
// store) and bridges the legacy contact_tags join table so RecordService can
// expose one tag API across every object (D4) without touching the contact repo.
type LinkRepository interface {
	Create(ctx context.Context, link *ObjectLink) error
	// GetByID returns the active link or (nil, nil) when absent.
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*ObjectLink, error)
	// SoftDelete marks one link deleted; returns (false, nil) when nothing matched.
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	// FindEdge returns the matching active edge or (nil, nil) — used to keep
	// AddLink idempotent and to recover from a unique-index race.
	FindEdge(ctx context.Context, orgID uuid.UUID, fromSlug string, fromID uuid.UUID, relationKey, toSlug string, toID uuid.UUID) (*ObjectLink, error)
	// ListFrom returns a record's active outgoing edges (both relations and tags).
	ListFrom(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]ObjectLink, error)
	// CascadeSoftDelete soft-deletes every active edge where the record is either
	// endpoint. Called when a record is deleted (R3).
	CascadeSoftDelete(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error

	// --- legacy contact_tags bridge (retired in P7) ---
	AddContactTag(ctx context.Context, contactID, tagID uuid.UUID) error
	RemoveContactTag(ctx context.Context, contactID, tagID uuid.UUID) (bool, error)
	ListContactTagIDs(ctx context.Context, contactID uuid.UUID) ([]uuid.UUID, error)
}
