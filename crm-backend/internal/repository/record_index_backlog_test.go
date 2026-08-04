package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupBacklog builds the minimum schema the reconciliation queries touch: the two
// custom-object tables, the generic index (from the real 000018 migration, so the
// anti-join runs against the shipped DDL), and contacts/companies for the contact
// arm.
func setupBacklog(t *testing.T) (*gorm.DB, domain.RecordIndexBacklog, uuid.UUID, func()) {
	t.Helper()
	db, done := startPostgres(t)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS vector`).Error; err != nil {
		done()
		t.Skipf("pgvector extension unavailable — skipping search reconciliation test: %v", err)
	}
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		slug varchar(100) NOT NULL,
		deleted_at timestamptz
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_records (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		object_def_id uuid NOT NULL,
		display_name varchar(500) NOT NULL DEFAULT '',
		data jsonb DEFAULT '{}',
		owner_user_id uuid,
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS companies (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		name varchar(255) NOT NULL,
		industry varchar(100),
		website varchar(500),
		custom_fields jsonb DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS contacts (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		first_name varchar(100) NOT NULL,
		last_name varchar(100) NOT NULL DEFAULT '',
		email varchar(255),
		phone varchar(50),
		company_id uuid,
		owner_user_id uuid,
		custom_fields jsonb DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz,
		embedding vector(768)
	)`).Error)
	runMigrationFile(t, db, "000018_record_embeddings.up.sql")

	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	return db, NewRecordIndexBacklogRepository(db), orgID, done
}

func insertDef(t *testing.T, db *gorm.DB, orgID uuid.UUID, slug string, searchable bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO custom_object_defs (id, org_id, slug, searchable) VALUES (?, ?, ?, ?)`,
		id, orgID, slug, searchable).Error)
	return id
}

func insertRecord(t *testing.T, db *gorm.DB, orgID, defID uuid.UUID, display, data string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO custom_object_records (id, org_id, object_def_id, display_name, data) VALUES (?, ?, ?, ?, ?::jsonb)`,
		id, orgID, defID, display, data).Error)
	return id
}

func candIDs(cands []domain.RecordIndexCandidate) map[uuid.UUID]domain.RecordIndexCandidate {
	m := make(map[uuid.UUID]domain.RecordIndexCandidate, len(cands))
	for _, c := range cands {
		m[c.RecordID] = c
	}
	return m
}

// ARM 1 — the anti-join. A dropped EnqueueRecord inserts NOTHING into
// record_embeddings, so the row it should have created does not exist and no
// "WHERE embedding IS NULL" query could ever find it. Only the absence is visible.
func TestBacklog_MissingRecords_FindsRowsWithNoIndexEntryAtAll(t *testing.T) {
	db, repo, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	defID := insertDef(t, db, orgID, "ticket", true)
	dropped := insertRecord(t, db, orgID, defID, "Dropped ticket", `{"priority":"high"}`)
	indexed := insertRecord(t, db, orgID, defID, "Indexed ticket", `{}`)

	// The one that made it through the queue has a row; the dropped one has none.
	require.NoError(t, NewRecordEmbeddingRepository(db).Upsert(ctx, domain.RecordEmbedding{
		OrgID: orgID, ObjectSlug: "ticket", RecordID: indexed, Content: "Indexed ticket",
	}))

	got, err := repo.MissingRecords(ctx, 25, 100)
	require.NoError(t, err)
	byID := candIDs(got)
	require.Len(t, got, 1, "only the record with no index row at all is missing")
	require.Contains(t, byID, dropped)

	// And the content it rebuilds is the write path's, not an approximation.
	require.Equal(t, "Dropped ticket priority: high", byID[dropped].Content())
	require.Equal(t, "ticket", byID[dropped].ObjectSlug)
	require.Equal(t, orgID, byID[dropped].OrgID)
}

// THE BIGGER HOLE. UpdateDef flips `searchable` and backfills nothing, and
// indexRecord only fires on create/update — so before this sweep every record that
// existed before an admin ticked the box was invisible to search forever. The flip
// alone must make them eligible.
func TestBacklog_MissingRecords_BackfillsAfterSearchableIsTurnedOn(t *testing.T) {
	db, repo, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	// Records created while the object was NOT searchable: no enqueue ever happened.
	defID := insertDef(t, db, orgID, "ticket", false)
	a := insertRecord(t, db, orgID, defID, "Old ticket A", `{}`)
	b := insertRecord(t, db, orgID, defID, "Old ticket B", `{}`)

	before, err := repo.MissingRecords(ctx, 25, 100)
	require.NoError(t, err)
	require.Empty(t, before, "a non-searchable object contributes nothing")

	// The admin ticks "searchable" — exactly what customObjectUseCase.UpdateDef does.
	require.NoError(t, db.Exec(`UPDATE custom_object_defs SET searchable = TRUE WHERE id = ?`, defID).Error)

	after, err := repo.MissingRecords(ctx, 25, 100)
	require.NoError(t, err)
	require.Len(t, after, 2, "every pre-existing record becomes eligible the moment the flag flips")
	byID := candIDs(after)
	require.Contains(t, byID, a)
	require.Contains(t, byID, b)
}

