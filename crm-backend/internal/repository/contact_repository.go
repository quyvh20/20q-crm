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

// phoneSearchVariants decides whether a search term is a phone number and, if so,
// returns the digit strings worth matching it against. An empty result means "this
// is not a phone number, search it as text".
//
// A term qualifies only when it is ENTIRELY digits and phone punctuation, with at
// least minPhoneSearchDigits digits. Both halves matter: the punctuation check keeps
// "Suite 500" and "Level 3 Support" out (they'd otherwise reduce to stray digits),
// and the length floor keeps a 3-digit term from matching half the org.
//
// Unlike FindByNormalizedPhone — which is dedupe and stays deliberately strict
// because a wrong match there MERGES two people — this is search, where a wrong
// match only shows an extra row the user can ignore. That asymmetry is why the
// leading-1 variant below is safe here and would not be safe there. Do not
// "harmonize" the two.
func phoneSearchVariants(q string) []string {
	digits := make([]rune, 0, len(q))
	for _, r := range q {
		switch {
		case r >= '0' && r <= '9':
			digits = append(digits, r)
		case r == ' ' || r == '+' || r == '-' || r == '(' || r == ')' || r == '.':
			// phone punctuation — allowed, contributes nothing
		default:
			return nil // a letter (or anything else) means this is a text search
		}
	}
	if len(digits) < minPhoneSearchDigits {
		return nil
	}

	d := string(digits)
	out := []string{d}
	// Stored numbers are raw user input, so the same line lives in the DB both with
	// and without a US country code. Matching both forms is two index lookups.
	switch {
	case len(d) == 11 && d[0] == '1':
		out = append(out, d[1:])
	case len(d) == 10:
		out = append(out, "1"+d)
	}
	return out
}

// minPhoneSearchDigits mirrors integrations.minPhoneDigits: shorter than this and a
// "phone" is really just a number the user typed.
const minPhoneSearchDigits = 7

// ============================================================
// List — cursor pagination + full-text search
// ============================================================

