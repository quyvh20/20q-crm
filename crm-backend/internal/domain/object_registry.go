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
	// NumberPrefix is the admin-editable label prefix for record numbers; nil falls
	// back to the uppercased slug at read time.
	NumberPrefix *string `gorm:"size:16" json:"number_prefix,omitempty"`
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
	// ViaField/SourceField configure a "mirror" field: follow the relation named by
	// ViaField to the linked record, then display that record's SourceField. Both nil
	// for every non-mirror field.
	ViaField    *string `gorm:"size:100" json:"via_field,omitempty"`
	SourceField *string `gorm:"size:100" json:"source_field,omitempty"`
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
	// Searchable surfaces whether the object is opted into generic semantic +
	// fulltext search (P6), so the UI can badge it and the search screen can
	// enumerate which objects participate.
	Searchable bool `json:"searchable"`
}

// ObjectDescriptor is the full schema for one object. The frontend (P3) renders
// any object from this single shape, system or custom alike.
//
// As of P8, the descriptor also carries the caller's effective detail layout
// (resolved from configured layouts, intersected with FLS). Layout is nil/empty
// when no layout has been configured — the renderer falls back to flat field order.
type ObjectDescriptor struct {
	Slug         string            `json:"slug"`
	Label        string            `json:"label"`
	LabelPlural  string            `json:"label_plural"`
	Icon         string            `json:"icon"`
	Color        string            `json:"color"`
	IsSystem     bool              `json:"is_system"`
	Searchable   bool              `json:"searchable"`
	DisplayField string            `json:"display_field"`
	// HasOwner reports whether this object's records carry an owner (U6.3) — true
	// for contacts, deals and every custom object; false for company, which is
	// org-wide by design and has no owner_user_id column. The frontend renders the
	// owner picker and the Share button off this flag rather than probing for a
	// field that isn't in the registry.
	HasOwner bool `json:"has_owner"`
	// NumberPrefix is the label prefix for this object's record numbers (e.g.
	// "DEAL" → DEAL-0001). Defaults to the uppercased slug; admin-editable.
	NumberPrefix string            `json:"number_prefix"`
	Fields       []FieldDescriptor `json:"fields"`
	// Layout is the caller's effective detail layout (P8). Empty/nil means
	// no layout configured; the frontend renders the flat Fields list instead.
	// This field is populated by the HTTP handler after schema assembly by calling
	// ObjectLayoutUseCase.ResolveLayout, so it is omitted from the schema endpoint
	// when the feature is not yet wired (zero value = omitempty = absent from JSON).
	Layout []LayoutSection `json:"layout,omitempty"`
}

// FieldDescriptor is one field in an ObjectDescriptor. storage_kind / maps_to_column
// are deliberately omitted — they are internal and never user-visible.
type FieldDescriptor struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Type       string   `json:"type"`
	Options    []string `json:"options,omitempty"`
	TargetSlug string   `json:"target_slug,omitempty"`
	// Mirror-field config (see ObjectField): follow ViaField to the linked record
	// and display its SourceField. Empty for non-mirror fields.
	ViaField    string `json:"via_field,omitempty"`
	SourceField string `json:"source_field,omitempty"`
	IsSystem    bool   `json:"is_system"`
	Required    bool   `json:"required"`
	Unique      bool   `json:"unique,omitempty"`
}

// ============================================================
// Records — the uniform read/write surface (P3)
// ============================================================

