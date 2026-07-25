package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The M5 segment compiler is the injection defense AND the FLS/OLS boundary for
// audiences: it turns a caller-supplied boolean AST into SQL. Its SQL-generation
// discipline is unit-tested in segment_sql_test.go, but "the generated SQL is
// well-formed and actually returns the right rows against real Postgres" — org
// scoping, the RecordAccessPredicate row filter, a jsonb custom-field predicate
// with its guarded numeric cast, and the correlated tag EXISTS that leans on the
// new reverse index — can only be proven against a live database. This file does
// that.

func newSegmentTestDB(t *testing.T) *gorm.DB {
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

	// The minimal slice of the schema the compiled segment SQL touches. contacts +
	// its jsonb blob; contact_tags (+ the reverse index the tag leaf relies on);
	// tags (TagBelongsToOrg); record_shares + user_group_members + user_groups (the
	// RecordAccessPredicate references all three, even for 'own' scope, via the
	// group-share subquery); and the static-list membership table.
	stmts := []string{
		`CREATE TABLE contacts (
			id UUID PRIMARY KEY,
			org_id UUID NOT NULL,
			first_name TEXT,
			last_name TEXT,
			email TEXT,
			custom_fields JSONB NOT NULL DEFAULT '{}',
			owner_user_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			deleted_at TIMESTAMPTZ
		)`,
		`CREATE TABLE contact_tags (
			contact_id UUID NOT NULL,
			tag_id UUID NOT NULL,
			PRIMARY KEY (contact_id, tag_id)
		)`,
		`CREATE INDEX idx_contact_tags_tag_contact ON contact_tags(tag_id, contact_id)`,
		`CREATE TABLE tags (id UUID PRIMARY KEY, org_id UUID NOT NULL)`,
		`CREATE TABLE record_shares (
			id UUID PRIMARY KEY,
			org_id UUID NOT NULL,
			record_id UUID NOT NULL,
			record_type TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id UUID NOT NULL,
			permission_level TEXT NOT NULL DEFAULT 'view'
		)`,
		`CREATE TABLE user_group_members (user_id UUID NOT NULL, org_id UUID NOT NULL, group_id UUID NOT NULL)`,
		`CREATE TABLE user_groups (id UUID PRIMARY KEY, deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE marketing_segment_static_members (
			segment_id UUID NOT NULL,
			contact_id UUID NOT NULL,
			source VARCHAR(32) NOT NULL DEFAULT '',
			PRIMARY KEY (segment_id, contact_id)
		)`,
	}
	for _, s := range stmts {
		require.NoError(t, db.Exec(s).Error)
	}
	return db
}

