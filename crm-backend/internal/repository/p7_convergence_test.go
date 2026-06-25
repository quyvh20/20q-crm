package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestBackfillObjectFieldsFromBlob proves the P7 field-def convergence: admin-defined
// custom fields in the legacy org_settings.custom_field_defs blob are copied into
// object_fields (is_system=false), a key that collides with a native column is
// skipped, and a re-run is a no-op (idempotent).
func TestBackfillObjectFieldsFromBlob(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	orgID := applyRegistrySchema(t, db) // uuid-ossp + organizations + 000015 + one org

	// Minimal org_settings carrying the legacy blob: one genuine custom field plus a
	// "first_name" entry that collides with the contact's native column.
	require.NoError(t, db.Exec(`CREATE TABLE org_settings (org_id uuid PRIMARY KEY, custom_field_defs jsonb DEFAULT '[]')`).Error)
	blob := `[
		{"key":"shoe_size","label":"Shoe Size","type":"number","entity_type":"contact","required":true,"position":5},
		{"key":"renewal_risk","label":"Renewal Risk","type":"select","entity_type":"deal","options":["low","high"]},
		{"key":"first_name","label":"Collides","type":"text","entity_type":"contact"}
	]`
	require.NoError(t, db.Exec(`INSERT INTO org_settings (org_id, custom_field_defs) VALUES (?, ?::jsonb)`, orgID, blob).Error)

	n, err := BackfillObjectFieldsFromBlob(db)
	require.NoError(t, err)
	require.Equal(t, int64(2), n, "two genuine custom fields backfilled; first_name skipped (native collision)")

	// shoe_size landed on the contact def as a custom (is_system=false) jsonb field.
	var count int64
	require.NoError(t, db.Raw(`
		SELECT count(*) FROM object_fields f
		JOIN object_defs d ON d.id = f.object_def_id
		WHERE f.org_id = ? AND d.slug = 'contact' AND f.key = 'shoe_size'
		  AND f.is_system = false AND f.storage_kind = 'jsonb'`, orgID).Scan(&count).Error)
	require.Equal(t, int64(1), count, "shoe_size should be a custom field on contact")

	// first_name was NOT duplicated — exactly one (the native is_system=true column).
	require.NoError(t, db.Raw(`
		SELECT count(*) FROM object_fields f
		JOIN object_defs d ON d.id = f.object_def_id
		WHERE f.org_id = ? AND d.slug = 'contact' AND f.key = 'first_name'`, orgID).Scan(&count).Error)
	require.Equal(t, int64(1), count, "native first_name must not be duplicated by the backfill")

	// Idempotent: a second run inserts nothing.
	n2, err := BackfillObjectFieldsFromBlob(db)
	require.NoError(t, err)
	require.Equal(t, int64(0), n2, "re-run must be a no-op")
}

