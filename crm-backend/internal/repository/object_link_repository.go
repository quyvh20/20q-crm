package repository

import (
	"context"
	"errors"
	"fmt"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// objectLinkRepository persists the universal relationship table (object_links)
// and bridges the legacy contact_tags join table. Keeping both behind one port
// lets RecordService present a single tag API across every object (D4) without
// widening the contact repository.
//
// All object_links reads filter on org_id; gorm's soft-delete scope keeps
// deleted edges out automatically (the model's gorm.DeletedAt maps to the
// partial-unique-indexed deleted_at column).
type objectLinkRepository struct {
	db *gorm.DB
}

func NewLinkRepository(db *gorm.DB) domain.LinkRepository {
	return &objectLinkRepository{db: db}
}

func (r *objectLinkRepository) Create(ctx context.Context, link *domain.ObjectLink) error {
	if link.ID == uuid.Nil {
		link.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(link).Error
}

func (r *objectLinkRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.ObjectLink, error) {
	var link domain.ObjectLink
	err := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *objectLinkRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&domain.ObjectLink{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *objectLinkRepository) FindEdge(ctx context.Context, orgID uuid.UUID, fromSlug string, fromID uuid.UUID, relationKey, toSlug string, toID uuid.UUID) (*domain.ObjectLink, error) {
	var link domain.ObjectLink
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND from_slug = ? AND from_id = ? AND relation_key = ? AND to_slug = ? AND to_id = ?",
			orgID, fromSlug, fromID, relationKey, toSlug, toID).
		First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *objectLinkRepository) ListFrom(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) ([]domain.ObjectLink, error) {
	var links []domain.ObjectLink
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND from_slug = ? AND from_id = ?", orgID, slug, id).
		Order("created_at ASC").
		Find(&links).Error
	return links, err
}

func (r *objectLinkRepository) CascadeSoftDelete(ctx context.Context, orgID uuid.UUID, slug string, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("org_id = ? AND ((from_slug = ? AND from_id = ?) OR (to_slug = ? AND to_id = ?))",
			orgID, slug, id, slug, id).
		Delete(&domain.ObjectLink{}).Error
}

// ============================================================
// Legacy contact_tags bridge (retired in P7)
// ============================================================
//
// contact_tags has no org_id or deleted_at — it is a plain (contact_id, tag_id)
// join keyed off the contact, which is already org-scoped. RecordService
// validates org ownership of both the contact and the tag before calling these.

func (r *objectLinkRepository) AddContactTag(ctx context.Context, contactID, tagID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(
		"INSERT INTO contact_tags (contact_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
		contactID, tagID,
	).Error
}

func (r *objectLinkRepository) RemoveContactTag(ctx context.Context, contactID, tagID uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Exec(
		"DELETE FROM contact_tags WHERE contact_id = ? AND tag_id = ?",
		contactID, tagID,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *objectLinkRepository) ListContactTagIDs(ctx context.Context, contactID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).
		Table("contact_tags").
		Where("contact_id = ?", contactID).
		Pluck("tag_id", &ids).Error
	return ids, err
}

// ============================================================
// P7 backfill: custom_object_records.{contact_id,deal_id} → object_links
// ============================================================
//
// Custom records used to relate to a contact/deal via two hardcoded FK columns.
// P7 makes object_links the single relationship store, so we copy those FKs into
// 'contact'/'deal' edges and then drop the columns. The edge relation_key matches
// the target slug, mirroring how the registry models a relation field.

// backfillRecordLinkSQL builds the idempotent INSERT for one FK column. %[1]s is the
// column name and the relation_key/to_slug (both equal the slug, e.g. "contact").
const backfillRecordLinkSQLTemplate = `
INSERT INTO object_links (id, org_id, from_slug, from_id, to_slug, to_id, relation_key, created_at)
SELECT uuid_generate_v4(), r.org_id, d.slug, r.id, '%[1]s', r.%[1]s_id, '%[1]s', NOW()
FROM custom_object_records r
JOIN custom_object_defs d ON d.id = r.object_def_id
WHERE r.%[1]s_id IS NOT NULL AND r.deleted_at IS NULL
  AND NOT EXISTS (
        SELECT 1 FROM object_links l
        WHERE l.org_id = r.org_id AND l.from_slug = d.slug AND l.from_id = r.id
          AND l.relation_key = '%[1]s' AND l.to_slug = '%[1]s' AND l.to_id = r.%[1]s_id
          AND l.deleted_at IS NULL);`

// BackfillObjectLinksFromRecordFKs is the P7 convergence step that makes object_links
// the single relationship store: it copies custom_object_records.contact_id/deal_id
// into 'contact'/'deal' edges, then drops the two legacy columns. Returns the number
// of edges inserted.
//
// Idempotent and safe to run on every boot (golang-migrate is dead on prod, so this
// runs as a boot guard). The column-existence guard makes a re-run after the columns
// are already gone a no-op, so the backfill SELECT never references a dropped column.
func BackfillObjectLinksFromRecordFKs(db *gorm.DB) (int64, error) {
	hasContact := hasColumn(db, "custom_object_records", "contact_id")
	hasDeal := hasColumn(db, "custom_object_records", "deal_id")
	if !hasContact && !hasDeal {
		return 0, nil // already converged
	}

	var total int64
	for _, c := range []struct {
		slug    string
		present bool
	}{{"contact", hasContact}, {"deal", hasDeal}} {
		if !c.present {
			continue
		}
		res := db.Exec(fmt.Sprintf(backfillRecordLinkSQLTemplate, c.slug))
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
	}

	// Edges are now authoritative — drop the hardcoded columns.
	if err := db.Exec(`ALTER TABLE custom_object_records DROP COLUMN IF EXISTS contact_id`).Error; err != nil {
		return total, err
	}
	if err := db.Exec(`ALTER TABLE custom_object_records DROP COLUMN IF EXISTS deal_id`).Error; err != nil {
		return total, err
	}
	return total, nil
}

// hasColumn reports whether a column is still present, so a backfill that reads it
// can be skipped once the column has been dropped (idempotent re-runs).
func hasColumn(db *gorm.DB, table, column string) bool {
	var n int64
	if err := db.Raw(
		`SELECT count(*) FROM information_schema.columns WHERE table_name = ? AND column_name = ?`,
		table, column,
	).Scan(&n).Error; err != nil {
		return false
	}
	return n > 0
}
