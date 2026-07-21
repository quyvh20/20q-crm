package repository

import (
	"context"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Contact search ORs three arms together (name/email tsvector, company-name
// subquery, phone digits) and then ANDs the result with the org filter that
// applyScopeFromCtx contributes. That precedence is the whole ballgame: if the OR
// chain ever flattens instead of being parenthesized, `org_id = A AND name-match OR
// company-match` is a cross-tenant read, and it would look perfectly fine in every
// single-org test. TestContactSearch_CompanyNameIsOrgScoped is the guard.
func setupContactSearch(t *testing.T) (domain.ContactRepository, *gorm.DB, func()) {
	t.Helper()
	db, done := startPostgres(t)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), full_name varchar, email varchar)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE companies (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		name varchar(255) NOT NULL,
		custom_fields jsonb DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE contacts (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		first_name varchar(100) NOT NULL,
		last_name varchar(100) NOT NULL DEFAULT '',
		email varchar(255),
		phone varchar(50),
		company_id uuid,
		owner_user_id uuid,
		custom_fields jsonb DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE tags (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid NOT NULL,
		name varchar(100) NOT NULL, color varchar(20) DEFAULT '#6B7280',
		created_at timestamptz NOT NULL DEFAULT NOW(), updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE contact_tags (contact_id uuid NOT NULL, tag_id uuid NOT NULL, PRIMARY KEY (contact_id, tag_id))`).Error)

	// The indexes exactly as the boot guards in cmd/server/main.go create them —
	// this is what makes TestContactSearch_UsesCompanyFulltextIndex meaningful.
	require.NoError(t, db.Exec(`CREATE INDEX idx_companies_fulltext ON companies USING GIN (to_tsvector('simple', name))`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX idx_contacts_fulltext ON contacts USING GIN (to_tsvector('simple', first_name || ' ' || last_name || ' ' || COALESCE(email, '')))`).Error)

	return NewContactRepository(db), db, done
}

func seedOrg(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, id).Error)
	return id
}

func seedCompany(t *testing.T, db *gorm.DB, orgID uuid.UUID, name string, deleted bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	del := "NULL"
	if deleted {
		del = "NOW()"
	}
	require.NoError(t, db.Exec(
		`INSERT INTO companies (id, org_id, name, deleted_at) VALUES (?, ?, ?, `+del+`)`,
		id, orgID, name).Error)
	return id
}

func seedContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, first, last, email, phone string, companyID *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var e, p interface{}
	if email != "" {
		e = email
	}
	if phone != "" {
		p = phone
	}
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email, phone, company_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, orgID, first, last, e, p, companyID).Error)
	return id
}

func searchNames(t *testing.T, repo domain.ContactRepository, orgID uuid.UUID, q string) []string {
	t.Helper()
	got, _, err := repo.List(context.Background(), orgID, domain.ContactFilter{Q: q, Limit: 50})
	require.NoError(t, err)
	names := make([]string, 0, len(got))
	for _, c := range got {
		names = append(names, c.FirstName)
	}
	return names
}

// The feature: a query matches the related company's name.
func TestContactSearch_MatchesCompanyName(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	acme := seedCompany(t, db, org, "Acme Corporation", false)
	seedContact(t, db, org, "Bob", "Smith", "bob@example.com", "501-222-7363", &acme)
	seedContact(t, db, org, "Nora", "Webb", "nora@other.com", "", nil)

	// Exact and partial-token, matching how the name/email half already behaves.
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Acme"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Acme Corporation"))

	// The other arms still work — this replaced their WHERE clause.
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Bob"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "bob@example.com"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "(501) 222-7363"))
	assert.Equal(t, []string{"Nora"}, searchNames(t, repo, org, "Nora"))

	assert.Empty(t, searchNames(t, repo, org, "Zenith"))
}

