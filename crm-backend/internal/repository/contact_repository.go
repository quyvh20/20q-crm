package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type contactRepository struct {
	db *gorm.DB
}

func NewContactRepository(db *gorm.DB) domain.ContactRepository {
	return &contactRepository{db: db}
}

// cursor encodes created_at + id for stable pagination
type cursorData struct {
	CreatedAt time.Time `json:"c"`
	ID        uuid.UUID `json:"i"`
}

func encodeCursor(c cursorData) string {
	b, _ := json.Marshal(c)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorData, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return cursorData{}, err
	}
	var c cursorData
	if err := json.Unmarshal(b, &c); err != nil {
		return cursorData{}, err
	}
	return c, nil
}

// ============================================================
// List — cursor pagination + full-text search
// ============================================================

func (r *contactRepository) List(ctx context.Context, orgID uuid.UUID, f domain.ContactFilter) ([]domain.Contact, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	query := r.db.WithContext(ctx).
		Where("contacts.org_id = ?", orgID).
		Preload("Company").
		Preload("Tags").
		Preload("Owner")

	// Full-text search
	if f.Q != "" {
		query = query.Where(
			"to_tsvector('simple', contacts.first_name || ' ' || contacts.last_name || ' ' || COALESCE(contacts.email, '')) @@ plainto_tsquery('simple', ?)",
			f.Q,
		)
	}

	// Filters
	if f.CompanyID != nil {
		query = query.Where("contacts.company_id = ?", *f.CompanyID)
	}
	if f.OwnerUserID != nil {
		query = query.Where("contacts.owner_user_id = ?", *f.OwnerUserID)
	}
	if len(f.TagIDs) > 0 {
		query = query.Where("contacts.id IN (SELECT contact_id FROM contact_tags WHERE tag_id IN ?)", f.TagIDs)
	}

	// Cursor pagination (keyset)
	if f.Cursor != "" {
		cur, err := decodeCursor(f.Cursor)
		if err == nil {
			query = query.Where(
				"(contacts.created_at, contacts.id) < (?, ?)",
				cur.CreatedAt, cur.ID,
			)
		}
	}

	var contacts []domain.Contact
	err := query.
		Order("contacts.created_at DESC, contacts.id DESC").
		Limit(limit + 1). // fetch one extra to determine if there's a next page
		Find(&contacts).Error
	if err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(contacts) > limit {
		last := contacts[limit-1]
		nextCursor = encodeCursor(cursorData{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
		contacts = contacts[:limit]
	}

	return contacts, nextCursor, nil
}

// ============================================================
// GetByID
// ============================================================

func (r *contactRepository) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Preload("Company").
		Preload("Tags").
		Preload("Owner").
		First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

// ============================================================
// Create
// ============================================================

func (r *contactRepository) Create(ctx context.Context, c *domain.Contact) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// ============================================================
// Update
// ============================================================

func (r *contactRepository) Update(ctx context.Context, c *domain.Contact) error {
	return r.db.WithContext(ctx).Save(c).Error
}

// ============================================================
// SoftDelete
// ============================================================

func (r *contactRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	result := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&domain.Contact{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// ============================================================
// BulkCreate — batch insert, skip on email conflict per org
// ============================================================

func (r *contactRepository) BulkCreate(ctx context.Context, contacts []domain.Contact) (int64, error) {
	if len(contacts) == 0 {
		return 0, nil
	}

	// Use simple batch insert — deduplication is handled at the usecase level
	// The partial unique index will cause failures for true duplicates,
	// so we insert one-by-one to skip conflicts gracefully
	var created int64
	for i := range contacts {
		err := r.db.WithContext(ctx).Create(&contacts[i]).Error
		if err != nil {
			// Skip duplicate email conflicts
			continue
		}
		created++
	}

	return created, nil
}

// ============================================================
// Count
// ============================================================

func (r *contactRepository) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&domain.Contact{}).
		Where("org_id = ?", orgID).
		Count(&count).Error
	return count, err
}

// ============================================================
// Tag Helpers
// ============================================================

func (r *contactRepository) FindTagsByNames(ctx context.Context, orgID uuid.UUID, names []string) ([]domain.Tag, error) {
	var tags []domain.Tag
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND name IN ?", orgID, names).
		Find(&tags).Error
	return tags, err
}

func (r *contactRepository) CreateTags(ctx context.Context, tags []domain.Tag) error {
	if len(tags) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&tags).Error
}

func (r *contactRepository) ReplaceContactTags(ctx context.Context, contactID uuid.UUID, tagIDs []uuid.UUID) error {
	// Remove existing
	if err := r.db.WithContext(ctx).Exec(
		"DELETE FROM contact_tags WHERE contact_id = ?", contactID,
	).Error; err != nil {
		return err
	}
	// Insert new
	if len(tagIDs) == 0 {
		return nil
	}
	type contactTag struct {
		ContactID uuid.UUID `gorm:"type:uuid"`
		TagID     uuid.UUID `gorm:"type:uuid"`
	}
	var rows []contactTag
	for _, tid := range tagIDs {
		rows = append(rows, contactTag{ContactID: contactID, TagID: tid})
	}
	return r.db.WithContext(ctx).
		Table("contact_tags").
		Create(&rows).Error
}

// ============================================================
// Company Helpers
// ============================================================

func (r *contactRepository) FindCompanyByName(ctx context.Context, orgID uuid.UUID, name string) (*domain.Company, error) {
	var company domain.Company
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND name = ?", orgID, name).
		First(&company).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &company, nil
}

func (r *contactRepository) CreateCompany(ctx context.Context, c *domain.Company) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// Ensure unused import is used
var _ = fmt.Sprintf
