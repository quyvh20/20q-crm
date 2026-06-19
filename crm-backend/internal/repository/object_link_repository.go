package repository

import (
	"context"
	"errors"

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