// UniformRecord is the single shape every object's record is served as — system
// (contact/deal/company) and custom alike (plan §5). Fields is keyed by the
// object's field keys (the registry `key`); relation values are UUID strings.
// The shape is identical regardless of whether the record is backed by a typed
// table or a JSONB blob — that is the whole point of "all objects equal".
type UniformRecord struct {
	ID      uuid.UUID `json:"id"`
	Object  string    `json:"object"` // slug
	Display string    `json:"display"`
	// Number is the human-readable record identifier (e.g. "DEAL-0001"), resolved
	// on read from the per-object sequence. Empty when numbering is unavailable
	// (e.g. a record created before numbering, until the backfill runs).
	Number string `json:"number,omitempty"`
	// OwnerUserID is the record's owner — the anchor for row scope ('own'/'team'),
	// assignment and sharing. It is a first-class field on the record rather than a
	// registry field row (U6.3): the registry deliberately excludes ownership/audit
	// columns, and making owner a field would drag it into the FLS grid, layouts and
	// the report virtual-field catalog. nil = unassigned, which is reachable only by
	// an 'all'-scoped role. Objects without an ownership model (company) always
	// carry nil — see ObjectDescriptor.HasOwner.
	OwnerUserID *uuid.UUID             `json:"owner_user_id,omitempty"`
	Fields      map[string]interface{} `json:"fields"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// RecordListInput is the uniform, storage-agnostic list query. Cursor is opaque
// to callers: for system objects it is the typed repo's keyset cursor; for
// custom objects it encodes the next offset. Either way the frontend just echoes
// next_cursor back to fetch the following page.
//
// Filters/TagIDs/Semantic bring the generic list to parity with the legacy
// per-object pages (P7): Filters maps a relation field key (e.g. "company",
// "stage", "owner_user_id") to a UUID string; TagIDs filters by tag; Semantic
// switches contacts to vector search. System objects translate these into their
// typed filter structs; custom objects honour what they can and ignore the rest.
type RecordListInput struct {
	Limit    int
	Q        string
	Cursor   string
	Filters  map[string]string
	TagIDs   []uuid.UUID
	Semantic bool
}

// RecordList is one page of uniform records plus an opaque forward cursor. An
// empty NextCursor means there are no more records.
type RecordList struct {
	Records    []UniformRecord `json:"records"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// RelatedList is one "reverse" relationship group on a record's page: every
// record of a child object that points back at this record through a typed
// relation field. E.g. on a Contact, the child object "deal" with field "contact"
// yields the contact's Deals. Derived entirely from the registry — wherever a
// field has type=relation and target_slug == this record's object, a related list
// appears, in both directions, with zero per-object code.
type RelatedList struct {
	Object     string          `json:"object"`      // child object slug (e.g. "deal")
	Label      string          `json:"label"`       // child object's plural label (e.g. "Deals")
	Icon       string          `json:"icon"`        // child object's icon
	FieldKey   string          `json:"field_key"`   // the relation field on the child that points back
	FieldLabel string          `json:"field_label"` // that field's label (e.g. "Contact")
	Records    []UniformRecord `json:"records"`
	Count      int             `json:"count"`
	// HasMore is true when more children exist than were returned (the preview is
	// capped), so the UI can show e.g. "50+" instead of a misleading exact count.
	HasMore bool `json:"has_more"`
}

// RelatedListsUseCase assembles a record's reverse related lists by asking the
// registry for incoming relation fields and querying each child object through
// RecordService (so OLS/FLS apply uniformly). It composes the registry and record
// services rather than living on RecordService, keeping that interface — and its
// many constructor call sites — unchanged.
type RelatedListsUseCase interface {
	ListRelatedLists(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]RelatedList, error)
}

// IncomingRelation is one (child object, relation field) pair whose typed
// relation points at a target object — the seed of one reverse related list.
// The registry resolves all of them in a single query so the related-lists
// builder doesn't have to walk every object's full schema.
type IncomingRelation struct {
	ChildSlug        string
	ChildLabelPlural string
	ChildIcon        string
	FieldKey         string
	FieldLabel       string
}

// RecordWriteInput is the uniform create/update payload: a flat field map keyed
// by field key. Splitting native columns from the JSONB blob, validation, and
// display recompute are all the service's job, not the caller's.
type RecordWriteInput struct {
	Fields map[string]interface{} `json:"fields"`
}

// RecordEventEmitter fires an automation trigger after a write. It mirrors the
// per-handler emitter callbacks (ContactEventEmitter, CustomObjectEventEmitter)
// so the uniform write path keeps automation working without RecordService
// depending on the automation package.
type RecordEventEmitter func(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any)

// RecordService is the single read/write chokepoint over every object. It
// dispatches on the object's storage kind — typed table vs JSONB — internally,
// so HTTP handlers, AI, and automation only ever see "objects". Org-scoping,
// validation, and (in later phases) FLS and audit all live here so they cannot
// be forgotten in a per-object handler.
type RecordService interface {
	List(ctx context.Context, orgID uuid.UUID, slug string, in RecordListInput) (*RecordList, error)
	Get(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) (*UniformRecord, error)
	Create(ctx context.Context, orgID, userID uuid.UUID, slug string, in RecordWriteInput) (*UniformRecord, error)
	Update(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID, in RecordWriteInput) (*UniformRecord, error)
	Delete(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error
	// SetEventEmitter wires the automation trigger callback, called once at
	// startup. It is part of the interface (rather than a private method reached
	// via a type assertion) so that a signature drift fails the build instead of
	// silently disabling automation for the uniform write path.
	SetEventEmitter(fn RecordEventEmitter)

	// SetSearchIndexer wires the generic search indexer (P6), called once at
	// startup. On the interface for the same reason as SetEventEmitter: a drift
	// should fail the build, not silently stop indexing searchable records. Until
	// set, writes to searchable objects simply skip indexing.
	SetSearchIndexer(idx RecordIndexer)

	// SetNumberRepo wires the human-readable record-number allocator, called once
	// at startup. Until set, records carry no Number (numbering is simply absent),
	// so unit tests that don't exercise numbering need no extra wiring.
	SetNumberRepo(repo RecordNumberRepository)

	// --- Universal relationships + tags (P4) ---

	// AddLink relates one record to another (any object to any object). It is
	// idempotent: re-adding an existing edge returns it rather than erroring.
	// Tag edges are rejected here — use AddTag, which keeps contacts on their
	// legacy store.
	AddLink(ctx context.Context, orgID, userID uuid.UUID, slug string, id uuid.UUID, in LinkInput) (*LinkView, error)
	// ListLinks returns a record's outgoing relationships (tags excluded), each
	// resolved to the target's current display title.
	ListLinks(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]LinkView, error)
	// RemoveLink soft-deletes one relationship edge by id.
	RemoveLink(ctx context.Context, orgID, linkID uuid.UUID) error

	// ListTags returns a record's tags, uniformly for every object (contacts via
	// contact_tags, everyone else via object_links).
	ListTags(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]Tag, error)
	// AddTag tags a record; idempotent. RemoveTag untags it.
	AddTag(ctx context.Context, orgID, userID uuid.UUID, slug string, id, tagID uuid.UUID) error
	RemoveTag(ctx context.Context, orgID uuid.UUID, slug string, id, tagID uuid.UUID) error
}