// TestBackfillObjectLinksFromRecordFKs proves the P7 relationship convergence: the
// hardcoded custom_object_records.contact_id/deal_id FKs become object_links edges,
// the columns are dropped, and a re-run after the drop is a safe no-op.
func TestBackfillObjectLinksFromRecordFKs(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	// object_links.created_by FKs users(id), so the table must exist for 000016.
	require.NoError(t, db.Exec(`CREATE TABLE users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	runMigrationFile(t, db, "000016_object_links.up.sql")

	// Minimal custom-object tables with the legacy FK columns.
	require.NoError(t, db.Exec(`CREATE TABLE custom_object_defs (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid, slug varchar(100), deleted_at timestamptz)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE custom_object_records (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid, object_def_id uuid,
		contact_id uuid, deal_id uuid, deleted_at timestamptz)`).Error)

	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	defID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug) VALUES (?, ?, 'asset')`, defID, orgID).Error)

	recID := uuid.New()
	contactID := uuid.New()
	dealID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO custom_object_records (id, org_id, object_def_id, contact_id, deal_id) VALUES (?, ?, ?, ?, ?)`,
		recID, orgID, defID, contactID, dealID).Error)
	// A soft-deleted record must NOT be backfilled.
	require.NoError(t, db.Exec(`INSERT INTO custom_object_records (id, org_id, object_def_id, contact_id, deleted_at) VALUES (?, ?, ?, ?, NOW())`,
		uuid.New(), orgID, defID, uuid.New()).Error)

	n, err := BackfillObjectLinksFromRecordFKs(db)
	require.NoError(t, err)
	require.Equal(t, int64(2), n, "one contact edge + one deal edge from the live record")

	// The contact edge exists with relation_key/to_slug = 'contact'.
	var count int64
	require.NoError(t, db.Raw(`
		SELECT count(*) FROM object_links
		WHERE org_id = ? AND from_slug = 'asset' AND from_id = ?
		  AND relation_key = 'contact' AND to_slug = 'contact' AND to_id = ?`, orgID, recID, contactID).Scan(&count).Error)
	require.Equal(t, int64(1), count, "contact FK should become an object_links edge")

	require.NoError(t, db.Raw(`SELECT count(*) FROM object_links WHERE relation_key = 'deal' AND to_id = ?`, dealID).Scan(&count).Error)
	require.Equal(t, int64(1), count, "deal FK should become an object_links edge")

	// The legacy columns are gone.
	require.False(t, columnExists(t, db, "custom_object_records", "contact_id"), "contact_id should be dropped")
	require.False(t, columnExists(t, db, "custom_object_records", "deal_id"), "deal_id should be dropped")

	// Idempotent: re-run after the drop is a safe no-op (the column guard prevents a
	// reference to a dropped column).
	n2, err := BackfillObjectLinksFromRecordFKs(db)
	require.NoError(t, err)
	require.Equal(t, int64(0), n2, "re-run after drop must be a no-op")
}

// TestConvergeCustomObjectsToRegistry proves the final P7 convergence: a custom
// object's def + fields move into object_defs/object_fields (reusing the id), the
// records FK is repointed to object_defs, the record still resolves, and a re-run is
// a no-op.
func TestConvergeCustomObjectsToRegistry(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	orgID := applyRegistrySchema(t, db) // uuid-ossp + organizations + 000015 + one org

	// Minimal legacy custom-object tables with the records→defs FK the convergence
	// repoints. (The real 000005 also FKs contacts/deals/users, irrelevant here.)
	require.NoError(t, db.Exec(`CREATE TABLE custom_object_defs (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid, slug varchar(100),
		label varchar(255), label_plural varchar(255), icon varchar(50),
		fields jsonb DEFAULT '[]', searchable boolean NOT NULL DEFAULT false,
		created_at timestamptz DEFAULT now(), updated_at timestamptz DEFAULT now(), deleted_at timestamptz)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE custom_object_records (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid,
		object_def_id uuid NOT NULL REFERENCES custom_object_defs(id) ON DELETE CASCADE,
		display_name varchar(500) DEFAULT '', data jsonb DEFAULT '{}', deleted_at timestamptz)`).Error)

	defID := uuid.New()
	fields := `[{"key":"name","label":"Name","type":"text","required":true,"position":0},
	            {"key":"budget","label":"Budget","type":"number","position":1}]`
	require.NoError(t, db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug, label, label_plural, icon, fields, searchable)
		VALUES (?, ?, 'project', 'Project', 'Projects', '📁', ?::jsonb, true)`, defID, orgID, fields).Error)
	recID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO custom_object_records (id, org_id, object_def_id, display_name)
		VALUES (?, ?, ?, 'Apollo')`, recID, orgID, defID).Error)

	n, err := ConvergeCustomObjectsToRegistry(db)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "one custom def converged")

	// Def landed in object_defs with the SAME id, non-system, jsonb, searchable carried.
	var od struct {
		IsSystem   bool
		Storage    string
		Searchable bool
		Slug       string
	}
	require.NoError(t, db.Raw(`SELECT is_system, storage, searchable, slug FROM object_defs WHERE id = ?`, defID).Scan(&od).Error)
	require.False(t, od.IsSystem)
	require.Equal(t, "jsonb", od.Storage)
	require.True(t, od.Searchable)
	require.Equal(t, "project", od.Slug)

	// Fields expanded into object_fields (is_system=false).
	var fcount int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM object_fields WHERE object_def_id = ? AND is_system = false`, defID).Scan(&fcount).Error)
	require.Equal(t, int64(2), fcount)

	// The records FK now references object_defs, and the record still resolves.
	var confrel string
	require.NoError(t, db.Raw(`SELECT confrelid::regclass::text FROM pg_constraint
		WHERE conrelid = 'custom_object_records'::regclass AND contype = 'f'`).Scan(&confrel).Error)
	require.Equal(t, "object_defs", confrel, "records FK should point at object_defs")
	var rcount int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM custom_object_records r
		JOIN object_defs o ON o.id = r.object_def_id WHERE r.id = ?`, recID).Scan(&rcount).Error)
	require.Equal(t, int64(1), rcount, "record must still resolve through object_defs")

	// Idempotent.
	n2, err := ConvergeCustomObjectsToRegistry(db)
	require.NoError(t, err)
	require.Equal(t, int64(0), n2, "re-run must be a no-op")
}
