package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type contactRepository struct {
	db *gorm.DB
}

func NewContactRepository(db *gorm.DB) domain.ContactRepository {
	return &contactRepository{db: db}
}

// contactSortColumns is the whitelist behind ContactFilter.SortBy.
//
// email is the one nullable sort column in the app, and it is why keyset_cursor.go
// carries null arms at all: a row-value comparison against NULL yields NULL, not
// true, so a naive (email, id) < (?, ?) drops every contact without an address
// from every page after the first — they would simply disappear from a sorted
// list. The arms follow Postgres's DEFAULT null placement, which orderClause
// leaves untouched, so adopting this changed nobody's ordering.
var contactSortColumns = map[string]keysetColumn{
	"created_at": {col: "contacts.created_at", kind: keysetTime},
	"name":       {col: "contacts.first_name", kind: keysetText},
	"email":      {col: "contacts.email", kind: keysetText, nullable: true},
}

// contactSortValue reads the sort column off the last row of a page. The default
// arm must match the defaultKey passed to newKeysetOrdering in List.
func contactSortValue(c *domain.Contact, key string) any {
	switch key {
	case "name":
		return c.FirstName
	case "email":
		if c.Email == nil {
			return nil // NULL, not a typed nil — the predicate switches on exactly this
		}
		return *c.Email
	default:
		return c.CreatedAt
	}
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
	// with a digits match when the term looks like a phone number; the last word of
	// the term is a prefix. The tsvector deliberately does not index phone
	// (formatting makes it useless as a text token), so before that branch existed
	// searching "501-222-7363" tokenized into terms the vector could never contain
	// and returned zero rows for a contact that was sitting right there.
	//
	// Q is FUZZY on every axis. A caller that wants one specific contact wants
	// f.Email (exact, below), not this.
	if f.Q != "" {
		// Search-as-you-type. plainto_tsquery matches whole tokens only, so "Acm"
		// found neither Acme the person nor Acme Corporation the company — the box
		// looked broken for exactly the half-typed word people actually type.
		// Appending :* to the tsquery's LAST lexeme makes that trailing word a prefix
		// while earlier words stay exact, the conventional search-as-you-type rule:
		// "Acme Corp" finds Acme Corporation, "Corp Acme" still finds nothing.
		//
		// Built by rewriting plainto_tsquery's OUTPUT rather than tokenizing in Go,
		// and that is load-bearing: the 'simple' config keeps an address like
		// bob@example.com as ONE lexeme, so a Go-side split on non-alphanumerics would
		// emit 'bob' & 'example' & 'com' and silently break email search.
		//
		// NULLIF/COALESCE guards a term that yields no lexemes at all ("!!!"): there
		// '' || ':*' is ':*', a tsquery syntax ERROR, whereas ''::tsquery is simply a
		// query that matches nothing.
		//
		// Index-safe: :* changes the QUERY side only. Both GIN indexes are on the
		// to_tsvector side and stay exactly as the main.go boot guards build them.
		const prefixQuery = `COALESCE(NULLIF(plainto_tsquery('simple', ?)::text, '') || ':*', '')::tsquery`

		const textMatch = "to_tsvector('simple', contacts.first_name || ' ' || contacts.last_name || ' ' || COALESCE(contacts.email, '')) @@ " + prefixQuery

		// "Everyone at Acme" is a contact search users expect to work, but the company
		// name lives on another table and so cannot join the contacts tsvector.
		//
		// A subquery rather than a JOIN: the relation is many-to-one so a join would
		// need no DISTINCT today, but it would still fight Preload("Company") and widen
		// the select list, and this file already spells "contact matches a related row"
		// this way — see the TagIDs filter below.
		//
		// Same tokenizer AND the same prefix rule as the name/email half, so one term
		// behaves consistently across both. org_id is not redundant despite company_id
		// already being org-local — it lets the planner cut the company scan to one org
		// before the GIN probe. deleted_at IS NULL has to be written out: GORM's
		// soft-delete clause does not reach inside a raw subquery, so without it a
		// deleted company would go on matching its contacts.
		const companyMatch = `contacts.company_id IN (
			SELECT c.id FROM companies c
			WHERE c.org_id = ? AND c.deleted_at IS NULL
			  AND to_tsvector('simple', c.name) @@ ` + prefixQuery + `
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

	// Exact address, for callers that mean equality rather than search. LOWER(email)
	// is character-identical to idx_contacts_org_lower_email (a main.go boot guard),
	// so this is an index probe, not a scan.
	if f.Email != "" {
		query = query.Where("LOWER(contacts.email) = LOWER(?)", f.Email)
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

	// ── Sort + keyset cursor ─────────────────────────────────────────────────
	// The predicate and the ORDER BY come from ONE resolved ordering. Before that,
	// the cursor compared (created_at, id) unconditionally while the ORDER BY
	// already honoured f.SortBy — correct only for as long as created_at stayed the
	// single reachable ordering, and silently wrong the moment it did not.
	ord := newKeysetOrdering(contactSortColumns, "created_at", "contacts.id", f.SortBy, f.SortOrder)
	query = ord.applyCursor(query, f.Cursor)

	var contacts []domain.Contact
	err := query.
		Order(ord.orderClause()).
		Limit(limit + 1). // fetch one extra to determine if there's a next page
		Find(&contacts).Error
	if err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(contacts) > limit {
		contacts = contacts[:limit]
		last := contacts[len(contacts)-1]
		nextCursor = ord.nextCursor(contactSortValue(&last, ord.key), last.ID)
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

// ListPhoneDuplicateGroups groups active contacts by normalized phone digits.
// It re-derives the digit set with the same regexp_replace expression as the
// (idx_contacts_org_phone_digits) index, then hands each group to
// FindByNormalizedPhone rather than re-implementing its row shape/limit here.
func (r *contactRepository) ListPhoneDuplicateGroups(ctx context.Context, orgID uuid.UUID) ([]domain.DuplicateGroup, error) {
	var digitsList []string
	err := r.db.WithContext(ctx).Model(&domain.Contact{}).
		Select("regexp_replace(phone, '[^0-9]', '', 'g') AS digits").
		Where("org_id = ? AND deleted_at IS NULL AND phone IS NOT NULL AND phone <> ''", orgID).
		Group("digits").
		Having("COUNT(*) > 1 AND length(regexp_replace(phone, '[^0-9]', '', 'g')) >= 7").
		Order("digits").
		Pluck("digits", &digitsList).Error
	if err != nil {
		return nil, err
	}
	groups := make([]domain.DuplicateGroup, 0, len(digitsList))
	for _, d := range digitsList {
		contacts, err := r.FindByNormalizedPhone(ctx, orgID, d)
		if err != nil {
			return nil, err
		}
		if len(contacts) > 1 {
			groups = append(groups, domain.DuplicateGroup{Key: d, Contacts: contacts})
		}
	}
	return groups, nil
}

// MergeRepoint is documented on the interface (domain.ContactRepository). The
// object_links INSERTs target the table's own partial unique index
// (uix_object_links_unique) with ON CONFLICT DO NOTHING, so a link that
// already exists on the survivor is silently skipped rather than duplicated
// — no pre-read/dedup pass needed. The loser's ORIGINAL edges are left alone
// here on purpose: RecordService.Delete's cascade removes them when the
// caller deletes the loser afterward, so this never soft-deletes a link out
// from under a merge that failed partway.
func (r *contactRepository) MergeRepoint(ctx context.Context, orgID, survivorID, loserID uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO object_links (id, org_id, from_slug, from_id, to_slug, to_id, relation_key, created_by, created_at)
			SELECT uuid_generate_v4(), org_id, from_slug, ?, to_slug, to_id, relation_key, created_by, NOW()
			FROM object_links
			WHERE org_id = ? AND from_slug = 'contact' AND from_id = ? AND deleted_at IS NULL
			ON CONFLICT (org_id, from_slug, from_id, relation_key, to_slug, to_id) WHERE deleted_at IS NULL DO NOTHING
		`, survivorID, orgID, loserID).Error; err != nil {
			return fmt.Errorf("repoint outgoing links: %w", err)
		}
		if err := tx.Exec(`
			INSERT INTO object_links (id, org_id, from_slug, from_id, to_slug, to_id, relation_key, created_by, created_at)
			SELECT uuid_generate_v4(), org_id, from_slug, from_id, to_slug, ?, relation_key, created_by, NOW()
			FROM object_links
			WHERE org_id = ? AND to_slug = 'contact' AND to_id = ? AND deleted_at IS NULL
			ON CONFLICT (org_id, from_slug, from_id, relation_key, to_slug, to_id) WHERE deleted_at IS NULL DO NOTHING
		`, survivorID, orgID, loserID).Error; err != nil {
			return fmt.Errorf("repoint incoming links: %w", err)
		}
		if err := tx.Exec(`UPDATE tasks SET contact_id = ? WHERE contact_id = ? AND org_id = ?`,
			survivorID, loserID, orgID).Error; err != nil {
			return fmt.Errorf("repoint tasks: %w", err)
		}
		if err := tx.Exec(`UPDATE activities SET contact_id = ? WHERE contact_id = ? AND org_id = ?`,
			survivorID, loserID, orgID).Error; err != nil {
			return fmt.Errorf("repoint activities: %w", err)
		}
		// contact_tags has no org_id column (see object_link_repository.go's
		// legacy-bridge comment) — the org check already happened one level up,
		// via the two contactRepo.GetByID calls the usecase makes before this.
		if err := tx.Exec(`INSERT INTO contact_tags (contact_id, tag_id) SELECT ?, tag_id FROM contact_tags WHERE contact_id = ? ON CONFLICT DO NOTHING`,
			survivorID, loserID).Error; err != nil {
			return fmt.Errorf("repoint contact_tags: %w", err)
		}
		if err := tx.Exec(`DELETE FROM contact_tags WHERE contact_id = ?`, loserID).Error; err != nil {
			return fmt.Errorf("clear loser contact_tags: %w", err)
		}
		return nil
	})
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

func (r *contactRepository) BulkDeleteByIDs(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]domain.Contact, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// RETURNING the deleted rows (id + email) rather than a count. A count answers
	// "how many", and the caller needs "which": the write scope above can skip rows
	// an own-scoped caller does not own, so requested-minus-deleted is a non-empty
	// set the caller must not mistake for deleted. Ledger erasure keys off the ids;
	// the marketing-state collapse keys off the emails — both must follow the scope,
	// never the request. Email is nullable (*string), so a returned row may carry none.
	var deleted []domain.Contact
	result := applyWriteScopeFromCtx(r.db.WithContext(ctx), ctx, orgID, "contacts", "contact").
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}, {Name: "email"}}}).
		Where("contacts.id IN ?", ids).
		Delete(&deleted)
	if result.Error != nil {
		return nil, result.Error
	}
	return deleted, nil
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