// ARM 2 — a row that DOES exist without a vector. processRecord upserts
// content-only when the embed call fails (deliberately, so fulltext survives), and
// the anti-join is blind to those by construction.
func TestBacklog_UnvectoredRecords_FindsContentOnlyRows(t *testing.T) {
	db, repo, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	embeddings := NewRecordEmbeddingRepository(db)
	defID := insertDef(t, db, orgID, "ticket", true)
	failedEmbed := insertRecord(t, db, orgID, defID, "Embed failed", `{}`)
	fullyIndexed := insertRecord(t, db, orgID, defID, "All good", `{}`)

	require.NoError(t, embeddings.Upsert(ctx, domain.RecordEmbedding{
		OrgID: orgID, ObjectSlug: "ticket", RecordID: failedEmbed, Content: "Embed failed",
	}))
	require.NoError(t, embeddings.Upsert(ctx, domain.RecordEmbedding{
		OrgID: orgID, ObjectSlug: "ticket", RecordID: fullyIndexed,
		Content: "All good", Embedding: vec768(map[int]float32{0: 1}),
	}))

	got, err := repo.UnvectoredRecords(ctx, 25, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, failedEmbed, got[0].RecordID)

	// The two arms are disjoint: this row is present, so the anti-join must not
	// also return it (that is what makes a second arm necessary rather than
	// redundant).
	missing, err := repo.MissingRecords(ctx, 25, 100)
	require.NoError(t, err)
	require.Empty(t, missing)
}

// Eligibility rules, asserted once for both arms since they share the CTE:
// soft-deleted records, soft-deleted defs, and non-searchable defs contribute
// nothing — a sweep must never resurrect a deleted row into the index.
func TestBacklog_MissingRecords_ExcludesDeletedAndNonSearchable(t *testing.T) {
	db, repo, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	liveDef := insertDef(t, db, orgID, "ticket", true)
	live := insertRecord(t, db, orgID, liveDef, "Live", `{}`)

	deletedRec := insertRecord(t, db, orgID, liveDef, "Deleted record", `{}`)
	require.NoError(t, db.Exec(`UPDATE custom_object_records SET deleted_at = NOW() WHERE id = ?`, deletedRec).Error)

	deletedDef := insertDef(t, db, orgID, "archived", true)
	insertRecord(t, db, orgID, deletedDef, "Under a deleted def", `{}`)
	require.NoError(t, db.Exec(`UPDATE custom_object_defs SET deleted_at = NOW() WHERE id = ?`, deletedDef).Error)

	plainDef := insertDef(t, db, orgID, "note", false)
	insertRecord(t, db, orgID, plainDef, "Not searchable", `{}`)

	got, err := repo.MissingRecords(ctx, 25, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, live, got[0].RecordID)
}

// The cost + fairness ceiling. A single org's backlog must not consume the whole
// pass, or every other tenant waits behind it indefinitely.
func TestBacklog_MissingRecords_PerOrgAndGlobalCaps(t *testing.T) {
	db, repo, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	otherOrg := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, otherOrg).Error)

	bigDef := insertDef(t, db, orgID, "ticket", true)
	for i := 0; i < 10; i++ {
		insertRecord(t, db, orgID, bigDef, "Bulk", `{}`)
	}
	smallDef := insertDef(t, db, otherOrg, "ticket", true)
	quiet := insertRecord(t, db, otherOrg, smallDef, "Quiet tenant", `{}`)

	// perOrg=2 → the noisy org contributes at most 2, so the quiet org still gets in.
	got, err := repo.MissingRecords(ctx, 2, 100)
	require.NoError(t, err)
	require.Len(t, got, 3)
	byOrg := map[uuid.UUID]int{}
	for _, c := range got {
		byOrg[c.OrgID]++
	}
	require.Equal(t, 2, byOrg[orgID], "noisy org is capped per pass")
	require.Equal(t, 1, byOrg[otherOrg], "quiet org is never starved")
	require.Contains(t, candIDs(got), quiet)

	// The global cap is the hard cost ceiling on top of that.
	capped, err := repo.MissingRecords(ctx, 25, 4)
	require.NoError(t, err)
	require.Len(t, capped, 4)
}

// ARM 3 — contacts. Here the plan's original "WHERE embedding IS NULL" IS right,
// because contacts embed in place; the row is present, only the vector is missing.
func TestBacklog_UnvectoredContacts_FindsNullEmbeddingsWithCompany(t *testing.T) {
	db, repo, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	companyID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO companies (id, org_id, name) VALUES (?, ?, ?)`, companyID, orgID, "Acme").Error)

	unembedded := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, company_id) VALUES (?, ?, ?, ?, ?)`,
		unembedded, orgID, "Ada", "Lovelace", companyID).Error)

	embedded := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, embedding) VALUES (?, ?, ?, ?, ?::vector)`,
		embedded, orgID, "Alan", "Turing", vectorLiteral(vec768(map[int]float32{0: 1}))).Error)

	deleted := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, deleted_at) VALUES (?, ?, ?, NOW())`,
		deleted, orgID, "Gone").Error)

	got, err := repo.UnvectoredContacts(ctx, 25, 100)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, unembedded, got[0].ID)
	require.NotNil(t, got[0].Company, "Company must be preloaded — it is part of the embedded text")
	require.Equal(t, "Acme", got[0].Company.Name)
}

// Multi-pod safety: the second holder must be told it did not get the lock rather
// than running the same paid embedding calls a second time.
func TestBacklog_WithReconcileLock_IsFleetWideSingleton(t *testing.T) {
	db, repo, _, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	other := NewRecordIndexBacklogRepository(db)

	inner := 0
	ran, err := repo.WithReconcileLock(ctx, func() error {
		// A second "pod" tries to sweep while this one holds the lock.
		ranNested, nestedErr := other.WithReconcileLock(ctx, func() error {
			inner++
			return nil
		})
		require.NoError(t, nestedErr)
		require.False(t, ranNested, "a concurrent sweep must be refused, not duplicated")
		return nil
	})
	require.NoError(t, err)
	require.True(t, ran)
	require.Zero(t, inner)

	// Released on the way out, not leaked onto a pooled connection.
	ranAgain, err := repo.WithReconcileLock(ctx, func() error { return nil })
	require.NoError(t, err)
	require.True(t, ranAgain, "the lock must be released for the next pass")
}
