package repository

import (
	"context"
	"fmt"

	"crm-backend/internal/domain"

	"gorm.io/gorm"
)

// recordIndexBacklogRepository answers "what is missing from the search indexes?"
// for the reconciliation sweep (R6.3). See domain.RecordIndexBacklog for why the
// two indexes need three different questions asked of them.
//
// Every query is bounded twice: `perOrg` caps how many rows a SINGLE org can
// contribute to one pass, and `limit` caps the pass overall. The per-org cap is
// the one that matters — without it, one org backfilling a 500k-record object
// would own every sweep for days and no other tenant's records would ever be
// indexed. Rows come back oldest-first (updated_at ASC), which makes the sweep
// progressive: a row that indexes successfully drops out of the next pass, so the
// backlog drains front-to-back instead of re-serving the same head forever.
type recordIndexBacklogRepository struct {
	db *gorm.DB
}

func NewRecordIndexBacklogRepository(db *gorm.DB) domain.RecordIndexBacklog {
	return &recordIndexBacklogRepository{db: db}
}

// searchableRecordCTE is the shared body of both record arms: every LIVE record of
// every LIVE object def that is opted into search, joined to its index row. The two
// arms differ only in how they filter that join (`joinFilter`), so the eligibility
// rules — soft-deleted records excluded, soft-deleted defs excluded, def and record
// in the same org — are written once and cannot diverge between them.
//
// `d.org_id = r.org_id` is redundant against the FK but free, and it keeps the
// join honest if a def is ever moved.
const searchableRecordCTE = `
SELECT org_id, object_slug, record_id, display_name, data, owner_user_id
FROM (
    SELECT r.org_id           AS org_id,
           d.slug             AS object_slug,
           r.id               AS record_id,
           r.display_name     AS display_name,
           r.data             AS data,
           r.owner_user_id    AS owner_user_id,
           r.updated_at       AS updated_at,
           ROW_NUMBER() OVER (PARTITION BY r.org_id ORDER BY r.updated_at ASC, r.id ASC) AS rn
    FROM custom_object_records r
    JOIN custom_object_defs d
      ON d.id = r.object_def_id
     AND d.org_id = r.org_id
     AND d.deleted_at IS NULL
     AND d.searchable
    %s
    WHERE r.deleted_at IS NULL
      AND %s
) c
WHERE c.rn <= ?
ORDER BY c.updated_at ASC, c.record_id ASC
LIMIT ?`

// MissingRecords is the ANTI-JOIN arm, and the reason a NULL-embedding sweep was
// never going to be enough. A custom record is indexed into a SEPARATE table by the
// worker, so a dropped EnqueueRecord (queue full, pod restart) inserts NOTHING —
// there is no row with a NULL embedding to find, only an absence. This is also the
// arm that backfills an object whose `searchable` flag was turned on after its
// records already existed: UpdateDef flips the flag and busts the schema cache but
// indexes nothing, and indexRecord only ever fires on create/update, so every
// pre-existing record is invisible to search until someone happens to edit it.
func (r *recordIndexBacklogRepository) MissingRecords(ctx context.Context, perOrg, limit int) ([]domain.RecordIndexCandidate, error) {
	return r.records(ctx, perOrg, limit,
		`LEFT JOIN record_embeddings re
      ON re.org_id = r.org_id AND re.object_slug = d.slug AND re.record_id = r.id`,
		`re.record_id IS NULL`)
}

// UnvectoredRecords is the second arm, and it is genuinely needed alongside the
// anti-join: processRecord upserts content-only when the embed CALL fails, on
// purpose, so fulltext keeps working while semantic search waits. Those rows DO
// exist with a NULL embedding, and only this arm can see them.
func (r *recordIndexBacklogRepository) UnvectoredRecords(ctx context.Context, perOrg, limit int) ([]domain.RecordIndexCandidate, error) {
	return r.records(ctx, perOrg, limit,
		`JOIN record_embeddings re
      ON re.org_id = r.org_id AND re.object_slug = d.slug AND re.record_id = r.id`,
		`re.embedding IS NULL`)
}