// ============================================================
// Ports
// ============================================================

// RecordNumberRepository allocates and resolves human-readable record numbers.
// Numbers live in a side table keyed by (org, object_slug, record_id), so the same
// allocator serves typed and JSONB objects uniformly.
type RecordNumberRepository interface {
	// Allocate assigns the next sequence number to a record (idempotent: a record
	// that already has one keeps it). Called from the write path after create.
	Allocate(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) error
	// NumbersFor returns the formatted number (e.g. "DEAL-0001") for each id that
	// has one, using the object's current prefix. Ids without a number are omitted.
	NumbersFor(ctx context.Context, orgID uuid.UUID, slug string, ids []uuid.UUID) (map[uuid.UUID]string, error)
}

type ObjectRegistryRepository interface {
	// EnsureSystemObjects idempotently seeds the three system object defs and
	// their native fields for an org. Safe to call on every read.
	EnsureSystemObjects(ctx context.Context, orgID uuid.UUID) error
	ListDefs(ctx context.Context, orgID uuid.UUID) ([]ObjectDef, error)
	GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*ObjectDef, error)
	ListFields(ctx context.Context, objectDefID uuid.UUID) ([]ObjectField, error)
	// FieldCounts returns object_def_id → number of (non-deleted) fields for the org.
	FieldCounts(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]int, error)
	// ListIncomingRelations returns every (child object, relation field) pair whose
	// typed relation targets targetSlug, in the same stable object order as
	// ListDefs — one query, powering reverse related lists.
	ListIncomingRelations(ctx context.Context, orgID uuid.UUID, targetSlug string) ([]IncomingRelation, error)

	// --- Custom-field CRUD on system objects (P7) ---
	//
	// After the P7 cutover, admin-defined ("custom") fields on system objects live
	// in object_fields (is_system=false) instead of the org_settings.custom_field_defs
	// blob. These methods back OrgSettingsUseCase so its public API is unchanged while
	// the storage is unified — which also removes the lost-update race on the blob
	// (symptom #3 / R6).

	// ListCustomFields returns a system object's admin-defined fields (is_system=false),
	// ordered by position. Native fields are excluded.
	ListCustomFields(ctx context.Context, objectDefID uuid.UUID) ([]ObjectField, error)
	// GetFieldByDefKey returns any field (native or custom) on a def by key, or nil —
	// used to reject a custom field that would collide with an existing key.
	GetFieldByDefKey(ctx context.Context, objectDefID uuid.UUID, key string) (*ObjectField, error)
	// FindCustomFieldByKey returns the first admin-defined field with the given key
	// across the org's system objects, plus the owning object's slug. nil when none —
	// matches the legacy "update/delete a field def by key alone" handler contract.
	FindCustomFieldByKey(ctx context.Context, orgID uuid.UUID, key string) (*ObjectField, string, error)
	CreateField(ctx context.Context, f *ObjectField) error
	SaveField(ctx context.Context, f *ObjectField) error
	SoftDeleteFieldByID(ctx context.Context, orgID, id uuid.UUID) error

	// SetNumberPrefix updates an object's record-number label prefix (empty clears
	// it back to the slug default). Returns ErrNotFound when the slug is unknown.
	SetNumberPrefix(ctx context.Context, orgID uuid.UUID, slug, prefix string) error
}

type ObjectRegistryUseCase interface {
	// ListObjects returns every object (system + custom) as summaries.
	ListObjects(ctx context.Context, orgID uuid.UUID) ([]ObjectSummary, error)
	// GetSchema returns the full descriptor for one object by slug.
	GetSchema(ctx context.Context, orgID uuid.UUID, slug string) (*ObjectDescriptor, error)
	// SetNumberPrefix updates an object's record-number prefix (e.g. "INV"). An
	// empty prefix resets to the slug default.
	SetNumberPrefix(ctx context.Context, orgID uuid.UUID, slug, prefix string) error
	// ListIncomingRelations returns every (child object, relation field) pair
	// whose typed relation targets targetSlug — the input to reverse related
	// lists, resolved in one query instead of a per-object schema walk.
	ListIncomingRelations(ctx context.Context, orgID uuid.UUID, targetSlug string) ([]IncomingRelation, error)
}
