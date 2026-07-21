package repository

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// RevokeUserGrants had NO test, which is exactly how it shipped repairing one half
// of a two-part binding. It pruned the member from every source's `owner_pool` and
// left `default_owner_id` pointing at them — and the non-pooled branch of
// resolveOwner stamps that column UNCHECKED, so a removed rep went on being assigned
// new leads forever. Own-scoped reps cannot see records owned by someone else, so
// those leads were invisible and nobody triaged them.
//
// The two bindings are asserted TOGETHER here, in one test, because the failure was
// precisely that they drifted apart: a test covering only the pool would have passed
// throughout the entire life of the bug.

func newOffboardTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
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
		t.Skipf("Docker not available — skipping integration test: %v", err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	// FK prerequisites the lead_sources migration expects.
	require.NoError(t, db.Exec(`CREATE TABLE organizations (id UUID PRIMARY KEY, deleted_at TIMESTAMPTZ)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE users (id UUID PRIMARY KEY)`).Error)
	// 000043 also indexes contacts (the dedupe indexes), so the table has to exist
	// even though nothing here reads it.
	require.NoError(t, db.Exec(`CREATE TABLE contacts (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		email TEXT,
		phone VARCHAR(50),
		deleted_at TIMESTAMPTZ
	)`).Error)

	// The SHIPPED migrations, not a hand-rolled approximation: default_owner_id comes
	// from 000043 and owner_pool from 000044, and the whole point of this test is that
	// one statement must touch both columns as they actually exist.
	for _, m := range []string{"000043_lead_integrations.up.sql", "000044_lead_owner_pool.up.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "migrations", m))
		require.NoError(t, err, "read migration %s", m)
		require.NoError(t, db.Exec(string(b)).Error, "the shipped migration %s must be valid SQL", m)
	}

	// The other tables RevokeUserGrants writes in the same transaction. Derived from
	// the domain structs so they cannot drift from what the statement expects.
	require.NoError(t, db.AutoMigrate(
		&domain.RecordShare{}, &domain.ReportShare{}, &domain.APIToken{}, &domain.UserGroupMember{},
	))
	return db
}

