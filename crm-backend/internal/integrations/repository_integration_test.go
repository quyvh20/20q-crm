package integrations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newIntegrationsTestDB brings up Postgres and applies the REAL migration file, so
// these tests validate the shipped SQL rather than a hand-rolled approximation of
// it. That matters most for the partial unique indexes below: they are the first
// ones in this codebase, and their behaviour is the whole dedupe guarantee.
func newIntegrationsTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
		tcpostgres.WithSQLDriver("pgx"),
	)
	if err != nil {
		t.Skipf("Docker not available — skipping integrations integration test: %v", err)
	}
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	// FK prerequisites the migration expects to already exist.
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id UUID PRIMARY KEY DEFAULT uuid_generate_v4(), deleted_at TIMESTAMPTZ)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS contacts (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		email TEXT,
		phone VARCHAR(50),
		deleted_at TIMESTAMPTZ
	)`).Error)

	// org_users backs the owner-routing liveness check (000044's feature, not its
	// schema) — the real table lives in an earlier migration this harness does not run.
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		status VARCHAR(50) NOT NULL DEFAULT 'active',
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id)
	)`).Error)

	// Applied in order, so the tests validate the SHIPPED SQL rather than a
	// hand-rolled approximation — including that 000044's ALTERs actually apply to
	// the table 000043 created.
	for _, m := range []string{
		"000043_lead_integrations.up.sql",
		"000044_lead_owner_pool.up.sql",
		"000045_lead_batch.up.sql",
		"000046_lead_consent.up.sql",
		"000047_lead_google_ads.up.sql",
		"000048_lead_form_embed.up.sql",
		"000049_lead_provider_connections.up.sql",
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", "migrations", m))
		require.NoError(t, err, "read migration %s", m)
		require.NoError(t, db.Exec(string(b)).Error, "the shipped migration %s must be valid SQL", m)
	}

	return db, func() { _ = pg.Terminate(ctx) }
}

func seedOrg(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, id).Error)
	return id
}

func seedSource(t *testing.T, repo *Repository, orgID uuid.UUID) *LeadSource {
	t.Helper()
	_, hash, prefix, err := GenerateLeadKey()
	require.NoError(t, err)
	src := &LeadSource{
		OrgID: orgID, Kind: KindAPI, Name: "test", TokenHash: hash, TokenPrefix: prefix,
		TargetSlug: "contact", UpdatePolicy: UpdatePolicyFillBlankOnly, Status: SourceStatusActive,
		MatchFields: []byte(`["email"]`), FieldMap: []byte(`{}`), Config: []byte(`{}`),
	}
	require.NoError(t, repo.CreateSource(context.Background(), src))
	return src
}

// TestMigration043_Applies is the cheapest guard against the failure mode this
// project keeps hitting: schema that works locally and 500s in prod. It executes
// the shipped file, so a typo cannot pass. (The prod twin is main.go's boot-guard
// block — golang-migrate never runs there — and the two must stay in step.)
func TestMigration043_Applies(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()

	for _, table := range []string{"lead_sources", "integration_events"} {
		var n int64
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = ?`, table).Scan(&n).Error)
		require.Equal(t, int64(1), n, "%s should exist", table)
	}
	// The dedupe guarantee is these two indexes; assert both, by name.
	for _, idx := range []string{"idx_integration_events_source_provider", "idx_integration_events_conn_provider", "idx_contacts_org_lower_email"} {
		var n int64
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pg_indexes WHERE indexname = ?`, idx).Scan(&n).Error)
		require.Equal(t, int64(1), n, "%s should exist", idx)
	}
}

func TestInsertEventDeduped_SourceScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	id := "delivery-1"
	first := &IntegrationEvent{OrgID: orgID, SourceID: &src.ID, ProviderEventID: &id, Status: EventStatusProcessing}
	inserted, err := repo.InsertEventDeduped(ctx, first)
	require.NoError(t, err)
	require.True(t, inserted, "the first delivery must insert")

	again := &IntegrationEvent{OrgID: orgID, SourceID: &src.ID, ProviderEventID: &id, Status: EventStatusProcessing}
	inserted, err = repo.InsertEventDeduped(ctx, again)
	require.NoError(t, err)
	require.False(t, inserted, "a redelivery must NOT insert a second row")
}

