package domain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ============================================================
// Saved list views
// ============================================================
//
// A ListView is one user's saved configuration of the uniform record list — a
// name over the SAME inputs GET /records already accepts (filter AST, q, tags,
// sort). The definition is stored verbatim and replayed by the frontend as
// query params, so the view itself grants nothing: every execution still runs
// through RecordService's OLS → FLS → row-scope pipeline as the viewer.
//
// The one place a view IS a security surface is save time: `shared` views are
// listed to the whole org, so a definition that filters on an FLS-hidden field
// would hand every teammate an equality/range oracle on values they cannot
// read. The usecase therefore applies the segment save-time doctrine — saved
// definitions are trusted later, so the save-side gate (hidden field → 403,
// compile-check → 400) must be airtight.

// ListViewDefinition is the persisted shape of `definition`. It deliberately
// mirrors the record-list query surface (parseRecordListQuery) rather than
// inventing its own grammar: Filter is the same ReportFilterGroup AST the
// `filter` param carries, and the scalars map 1:1 onto q/tag_ids/sort_by/
// sort_order. Unknown JSON keys are ignored on read, so the shape can grow
// without invalidating saved rows.
type ListViewDefinition struct {
	Filter    *ReportFilterGroup `json:"filter,omitempty"`
	Q         string             `json:"q,omitempty"`
	Tags      []string           `json:"tags,omitempty"`
	SortBy    string             `json:"sort_by,omitempty"`
	SortOrder string             `json:"sort_order,omitempty"`
}

// ListView is a saved record-list configuration, owned by one user and
// optionally shared org-wide. Soft-deletable.
type ListView struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID       uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	OwnerUserID uuid.UUID `gorm:"type:uuid;not null" json:"owner_user_id"`
	ObjectSlug  string    `gorm:"type:varchar(100);not null" json:"object_slug"`
	Name        string    `gorm:"type:varchar(120);not null" json:"name"`
	// Definition is the ListViewDefinition blob — datatypes.JSON so GORM binds it
	// as jsonb, never bytea (the M4 22P02 lesson).
	Definition datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"definition"`
	Shared     bool           `gorm:"type:boolean;not null;default:false" json:"shared"`
	CreatedAt  time.Time      `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt  time.Time      `gorm:"not null;default:now()" json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName pins the table name.
func (ListView) TableName() string { return "list_views" }

// ListViewInput is the create/update request body. Every field is optional at
// the JSON layer so ONE shape serves both verbs: Create requires Name and
// defaults the rest; Update treats nil as "unchanged" (a partial PUT), which is
// what lets a rename leave the definition untouched instead of blanking it.
type ListViewInput struct {
	Name       *string         `json:"name"`
	Shared     *bool           `json:"shared"`
	Definition json.RawMessage `json:"definition"`
}

// ListViewRepository is the persistence port. Org-scoped everywhere;
// soft-delete aware (gorm.DeletedAt).
type ListViewRepository interface {
	// List returns the caller's own views PLUS org-shared ones for the object,
	// ordered by name asc. Visibility is decided HERE (owner OR shared) so no
	// caller-side filtering can forget it.
	List(ctx context.Context, orgID uuid.UUID, objectSlug string, userID uuid.UUID) ([]ListView, error)
	// GetByID is org-scoped: a foreign org's id resolves to nil, never an error,
	// so the usecase can 404 without an existence oracle.
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*ListView, error)
	Create(ctx context.Context, v *ListView) error
	Update(ctx context.Context, v *ListView) error
	// SoftDelete reports whether a row was actually removed (false → 404).
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	// CountForObject counts live views per (org, object) — the Create cap's input.
	CountForObject(ctx context.Context, orgID uuid.UUID, objectSlug string) (int64, error)
}

// ListViewUseCase is the saved-views surface. Every method re-authorizes
// ActionRead on the object (a view is a read configuration — no write grant is
// implied); Update/Delete are additionally owner-only, shared or not.
type ListViewUseCase interface {
	List(ctx context.Context, orgID, userID uuid.UUID, objectSlug string) ([]ListView, error)
	Create(ctx context.Context, orgID, userID uuid.UUID, objectSlug string, in ListViewInput) (*ListView, error)
	Update(ctx context.Context, orgID, userID, id uuid.UUID, in ListViewInput) (*ListView, error)
	Delete(ctx context.Context, orgID, userID, id uuid.UUID) error
}
