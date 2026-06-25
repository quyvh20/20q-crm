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

// ============================================================
// Custom-field CRUD on system objects (P7)
// ============================================================
//
// Admin-defined fields on system objects are object_fields rows with
// is_system=false. They back OrgSettingsUseCase after the P7 cutover, so the
// org-settings custom-field API keeps its shape while the storage is unified.

func (r *objectRegistryRepository) ListCustomFields(ctx context.Context, objectDefID uuid.UUID) ([]domain.ObjectField, error) {
	var fields []domain.ObjectField
	err := r.db.WithContext(ctx).
		Where("object_def_id = ? AND is_system = ?", objectDefID, false).
		Order("position ASC, created_at ASC").
		Find(&fields).Error
	return fields, err
}

func (r *objectRegistryRepository) GetFieldByDefKey(ctx context.Context, objectDefID uuid.UUID, key string) (*domain.ObjectField, error) {
	var field domain.ObjectField
	err := r.db.WithContext(ctx).
		Where("object_def_id = ? AND key = ?", objectDefID, key).
		First(&field).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &field, nil
}

func (r *objectRegistryRepository) FindCustomFieldByKey(ctx context.Context, orgID uuid.UUID, key string) (*domain.ObjectField, string, error) {
	type row struct {
		domain.ObjectField
		Slug string `gorm:"column:slug"`
	}
	var res row
	err := r.db.WithContext(ctx).
		Table("object_fields AS f").
		Select("f.*, d.slug AS slug").
		Joins("JOIN object_defs d ON d.id = f.object_def_id AND d.is_system = true AND d.deleted_at IS NULL").
		Where("f.org_id = ? AND f.key = ? AND f.is_system = ? AND f.deleted_at IS NULL", orgID, key, false).
		Order("f.created_at ASC").
		First(&res).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", nil
		}
		return nil, "", err
	}
	field := res.ObjectField
	return &field, res.Slug, nil
}

func (r *objectRegistryRepository) CreateField(ctx context.Context, f *domain.ObjectField) error {
	return r.db.WithContext(ctx).Create(f).Error
}

func (r *objectRegistryRepository) SaveField(ctx context.Context, f *domain.ObjectField) error {
	return r.db.WithContext(ctx).Save(f).Error
}