// Search-as-you-type: the last word of the term is a prefix. Before this, a search
// box showed nothing until you finished typing the word.
func TestContactSearch_PrefixMatching(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	acme := seedCompany(t, db, org, "Acme Corporation", false)
	seedContact(t, db, org, "Bob", "Smith", "bob@example.com", "", &acme)

	// Company name, the reported case.
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Acm"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Acme Corp"))
	// Contact name and email get the same rule — one term, one behaviour.
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Bo"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Smi"))

	// Only the LAST lexeme is a prefix; earlier words still have to match whole.
	// "Corp" is not a word in "Acme Corporation" reversed order, so this stays empty.
	assert.Empty(t, searchNames(t, repo, org, "Corp Acme"))
	assert.Empty(t, searchNames(t, repo, org, "Zeni"))
}

// The reason the prefix is built by rewriting plainto_tsquery's OUTPUT instead of
// tokenizing in Go: the 'simple' config keeps a COMPLETE address as one lexeme
// ('bob@example.com'), so a Go-side split on non-alphanumerics would emit
// 'bob' & 'example' & 'com' and match nothing.
func TestContactSearch_EmailTokenization(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	seedContact(t, db, org, "Bob", "Smith", "bob@example.com", "", nil)

	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "bob@example.com"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "bob"))
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "bob@"))

	// A HALF-typed address does not match, and cannot under this design. Postgres
	// tokenizes a complete address as ONE lexeme but an incomplete one as TWO
	// ('bob' & 'exam' — "exam" has no TLD, so it is no longer an email token), so
	// the prefix lands on a token the stored vector never contains. Likewise the
	// domain alone: the lexeme is the whole address, and it does not start with
	// "example". Both were already true before prefixes existed; typing the local
	// part or stopping at the @ is what works. Fixing it means trigrams, not tsquery.
	assert.Empty(t, searchNames(t, repo, org, "bob@exam"))
	assert.Empty(t, searchNames(t, repo, org, "example"))
}

// ContactFilter.Email is the exactness escape hatch from fuzzy Q. The CSV import
// path depends on it: it replaces the matched contact's tags outright, so a
// near-miss would wipe an unrelated contact's tags. Q's prefix rule makes
// "bob@example.com" match "bob@example.com.au"; Email must not.
func TestContactSearch_ExactEmailFilterIsNotFuzzy(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	seedContact(t, db, org, "Bob", "Smith", "bob@example.com", "", nil)
	// Inserted second, so created_at DESC puts it FIRST — it is exactly the row that
	// would crowd out the real match under Q + Limit 1.
	seedContact(t, db, org, "Imposter", "Twin", "bob@example.com.au", "", nil)

	exact, _, err := repo.List(context.Background(), org, domain.ContactFilter{Email: "bob@example.com", Limit: 1})
	require.NoError(t, err)
	require.Len(t, exact, 1)
	assert.Equal(t, "Bob", exact[0].FirstName)

	// Case-insensitive, matching idx_contacts_org_lower_email.
	upper, _, err := repo.List(context.Background(), org, domain.ContactFilter{Email: "BOB@Example.COM", Limit: 1})
	require.NoError(t, err)
	require.Len(t, upper, 1)
	assert.Equal(t, "Bob", upper[0].FirstName)

	// And the demonstration that Q is the wrong tool for this: it returns the
	// imposter first, which is why the import path no longer uses it.
	assert.Equal(t, []string{"Imposter", "Bob"}, searchNames(t, repo, org, "bob@example.com"))
}

// A term that reduces to no lexemes at all must return nothing, not explode:
// '' || ':*' is ':*', which is a tsquery syntax error. Guarded with NULLIF/COALESCE.
func TestContactSearch_LexemelessTermIsNotAnError(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	acme := seedCompany(t, db, org, "Acme Corporation", false)
	seedContact(t, db, org, "Bob", "Smith", "bob@example.com", "", &acme)

	for _, q := range []string{"!!!", "?", "&", "|", "  ", "()"} {
		got, _, err := repo.List(context.Background(), org, domain.ContactFilter{Q: q, Limit: 50})
		require.NoError(t, err, "query %q must not error", q)
		assert.Empty(t, got, "query %q must match nothing", q)
	}
}

