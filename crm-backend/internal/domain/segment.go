package domain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Segment types.
const (
	SegmentTypeStatic  = "static"  // an explicit contact list (marketing_segment_static_members)
	SegmentTypeDynamic = "dynamic" // a saved boolean predicate compiled to SQL at query time
)

// SegmentFilter is the boolean AND/OR/NOT AST for a dynamic segment (M5). It mirrors
// ReportFilterGroup (op/rules OR field/operator/value) and adds NOT groups and a tag
// leaf. Exactly one kind per node, resolved in precedence order: tag leaf (TagID set)
// → field leaf (Field set) → group (Op/Rules). The whole tree is compiled to
// fully-parameterized SQL by the repository (never interpolated).
type SegmentFilter struct {
	Op    string          `json:"op,omitempty"` // group: "and" | "or" | "not"
	Rules []SegmentFilter `json:"rules,omitempty"`
	// Field leaf:
	Field    string `json:"field,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    any    `json:"value,omitempty"`
	// Tag leaf: presence of TagID makes this a tag membership test; TagNot negates it.
	TagID  string `json:"tag_id,omitempty"`
	TagNot bool   `json:"tag_not,omitempty"`
}

// IsTagLeaf reports a tag membership node.
func (f SegmentFilter) IsTagLeaf() bool { return f.TagID != "" }

// IsFieldLeaf reports a field condition node.
func (f SegmentFilter) IsFieldLeaf() bool { return f.TagID == "" && f.Field != "" }

// Touched walks the AST collecting the field keys and tag ids it references, so the
// usecase can FLS-check fields (reject hidden) and validate tag ownership before
// compiling — pure (no SQL), mirroring the precedence tag → field → group.
func (f SegmentFilter) Touched(fields *[]string, tags *[]string) {
	if f.IsTagLeaf() {
		*tags = append(*tags, f.TagID)
		return
	}
	if f.IsFieldLeaf() {
		*fields = append(*fields, f.Field)
		return
	}
	for _, r := range f.Rules {
		r.Touched(fields, tags)
	}
}

// Segment is a saved audience: a dynamic predicate or a static list. Soft-deletable.
type Segment struct {
	ID            uuid.UUID      `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID         uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name          string         `gorm:"type:varchar(160);not null" json:"name"`
	Type          string         `gorm:"type:varchar(16);not null;default:'dynamic'" json:"type"`
	// Definition is the SegmentFilter AST (dynamic) — datatypes.JSON so GORM binds it
	// as jsonb, never bytea (the M4 22P02 lesson). Empty '{}' for a static list.
	Definition    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"definition"`
	Materialized  bool           `gorm:"type:boolean;not null;default:false" json:"materialized"`
	CountCached   int            `gorm:"type:int;not null;default:0" json:"count_cached"`
	CountCachedAt *time.Time     `json:"count_cached_at,omitempty"`
	RefreshedAt   *time.Time     `json:"refreshed_at,omitempty"`
	CreatedBy     *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt     time.Time      `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"not null;default:now()" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
	// DefinitionRestricted is a COMPUTED read-only flag (never stored): set on Get/List
	// when the caller's FLS mask hides a field the definition filters on, in which case
	// Definition is blanked in the returned copy so the hidden field key/threshold don't
	// leak. Such a caller also cannot edit the segment (Update rejects them).
	DefinitionRestricted bool `gorm:"-" json:"definition_restricted,omitempty"`
}

// TableName pins the table name.
func (Segment) TableName() string { return "marketing_segments" }

// SegmentStaticMember is an explicit membership row for a static list (composite PK,
// no soft-delete — membership is added/removed physically).
type SegmentStaticMember struct {
	SegmentID uuid.UUID `gorm:"type:uuid;primaryKey" json:"segment_id"`
	ContactID uuid.UUID `gorm:"type:uuid;primaryKey" json:"contact_id"`
	Source    string    `gorm:"type:varchar(32);not null;default:''" json:"source"`
}

// TableName pins the table name.
func (SegmentStaticMember) TableName() string { return "marketing_segment_static_members" }

// SegmentMember is a materialized dynamic-segment membership row (opt-in
// materialization; composite PK, no soft-delete — incremental upsert + prune).
type SegmentMember struct {
	SegmentID uuid.UUID `gorm:"type:uuid;primaryKey" json:"segment_id"`
	ContactID uuid.UUID `gorm:"type:uuid;primaryKey" json:"contact_id"`
	MatchedAt time.Time `gorm:"not null;default:now()" json:"matched_at"`
}

