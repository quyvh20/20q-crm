package repository

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type objectRegistryRepository struct {
	db *gorm.DB
}

func NewObjectRegistryRepository(db *gorm.DB) domain.ObjectRegistryRepository {
	return &objectRegistryRepository{db: db}
}

// ============================================================
// Canonical system-object specification
// ============================================================
//
// The three system objects keep their real typed tables; the registry just
// describes them. Each field maps to a native column via mapsToColumn. This spec
// is the single source of truth for the idempotent seed below and intentionally
// covers the user-facing business columns only (audit/ownership columns are left
// out of the registry surface in P2).

type sysFieldSpec struct {
	key        string
	label      string
	fieldType  string // matches FieldDescriptor.Type
	mapsTo     string // native column
	targetSlug string // for relations; "" when unresolved/none
	required   bool
}

type sysObjectSpec struct {
	slug        string
	label       string
	labelPlural string
	icon        string
	color       string
	recordTable string
	displayKey  string // which field renders as the record title
	fields      []sysFieldSpec
}

var systemObjectSpecs = []sysObjectSpec{
	{
		slug: "contact", label: "Contact", labelPlural: "Contacts",
		icon: "👤", color: "#3B82F6", recordTable: "contacts", displayKey: "first_name",
		fields: []sysFieldSpec{
			{key: "first_name", label: "First Name", fieldType: "text", mapsTo: "first_name", required: true},
			{key: "last_name", label: "Last Name", fieldType: "text", mapsTo: "last_name"},
			{key: "email", label: "Email", fieldType: "text", mapsTo: "email"},
			{key: "phone", label: "Phone", fieldType: "text", mapsTo: "phone"},
			{key: "company", label: "Company", fieldType: "relation", mapsTo: "company_id", targetSlug: "company"},
		},
	},
	{
		slug: "company", label: "Company", labelPlural: "Companies",
		icon: "🏢", color: "#8B5CF6", recordTable: "companies", displayKey: "name",
		fields: []sysFieldSpec{
			{key: "name", label: "Name", fieldType: "text", mapsTo: "name", required: true},
			{key: "industry", label: "Industry", fieldType: "text", mapsTo: "industry"},
			{key: "website", label: "Website", fieldType: "url", mapsTo: "website"},
		},
	},
	{
		slug: "deal", label: "Deal", labelPlural: "Deals",
		icon: "💰", color: "#10B981", recordTable: "deals", displayKey: "title",
		fields: []sysFieldSpec{
			{key: "title", label: "Title", fieldType: "text", mapsTo: "title", required: true},
			{key: "value", label: "Value", fieldType: "number", mapsTo: "value"},
			{key: "probability", label: "Probability", fieldType: "number", mapsTo: "probability"},
			// stage maps to pipeline_stages, which is not (yet) a registered object,
			// so targetSlug is left unresolved in P2.
			{key: "stage", label: "Stage", fieldType: "relation", mapsTo: "stage_id"},
			{key: "contact", label: "Contact", fieldType: "relation", mapsTo: "contact_id", targetSlug: "contact"},
			{key: "company", label: "Company", fieldType: "relation", mapsTo: "company_id", targetSlug: "company"},
			{key: "expected_close_at", label: "Expected Close", fieldType: "date", mapsTo: "expected_close_at"},
		},
	},
}

// ============================================================
// Seed
// ============================================================