// seedRoutedSource inserts a lead source with the given routing bindings and returns
// its id. owner_pool is written by targeted SQL because the column is deliberately
// absent from the Go struct.
func seedRoutedSource(t *testing.T, db *gorm.DB, orgID uuid.UUID, name string, defaultOwner *uuid.UUID, pool string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO lead_sources
		    (id, org_id, kind, name, token_hash, token_prefix, target_slug,
		     match_fields, field_map, config, status, default_owner_id, owner_pool)
		VALUES (?, ?, 'api', ?, ?, 'crm_lead_x', 'contact',
		        '["email"]'::jsonb, '{}'::jsonb, '{}'::jsonb, 'active', ?, ?::jsonb)`,
		id, orgID, name, id.String(), defaultOwner, pool).Error)
	return id
}

func routingOf(t *testing.T, db *gorm.DB, id uuid.UUID) (defaultOwner *string, pool string) {
	t.Helper()
	var row struct {
		DefaultOwnerID *string
		OwnerPool      string
	}
	require.NoError(t, db.Raw(
		`SELECT default_owner_id::text AS default_owner_id, COALESCE(owner_pool::text, 'null') AS owner_pool
		   FROM lead_sources WHERE id = ?`, id).Scan(&row).Error)
	return row.DefaultOwnerID, row.OwnerPool
}

func TestRevokeUserGrants_ClearsBothRoutingBindings(t *testing.T) {
	db := newOffboardTestDB(t)
	repo := NewOffboardRepository(db)
	ctx := context.Background()

	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	leaver, stayer := uuid.New(), uuid.New()
	for _, u := range []uuid.UUID{leaver, stayer} {
		require.NoError(t, db.Exec(`INSERT INTO users (id) VALUES (?)`, u).Error)
	}

	// The binding that was already repaired.
	pooled := seedRoutedSource(t, db, orgID, "Rotation", nil,
		`["`+leaver.String()+`","`+stayer.String()+`"]`)
	// The binding that was NOT — and the commonest shape, since most sources have a
	// single owner and no rotation at all.
	defaulted := seedRoutedSource(t, db, orgID, "Website form", &leaver, `[]`)
	// Both at once.
	both := seedRoutedSource(t, db, orgID, "Both", &leaver, `["`+leaver.String()+`"]`)
	// An untouched control: proves the statements are scoped to the leaver rather
	// than blanking every source in the org.
	other := seedRoutedSource(t, db, orgID, "Someone else's", &stayer, `["`+stayer.String()+`"]`)

	require.NoError(t, repo.RevokeUserGrants(ctx, orgID, leaver))

	owner, pool := routingOf(t, db, pooled)
	require.Nil(t, owner)
	require.NotContains(t, pool, leaver.String(), "the leaver must be out of the rotation")
	require.Contains(t, pool, stayer.String(), "pruning must not disturb the rest of the pool")

	owner, _ = routingOf(t, db, defaulted)
	require.Nil(t, owner, "THE BUG: a removed member must not stay the default owner — every new lead here was being assigned to someone with no org_users row")

	owner, pool = routingOf(t, db, both)
	require.Nil(t, owner)
	require.NotContains(t, pool, leaver.String())

	owner, pool = routingOf(t, db, other)
	require.NotNil(t, owner)
	require.Equal(t, stayer.String(), *owner, "another member's routing must be untouched")
	require.Contains(t, pool, stayer.String())
}

// TestRevokeUserGrants_IsOrgScoped — the same person can be a member of two
// workspaces, and leaving one must not disarm their lead routing in the other.
func TestRevokeUserGrants_IsOrgScoped(t *testing.T) {
	db := newOffboardTestDB(t)
	repo := NewOffboardRepository(db)

	orgA, orgB := uuid.New(), uuid.New()
	for _, o := range []uuid.UUID{orgA, orgB} {
		require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, o).Error)
	}
	leaver := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO users (id) VALUES (?)`, leaver).Error)

	inA := seedRoutedSource(t, db, orgA, "A", &leaver, `["`+leaver.String()+`"]`)
	inB := seedRoutedSource(t, db, orgB, "B", &leaver, `["`+leaver.String()+`"]`)

	require.NoError(t, repo.RevokeUserGrants(context.Background(), orgA, leaver))

	owner, pool := routingOf(t, db, inA)
	require.Nil(t, owner)
	require.NotContains(t, pool, leaver.String())

	owner, pool = routingOf(t, db, inB)
	require.NotNil(t, owner, "leaving one workspace must not touch their routing in another")
	require.Contains(t, pool, leaver.String())
}

// TestRoutingSourceNames_MatchesWhatRevokeRepairs is the drift guard.
//
// RoutingSourceNames is what the admin is TOLD about, and RevokeUserGrants is what
// actually changes. The bug was these two disagreeing: the query matched
// `default_owner_id = ? OR owner_pool @> ?` while the repair covered only the second
// half, so the 409 promised "they will also be removed from the lead rotation on: …"
// and then didn't do it for half the sources it named. Asserting the promise and the
// repair against the same fixture is what stops them drifting again.
func TestRoutingSourceNames_MatchesWhatRevokeRepairs(t *testing.T) {
	db := newOffboardTestDB(t)
	repo := NewOffboardRepository(db)
	ctx := context.Background()

	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	leaver := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO users (id) VALUES (?)`, leaver).Error)

	seedRoutedSource(t, db, orgID, "Default owner only", &leaver, `[]`)
	seedRoutedSource(t, db, orgID, "Pool only", nil, `["`+leaver.String()+`"]`)
	seedRoutedSource(t, db, orgID, "Unrelated", nil, `[]`)

	named, err := repo.RoutingSourceNames(ctx, orgID, leaver)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"Default owner only", "Pool only"}, named)

	require.NoError(t, repo.RevokeUserGrants(ctx, orgID, leaver))

	// Every source the admin was told about now routes nowhere near the leaver.
	after, err := repo.RoutingSourceNames(ctx, orgID, leaver)
	require.NoError(t, err)
	require.Empty(t, after, "everything the disclosure named must actually have been repaired")
}