// TestInsertEventDeduped_ConnectionScopedWithNullSource is the regression for the
// bug the plan review caught by inspection: provider webhooks resolve a connection
// but not yet a source, so source_id is NULL at insert — and Postgres treats NULLs
// as DISTINCT, meaning a single (source_id, provider_event_id) index would never
// fire and every one of Meta's 36 hours of retries would create another contact.
// The connection-scoped twin is what actually holds on that path.
func TestInsertEventDeduped_ConnectionScopedWithNullSource(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	connID := uuid.New()

	id := "leadgen-1"
	first := &IntegrationEvent{OrgID: orgID, ConnectionID: &connID, ProviderEventID: &id, Status: EventStatusPending}
	inserted, err := repo.InsertEventDeduped(ctx, first)
	require.NoError(t, err)
	require.True(t, inserted)

	again := &IntegrationEvent{OrgID: orgID, ConnectionID: &connID, ProviderEventID: &id, Status: EventStatusPending}
	inserted, err = repo.InsertEventDeduped(ctx, again)
	require.NoError(t, err)
	require.False(t, inserted, "a connection-scoped redelivery must dedupe even though source_id is NULL")

	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM integration_events WHERE provider_event_id = ?`, id).Scan(&n).Error)
	require.Equal(t, int64(1), n, "exactly one row may exist for a redelivered leadgen id")
}

// TestInsertEventDeduped_NoProviderIDAlwaysInserts pins the deliberate gap: a
// caller that sends no Idempotency-Key has nothing stable to dedupe against, so
// each delivery is genuinely new. The partial indexes exclude NULLs, which is what
// makes this work rather than collapsing every un-keyed lead into one row.
func TestInsertEventDeduped_NoProviderIDAlwaysInserts(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	for i := 0; i < 2; i++ {
		inserted, err := repo.InsertEventDeduped(ctx, &IntegrationEvent{
			OrgID: orgID, SourceID: &src.ID, Status: EventStatusProcessing,
		})
		require.NoError(t, err)
		require.True(t, inserted, "an un-keyed delivery always inserts")
	}
}

// TestFindSourceByTokenHash_SoftDeleteRevokes pins that retiring a source kills its
// credential immediately, even though the row (and its ledger) survives.
func TestFindSourceByTokenHash_SoftDeleteRevokes(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	found, err := repo.FindSourceByTokenHash(ctx, src.TokenHash)
	require.NoError(t, err)
	require.NotNil(t, found, "a live source's key must resolve")

	require.NoError(t, repo.SoftDeleteSource(ctx, orgID, src.ID))
	found, err = repo.FindSourceByTokenHash(ctx, src.TokenHash)
	require.NoError(t, err)
	require.Nil(t, found, "a deleted source's key must stop resolving")
}

// TestFindSourceByTokenHash_OrgSoftDeleteRevokes is the regression for the review's
// highest-impact finding. Workspace deletion is a SOFT delete, so the organizations
// row survives and the ON DELETE CASCADE on lead_sources.org_id can never fire: the
// source stays status='active' and its key would go on writing contacts — with
// billable side effects — into a workspace the customer deleted. Nobody could stop
// it either, because deletion evicts every member, so no one can authenticate to
// reach the management API and disable the source.
func TestFindSourceByTokenHash_OrgSoftDeleteRevokes(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	found, err := repo.FindSourceByTokenHash(ctx, src.TokenHash)
	require.NoError(t, err)
	require.NotNil(t, found, "a live workspace's key must resolve")

	// Exactly what authRepository.SoftDeleteOrganization does.
	require.NoError(t, db.Exec(`UPDATE organizations SET deleted_at = NOW() WHERE id = ?`, orgID).Error)

	// The row is still there and still 'active' — which is the whole point: nothing
	// about the SOURCE changed, so only an org-liveness check can catch this.
	var surviving int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM lead_sources WHERE id = ?`, src.ID).Scan(&surviving).Error)
	require.Equal(t, int64(1), surviving, "soft delete leaves the source row; the FK cascade cannot fire")

	found, err = repo.FindSourceByTokenHash(ctx, src.TokenHash)
	require.NoError(t, err)
	require.Nil(t, found, "a deleted workspace's capture key must stop resolving")
}