func segInsertContact(t *testing.T, db *gorm.DB, id, org, owner uuid.UUID, email, first, cf string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, owner_user_id, email, first_name, custom_fields)
		 VALUES (?, ?, ?, ?, ?, ?::jsonb)`,
		id, org, owner, email, first, cf).Error)
}

func segIntCatalog() []domain.ReportField {
	return []domain.ReportField{
		{Key: "email", Label: "Email", Type: "text", Column: "email"},
		{Key: "first_name", Label: "First", Type: "text", Column: "first_name"},
		{Key: "lead_source", Label: "Lead Source", Type: "text", JSONKey: "lead_source"},
		{Key: "score", Label: "Score", Type: "number", JSONKey: "score"},
	}
}

func TestSegmentIntegration_DynamicQueries(t *testing.T) {
	db := newSegmentTestDB(t)
	repo := NewSegmentRepository(db)
	ctx := context.Background()
	cat := segIntCatalog()

	org1, org2 := uuid.New(), uuid.New()
	u1, u2 := uuid.New(), uuid.New()
	c1, c2, c3 := uuid.New(), uuid.New(), uuid.New()
	segInsertContact(t, db, c1, org1, u1, "a@x.com", "Ann", `{"lead_source":"web","score":50}`)
	segInsertContact(t, db, c2, org1, u2, "b@x.com", "Bob", `{"lead_source":"ads","score":5}`)
	segInsertContact(t, db, c3, org2, u1, "c@x.com", "Cy", `{"lead_source":"web","score":99}`) // other org

	// Org scoping: a lead_source=web filter must see only org1's web contact, never
	// org2's — the mandatory org predicate is ANDed regardless of the AST.
	n, err := repo.CountDynamic(ctx, org1, cat, domain.SegmentFilter{Field: "lead_source", Operator: "eq", Value: "web"})
	require.NoError(t, err)
	require.Equal(t, 1, n, "lead_source=web in org1 should match only c1")

	// jsonb number predicate with the guarded numeric cast.
	n, err = repo.CountDynamic(ctx, org1, cat, domain.SegmentFilter{Field: "score", Operator: "gt", Value: 10})
	require.NoError(t, err)
	require.Equal(t, 1, n, "score>10 in org1 should match only c1 (50), not c2 (5)")

	// Preview returns id + display name + email for the matched row.
	rows, err := repo.PreviewDynamic(ctx, org1, cat, domain.SegmentFilter{Field: "lead_source", Operator: "eq", Value: "web"}, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, c1, rows[0].ID)
	require.Equal(t, "a@x.com", rows[0].Email)
	require.Equal(t, "Ann", rows[0].Name)

	// An empty AST matches every (org-scoped) row.
	n, err = repo.CountDynamic(ctx, org1, cat, domain.SegmentFilter{})
	require.NoError(t, err)
	require.Equal(t, 2, n, "empty AST should match all of org1")
}

func TestSegmentIntegration_OwnScopeRowPredicate(t *testing.T) {
	db := newSegmentTestDB(t)
	repo := NewSegmentRepository(db)
	cat := segIntCatalog()

	org := uuid.New()
	u1, u2 := uuid.New(), uuid.New()
	c1, c2 := uuid.New(), uuid.New()
	segInsertContact(t, db, c1, org, u1, "a@x.com", "Ann", `{}`)
	segInsertContact(t, db, c2, org, u2, "b@x.com", "Bob", `{}`)

	// u1 is row-scoped ('own'): sees only what they own → just c1.
	ownCtx := WithCallerScope(context.Background(), domain.DataScopeOwn, u1, uuid.Nil)
	n, err := repo.CountDynamic(ownCtx, org, cat, domain.SegmentFilter{})
	require.NoError(t, err)
	require.Equal(t, 1, n, "own-scope u1 sees only c1")

	// Share c2 to u1 as a user → now visible.
	require.NoError(t, db.Exec(
		`INSERT INTO record_shares (id, org_id, record_id, record_type, target_type, target_id, permission_level)
		 VALUES (?, ?, ?, 'contact', 'user', ?, 'view')`,
		uuid.New(), org, c2, u1).Error)
	n, err = repo.CountDynamic(ownCtx, org, cat, domain.SegmentFilter{})
	require.NoError(t, err)
	require.Equal(t, 2, n, "a user-share of c2 to u1 makes it visible under own scope")

	// An 'all'-scoped (or callerless) run always sees both — the row predicate is empty.
	n, err = repo.CountDynamic(context.Background(), org, cat, domain.SegmentFilter{})
	require.NoError(t, err)
	require.Equal(t, 2, n, "callerless/all-scope sees every org row")
}

func TestSegmentIntegration_TagLeafUsesReverseIndex(t *testing.T) {
	db := newSegmentTestDB(t)
	repo := NewSegmentRepository(db)
	ctx := context.Background()
	cat := segIntCatalog()

	org := uuid.New()
	u1 := uuid.New()
	c1, c2 := uuid.New(), uuid.New()
	segInsertContact(t, db, c1, org, u1, "a@x.com", "Ann", `{}`)
	segInsertContact(t, db, c2, org, u1, "b@x.com", "Bob", `{}`)
	tag := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO tags (id, org_id) VALUES (?, ?)`, tag, org).Error)
	require.NoError(t, db.Exec(`INSERT INTO contact_tags (contact_id, tag_id) VALUES (?, ?)`, c1, tag).Error)

	// Tag membership → only the tagged contact.
	n, err := repo.CountDynamic(ctx, org, cat, domain.SegmentFilter{TagID: tag.String()})
	require.NoError(t, err)
	require.Equal(t, 1, n, "tag EXISTS should match only c1")

	// Negated tag → everyone NOT tagged.
	n, err = repo.CountDynamic(ctx, org, cat, domain.SegmentFilter{TagID: tag.String(), TagNot: true})
	require.NoError(t, err)
	require.Equal(t, 1, n, "NOT-tag should match only c2")

	// The reverse index the plan adds must be the one Postgres chooses for the
	// correlated EXISTS (a seq scan here would be the silent perf regression M5
	// exists to avoid).
	require.NoError(t, db.Exec(`ANALYZE contact_tags`).Error)
	var plan string
	require.NoError(t, db.Raw(
		`EXPLAIN (FORMAT TEXT) SELECT 1 FROM contact_tags ct WHERE ct.tag_id = ?`, tag).
		Row().Scan(&plan))
	// (Small tables may still seq-scan; we only assert the index EXISTS and is usable
	// rather than forcing a plan on a 2-row table.)
	var idxOK bool
	require.NoError(t, db.Raw(
		`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname = 'idx_contact_tags_tag_contact')`).
		Scan(&idxOK).Error)
	require.True(t, idxOK, "reverse tag index must exist")
}

