package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// vec768 builds a 768-dim float vector with the given non-zero positions set, the
// rest left zero — enough to control cosine distances deterministically.
func vec768(set map[int]float32) []float32 {
	v := make([]float32, 768)
	for i, x := range set {
		v[i] = x
	}
	return v
}

func setupEmbeddings(t *testing.T) (repo domain.RecordEmbeddingRepository, orgID uuid.UUID, cleanup func()) {
	t.Helper()
	db, done := startPostgres(t)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS vector`).Error; err != nil {
		done()
		t.Skipf("pgvector extension unavailable — skipping record_embeddings integration test: %v", err)
	}
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	runMigrationFile(t, db, "000018_record_embeddings.up.sql")

	orgID = uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	return NewRecordEmbeddingRepository(db), orgID, done
}

// TestMigration000018_UpDownRoundTrip proves .down drops cleanly and .up is
// re-runnable (up → down → up), including the custom_object_defs.searchable column.
func TestMigration000018_UpDownRoundTrip(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS vector`).Error; err != nil {
		t.Skipf("pgvector extension unavailable — skipping: %v", err)
	}
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)

	runMigrationFile(t, db, "000018_record_embeddings.up.sql")
	require.True(t, tableExists(t, db, "record_embeddings"), "record_embeddings should exist after up")
	require.True(t, columnExists(t, db, "custom_object_defs", "searchable"), "searchable column should exist after up")

	runMigrationFile(t, db, "000018_record_embeddings.down.sql")
	require.False(t, tableExists(t, db, "record_embeddings"), "record_embeddings should be gone after down")
	require.False(t, columnExists(t, db, "custom_object_defs", "searchable"), "searchable column should be gone after down")

	runMigrationFile(t, db, "000018_record_embeddings.up.sql")
	require.True(t, tableExists(t, db, "record_embeddings"), "record_embeddings should exist after re-up")
}

func TestRecordEmbeddingRepository_SemanticRankingAndThreshold(t *testing.T) {
	repo, orgID, cleanup := setupEmbeddings(t)
	defer cleanup()
	ctx := context.Background()

	near := uuid.New()  // identical to the query → distance 0
	mid := uuid.New()   // ~0.29 distance → within threshold
	far := uuid.New()   // orthogonal → distance 1.0 → beyond threshold
	other := uuid.New() // different slug → excluded by the slug filter

	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: near, Embedding: vec768(map[int]float32{0: 1}), Content: "alpha"}))
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: mid, Embedding: vec768(map[int]float32{0: 0.7, 1: 0.7}), Content: "bravo"}))
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: far, Embedding: vec768(map[int]float32{1: 1}), Content: "charlie"}))
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "secret", RecordID: other, Embedding: vec768(map[int]float32{0: 1}), Content: "delta"}))

	query := vec768(map[int]float32{0: 1})
	hits, err := repo.SearchSemantic(ctx, orgID, []string{"ticket"}, query, 0.5, 10)
	require.NoError(t, err)

	// near + mid within threshold, far excluded, other excluded by slug filter.
	require.Len(t, hits, 2)
	require.Equal(t, near, hits[0].RecordID, "identical vector should rank first")
	require.Equal(t, mid, hits[1].RecordID)
	require.InDelta(t, 0.0, hits[0].Distance, 0.001)
	require.InDelta(t, 0.293, hits[1].Distance, 0.02)
}

func TestRecordEmbeddingRepository_Fulltext(t *testing.T) {
	repo, orgID, cleanup := setupEmbeddings(t)
	defer cleanup()
	ctx := context.Background()

	a := uuid.New()
	b := uuid.New()
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: a, Content: "alpha bravo charlie"}))
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: b, Content: "delta echo"}))

	hits, err := repo.SearchFulltext(ctx, orgID, []string{"ticket"}, "bravo", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, a, hits[0].RecordID)

	// A slug not in the allow-list returns nothing even on a content match.
	none, err := repo.SearchFulltext(ctx, orgID, []string{"other"}, "bravo", 10)
	require.NoError(t, err)
	require.Empty(t, none)
}

// Content-only upsert must keep a previously stored vector (so a transient embed
// failure never wipes semantic searchability), while refreshing fulltext content.
func TestRecordEmbeddingRepository_ContentOnlyPreservesVector(t *testing.T) {
	repo, orgID, cleanup := setupEmbeddings(t)
	defer cleanup()
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: id, Embedding: vec768(map[int]float32{0: 1}), Content: "old text"}))

	// Re-upsert with content only (no embedding) — simulates an embed outage.
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: id, Content: "new text"}))

	// Vector survived: semantic search still finds it.
	hits, err := repo.SearchSemantic(ctx, orgID, []string{"ticket"}, vec768(map[int]float32{0: 1}), 0.5, 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, id, hits[0].RecordID)

	// Content refreshed: fulltext now matches the new text, not the old.
	newHits, err := repo.SearchFulltext(ctx, orgID, []string{"ticket"}, "new", 10)
	require.NoError(t, err)
	require.Len(t, newHits, 1)
	oldHits, err := repo.SearchFulltext(ctx, orgID, []string{"ticket"}, "old", 10)
	require.NoError(t, err)
	require.Empty(t, oldHits)
}

func TestRecordEmbeddingRepository_Delete(t *testing.T) {
	repo, orgID, cleanup := setupEmbeddings(t)
	defer cleanup()
	ctx := context.Background()

	id := uuid.New()
	require.NoError(t, repo.Upsert(ctx, domain.RecordEmbedding{OrgID: orgID, ObjectSlug: "ticket", RecordID: id, Embedding: vec768(map[int]float32{0: 1}), Content: "alpha"}))
	require.NoError(t, repo.Delete(ctx, orgID, "ticket", id))

	hits, err := repo.SearchSemantic(ctx, orgID, []string{"ticket"}, vec768(map[int]float32{0: 1}), 0.5, 10)
	require.NoError(t, err)
	require.Empty(t, hits, "deleted row must not be searchable")

	// Deleting a missing row is a no-op (idempotent).
	require.NoError(t, repo.Delete(ctx, orgID, "ticket", uuid.New()))
}