// TestNormalizePhone_MatchesTheSQLExpression is the guard for a silent drift.
//
// Phone matching only works because Go's normalizePhone and the SQL expression
// behind idx_contacts_org_phone_digits produce IDENTICAL strings. If they ever
// disagree, the query stops matching the index — dedupe quietly degrades to "never
// matches" and every lead becomes a duplicate, with no error anywhere. A unit test
// cannot catch that; only asking Postgres can.
func TestNormalizePhone_MatchesTheSQLExpression(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()

	for _, in := range []string{
		"+1 (555) 123-4567",
		"555.123.4567",
		"  555 123 4567  ",
		"+44 20 7946 0958",
		"(0)20-7946-0958",
		"",
		"not a phone",
	} {
		var sqlOut string
		require.NoError(t, db.Raw(`SELECT regexp_replace(?::text, '[^0-9]', '', 'g')`, in).Scan(&sqlOut).Error)
		require.Equal(t, sqlOut, normalizePhone(in),
			"Go and SQL must agree on %q — a drift silently stops dedupe using the index", in)
	}
}

// TestPhoneDigitsIndexExists pins the index by name: without it every phone match
// is a sequential scan of contacts, which is silent until the table is large.
func TestPhoneDigitsIndexExists(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_contacts_org_phone_digits'`).Scan(&n).Error)
	require.Equal(t, int64(1), n, "the shipped migration must create the phone-digits index")
	// And it must NOT be unique: a shared phone is legitimate, so uniqueness would
	// fail to build on real data and assert something untrue about people.
	var unique bool
	require.NoError(t, db.Raw(`SELECT i.indisunique FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid WHERE c.relname = 'idx_contacts_org_phone_digits'`).Scan(&unique).Error)
	require.False(t, unique, "the phone index must stay NON-unique")
}

// TestSetDealConfig_PreservesSiblingKeys pins the reason UpdateSource omits
// `config` and this writes through jsonb_set instead of replacing the blob.
//
// L3 (google_ads) and L5 (facebook) will store source-kind config in the same
// column. A whole-blob write here would delete theirs the first time an admin
// toggled the deal checkbox — silently, with a 200.
func TestSetDealConfig_PreservesSiblingKeys(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	require.NoError(t, db.Exec(
		`UPDATE lead_sources SET config = '{"google_ads":{"customer_id":"123-456"}}'::jsonb WHERE id = ?`,
		src.ID).Error)

	stage := uuid.New()
	require.NoError(t, repo.SetDealConfig(ctx, orgID, src.ID,
		DealConfig{Enabled: true, StageID: &stage, NameTemplate: "{{full_name}}"}))

	reloaded, err := repo.GetSource(ctx, orgID, src.ID)
	require.NoError(t, err)
	require.Contains(t, string(reloaded.Config), "123-456", "a sibling config key was destroyed")

	cfg := ParseDealConfig(reloaded.Config)
	require.True(t, cfg.Enabled)
	require.Equal(t, stage, *cfg.StageID)
}

// TestUpdateSource_DoesNotClobberConfig is the other half: the model Save must
// leave the blob alone entirely, because the struct it saves was read at the start
// of the request and may be stale by the time it writes.
func TestUpdateSource_DoesNotClobberConfig(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	stage := uuid.New()
	require.NoError(t, repo.SetDealConfig(ctx, orgID, src.ID, DealConfig{Enabled: true, StageID: &stage}))

	// A stale in-memory struct — exactly what a concurrent PATCH holds.
	src.Config = datatypes.JSON(`{}`)
	src.Name = "renamed"
	require.NoError(t, repo.UpdateSource(ctx, src))

	reloaded, err := repo.GetSource(ctx, orgID, src.ID)
	require.NoError(t, err)
	require.Equal(t, "renamed", reloaded.Name, "the rename must still apply")
	require.True(t, ParseDealConfig(reloaded.Config).Enabled,
		"renaming a source must not switch its deal option off")
}
