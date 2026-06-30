package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// recordNumberRepository allocates and resolves human-readable record numbers,
// stored in a side table (record_numbers) keyed by (org, object_slug, record_id)
// with a per-(org, object) counter (object_number_seqs). The same allocator works
// for typed and JSONB objects, so RecordService can number every object uniformly.
type recordNumberRepository struct {
	db *gorm.DB
}

func NewRecordNumberRepository(db *gorm.DB) domain.RecordNumberRepository {
	return &recordNumberRepository{db: db}
}

// Allocate assigns the next sequence to a record. It is idempotent: a record that
// already has a number is left untouched (the INSERT ... ON CONFLICT DO NOTHING on
// record_numbers' PK absorbs a retry). The counter is bumped atomically in a single
// UPSERT so concurrent creates can't collide on a seq.
func (r *recordNumberRepository) Allocate(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Already numbered? Nothing to do.
		var existing int64
		if err := tx.Table("record_numbers").
			Where("org_id = ? AND object_slug = ? AND record_id = ?", orgID, slug, recordID).
			Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			return nil
		}

		// Atomically allocate the next seq: first allocation seeds next_seq=2 and
		// takes 1; subsequent ones bump next_seq and take the prior value.
		var seq int64
		if err := tx.Raw(`
			INSERT INTO object_number_seqs (org_id, object_slug, next_seq)
			VALUES (?, ?, 2)
			ON CONFLICT (org_id, object_slug)
			DO UPDATE SET next_seq = object_number_seqs.next_seq + 1
			RETURNING next_seq - 1`, orgID, slug).Scan(&seq).Error; err != nil {
			return err
		}

		return tx.Exec(`
			INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (org_id, object_slug, record_id) DO NOTHING`,
			orgID, slug, recordID, seq).Error
	})
}

// NumbersFor returns formatted numbers (prefix + "-" + zero-padded seq) for the
// given record ids, using the object's current prefix (falling back to the
// uppercased slug). Ids without an assigned seq are omitted from the map.
func (r *recordNumberRepository) NumbersFor(ctx context.Context, orgID uuid.UUID, slug string, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		RecordID uuid.UUID
		Number   string
	}
	var rows []row
	// COALESCE(number_prefix, UPPER(slug)) keeps unset/new objects rendering as
	// e.g. DEAL-0001 without a populated prefix; LPAD pads to 4 digits but a longer
	// seq simply overflows the pad (DEAL-12345), never truncates.
	if err := r.db.WithContext(ctx).Raw(`
		SELECT rn.record_id AS record_id,
		       COALESCE(NULLIF(d.number_prefix, ''), UPPER(rn.object_slug)) || '-' || LPAD(rn.seq::text, 4, '0') AS number
		FROM record_numbers rn
		LEFT JOIN object_defs d
		       ON d.org_id = rn.org_id AND d.slug = rn.object_slug AND d.deleted_at IS NULL
		WHERE rn.org_id = ? AND rn.object_slug = ? AND rn.record_id IN ?`,
		orgID, slug, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.RecordID] = rw.Number
	}
	return out, nil
}

// backfillRecordNumberSQL assigns a seq to every existing record per (org, object)
// in created_at order, for one source table mapped to a slug. Idempotent via the
// NOT EXISTS guard; re-runs assign numbers only to records added since last run,
// continuing after the current max seq.
func backfillRecordNumberSQL(table, slug string) string {
	return `
		INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
		SELECT t.org_id, '` + slug + `', t.id,
		       COALESCE(m.maxseq, 0) + ROW_NUMBER() OVER (PARTITION BY t.org_id ORDER BY t.created_at, t.id)
		FROM ` + table + ` t
		LEFT JOIN (SELECT org_id, MAX(seq) AS maxseq FROM record_numbers WHERE object_slug='` + slug + `' GROUP BY org_id) m
		       ON m.org_id = t.org_id
		WHERE t.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM record_numbers r WHERE r.org_id=t.org_id AND r.object_slug='` + slug + `' AND r.record_id=t.id)`
}

// BackfillRecordNumbers assigns numbers to all existing records (system + custom)
// and seeds the per-object counters past the current max. Boot-guarded and
// idempotent, mirroring migrations/000023_record_numbers.up.sql. Returns the number
// of records numbered on this run.
func BackfillRecordNumbers(db *gorm.DB) (int64, error) {
	var total int64
	for _, ts := range []struct{ table, slug string }{
		{"contacts", "contact"}, {"companies", "company"}, {"deals", "deal"},
	} {
		res := db.Exec(backfillRecordNumberSQL(ts.table, ts.slug))
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
	}

	// Custom objects: slug comes from object_defs (joined on object_def_id).
	resCustom := db.Exec(`
		INSERT INTO record_numbers (org_id, object_slug, record_id, seq)
		SELECT r.org_id, d.slug, r.id,
		       COALESCE(m.maxseq, 0) + ROW_NUMBER() OVER (PARTITION BY r.org_id, d.slug ORDER BY r.created_at, r.id)
		FROM custom_object_records r
		JOIN object_defs d ON d.id = r.object_def_id AND d.is_system = false AND d.deleted_at IS NULL
		LEFT JOIN (SELECT org_id, object_slug, MAX(seq) AS maxseq FROM record_numbers GROUP BY org_id, object_slug) m
		       ON m.org_id=r.org_id AND m.object_slug=d.slug
		WHERE r.deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM record_numbers x WHERE x.org_id=r.org_id AND x.object_slug=d.slug AND x.record_id=r.id)`)
	if resCustom.Error != nil {
		return total, resCustom.Error
	}
	total += resCustom.RowsAffected

	// Seed/advance the counters past the highest assigned seq for each object.
	if err := db.Exec(`
		INSERT INTO object_number_seqs (org_id, object_slug, next_seq)
		SELECT org_id, object_slug, MAX(seq) + 1 FROM record_numbers GROUP BY org_id, object_slug
		ON CONFLICT (org_id, object_slug) DO UPDATE SET next_seq = GREATEST(object_number_seqs.next_seq, EXCLUDED.next_seq)`).Error; err != nil {
		return total, err
	}
	return total, nil
}