func (r *contactRepository) List(ctx context.Context, orgID uuid.UUID, f domain.ContactFilter) ([]domain.Contact, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}

	query := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts", "contact").
		Preload("Company").
		Preload("Tags").
		Preload("Owner")

	// Full-text search over name + email, OR'd with the related company's name, and
	// with a digits match when the term looks like a phone number. The tsvector
	// deliberately does not index phone (formatting makes it useless as a text
	// token), so before that branch existed searching "501-222-7363" tokenized into
	// terms the vector could never contain and returned zero rows for a contact that
	// was sitting right there.
	if f.Q != "" {
		const textMatch = "to_tsvector('simple', contacts.first_name || ' ' || contacts.last_name || ' ' || COALESCE(contacts.email, '')) @@ plainto_tsquery('simple', ?)"

		// "Everyone at Acme" is a contact search users expect to work, but the company
		// name lives on another table and so cannot join the contacts tsvector.
		//
		// A subquery rather than a JOIN: the relation is many-to-one so a join would
		// need no DISTINCT today, but it would still fight Preload("Company") and widen
		// the select list, and this file already spells "contact matches a related row"
		// this way — see the TagIDs filter below.
		//
		// Same tokenizer as the name/email half, so one term behaves consistently
		// across both: "Acme" finds Acme Corporation, and a prefix like "Acm" finds
		// neither. org_id is not redundant despite company_id already being org-local —
		// it lets the planner cut the company scan to one org before the GIN probe.
		// deleted_at IS NULL has to be written out: GORM's soft-delete clause does not
		// reach inside a raw subquery, so without it a deleted company would go on
		// matching its contacts.
		const companyMatch = `contacts.company_id IN (
			SELECT c.id FROM companies c
			WHERE c.org_id = ? AND c.deleted_at IS NULL
			  AND to_tsvector('simple', c.name) @@ plainto_tsquery('simple', ?)
		)`

		// The company arm is unscoped by design. Every contact row returned still goes
		// through applyScopeFromCtx above, so this widens which of the caller's OWN
		// contacts match — never which contacts they see. The company name it matches
		// on is already Preloaded onto those same rows.
		cond := r.db.Where(textMatch, f.Q).Or(companyMatch, orgID, f.Q)

		if variants := phoneSearchVariants(f.Q); len(variants) > 0 {
			// The digits expression is character-identical to idx_contacts_org_phone_digits
			// (and to integrations.normalizePhone) so this stays sargable — see
			// FindByNormalizedPhone. GORM's soft-delete clause supplies the
			// deleted_at IS NULL the partial index also requires.
			cond = cond.Or(
				"contacts.phone IS NOT NULL AND contacts.phone <> '' AND regexp_replace(contacts.phone, '[^0-9]', '', 'g') IN ?",
				variants,
			)
		}

		query = query.Where(cond)
	}

	// Filters
	if f.CompanyID != nil {
		query = query.Where("contacts.company_id = ?", *f.CompanyID)
	}
	if f.OwnerUserID != nil {
		query = query.Where("contacts.owner_user_id = ?", *f.OwnerUserID)
	}
	for k, v := range f.CustomFilters {
		if k == "" || v == "" {
			continue
		}
		query = query.Where("contacts.custom_fields ->> ? = ?", k, v)
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
	err := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts", "contact").
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

// FindByNormalizedEmail returns the org's contact whose email matches `email`
// case-insensitively, or (nil, nil) when there is none. It backs lead-ingestion
// dedupe; the app had no exact-email lookup before (callers abused List(Q:…),
// which is a to_tsvector full-text search and can match the wrong row).
//
// DELIBERATELY UNSCOPED — org filter only, no applyScopeFromCtx. Dedupe is not a
// read of the caller's data: an own-scoped caller who cannot see a contact must
// still not be allowed to create a second one with the same email. Scoping this
// would silently turn every invisible contact into a duplicate. Callers must
// therefore treat the result as an existence check, never as a record to echo
// back to a user.
//
// Soft-deleted rows are excluded (gorm's DeletedAt), matching the partial unique
// index that guards the column — so a lead whose contact was soft-deleted creates
// a new row rather than resurrecting the old one.
//
// The ORDER BY is load-bearing, not cosmetic: the unique index is case-SENSITIVE
// (raw email), so "Bob@x.com" and "bob@x.com" can already coexist and this
// case-insensitive lookup can legitimately see several. Oldest-first makes the
// pick deterministic instead of whatever Postgres returns first.
func (r *contactRepository) FindByNormalizedEmail(ctx context.Context, orgID uuid.UUID, email string) (*domain.Contact, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return nil, nil // a blank email matches nothing; the partial index ignores NULLs too
	}
	var contact domain.Contact
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND LOWER(email) = ?", orgID, normalized).
		Order("created_at ASC").
		First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

// FindByNormalizedPhone returns EVERY org contact whose phone reduces to the same
// digits, oldest first. Backs lead-ingestion phone dedupe.
//
// It returns a SLICE, not a single row, and that is the whole point. A duplicate
// email means one person; a duplicate PHONE routinely means several — spouses, a
// company switchboard, a shared mobile, a recycled number. A First()-style lookup
// would deterministically pick the oldest and let every later lead update THAT
// row, silently merging distinct people into one contact (and under an overwrite
// policy, letting one person's data destroy another's). Returning all matches lets
// the caller SEE the ambiguity and refuse to guess.
//
// Matching is on digits only, computed by the same expression as the index
// (idx_contacts_org_phone_digits) so the query is sargable, and mirrored by
// integrations.normalizePhone in Go — the two must agree exactly or matching
// silently stops using the index.
//
// Deliberately conservative: "+1 555 0100" and "555 0100" reduce to DIFFERENT
// digit strings and will not match, because there is no per-org region to resolve
// the country code with. That is a miss, which creates a duplicate — recoverable.
// The alternative (guessing a country code) risks a wrong merge, which is not.
//
// Unscoped for the same reason as FindByNormalizedEmail: dedupe is an existence
// check, not a read of the caller's data.
func (r *contactRepository) FindByNormalizedPhone(ctx context.Context, orgID uuid.UUID, digits string) ([]domain.Contact, error) {
	if digits == "" {
		return nil, nil
	}
	var out []domain.Contact
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND phone IS NOT NULL AND phone <> '' AND regexp_replace(phone, '[^0-9]', '', 'g') = ?", orgID, digits).
		Order("created_at ASC").
		Limit(10). // enough to prove ambiguity; nobody needs the 400th switchboard contact
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
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
	// Save writes by primary key with no scope, so enforce write-level row scope
	// first: read visibility (which loaded c) is not write access (U0.4).
	if err := requireWriteVisible(r.db, ctx, c.OrgID, "contacts", "contact", c.ID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Save(c).Error
}

// ============================================================
// SoftDelete
// ============================================================

func (r *contactRepository) SoftDelete(ctx context.Context, orgID, id uuid.UUID) error {
	result := applyWriteScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts", "contact").
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
	err := applyScopeFromCtx(r.db.WithContext(ctx).Model(&domain.Contact{}), ctx, orgID, "contacts", "contact").
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
	result := applyWriteScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts", "contact").
		Where("contacts.id IN ?", ids).
		Delete(&domain.Contact{})
	return result.RowsAffected, result.Error
}

func (r *contactRepository) BulkAssignTag(ctx context.Context, orgID uuid.UUID, contactIDs []uuid.UUID, tagID uuid.UUID) (int64, error) {
	if len(contactIDs) == 0 {
		return 0, nil
	}

	// Tagging is a WRITE to a record, so the ids have to survive the write predicate
	// first. The raw INSERT below carries no org filter and no row scope of its own:
	// handed a list of ids, it would happily tag contacts in another workspace, or a
	// row-scoped caller's colleagues' contacts.
	var allowed []uuid.UUID
	if err := applyWriteScopeFromCtx(r.db.WithContext(ctx).Model(&domain.Contact{}), ctx, orgID, "contacts", "contact").
		Where("contacts.id IN ?", contactIDs).
		Pluck("contacts.id", &allowed).Error; err != nil {
		return 0, err
	}
	if len(allowed) == 0 {
		return 0, nil
	}

	// Build multi-row INSERT INTO contact_tags ON CONFLICT DO NOTHING
	sql := "INSERT INTO contact_tags (contact_id, tag_id) VALUES "
	args := make([]interface{}, 0, len(allowed)*2)
	for i, cid := range allowed {
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
	err := applyScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts", "contact").
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
