package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

	query := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts").
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

	// ── Sort clause ──────────────────────────────────────────────────────────
	allowedContactSort := map[string]string{
		"created_at": "contacts.created_at",
		"name":       "contacts.first_name",
		"email":      "contacts.email",
	}
	sortCol := "contacts.created_at"
	if col, ok := allowedContactSort[f.SortBy]; ok {
		sortCol = col
	}
	dir := "DESC"
	if strings.ToUpper(f.SortOrder) == "ASC" {
		dir = "ASC"
	}
	orderClause := fmt.Sprintf("%s %s, contacts.id %s", sortCol, dir, dir)

	err := query.
		Order(orderClause).
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
	err := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts").
		Where("contacts.id = ?", id).
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
	result := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts").
		Where("contacts.id = ?", id).
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
// Uses chunked raw SQL (500 rows/chunk) for O(n/500) round-trips instead of O(n).
// ============================================================

const bulkChunkSize = 500

func (r *contactRepository) BulkCreate(ctx context.Context, contacts []domain.Contact) (int64, error) {
	if len(contacts) == 0 {
		return 0, nil
	}

	var totalCreated int64

	for start := 0; start < len(contacts); start += bulkChunkSize {
		end := start + bulkChunkSize
		if end > len(contacts) {
			end = len(contacts)
		}
		chunk := contacts[start:end]

		// Build INSERT ... VALUES (...), (...) ON CONFLICT DO NOTHING
		// columns: id, org_id, first_name, last_name, email, phone, company_id, created_at, updated_at
		sql := "INSERT INTO contacts (id, org_id, first_name, last_name, email, phone, company_id, created_at, updated_at) VALUES "
		args := make([]interface{}, 0, len(chunk)*9)
		now := time.Now().UTC()

		for i, c := range chunk {
			id := uuid.New()
			if i > 0 {
				sql += ","
			}
			sql += "(?,?,?,?,?,?,?,?,?)"
			var emailVal interface{} = nil
			if c.Email != nil {
				emailVal = *c.Email
			}
			var phoneVal interface{} = nil
			if c.Phone != nil {
				phoneVal = *c.Phone
			}
			var companyVal interface{} = nil
			if c.CompanyID != nil {
				companyVal = *c.CompanyID
			}
			args = append(args, id, c.OrgID, c.FirstName, c.LastName, emailVal, phoneVal, companyVal, now, now)
		}

		// ON CONFLICT on partial unique index (org_id, email) where email IS NOT NULL
		sql += " ON CONFLICT DO NOTHING"

		result := r.db.WithContext(ctx).Exec(sql, args...)
		if result.Error != nil {
			// If batch fails, fall back to one-by-one for this chunk
			for i := range chunk {
				if err := r.db.WithContext(ctx).Create(&chunk[i]).Error; err == nil {
					totalCreated++
				}
			}
		} else {
			totalCreated += result.RowsAffected
		}
	}

	return totalCreated, nil
}


// ============================================================
// Count
// ============================================================

func (r *contactRepository) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var count int64
	err := applyScopeFromCtx(r.db.WithContext(ctx).Model(&domain.Contact{}), ctx, orgID, "contacts").
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

// ============================================================
// Bulk Actions
// ============================================================

func (r *contactRepository) BulkDeleteByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	result := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts").
		Where("contacts.id IN ?", ids).
		Delete(&domain.Contact{})
	return result.RowsAffected, result.Error
}

func (r *contactRepository) BulkAssignTag(ctx context.Context, orgID uuid.UUID, contactIDs []uuid.UUID, tagID uuid.UUID) (int64, error) {
	if len(contactIDs) == 0 {
		return 0, nil
	}
	// Build multi-row INSERT INTO contact_tags ON CONFLICT DO NOTHING
	sql := "INSERT INTO contact_tags (contact_id, tag_id) VALUES "
	args := make([]interface{}, 0, len(contactIDs)*2)
	for i, cid := range contactIDs {
		if i > 0 {
			sql += ","
		}
		sql += "(?,?)"
		args = append(args, cid, tagID)
	}
	sql += " ON CONFLICT DO NOTHING"
	result := r.db.WithContext(ctx).Exec(sql, args...)
	return result.RowsAffected, result.Error
}

// Ensure unused import is used
var _ = fmt.Sprintf

// ============================================================
// SemanticSearch — hybrid full-text / vector search
// ============================================================

// SemanticSearch uses the pre-computed embedding to find similar contacts.
// threshold is max cosine distance (0.0 = identical, 2.0 = opposite).
func (r *contactRepository) SemanticSearch(ctx context.Context, orgID uuid.UUID, vec []float32, threshold float32, limit int) ([]domain.Contact, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Format vector as Postgres literal: '[0.1,0.2,...]'
	vecStr := "["
	for i, v := range vec {
		if i > 0 {
			vecStr += ","
		}
		vecStr += fmt.Sprintf("%f", v)
	}
	vecStr += "]"

	var contacts []domain.Contact
	err := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts").
		Where("contacts.embedding IS NOT NULL").
		Where(fmt.Sprintf("contacts.embedding <=> '%s'::vector < ?", vecStr), threshold).
		Order(fmt.Sprintf("embedding <=> '%s'::vector", vecStr)).
		Limit(limit).
		Preload("Company").
		Preload("Owner").
		Preload("Tags").
		Find(&contacts).Error

	return contacts, err
}