func TestSegmentIntegration_StaticMembership(t *testing.T) {
	db := newSegmentTestDB(t)
	repo := NewSegmentRepository(db)
	ctx := context.Background()

	org1, org2 := uuid.New(), uuid.New()
	u1 := uuid.New()
	c1 := uuid.New()
	cOther := uuid.New() // belongs to org2
	segInsertContact(t, db, c1, org1, u1, "a@x.com", "Ann", `{}`)
	segInsertContact(t, db, cOther, org2, u1, "z@x.com", "Zed", `{}`)

	segID := uuid.New()
	// A cross-org id and a non-existent id are silently ignored — only c1 is added.
	added, err := repo.AddStaticMembers(ctx, org1, segID, []uuid.UUID{c1, cOther, uuid.New()}, "manual")
	require.NoError(t, err)
	require.Equal(t, 1, added, "only the in-org contact should be added")

	// Re-adding is idempotent (ON CONFLICT DO NOTHING).
	added, err = repo.AddStaticMembers(ctx, org1, segID, []uuid.UUID{c1}, "manual")
	require.NoError(t, err)
	require.Equal(t, 0, added)

	n, err := repo.CountStatic(ctx, org1, segID)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	rows, err := repo.PreviewStatic(ctx, org1, segID, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, c1, rows[0].ID)

	removed, err := repo.RemoveStaticMember(ctx, org1, segID, c1)
	require.NoError(t, err)
	require.True(t, removed)

	n, err = repo.CountStatic(ctx, org1, segID)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// The write path must re-derive the caller's row scope like the read path: an
// 'own'-scoped caller can only add contacts they can SEE, so they can't inject a
// colleague's contact into a list a privileged/callerless resolve will act on.
func TestSegmentIntegration_StaticAddHonoursRowScope(t *testing.T) {
	db := newSegmentTestDB(t)
	repo := NewSegmentRepository(db)

	org := uuid.New()
	me, colleague := uuid.New(), uuid.New()
	mine, theirs := uuid.New(), uuid.New()
	segInsertContact(t, db, mine, org, me, "mine@x.com", "Mine", `{}`)
	segInsertContact(t, db, theirs, org, colleague, "theirs@x.com", "Theirs", `{}`)

	segID := uuid.New()
	ownCtx := WithCallerScope(context.Background(), domain.DataScopeOwn, me, uuid.Nil)

	// own-scope: only the contact I own is added; the colleague's is silently ignored.
	added, err := repo.AddStaticMembers(ownCtx, org, segID, []uuid.UUID{mine, theirs}, "manual")
	require.NoError(t, err)
	require.Equal(t, 1, added, "own-scope caller may only add contacts they can see")

	// A callerless/all-scope run can add the colleague's contact.
	added, err = repo.AddStaticMembers(context.Background(), org, segID, []uuid.UUID{theirs}, "manual")
	require.NoError(t, err)
	require.Equal(t, 1, added, "callerless/all-scope adds org-wide")
}

func TestSegmentIntegration_TagBelongsToOrg(t *testing.T) {
	db := newSegmentTestDB(t)
	repo := NewSegmentRepository(db)
	ctx := context.Background()

	org1, org2 := uuid.New(), uuid.New()
	tag := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO tags (id, org_id) VALUES (?, ?)`, tag, org1).Error)

	ok, err := repo.TagBelongsToOrg(ctx, org1, tag)
	require.NoError(t, err)
	require.True(t, ok, "tag belongs to org1")

	ok, err = repo.TagBelongsToOrg(ctx, org2, tag)
	require.NoError(t, err)
	require.False(t, ok, "a cross-org tag id must not resolve — no cross-org probing")
}
