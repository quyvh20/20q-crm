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
