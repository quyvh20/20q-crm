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
			dispatched_at TIMESTAMPTZ,
			error TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ
		)`,
		`CREATE UNIQUE INDEX uix_campaign_recipients_campaign_email
			ON marketing_campaign_recipients(campaign_id, email_normalized)`,
		`CREATE TABLE org_marketing_profile (
			org_id UUID PRIMARY KEY,
			from_name VARCHAR(160) NOT NULL DEFAULT '',
			physical_postal_address TEXT NOT NULL DEFAULT '',
			marketing_paused BOOLEAN NOT NULL DEFAULT false
		)`,
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

// The claim query is the load-bearing send-lane SQL: pause/cancel = "drop out of the
// claim". This proves a 'sending' non-paused campaign's recipients are claimed (flipped
// to processing, attempts incremented) while a paused-campaign and a paused-org's rows
// are left untouched.
func TestCampaignIntegration_ClaimRespectsStatusAndPause(t *testing.T) {
	db := newCampaignTestDB(t)
	mkt := marketing.NewRepository(db)
	ctx := context.Background()

	orgLive, orgPaused := uuid.New(), uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO org_marketing_profile (org_id, from_name, physical_postal_address, marketing_paused) VALUES (?, 'Acme', '1 St', false)`, orgLive).Error)
	require.NoError(t, db.Exec(`INSERT INTO org_marketing_profile (org_id, from_name, physical_postal_address, marketing_paused) VALUES (?, 'Acme', '1 St', true)`, orgPaused).Error)

	newCampaign := func(org uuid.UUID, status string) uuid.UUID {
		var idStr string
		require.NoError(t, db.Raw(`INSERT INTO marketing_campaigns (org_id, name, status) VALUES (?, 'C', ?) RETURNING id::text`, org, status).Scan(&idStr).Error)
		id, err := uuid.Parse(idStr)
		require.NoError(t, err)
		return id
	}
	addRecipient := func(camp, org uuid.UUID, email string) {
		require.NoError(t, db.Exec(`INSERT INTO marketing_campaign_recipients (campaign_id, org_id, email_normalized) VALUES (?, ?, ?)`, camp, org, email).Error)
	}

	cSending := newCampaign(orgLive, "sending")   // claimable
	cPaused := newCampaign(orgLive, "paused")     // campaign not sending → skip
	cOrgPaused := newCampaign(orgPaused, "sending") // org paused → skip
	addRecipient(cSending, orgLive, "live@x.com")
	addRecipient(cPaused, orgLive, "paused@x.com")
	addRecipient(cOrgPaused, orgPaused, "orgpaused@x.com")

	claimed, err := mkt.ClaimSendableRecipients(ctx, 100)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "only the sending, non-paused campaign's recipient is claimable")
	require.Equal(t, "live@x.com", claimed[0].EmailNormalized)
	require.Equal(t, "processing", claimed[0].Status)
	require.Equal(t, 1, claimed[0].Attempts, "claim increments attempts")

	// The skipped rows stay pending.
	var pending int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM marketing_campaign_recipients WHERE status = 'pending'`).Scan(&pending).Error)
	require.Equal(t, int64(2), pending, "paused-campaign and paused-org rows remain pending")

	// A second claim finds nothing new (the one row is now processing).
	again, err := mkt.ClaimSendableRecipients(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, again)
}