// THE regression that matters. Two orgs own a company with the SAME name; a search
// in one must never surface the other's contact. This fails loudly if the OR chain
// stops being parenthesized against the org filter.
func TestContactSearch_CompanyNameIsOrgScoped(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	orgA, orgB := seedOrg(t, db), seedOrg(t, db)
	acmeA := seedCompany(t, db, orgA, "Acme Corporation", false)
	acmeB := seedCompany(t, db, orgB, "Acme Corporation", false)
	seedContact(t, db, orgA, "Bob", "Smith", "bob@a.com", "", &acmeA)
	seedContact(t, db, orgB, "Carol", "Jones", "carol@b.com", "", &acmeB)

	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, orgA, "Acme"))
	assert.Equal(t, []string{"Carol"}, searchNames(t, repo, orgB, "Acme"))
}

// A soft-deleted company is deleted. GORM's soft-delete clause does not reach into
// the raw subquery, so the predicate spells deleted_at IS NULL itself — if that is
// ever dropped, this catches it.
func TestContactSearch_SoftDeletedCompanyDoesNotMatch(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	dead := seedCompany(t, db, org, "Zenith Industries", true)
	seedContact(t, db, org, "Dave", "Brown", "dave@example.com", "", &dead)

	assert.Empty(t, searchNames(t, repo, org, "Zenith"))
	// The contact itself is not deleted and stays findable by its own fields.
	assert.Equal(t, []string{"Dave"}, searchNames(t, repo, org, "Dave"))
}

// A contact matching on name AND company must come back once, not twice. Cheap
// insurance that the subquery never becomes a row-multiplying JOIN.
func TestContactSearch_NoDuplicateRows(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	acme := seedCompany(t, db, org, "Acme Corporation", false)
	seedContact(t, db, org, "Acme", "Reseller", "hi@acme.com", "", &acme)

	assert.Equal(t, []string{"Acme"}, searchNames(t, repo, org, "Acme"))
}

// The query expression and the index expression must stay character-identical, or
// the index is built and maintained on every write and never used. Postgres will
// happily seq-scan a tiny test table, so seqscan is disabled to force the choice.
func TestContactSearch_UsesCompanyFulltextIndex(t *testing.T) {
	repo, db, done := setupContactSearch(t)
	defer done()

	org := seedOrg(t, db)
	acme := seedCompany(t, db, org, "Acme Corporation", false)
	seedContact(t, db, org, "Bob", "Smith", "bob@example.com", "", &acme)
	require.NoError(t, db.Exec(`ANALYZE companies`).Error)

	require.NoError(t, db.Exec(`SET enable_seqscan = off`).Error)
	defer db.Exec(`SET enable_seqscan = on`)

	// Every line, not just the first: the GIN access shows up as a "Bitmap Index
	// Scan on idx_companies_fulltext" CHILD of the top-level Bitmap Heap Scan.
	var planLines []string
	require.NoError(t, db.Raw(`EXPLAIN (FORMAT TEXT) SELECT id FROM companies c
		WHERE c.org_id = ? AND c.deleted_at IS NULL
		  AND to_tsvector('simple', c.name) @@ plainto_tsquery('simple', ?)`,
		org, "Acme").Scan(&planLines).Error)
	plan := strings.Join(planLines, "\n")
	assert.Contains(t, plan, "idx_companies_fulltext",
		"company-name search stopped using its GIN index — the expression in contactRepository.List and the one in the main.go boot guard have drifted apart")

	// And the search still returns the right row with that plan in force.
	assert.Equal(t, []string{"Bob"}, searchNames(t, repo, org, "Acme"))
}