func (r *objectRegistryRepository) SoftDeleteFieldByID(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.ObjectField{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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

// ============================================================
// P7 backfill: org_settings.custom_field_defs blob → object_fields
// ============================================================

// backfillObjectFieldsSQL copies every system object's admin-defined custom field
// defs out of the legacy org_settings.custom_field_defs JSONB blob and into
// object_fields (is_system=false, storage_kind='jsonb'). It is idempotent: the
// NOT EXISTS guard skips a field already present on the def (whether previously
// backfilled or a native column with the same key), so re-running inserts nothing.
const backfillObjectFieldsSQL = `
INSERT INTO object_fields
    (id, org_id, object_def_id, key, label, type, options, target_slug,
     is_required, is_unique, is_system, storage_kind, maps_to_column, position,
     created_at, updated_at)
SELECT uuid_generate_v4(), s.org_id, d.id,
       e.key,
       COALESCE(NULLIF(e.label, ''), e.key),
       COALESCE(NULLIF(e.type, ''), 'text'),
       e.options,
       NULL,
       e.required,
       false, false, 'jsonb', NULL,
       e.position,
       NOW(), NOW()
FROM org_settings s
CROSS JOIN LATERAL jsonb_array_elements(
       CASE WHEN jsonb_typeof(s.custom_field_defs) = 'array'
            THEN s.custom_field_defs ELSE '[]'::jsonb END) AS elem
CROSS JOIN LATERAL (
       SELECT elem->>'key'         AS key,
              elem->>'label'       AS label,
              elem->>'type'        AS type,
              elem->>'entity_type' AS entity_type,
              CASE WHEN jsonb_typeof(elem->'options') = 'array'
                   THEN elem->'options' ELSE '[]'::jsonb END   AS options,
              COALESCE((elem->>'required')::boolean, false)    AS required,
              COALESCE(NULLIF(elem->>'position', '')::int, 0)  AS position
) AS e
JOIN object_defs d
       ON d.org_id = s.org_id
      AND d.slug = e.entity_type
      AND d.is_system = true
      AND d.deleted_at IS NULL
WHERE e.key IS NOT NULL AND e.key <> ''
  AND e.entity_type IS NOT NULL
  AND NOT EXISTS (
        SELECT 1 FROM object_fields f
        WHERE f.object_def_id = d.id
          AND f.key = e.key
          AND f.deleted_at IS NULL);`

// ============================================================
// P7 convergence: custom_object_defs → object_defs/object_fields
// ============================================================
//
// Custom objects' defs + fields move into the registry tables so object_defs/
// object_fields is the single store for EVERY object (system and custom). Ids are
// reused, so custom_object_records.object_def_id still resolves; the records FK is
// repointed to object_defs. custom_object_defs is kept (unused) for rollback safety,
// like the org_settings blob column. Records themselves stay in custom_object_records.

// convergeDefsSQL copies each custom object def into object_defs, reusing its id so
// existing records still resolve. Skips a def whose slug collides with a system
// object (none today) and any already converged. Idempotent.
const convergeDefsSQL = `
INSERT INTO object_defs
    (id, org_id, slug, label, label_plural, icon, color, is_system, storage,
     record_table, searchable, created_at, updated_at, deleted_at)
SELECT d.id, d.org_id, d.slug, d.label, d.label_plural,
       COALESCE(NULLIF(d.icon, ''), '📦'), '#6B7280', false, 'jsonb', NULL,
       COALESCE(d.searchable, false), d.created_at, d.updated_at, d.deleted_at
FROM custom_object_defs d
WHERE NOT EXISTS (SELECT 1 FROM object_defs o WHERE o.id = d.id)
  AND NOT EXISTS (
        SELECT 1 FROM object_defs o2
        WHERE o2.org_id = d.org_id AND o2.slug = d.slug AND o2.deleted_at IS NULL);`

// convergeFieldsSQL expands each converged def's fields blob into object_fields
// rows (is_system=false, jsonb). Idempotent via the NOT EXISTS guard.
const convergeFieldsSQL = `
INSERT INTO object_fields
    (id, org_id, object_def_id, key, label, type, options, target_slug,
     is_required, is_unique, is_system, storage_kind, maps_to_column, position,
     created_at, updated_at)
SELECT uuid_generate_v4(), d.org_id, d.id,
       e.key, COALESCE(NULLIF(e.label, ''), e.key), COALESCE(NULLIF(e.type, ''), 'text'),
       e.options, NULL, e.required, false, false, 'jsonb', NULL, e.position, NOW(), NOW()
FROM custom_object_defs d
JOIN object_defs o ON o.id = d.id
CROSS JOIN LATERAL jsonb_array_elements(
       CASE WHEN jsonb_typeof(d.fields) = 'array' THEN d.fields ELSE '[]'::jsonb END) AS elem
CROSS JOIN LATERAL (
       SELECT elem->>'key'   AS key,
              elem->>'label' AS label,
              elem->>'type'  AS type,
              CASE WHEN jsonb_typeof(elem->'options') = 'array'
                   THEN elem->'options' ELSE '[]'::jsonb END   AS options,
              COALESCE((elem->>'required')::boolean, false)    AS required,
              COALESCE(NULLIF(elem->>'position', '')::int, 0)  AS position
) AS e
JOIN object_defs o2 ON o2.id = d.id  -- only converged defs (redundant safety)
WHERE e.key IS NOT NULL AND e.key <> ''
  AND NOT EXISTS (
        SELECT 1 FROM object_fields f
        WHERE f.object_def_id = d.id AND f.key = e.key AND f.deleted_at IS NULL);`

// repointRecordsFKSQL drops the records→custom_object_defs FK (whatever its name)
// and points it at object_defs. Idempotent: a no-op once already pointing there.
const repointRecordsFKSQL = `
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'
     AND confrelid = 'custom_object_defs'::regclass
   LIMIT 1;
  IF c IS NOT NULL THEN
    EXECUTE 'ALTER TABLE custom_object_records DROP CONSTRAINT ' || quote_ident(c);
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
     WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'
       AND confrelid = 'object_defs'::regclass
  ) THEN
    ALTER TABLE custom_object_records
      ADD CONSTRAINT custom_object_records_object_def_id_fkey
      FOREIGN KEY (object_def_id) REFERENCES object_defs(id) ON DELETE CASCADE;
  END IF;
END $$;`

// ConvergeCustomObjectsToRegistry is the final P7 convergence: custom object defs +
// fields move into object_defs/object_fields and the records FK is repointed there.
// Returns the number of defs converged. Idempotent and boot-guarded (golang-migrate
// is dead on prod). After this, object_defs/object_fields is the single store for
// every object, so one field editor can serve them all.
func ConvergeCustomObjectsToRegistry(db *gorm.DB) (int64, error) {
	res := db.Exec(convergeDefsSQL)
	if res.Error != nil {
		return 0, res.Error
	}
	converged := res.RowsAffected
	if err := db.Exec(convergeFieldsSQL).Error; err != nil {
		return converged, err
	}
	if err := db.Exec(repointRecordsFKSQL).Error; err != nil {
		return converged, err
	}
	return converged, nil
}

// BackfillObjectFieldsFromBlob is the P7 convergence step that makes object_fields
// the single field-def store. It copies admin-defined custom fields from the legacy
// org_settings.custom_field_defs blob into object_fields and returns the number of
// rows inserted. Idempotent and safe to run on every boot (golang-migrate is dead on
// prod, so this runs as a boot guard).
//
// System defs are seeded lazily on read; the boot guard runs before any request, so
// for each org that actually has blob fields we force EnsureSystemObjects first to
// guarantee the JOIN target exists. Orgs without custom fields are skipped entirely.
func BackfillObjectFieldsFromBlob(db *gorm.DB) (int64, error) {
	repo := &objectRegistryRepository{db: db}

	var orgIDs []uuid.UUID
	if err := db.Raw(`SELECT org_id FROM org_settings
		WHERE jsonb_typeof(custom_field_defs) = 'array'
		  AND jsonb_array_length(custom_field_defs) > 0`).Scan(&orgIDs).Error; err != nil {
		return 0, err
	}
	for _, id := range orgIDs {
		if err := repo.EnsureSystemObjects(context.Background(), id); err != nil {
			return 0, err
		}
	}

	res := db.Exec(backfillObjectFieldsSQL)
	return res.RowsAffected, res.Error
}
