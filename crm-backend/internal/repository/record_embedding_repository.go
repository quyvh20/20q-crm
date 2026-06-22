package repository

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// recordEmbeddingRepository persists and queries the generic search index
// (record_embeddings, P6) — the one additive table that makes any object
// semantically + fulltext searchable. All queries are org-scoped and additionally
// filtered to an explicit slug allow-list, so a row for an object later un-marked
// searchable (or one the caller can't read) is simply never returned.
type recordEmbeddingRepository struct {
	db *gorm.DB
}

func NewRecordEmbeddingRepository(db *gorm.DB) domain.RecordEmbeddingRepository {
	return &recordEmbeddingRepository{db: db}
}

// Upsert writes a record's index row. When an embedding is supplied it replaces
// both vector and content; when it is absent (embed not done / failed) it updates
// content only and PRESERVES any existing vector — so a transient embedding
// outage never wipes a good vector, and fulltext stays fresh either way.
func (r *recordEmbeddingRepository) Upsert(ctx context.Context, e domain.RecordEmbedding) error {
	if e.OrgID == uuid.Nil || e.ObjectSlug == "" || e.RecordID == uuid.Nil {
		return fmt.Errorf("record embedding requires org_id, object_slug, and record_id")
	}

	if len(e.Embedding) > 0 {
		return r.db.WithContext(ctx).Exec(`
			INSERT INTO record_embeddings (org_id, object_slug, record_id, embedding, content, updated_at)
			VALUES (?, ?, ?, ?::vector, ?, NOW())
			ON CONFLICT (org_id, object_slug, record_id)
			DO UPDATE SET embedding = EXCLUDED.embedding, content = EXCLUDED.content, updated_at = NOW()`,
			e.OrgID, e.ObjectSlug, e.RecordID, vectorLiteral(e.Embedding), e.Content,
		).Error
	}

	// Content-only path: keep the prior embedding if one exists.
	return r.db.WithContext(ctx).Exec(`
		INSERT INTO record_embeddings (org_id, object_slug, record_id, content, updated_at)
		VALUES (?, ?, ?, ?, NOW())
		ON CONFLICT (org_id, object_slug, record_id)
		DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()`,
		e.OrgID, e.ObjectSlug, e.RecordID, e.Content,
	).Error
}

func (r *recordEmbeddingRepository) Delete(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(
		"DELETE FROM record_embeddings WHERE org_id = ? AND object_slug = ? AND record_id = ?",
		orgID, slug, recordID,
	).Error
}

// embeddingHitRow scans the (object_slug, record_id, distance) projection both
// search queries return.
type embeddingHitRow struct {
	ObjectSlug string    `gorm:"column:object_slug"`
	RecordID   uuid.UUID `gorm:"column:record_id"`
	Distance   float32   `gorm:"column:distance"`
}

func (r *recordEmbeddingRepository) SearchSemantic(ctx context.Context, orgID uuid.UUID, slugs []string, vec []float32, threshold float32, limit int) ([]domain.RecordEmbeddingHit, error) {
	if len(slugs) == 0 || len(vec) == 0 {
		return nil, nil
	}
	limit = clampSearchLimit(limit)
	vecStr := vectorLiteral(vec)

	var rows []embeddingHitRow
	err := r.db.WithContext(ctx).
		Table("record_embeddings").
		Select("object_slug, record_id, (embedding <=> ?::vector) AS distance", vecStr).
		Where("org_id = ?", orgID).
		Where("object_slug IN ?", slugs).
		Where("embedding IS NOT NULL").
		Where("(embedding <=> ?::vector) < ?", vecStr, threshold).
		Order("distance ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return toHits(rows), nil
}

func (r *recordEmbeddingRepository) SearchFulltext(ctx context.Context, orgID uuid.UUID, slugs []string, query string, limit int) ([]domain.RecordEmbeddingHit, error) {
	if len(slugs) == 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	limit = clampSearchLimit(limit)

	var rows []embeddingHitRow
	err := r.db.WithContext(ctx).
		Table("record_embeddings").
		Select("object_slug, record_id, 0::real AS distance").
		Where("org_id = ?", orgID).
		Where("object_slug IN ?", slugs).
		Where("to_tsvector('simple', coalesce(content, '')) @@ plainto_tsquery('simple', ?)", query).
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return toHits(rows), nil
}

// ============================================================
// Helpers
// ============================================================

func toHits(rows []embeddingHitRow) []domain.RecordEmbeddingHit {
	hits := make([]domain.RecordEmbeddingHit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, domain.RecordEmbeddingHit{
			ObjectSlug: row.ObjectSlug,
			RecordID:   row.RecordID,
			Distance:   row.Distance,
		})
	}
	return hits
}

func clampSearchLimit(limit int) int {
	if limit <= 0 || limit > 50 {
		return 20
	}
	return limit
}

// vectorLiteral formats a float slice as the Postgres pgvector text form
// '[0.1,0.2,...]'. The values are floats we control, so building the string is
// injection-safe; it is always passed as a bound parameter cast with ?::vector.
func vectorLiteral(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
