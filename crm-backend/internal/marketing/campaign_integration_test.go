package marketing_test

import (
	"context"
	"testing"

	"crm-backend/internal/domain"
	"crm-backend/internal/marketing"
	"crm-backend/internal/repository"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The M7 roster fan-out is the one part that must be proven against real Postgres:
// a set-based INSERT…SELECT that unions dynamic + static segments, subtracts
// exclusions, dedupes on lower(email), drops empty/foreign-org emails, and — the
// headline Done-when — fans a >100-row segment fully into the roster (the old
// enroll_records path silently capped at 100).

func newCampaignTestDB(t *testing.T) *gorm.DB {
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

	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,
		`CREATE TABLE contacts (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id UUID NOT NULL,
			email TEXT,
			first_name TEXT,
			last_name TEXT,
			custom_fields JSONB NOT NULL DEFAULT '{}',
			owner_user_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)`,
		`CREATE TABLE marketing_segment_static_members (
			segment_id UUID NOT NULL,
			contact_id UUID NOT NULL,
			source VARCHAR(32) NOT NULL DEFAULT '',
			PRIMARY KEY (segment_id, contact_id)
		)`,
		`CREATE TABLE marketing_campaigns (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			org_id UUID NOT NULL,
			name VARCHAR(200) NOT NULL,
			status VARCHAR(16) NOT NULL DEFAULT 'draft',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)`,
		`CREATE TABLE marketing_campaign_recipients (
			id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
			campaign_id UUID NOT NULL,
			org_id UUID NOT NULL,
			contact_id UUID,
			email_normalized VARCHAR(320) NOT NULL,
			variant VARCHAR(32),
			status VARCHAR(16) NOT NULL DEFAULT 'pending',
			attempts INT NOT NULL DEFAULT 0,
			next_attempt_at TIMESTAMPTZ,
			scheduled_for TIMESTAMPTZ,
			locked_at TIMESTAMPTZ,
			provider_message_id VARCHAR(128),
			idempotency_key VARCHAR(160),
			error TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ
		)`,
		`CREATE UNIQUE INDEX uix_campaign_recipients_campaign_email
			ON marketing_campaign_recipients(campaign_id, email_normalized)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error)
	}
	return db
}

func campaignIntCatalog() []domain.ReportField {
	return []domain.ReportField{
		{Key: "email", Label: "Email", Type: "text", Column: "email"},
		{Key: "lead_source", Label: "Lead Source", Type: "text", JSONKey: "lead_source"},
	}
}

func mustID(t *testing.T, db *gorm.DB, org uuid.UUID, email string) uuid.UUID {
	t.Helper()
	// Scan id::text into a string then parse — gorm's Raw().Scan(&uuid.UUID) mishandles
	// a bare uuid target ("converting string to uint8").
	var s string
	require.NoError(t, db.Raw(`SELECT id::text FROM contacts WHERE org_id = ? AND email = ?`, org, email).Scan(&s).Error)
	id, err := uuid.Parse(s)
	require.NoError(t, err)
	return id
}

func TestCampaignIntegration_RosterFanOut(t *testing.T) {
	db := newCampaignTestDB(t)
	segRepo := repository.NewSegmentRepository(db)
	mkt := marketing.NewRepository(db)
	ctx := context.Background()
	cat := campaignIntCatalog()

	org1, org2 := uuid.New(), uuid.New()

	// 150 web-tagged contacts in org1 (proves the >100 fan-out) …
	require.NoError(t, db.Exec(`
		INSERT INTO contacts (org_id, email, custom_fields)
		SELECT ?, 'web'||g||'@x.com', '{"lead_source":"web"}'::jsonb
		FROM generate_series(0,149) g`, org1).Error)
	// … plus a web contact with an EMPTY email (must be dropped) …
	require.NoError(t, db.Exec(`INSERT INTO contacts (org_id, email, custom_fields) VALUES (?, '', '{"lead_source":"web"}'::jsonb)`, org1).Error)
	// … and a web contact in ANOTHER org (must be dropped by org scoping).
	require.NoError(t, db.Exec(`INSERT INTO contacts (org_id, email, custom_fields) VALUES (?, 'other@x.com', '{"lead_source":"web"}'::jsonb)`, org2).Error)
	// Two non-web contacts for the static list.
	require.NoError(t, db.Exec(`INSERT INTO contacts (org_id, email, custom_fields) VALUES (?, 's1@x.com', '{"lead_source":"other"}'::jsonb)`, org1).Error)
	require.NoError(t, db.Exec(`INSERT INTO contacts (org_id, email, custom_fields) VALUES (?, 's2@x.com', '{"lead_source":"other"}'::jsonb)`, org1).Error)

	web0 := mustID(t, db, org1, "web0@x.com")   // overlaps the static list → dedupe
	web5 := mustID(t, db, org1, "web5@x.com")   // excluded
	s1 := mustID(t, db, org1, "s1@x.com")
	s2 := mustID(t, db, org1, "s2@x.com")

	segStatic := uuid.New()
	segExclude := uuid.New()
	// static include list: web0 (overlap), s1, s2
	for _, cid := range []uuid.UUID{web0, s1, s2} {
		require.NoError(t, db.Exec(`INSERT INTO marketing_segment_static_members (segment_id, contact_id) VALUES (?, ?)`, segStatic, cid).Error)
	}
	// static exclude list: web5
	require.NoError(t, db.Exec(`INSERT INTO marketing_segment_static_members (segment_id, contact_id) VALUES (?, ?)`, segExclude, web5).Error)

	includes := []domain.ResolvedSegment{
		{ID: uuid.New(), Type: domain.SegmentTypeDynamic, Filter: domain.SegmentFilter{Field: "lead_source", Operator: "eq", Value: "web"}},
		{ID: segStatic, Type: domain.SegmentTypeStatic},
	}
	excludes := []domain.ResolvedSegment{
		{ID: segExclude, Type: domain.SegmentTypeStatic},
	}

	sql, args, err := segRepo.CompileAudienceQuery(org1, cat, includes, excludes)
	require.NoError(t, err)
	aud := domain.AudienceQuery{SelectSQL: sql, Args: args}

	// Expected: 150 web − web5 (excluded) = 149, + s1,s2 (static) = 151; web0 dedup;
	// empty-email + other-org dropped.
	const want = 151

	// A campaign to hang the roster on.
	var campIDStr string
	require.NoError(t, db.Raw(`INSERT INTO marketing_campaigns (org_id, name) VALUES (?, 'Blast') RETURNING id::text`, org1).Scan(&campIDStr).Error)
	campID, err := uuid.Parse(campIDStr)
	require.NoError(t, err)

	est, err := mkt.EstimateAudience(ctx, aud)
	require.NoError(t, err)
	require.Equal(t, want, est, "dry estimate")

	total, err := mkt.SnapshotRoster(ctx, campID, org1, aud)
	require.NoError(t, err)
	require.Equal(t, want, total, "roster size after fan-out")

	// Re-snapshot is idempotent (ON CONFLICT DO NOTHING on the dedupe unique).
	total2, err := mkt.SnapshotRoster(ctx, campID, org1, aud)
	require.NoError(t, err)
	require.Equal(t, want, total2, "re-snapshot must not duplicate")

	// Every roster row is pending, org-scoped, and web5 / empty / other-org are absent.
	byStatus, err := mkt.CountRosterByStatus(ctx, campID)
	require.NoError(t, err)
	require.Equal(t, want, byStatus[domain.RecipientStatusPending])

	var web5Rows, emptyRows, otherOrgRows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND email_normalized = 'web5@x.com'`, campID).Scan(&web5Rows).Error)
	require.NoError(t, db.Raw(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND email_normalized = ''`, campID).Scan(&emptyRows).Error)
	require.NoError(t, db.Raw(`SELECT count(*) FROM marketing_campaign_recipients WHERE campaign_id = ? AND email_normalized = 'other@x.com'`, campID).Scan(&otherOrgRows).Error)
	require.Zero(t, web5Rows, "excluded contact must not be in the roster")
	require.Zero(t, emptyRows, "empty-email contact must be dropped")
	require.Zero(t, otherOrgRows, "other-org contact must be dropped by org scoping")
}