func (r *recordIndexBacklogRepository) records(ctx context.Context, perOrg, limit int, join, filter string) ([]domain.RecordIndexCandidate, error) {
	perOrg, limit = clampBacklogBounds(perOrg, limit)
	// join/filter are compile-time constants from the two methods above — never
	// caller input — so the format is not an injection surface; the two bounds are
	// bound parameters.
	sql := fmt.Sprintf(searchableRecordCTE, join, filter)

	var out []domain.RecordIndexCandidate
	if err := r.db.WithContext(ctx).Raw(sql, perOrg, limit).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// UnvectoredContacts is the contact arm, and here the plan's original
// "WHERE embedding IS NULL" IS correct: contacts embed in place on their own
// column, so a dropped enqueue leaves the row visible with a NULL vector.
// Company is preloaded because the embedded text includes the company name
// (worker.EnqueueContact), and a sweep that embedded a different text than the
// write path would quietly produce two populations of vectors.
func (r *recordIndexBacklogRepository) UnvectoredContacts(ctx context.Context, perOrg, limit int) ([]domain.Contact, error) {
	perOrg, limit = clampBacklogBounds(perOrg, limit)

	var ids []struct {
		ID string `gorm:"column:id"`
	}
	err := r.db.WithContext(ctx).Raw(`
		SELECT id FROM (
		    SELECT c.id AS id, c.updated_at AS updated_at,
		           ROW_NUMBER() OVER (PARTITION BY c.org_id ORDER BY c.updated_at ASC, c.id ASC) AS rn
		    FROM contacts c
		    WHERE c.deleted_at IS NULL AND c.embedding IS NULL
		) t
		WHERE t.rn <= ?
		ORDER BY t.updated_at ASC, t.id ASC
		LIMIT ?`, perOrg, limit).Scan(&ids).Error
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	raw := make([]string, len(ids))
	for i := range ids {
		raw[i] = ids[i].ID
	}

	// Second hop rather than one query: Preload cannot ride a Raw scan, and
	// selecting contacts.* directly would drag the vector(768) column across the
	// wire for every row only to throw it away.
	var contacts []domain.Contact
	if err := r.db.WithContext(ctx).
		Preload("Company").
		Omit("Embedding").
		Where("id IN ?", raw).
		Find(&contacts).Error; err != nil {
		return nil, err
	}
	return contacts, nil
}

// WithReconcileLock serializes the sweep across the fleet.
//
// This one takes a lock where most pruners do not, and the reason is money rather
// than correctness: the upserts are idempotent, so N pods sweeping concurrently
// would converge on the same index — but each of them would pay for the SAME
// embedding calls through the Cloudflare AI gateway, multiplying the bill by the
// replica count for zero benefit. The automation scheduler's timer scan is the
// precedent, and the pinned-connection shape is copied from
// integrations.Repository.withAdvisoryLock: a session-level advisory lock belongs
// to the CONNECTION that took it, and locking/unlocking through a pool can release
// nothing and leak the lock until that backend is recycled — after which no replica
// would ever sweep again.
func (r *recordIndexBacklogRepository) WithReconcileLock(ctx context.Context, fn func() error) (bool, error) {
	sqlDB, err := r.db.DB()
	if err != nil {
		return false, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock(hashtext($1))", "search_index_reconcile").Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext($1))", "search_index_reconcile")
	}()

	return true, fn()
}

// clampBacklogBounds keeps a caller from turning the sweep into an unbounded scan
// (or a no-op) by passing 0/negative caps. The hard ceilings are defence in depth:
// the worker sets the real policy, this just makes a bad call site survivable.
func clampBacklogBounds(perOrg, limit int) (int, int) {
	if perOrg <= 0 || perOrg > 1000 {
		perOrg = 25
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if perOrg > limit {
		perOrg = limit
	}
	return perOrg, limit
}
