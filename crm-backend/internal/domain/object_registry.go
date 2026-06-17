package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ObjectDef is a registry descriptor row. It makes any object — system or custom
// — describable the same way above storage. In P2 the table holds only the three
// system objects (contact/deal/company) per org, seeded idempotently by
// EnsureSystemObjects. Custom objects continue to live in custom_object_defs and
// are merged into the registry view at read time; they are not copied here until
// the P7 cutover, so there is no dual-write to keep in sync.
type ObjectDef struct {
	ID          uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID       uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	Slug        string    `gorm:"size:100;not null" json:"slug"`
	Label       string    `gorm:"size:255;not null" json:"label"`
	LabelPlural string    `gorm:"size:255;not null" json:"label_plural"`
	Icon        string    `gorm:"size:50;default:'📦'" json:"icon"`
	Color       string    `gorm:"size:20;default:'#6B7280'" json:"color"`
	IsSystem    bool      `gorm:"not null;default:false" json:"is_system"`
	// Storage is an internal flag ('table' | 'jsonb') and is never user-visible.
	Storage        string         `gorm:"size:10;not null;default:'jsonb'" json:"-"`
	RecordTable    *string        `gorm:"size:63" json:"-"`
	DisplayFieldID *uuid.UUID     `gorm:"type:uuid" json:"display_field_id,omitempty"`
	Searchable     bool           `gorm:"not null;default:false" json:"searchable"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (ObjectDef) TableName() string { return "object_defs" }

// ObjectField is one field of an ObjectDef. storage_kind records how the value is
// physically stored: 'column' (a native typed column on a system table, addressed
// via maps_to_column) or 'jsonb' (inside the row's custom_fields/data blob).
type ObjectField struct {
	ID           uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID        uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	ObjectDefID  uuid.UUID      `gorm:"type:uuid;not null" json:"object_def_id"`
	Key          string         `gorm:"size:100;not null" json:"key"`
	Label        string         `gorm:"size:255;not null" json:"label"`
	Type         string         `gorm:"size:30;not null" json:"type"`
	Options      JSON           `gorm:"type:jsonb;default:'[]'" json:"options"`
	TargetSlug   *string        `gorm:"size:100" json:"target_slug,omitempty"`
	IsRequired   bool           `gorm:"not null;default:false" json:"is_required"`
	IsUnique     bool           `gorm:"not null;default:false" json:"is_unique"`
	IsSystem     bool           `gorm:"not null;default:false" json:"is_system"`
	StorageKind  string         `gorm:"size:10;not null;default:'jsonb'" json:"-"`
	MapsToColumn *string        `gorm:"size:63" json:"-"`
	Position     int            `gorm:"not null;default:0" json:"position"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (ObjectField) TableName() string { return "object_fields" }

// ============================================================
// Read DTOs (the uniform shape every object — system or custom — is served as)
// ============================================================

// ObjectSummary is the lightweight per-object entry returned by the list
// endpoint. Record counts are intentionally deferred to RecordService (P3).
type ObjectSummary struct {
	Slug        string `json:"slug"`
	Label       string `json:"label"`
	LabelPlural string `json:"label_plural"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
	IsSystem    bool   `json:"is_system"`
	FieldCount  int    `json:"field_count"`
}

// ObjectDescriptor is the full schema for one object. The frontend (P3) renders
// any object from this single shape, system or custom alike.
type ObjectDescriptor struct {
	Slug         string            `json:"slug"`
	Label        string            `json:"label"`
	LabelPlural  string            `json:"label_plural"`
	Icon         string            `json:"icon"`
	Color        string            `json:"color"`
	IsSystem     bool              `json:"is_system"`
	DisplayField string            `json:"display_field"`
	Fields       []FieldDescriptor `json:"fields"`
}

// FieldDescriptor is one field in an ObjectDescriptor. storage_kind / maps_to_column
// are deliberately omitted — they are internal and never user-visible.
type FieldDescriptor struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Type       string   `json:"type"`
	Options    []string `json:"options,omitempty"`
	TargetSlug string   `json:"target_slug,omitempty"`
	IsSystem   bool     `json:"is_system"`
	Required   bool     `json:"required"`
	Unique     bool     `json:"unique,omitempty"`
}

// ============================================================
// Ports
// ============================================================

type ObjectRegistryRepository interface {
	// EnsureSystemObjects idempotently seeds the three system object defs and
	// their native fields for an org. Safe to call on every read.
	EnsureSystemObjects(ctx context.Context, orgID uuid.UUID) error
	ListDefs(ctx context.Context, orgID uuid.UUID) ([]ObjectDef, error)
	GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*ObjectDef, error)
	ListFields(ctx context.Context, objectDefID uuid.UUID) ([]ObjectField, error)
	// FieldCounts returns object_def_id → number of (non-deleted) fields for the org.
	FieldCounts(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]int, error)
}

type ObjectRegistryUseCase interface {
	// ListObjects returns every object (system + custom) as summaries.
	ListObjects(ctx context.Context, orgID uuid.UUID) ([]ObjectSummary, error)
	// GetSchema returns the full descriptor for one object by slug.
	GetSchema(ctx context.Context, orgID uuid.UUID, slug string) (*ObjectDescriptor, error)
}