// TableName pins the table name.
func (SegmentMember) TableName() string { return "marketing_segment_members" }

// SegmentInput is the create/update request body.
type SegmentInput struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Definition json.RawMessage `json:"definition"`
}

// SegmentContactRow is one preview row (id + display fields only — no arbitrary
// field values, so preview never leaks a column the caller couldn't select).
type SegmentContactRow struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
}

// SegmentPreview is the preview payload: a sample of matching contacts + the total.
type SegmentPreview struct {
	Contacts []SegmentContactRow `json:"contacts"`
	Count    int                 `json:"count"`
}

// SegmentUseCase is the M5 audiences surface (route-gated by marketing.manage). Every
// preview/count re-derives the caller's view (OLS → FLS → row scope) like reports.
type SegmentUseCase interface {
	List(ctx context.Context, orgID uuid.UUID) ([]Segment, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (*Segment, error)
	Create(ctx context.Context, orgID, userID uuid.UUID, in SegmentInput) (*Segment, error)
	Update(ctx context.Context, orgID, id uuid.UUID, in SegmentInput) (*Segment, error)
	Delete(ctx context.Context, orgID, id uuid.UUID) error
	Preview(ctx context.Context, orgID, id uuid.UUID, limit int) (*SegmentPreview, error)
	Count(ctx context.Context, orgID, id uuid.UUID) (int, error)
	// PreviewDefinition dry-runs an UNSAVED dynamic definition (the builder's live
	// count/sample) through the same OLS → validate → FLS → compile → run path as a
	// saved preview, persisting nothing. Never cached (the definition has no id).
	PreviewDefinition(ctx context.Context, orgID uuid.UUID, definition json.RawMessage, limit int) (*SegmentPreview, error)
	ListFields(ctx context.Context, orgID uuid.UUID) ([]ReportFieldDescriptor, error)
	AddStaticMembers(ctx context.Context, orgID, id uuid.UUID, contactIDs []uuid.UUID) (int, error)
	RemoveStaticMember(ctx context.Context, orgID, id, contactID uuid.UUID) (bool, error)
}

// SegmentStore is what the usecase needs from persistence (implemented by the
// repository). Query methods take the pre-built, FLS-checked catalog + AST and run
// the fully-parameterized SQL under the caller's ctx scope.
type SegmentStore interface {
	CreateSegment(ctx context.Context, s *Segment) error
	GetSegmentByID(ctx context.Context, orgID, id uuid.UUID) (*Segment, error)
	ListSegmentsByOrg(ctx context.Context, orgID uuid.UUID) ([]Segment, error)
	UpdateSegment(ctx context.Context, s *Segment) error
	SoftDeleteSegment(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	SetSegmentCount(ctx context.Context, orgID, id uuid.UUID, count int) error

	// ValidateDefinition compiles-checks a dynamic AST against the catalog (unknown
	// field / bad operator / bad tag id / too deep) without running it.
	ValidateDefinition(catalog []ReportField, ast SegmentFilter) error

	// Dynamic-segment query (compiled from catalog + AST, run under ctx scope).
	CountDynamic(ctx context.Context, orgID uuid.UUID, catalog []ReportField, ast SegmentFilter) (int, error)
	PreviewDynamic(ctx context.Context, orgID uuid.UUID, catalog []ReportField, ast SegmentFilter, limit int) ([]SegmentContactRow, error)

	// Static-list membership.
	CountStatic(ctx context.Context, orgID, segmentID uuid.UUID) (int, error)
	PreviewStatic(ctx context.Context, orgID, segmentID uuid.UUID, limit int) ([]SegmentContactRow, error)
	AddStaticMembers(ctx context.Context, orgID, segmentID uuid.UUID, contactIDs []uuid.UUID, source string) (int, error)
	RemoveStaticMember(ctx context.Context, orgID, segmentID, contactID uuid.UUID) (bool, error)

	// TagBelongsToOrg validates a tag-leaf id at save time (prevents cross-org probing).
	TagBelongsToOrg(ctx context.Context, orgID, tagID uuid.UUID) (bool, error)
}