// EnsureSystemObjects idempotently seeds the three system object defs and their
// native fields for an org. It is called on the read path so existing and
// future orgs are both covered without a separate startup or org-creation hook.
//
// The fast path is a single indexed count; seeding happens at most once per org
// lifetime. When seeding is needed, a per-org transaction-scoped advisory lock
// serializes concurrent first-reads (e.g. a page that loads the object list and
// a record schema in parallel): the loser blocks, then its re-check inside the
// lock sees the winner's committed rows and no-ops. The partial unique indexes
// remain the ultimate backstop.
func (r *objectRegistryRepository) EnsureSystemObjects(ctx context.Context, orgID uuid.UUID) error {
	if seeded, err := r.systemObjectsSeeded(ctx, r.db, orgID); err != nil {
		return err
	} else if seeded {
		return nil // already seeded — no lock, no transaction
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize concurrent first-time seeds for this org. Released on commit.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", orgID.String()).Error; err != nil {
			return err
		}
		// Re-check under the lock: the request that lost the race exits here.
		if seeded, err := r.systemObjectsSeeded(ctx, tx, orgID); err != nil {
			return err
		} else if seeded {
			return nil
		}

		for _, spec := range systemObjectSpecs {
			def, err := ensureDef(tx, orgID, spec)
			if err != nil {
				return err
			}

			var displayFieldID *uuid.UUID
			for pos, fs := range spec.fields {
				field, err := ensureField(tx, orgID, def.ID, pos, fs)
				if err != nil {
					return err
				}
				if fs.key == spec.displayKey {
					id := field.ID
					displayFieldID = &id
				}
			}

			// Point the def at its display field once the fields exist.
			if displayFieldID != nil && (def.DisplayFieldID == nil || *def.DisplayFieldID != *displayFieldID) {
				if err := tx.Model(&domain.ObjectDef{}).
					Where("id = ?", def.ID).
					Update("display_field_id", *displayFieldID).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// systemObjectsSeeded reports whether all system object defs already exist for
// the org. Accepts the db/tx handle so it can run both outside (fast path) and
// inside the seeding transaction (re-check under the advisory lock).
func (r *objectRegistryRepository) systemObjectsSeeded(ctx context.Context, db *gorm.DB, orgID uuid.UUID) (bool, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&domain.ObjectDef{}).
		Where("org_id = ? AND is_system = ?", orgID, true).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count >= int64(len(systemObjectSpecs)), nil
}

func ensureDef(tx *gorm.DB, orgID uuid.UUID, spec sysObjectSpec) (*domain.ObjectDef, error) {
	var def domain.ObjectDef
	err := tx.Where("org_id = ? AND slug = ?", orgID, spec.slug).First(&def).Error
	if err == nil {
		return &def, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	recordTable := spec.recordTable
	def = domain.ObjectDef{
		ID:          uuid.New(),
		OrgID:       orgID,
		Slug:        spec.slug,
		Label:       spec.label,
		LabelPlural: spec.labelPlural,
		Icon:        spec.icon,
		Color:       spec.color,
		IsSystem:    true,
		Storage:     "table",
		RecordTable: &recordTable,
	}
	if err := tx.Create(&def).Error; err != nil {
		return nil, err
	}
	return &def, nil
}

func ensureField(tx *gorm.DB, orgID, defID uuid.UUID, pos int, fs sysFieldSpec) (*domain.ObjectField, error) {
	var field domain.ObjectField
	err := tx.Where("object_def_id = ? AND key = ?", defID, fs.key).First(&field).Error
	if err == nil {
		return &field, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	mapsTo := fs.mapsTo
	field = domain.ObjectField{
		ID:           uuid.New(),
		OrgID:        orgID,
		ObjectDefID:  defID,
		Key:          fs.key,
		Label:        fs.label,
		Type:         fs.fieldType,
		Options:      domain.JSON("[]"),
		IsRequired:   fs.required,
		IsSystem:     true,
		StorageKind:  "column",
		MapsToColumn: &mapsTo,
		Position:     pos,
	}
	if fs.targetSlug != "" {
		ts := fs.targetSlug
		field.TargetSlug = &ts
	}
	if err := tx.Create(&field).Error; err != nil {
		return nil, err
	}
	return &field, nil
}

// ============================================================
// Reads
// ============================================================

func (r *objectRegistryRepository) ListDefs(ctx context.Context, orgID uuid.UUID) ([]domain.ObjectDef, error) {
	var defs []domain.ObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		// System objects first; slug is the tiebreaker since the three system
		// defs are seeded in one transaction and share created_at.
		Order("is_system DESC, created_at ASC, slug ASC").
		Find(&defs).Error
	return defs, err
}

func (r *objectRegistryRepository) GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*domain.ObjectDef, error) {
	var def domain.ObjectDef
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND slug = ?", orgID, slug).
		First(&def).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &def, nil
}

func (r *objectRegistryRepository) ListFields(ctx context.Context, objectDefID uuid.UUID) ([]domain.ObjectField, error) {
	var fields []domain.ObjectField
	err := r.db.WithContext(ctx).
		Where("object_def_id = ?", objectDefID).
		Order("position ASC, created_at ASC").
		Find(&fields).Error
	return fields, err
}

func (r *objectRegistryRepository) FieldCounts(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]int, error) {
	type row struct {
		ObjectDefID uuid.UUID
		N           int
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Model(&domain.ObjectField{}).
		Select("object_def_id, COUNT(*) AS n").
		Where("org_id = ?", orgID).
		Group("object_def_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[uuid.UUID]int, len(rows))
	for _, r := range rows {
		counts[r.ObjectDefID] = r.N
	}
	return counts, nil
}
