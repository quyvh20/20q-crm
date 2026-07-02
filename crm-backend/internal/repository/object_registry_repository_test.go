package repository

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// startPostgres returns a clean Postgres-backed GORM handle for an integration
// test. If TEST_DATABASE_URL is set it connects there and resets the public
// schema for isolation (handy where testcontainers can't run, e.g. Docker
// Desktop on Windows). Otherwise it falls back to a throwaway testcontainers
// instance, skipping the test when Docker is unavailable.
func startPostgres(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if dsn := os.Getenv("TEST_DATABASE_URL"); dsn != "" {
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err, "connect to TEST_DATABASE_URL")
		// Fresh schema per test so the migration/seed assertions are isolated.
		require.NoError(t, db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public;`).Error)
		return db, func() {}
	}

	ctx := context.Background()
	// pgvector/pgvector:pg16 is plain Postgres 16 plus the `vector` extension, so
	// it serves every repository integration test (the registry/links/permissions
	// suites use no vector features) while also letting the record_embeddings (P6)
	// tests create `CREATE EXTENSION vector`. Stock postgres:16-alpine has no
	// pgvector, which would force the P6 tests to skip even under Docker.
	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
		tcpostgres.WithSQLDriver("pgx"),
	)
	if err != nil {
		t.Skipf("Docker not available — skipping registry integration test: %v", err)
	}

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	return db, func() { _ = pg.Terminate(ctx) }
}

func runMigrationFile(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
	require.NoError(t, err, "read migration %s", name)
	require.NoError(t, db.Exec(string(b)).Error, "exec migration %s", name)
}

func tableExists(t *testing.T, db *gorm.DB, table string) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(
		"SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = ?",
		table,
	).Scan(&n).Error)
	return n > 0
}

func columnExists(t *testing.T, db *gorm.DB, table, column string) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(
		"SELECT count(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = ? AND column_name = ?",
		table, column,
	).Scan(&n).Error)
	return n > 0
}

// applyRegistrySchema creates the prerequisites (uuid-ossp + a minimal
// organizations table the FKs need), runs the real 000015 up migration, brings
// object_defs/object_fields up to the columns the current repo reads, and inserts
// one organization. Returns its id to use as the org under test.
func applyRegistrySchema(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	runMigrationFile(t, db, "000015_object_registry.up.sql")

	// The registry repo reads columns added by later migrations (000023
	// number_prefix, 000024 via_field/source_field). We can't run those migration
	// files here — their backfills reference typed tables (contacts/deals/…) this
	// minimal schema doesn't create — so mirror just their object_defs/object_fields
	// column ALTERs, exactly as the main.go boot guard does.
	require.NoError(t, db.Exec(`ALTER TABLE object_defs ADD COLUMN IF NOT EXISTS number_prefix VARCHAR(16)`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE object_fields ADD COLUMN IF NOT EXISTS via_field VARCHAR(100)`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE object_fields ADD COLUMN IF NOT EXISTS source_field VARCHAR(100)`).Error)

	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	return orgID
}

// TestMigration000015_UpDownRoundTrip proves .down drops cleanly and .up is
// re-runnable (up → down → up).
func TestMigration000015_UpDownRoundTrip(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)

	runMigrationFile(t, db, "000015_object_registry.up.sql")
	require.True(t, tableExists(t, db, "object_defs"), "object_defs should exist after up")
	require.True(t, tableExists(t, db, "object_fields"), "object_fields should exist after up")

	runMigrationFile(t, db, "000015_object_registry.down.sql")
	require.False(t, tableExists(t, db, "object_defs"), "object_defs should be gone after down")
	require.False(t, tableExists(t, db, "object_fields"), "object_fields should be gone after down")

	// Re-apply: the migration is self-consistent on a second up.
	runMigrationFile(t, db, "000015_object_registry.up.sql")
	require.True(t, tableExists(t, db, "object_defs"), "object_defs should exist after re-up")
	require.True(t, tableExists(t, db, "object_fields"), "object_fields should exist after re-up")
}

func TestEnsureSystemObjects_SeedsAndIsIdempotent(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()
	orgID := applyRegistrySchema(t, db)

	repo := NewObjectRegistryRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.EnsureSystemObjects(ctx, orgID))

	defs, err := repo.ListDefs(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, defs, len(systemObjectSpecs), "should seed exactly the system objects")

	bySlug := map[string]domain.ObjectDef{}
	for _, d := range defs {
		require.True(t, d.IsSystem)
		require.Equal(t, "table", d.Storage)
		require.NotNil(t, d.RecordTable)
		require.NotNil(t, d.DisplayFieldID, "display_field_id must be set for %s", d.Slug)
		bySlug[d.Slug] = d
	}

	deal, ok := bySlug["deal"]
	require.True(t, ok, "deal must be seeded")

	dealFields, err := repo.ListFields(ctx, deal.ID)
	require.NoError(t, err)
	require.Len(t, dealFields, 7, "deal should have its 7 native fields")

	var titleSeen bool
	for _, f := range dealFields {
		require.True(t, f.IsSystem)
		require.Equal(t, "column", f.StorageKind)
		require.NotNil(t, f.MapsToColumn)
		if f.ID == *deal.DisplayFieldID {
			require.Equal(t, "title", f.Key)
			titleSeen = true
		}
	}
	require.True(t, titleSeen, "display field should resolve to 'title'")

	// Re-run is a no-op: no duplicate defs or fields.
	require.NoError(t, repo.EnsureSystemObjects(ctx, orgID))

	defs2, err := repo.ListDefs(ctx, orgID)
	require.NoError(t, err)
	require.Len(t, defs2, len(systemObjectSpecs), "re-run seed must not duplicate defs")

	counts, err := repo.FieldCounts(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, 7, counts[deal.ID], "re-run seed must not duplicate fields")
}

// TestEnsureSystemObjects_Concurrent proves the advisory-lock serialization:
// many simultaneous first-reads of a fresh org each succeed and seed exactly
// once (no unique-violation errors, no duplicate defs).
func TestEnsureSystemObjects_Concurrent(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()
	orgID := applyRegistrySchema(t, db)

	repo := NewObjectRegistryRepository(db)

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = repo.EnsureSystemObjects(context.Background(), orgID)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "goroutine %d should not error", i)
	}

	defs, err := repo.ListDefs(context.Background(), orgID)
	require.NoError(t, err)
	require.Len(t, defs, len(systemObjectSpecs), "concurrent seeds must not duplicate defs")
}

// TestListIncomingRelations proves the one-query reverse-relation lookup: child
// object metadata + field key/label in stable def order, excluding non-relation
// fields, the stage pseudo-relation (NULL target), soft-deleted fields, and
// other orgs.
func TestListIncomingRelations(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()
	orgID := applyRegistrySchema(t, db)

	repo := NewObjectRegistryRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.EnsureSystemObjects(ctx, orgID))

	// company is targeted by contact.company and deal.company (seed spec),
	// contact def first (seeded before deal).
	rels, err := repo.ListIncomingRelations(ctx, orgID, "company")
	require.NoError(t, err)
	require.Len(t, rels, 2)
	require.Equal(t, "contact", rels[0].ChildSlug)
	require.Equal(t, "Contacts", rels[0].ChildLabelPlural)
	require.Equal(t, "company", rels[0].FieldKey)
	require.Equal(t, "Company", rels[0].FieldLabel)
	require.Equal(t, "deal", rels[1].ChildSlug)
	require.Equal(t, "company", rels[1].FieldKey)

	// contact is targeted by deal.contact only; deal.stage (target_slug NULL)
	// never matches.
	rels, err = repo.ListIncomingRelations(ctx, orgID, "contact")
	require.NoError(t, err)
	require.Len(t, rels, 1)
	require.Equal(t, "deal", rels[0].ChildSlug)
	require.Equal(t, "contact", rels[0].FieldKey)

	// A soft-deleted relation field disappears from the results.
	require.NoError(t, db.Exec(
		`UPDATE object_fields SET deleted_at = now() WHERE org_id = ? AND key = 'contact' AND type = 'relation'`,
		orgID,
	).Error)
	rels, err = repo.ListIncomingRelations(ctx, orgID, "contact")
	require.NoError(t, err)
	require.Empty(t, rels)

	// Another org sees nothing.
	rels, err = repo.ListIncomingRelations(ctx, uuid.New(), "company")
	require.NoError(t, err)
	require.Empty(t, rels)
}
