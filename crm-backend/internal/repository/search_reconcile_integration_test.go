package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"
	"crm-backend/internal/worker"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// End-to-end reconciliation over the real schema: the worker's sweep, the real
// backlog queries, and the real record_embeddings repository, wired exactly as
// main.go wires them. It lives in this package because this is where the Postgres
// harness (startPostgres / runMigrationFile) lives; internal/worker imports only
// domain + ai, so there is no cycle.
//
// The embed service is deliberately nil, which is both network-free and the honest
// shape of the arm that matters: a missing index ROW is invisible to fulltext AND
// semantic search, and restoring it restores fulltext immediately even when
// embedding is unavailable. The two vector-fill arms need a live embedder, so they
// are covered by the query tests here plus the worker's dispatch unit tests.

func countIndexRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM record_embeddings`).Scan(&n).Error)
	return n
}

func indexedContent(t *testing.T, db *gorm.DB, orgID uuid.UUID, slug string, id uuid.UUID) string {
	t.Helper()
	var content string
	require.NoError(t, db.Raw(
		`SELECT coalesce(content, '') FROM record_embeddings WHERE org_id = ? AND object_slug = ? AND record_id = ?`,
		orgID, slug, id).Scan(&content).Error)
	return content
}

// The whole point of R6.3, proven end to end: an object marked searchable AFTER its
// records already existed has none of them in the index (UpdateDef backfills
// nothing and indexRecord only fires on create/update), and one sweep fixes it.
func TestReconcile_EndToEnd_BackfillsRecordsAfterSearchableFlip(t *testing.T) {
	db, backlog, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	defID := insertDef(t, db, orgID, "ticket", false)
	a := insertRecord(t, db, orgID, defID, "Roof leak", `{"priority":"high"}`)
	b := insertRecord(t, db, orgID, defID, "Broken lift", `{"priority":"low"}`)

	w := worker.NewEmbeddingWorker(nil, NewRecordEmbeddingRepository(db), db, zap.NewNop(), 10)
	w.SetBacklog(backlog)

	// Nothing is searchable yet, so a sweep must not invent index rows.
	stats, ran, err := w.ReconcileOnce(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Zero(t, stats.MissingRecords)
	require.Zero(t, countIndexRows(t, db))

	// The admin ticks "searchable" — the write path indexes nothing retroactively.
	require.NoError(t, db.Exec(`UPDATE custom_object_defs SET searchable = TRUE WHERE id = ?`, defID).Error)
	require.Zero(t, countIndexRows(t, db), "flipping the flag alone indexes nothing")

	stats, ran, err = w.ReconcileOnce(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 2, stats.MissingRecords)
	require.Equal(t, int64(2), countIndexRows(t, db))

	// Content is the write path's, so these rows are fulltext-searchable now.
	require.Equal(t, "Roof leak priority: high", indexedContent(t, db, orgID, "ticket", a))
	require.Equal(t, "Broken lift priority: low", indexedContent(t, db, orgID, "ticket", b))

	// Idempotent: a drained backlog is a no-op, not a re-index (and not a re-bill).
	stats, ran, err = w.ReconcileOnce(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Zero(t, stats.MissingRecords)
	require.Equal(t, int64(2), countIndexRows(t, db))
}

// A record whose enqueue was dropped (queue full, or a pod restart that discarded
// the buffer) presents identically — no index row at all — and the same sweep
// repairs it without disturbing the records that made it through.
func TestReconcile_EndToEnd_RepairsDroppedEnqueue(t *testing.T) {
	db, backlog, orgID, cleanup := setupBacklog(t)
	defer cleanup()
	ctx := context.Background()

	defID := insertDef(t, db, orgID, "ticket", true)
	survived := insertRecord(t, db, orgID, defID, "Survived", `{}`)
	dropped := insertRecord(t, db, orgID, defID, "Dropped", `{}`)

	// The write path indexed one of the two; the other's enqueue was dropped.
	embeddings := NewRecordEmbeddingRepository(db)
	require.NoError(t, embeddings.Upsert(ctx, domain.RecordEmbedding{
		OrgID: orgID, ObjectSlug: "ticket", RecordID: survived,
		Content: "Survived", Embedding: vec768(map[int]float32{0: 1}),
	}))

	w := worker.NewEmbeddingWorker(nil, embeddings, db, zap.NewNop(), 10)
	w.SetBacklog(backlog)

	stats, ran, err := w.ReconcileOnce(ctx)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, 1, stats.MissingRecords, "only the dropped record is re-indexed")
	require.Equal(t, "Dropped", indexedContent(t, db, orgID, "ticket", dropped))

	// The survivor's vector is untouched — the sweep must not blow away good work.
	var hasVec bool
	require.NoError(t, db.Raw(
		`SELECT embedding IS NOT NULL FROM record_embeddings WHERE org_id = ? AND record_id = ?`,
		orgID, survived).Scan(&hasVec).Error)
	require.True(t, hasVec)
}

// A nil backlog (the pre-R6.3 wiring, and unit tests) leaves the sweep inert
// rather than panicking.
func TestReconcile_EndToEnd_NoBacklogIsInert(t *testing.T) {
	db, _, _, cleanup := setupBacklog(t)
	defer cleanup()

	w := worker.NewEmbeddingWorker(nil, NewRecordEmbeddingRepository(db), db, zap.NewNop(), 10)
	stats, ran, err := w.ReconcileOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	require.Zero(t, stats.MissingRecords)
}
