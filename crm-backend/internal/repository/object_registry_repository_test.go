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
	pg, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
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

// applyRegistrySchema creates the prerequisites (uuid-ossp + a minimal
// organizations table the FKs need), runs the real 000015 up migration, and
// inserts one organization. Returns its id to use as the org under test.
func applyRegistrySchema(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	runMigrationFile(t, db, "000015_object_registry.up.sql")

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
