package repository

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ============================================================
// R4.2 — keyset pagination against real Postgres
// ============================================================
//
// This is the test that would have caught the shipped bug, and it is written the
// way it is because a WEAKER version of it passes with the bug present:
//
//   * ids are EXPLICIT, never uuid.New(). The defect is a comparator mismatch
//     between `id <` and `ORDER BY created_at`, so with random ids the outcome
//     depends on which uuid the generator happened to produce — the test would
//     pass most runs and be dismissed as flaky the rest.
//   * rows TIE on the sort column, straddling a page boundary. Without a tie, a
//     "fix" that drops the id tiebreaker from the ORDER BY still passes.
//   * one contact has a NULL email, and email is a sortable column. A row-value
//     comparison against NULL evaluates to NULL, so a naive tuple predicate loses
//     those rows silently and forever.
//   * assertions are over the WHOLE walk, not per page. Per-page assertions miss
//     the nastiest observed symptom — a 0-row page 2, which blanks nextCursor and
//     makes the frontend's "Load more" button disappear with half the list
//     unreachable. A short walk looks exactly like a legitimate end.
//   * limit is strictly smaller than the row count, so the limit+1 probe that
//     decides whether to mint a cursor is actually exercised.
//   * one tie pair (G, H) is inserted in the order OPPOSITE to its id ordering, so
//     the headline test fails on its own if the id tiebreaker is dropped from the
//     ORDER BY. Every other tie here happens to be inserted in id-DESC order, and
//     Postgres returns small tied sets in insertion order, so those pairs let the
//     tiebreaker be deleted without the assertion noticing.
//   * THREE deals have a NULL value and a NULL probability, because both columns
//     are nullable in production DDL (see setupPagination's note on the deals
//     table). Three, not one, so that at limit 2 the null block STRADDLES a page
//     boundary: a cursor minted from inside the block is the case that turns a
//     mishandled NULL from lost rows into an endless "Load more".
//
// Fixture ids are deliberately arranged so the pre-fix code demonstrably repeats
// and skips rows: paging deals returns A, F, A, B, A and never reaches E, C or D.

const pagingLimit = 2 // strictly less than the rows seeded, so limit+1 probing runs

// Deal ids. Ordered by created_at the canonical DESC sequence is A, F, B, E, C, D,
// H, G — note that it is NOT id order in either direction, which is the whole point.
var (
	pDealA = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	pDealB = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	pDealC = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	pDealD = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	pDealE = uuid.MustParse("55555555-5555-5555-5555-555555555555")
	pDealF = uuid.MustParse("66666666-6666-6666-6666-666666666666")
	pDealG = uuid.MustParse("77777777-7777-7777-7777-777777777777")
	pDealH = uuid.MustParse("88888888-8888-8888-8888-888888888888")
	pDealI = uuid.MustParse("99999999-9999-9999-9999-999999999999")
)

var (
	pCompA = uuid.MustParse("a1111111-1111-1111-1111-111111111111")
	pCompB = uuid.MustParse("a2222222-2222-2222-2222-222222222222")
	pCompC = uuid.MustParse("a3333333-3333-3333-3333-333333333333")
	pCompD = uuid.MustParse("a4444444-4444-4444-4444-444444444444")
	pCompE = uuid.MustParse("a5555555-5555-5555-5555-555555555555")
	pCompF = uuid.MustParse("a6666666-6666-6666-6666-666666666666")
)

var (
	pContA = uuid.MustParse("c1111111-1111-1111-1111-111111111111")
	pContB = uuid.MustParse("c2222222-2222-2222-2222-222222222222")
	pContC = uuid.MustParse("c3333333-3333-3333-3333-333333333333")
	pContD = uuid.MustParse("c4444444-4444-4444-4444-444444444444")
	pContE = uuid.MustParse("c5555555-5555-5555-5555-555555555555")
	pContF = uuid.MustParse("c6666666-6666-6666-6666-666666666666")
)

// Custom-object record ids, ASCENDING, and seedPagingRecords inserts them in that
// order — so heap/insertion order is the OPPOSITE of the id DESC tiebreaker.
var pRecs = []uuid.UUID{
	uuid.MustParse("d1111111-1111-1111-1111-111111111111"),
	uuid.MustParse("d2222222-2222-2222-2222-222222222222"),
	uuid.MustParse("d3333333-3333-3333-3333-333333333333"),
	uuid.MustParse("d4444444-4444-4444-4444-444444444444"),
	uuid.MustParse("d5555555-5555-5555-5555-555555555555"),
	uuid.MustParse("d6666666-6666-6666-6666-666666666666"),
	uuid.MustParse("d7777777-7777-7777-7777-777777777777"),
	uuid.MustParse("d8888888-8888-8888-8888-888888888888"),
	uuid.MustParse("d9999999-9999-9999-9999-999999999999"),
}

var pagingBase = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func pagingTime(minutes int) time.Time { return pagingBase.Add(time.Duration(minutes) * time.Minute) }

// setupPagination builds the minimum schema the three List methods touch,
// including the tables their Preloads reach for.
func setupPagination(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, done := startPostgres(t)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), full_name varchar, email varchar)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE companies (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		name varchar(255) NOT NULL,
		industry varchar(100),
		website varchar(500),
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
	require.NoError(t, db.Exec(`CREATE TABLE pipeline_stages (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid NOT NULL,
		name varchar(100) NOT NULL, position int NOT NULL DEFAULT 0,
		color varchar(20) DEFAULT '#3B82F6',
		is_won boolean NOT NULL DEFAULT false, is_lost boolean NOT NULL DEFAULT false,
		created_at timestamptz NOT NULL DEFAULT NOW(), updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	// value and probability are DEFAULT-but-NULLABLE, copied verbatim from
	// migrations/000002_schema.up.sql. They were written `NOT NULL DEFAULT 0` here
	// first, and that single word is what hid a real data-loss bug: the whitelist in
	// deal_repository.go claimed the same thing, the fixture agreed with the claim
	// instead of with production, and sorting by a nullable numeric dead-stopped
	// pagination on any org that had a NULL-valued deal. A fixture stricter than
	// production is not a safe simplification — it is the bug's alibi.
	// TestKeysetWhitelists_AgreeWithSchemaNullability now pins fixture, whitelist and
	// migration to each other.
	require.NoError(t, db.Exec(`CREATE TABLE deals (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		title varchar(255) NOT NULL,
		contact_id uuid, company_id uuid, stage_id uuid,
		pipeline_id uuid,
		value numeric(15,2) DEFAULT 0,
		probability int DEFAULT 0 CHECK (probability >= 0 AND probability <= 100),
		owner_user_id uuid,
		expected_close_at timestamptz,
		is_won boolean NOT NULL DEFAULT false,
		is_lost boolean NOT NULL DEFAULT false,
		closed_at timestamptz,
		custom_fields jsonb DEFAULT '{}',
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)
	// Nullability copied from migrations/000005_custom_objects.up.sql (+ the
	// owner_user_id added by 000039). FKs are omitted like everywhere else in this
	// fixture; column NULLability is not, for the reason above.
	require.NoError(t, db.Exec(`CREATE TABLE custom_object_records (
		id uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id uuid NOT NULL,
		object_def_id uuid NOT NULL,
		display_name varchar(500) NOT NULL DEFAULT '',
		data jsonb DEFAULT '{}',
		owner_user_id uuid,
		created_by uuid,
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`).Error)

	return db, done
}

// seedPagingDeals inserts nine deals whose sort columns TIE in pairs across page
// boundaries (limit 2 ⇒ boundaries after rows 2, 4, 6 and 8):
//
//	created_at, DESC:  A | F  B | E  C | D  H | G  I
//	                       ^tie^  ^tie^      ^tie^
//
// Under ASC the pairs shift by one, so each straddles a boundary in one direction
// or the other. title is deliberately given a DIFFERENT order (strictly descending
// A→H) so a sort column that is silently ignored cannot hide behind the shared
// layout.
//
// Two properties beyond "there are ties":
//
//   - G and H are inserted G-then-H while id DESC orders them H-then-G. Every other
//     pair is inserted in id-DESC order, and Postgres hands back a small tied set in
//     insertion order, so those pairs survive the id tiebreaker being deleted from
//     the ORDER BY. This one does not — which is what makes the headline test kill
//     that mutation by itself rather than leaning on the rest of the file.
//   - G, H and I carry NULL value and NULL probability, which production DDL allows
//     and the fixture used to forbid. domain.Deal holds both as non-pointer numbers,
//     so they scan back as 0 and the ORDER BY has to sort them as 0 too, or the
//     cursor is comparing against a value the row never had. THREE of them, so that
//     at limit 2 a cursor is minted from inside the null block in both directions —
//     the branch that separates "rows go missing" from "the walk never terminates".
func seedPagingDeals(t *testing.T, db *gorm.DB, orgID uuid.UUID) {
	t.Helper()
	rows := []struct {
		id          uuid.UUID
		title       string
		value       any // nil ⇒ SQL NULL
		probability any // nil ⇒ SQL NULL
		minutes     int
	}{
		{pDealA, "t1", 600, 60, 60},
		{pDealF, "t2", 400, 40, 40}, // ties with B on created_at/value/probability
		{pDealB, "t3", 400, 40, 40},
		{pDealE, "t4", 200, 20, 20}, // ties with C
		{pDealC, "t5", 200, 20, 20},
		{pDealD, "t6", 100, 10, 10},
		{pDealG, "t7", nil, nil, 5}, // ties with H on all three, inserted id-ASCENDING
		{pDealH, "t8", nil, nil, 5},
		{pDealI, "t9", nil, nil, 4}, // third NULL row: the null block straddles a page
	}
	for _, r := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO deals (id, org_id, title, value, probability, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.id, orgID, r.title, r.value, r.probability, pagingTime(r.minutes), pagingTime(r.minutes),
		).Error)
	}
}

// seedPagingRecords inserts nine custom-object records of which SIX share one
// created_at and THREE share another — the shape a bulk import or the
// industry-template seed produces, because created_at defaults to NOW() and NOW()
// is transaction-start time in Postgres, identical for every row written inside one
// transaction. Ids ascend with insertion order, so the id DESC tiebreaker orders
// each tied block opposite to the order the rows were written in.
func seedPagingRecords(t *testing.T, db *gorm.DB, orgID, defID uuid.UUID) {
	t.Helper()
	for i, id := range pRecs {
		minutes := 60
		if i >= 6 {
			minutes = 30
		}
		require.NoError(t, db.Exec(
			`INSERT INTO custom_object_records (id, org_id, object_def_id, display_name, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, orgID, defID, "rec", pagingTime(minutes), pagingTime(minutes),
		).Error)
	}
}

func seedPagingCompanies(t *testing.T, db *gorm.DB, orgID uuid.UUID) {
	t.Helper()
	rows := []struct {
		id      uuid.UUID
		name    string
		minutes int
	}{
		{pCompA, "Alpha", 60},
		{pCompF, "Foxtrot", 40}, // ties with Bravo
		{pCompB, "Bravo", 40},
		{pCompE, "Echo", 20}, // ties with Charlie
		{pCompC, "Charlie", 20},
		{pCompD, "Delta", 10},
	}
	for _, r := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO companies (id, org_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			r.id, orgID, r.name, pagingTime(r.minutes), pagingTime(r.minutes),
		).Error)
	}
}

// seedPagingContacts gives THREE contacts a NULL email. Three, not one, so that a
// cursor minted mid-null-block exists in both directions: under DESC Postgres puts
// NULLs first, so page 1 ends inside the null block; under ASC it puts them last,
// so page 2 does. Both are predicate branches that a single NULL row never reaches.
func seedPagingContacts(t *testing.T, db *gorm.DB, orgID uuid.UUID) {
	t.Helper()
	rows := []struct {
		id      uuid.UUID
		first   string
		email   any // nil ⇒ SQL NULL
		minutes int
	}{
		{pContA, "Zoe", nil, 60},
		{pContF, "Mia", "f@example.com", 40}, // ties with B on created_at and first_name
		{pContB, "Mia", nil, 40},
		{pContE, "Eve", "e@example.com", 20}, // ties with C
		{pContC, "Eve", nil, 20},
		{pContD, "Ann", "d@example.com", 10},
	}
	for _, r := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO contacts (id, org_id, first_name, last_name, email, created_at, updated_at)
			 VALUES (?, ?, ?, 'Lastname', ?, ?, ?)`,
			r.id, orgID, r.first, r.email, pagingTime(r.minutes), pagingTime(r.minutes),
		).Error)
	}
}

func seedPagingOrg(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, id).Error)
	return id
}

// walkPages drives a paginated list to exhaustion and returns every id it saw, in
// order. It fails rather than hangs if the cursor stops advancing — an
// always-page-1 cursor is a real failure mode (it makes "Load more" append the
// same rows forever), and a test that hangs on it teaches nobody anything.
func walkPages(t *testing.T, page func(cursor string) ([]uuid.UUID, string)) []uuid.UUID {
	t.Helper()
	out := []uuid.UUID{}
	cursor := ""
	for i := 0; ; i++ {
		require.Less(t, i, 25, "pagination did not terminate after 25 pages; cursor is not advancing")
		ids, next := page(cursor)
		out = append(out, ids...)
		if next == "" {
			return out
		}
		require.NotEqual(t, cursor, next, "page %d handed back the cursor it was given", i+1)
		cursor = next
	}
}

// assertFullWalk is the assertion that actually catches the bug: every seeded row
// exactly once, in the same order a single unpaged query returns them.
func assertFullWalk(t *testing.T, got, want []uuid.UUID) {
	t.Helper()

	seen := map[uuid.UUID]int{}
	for _, id := range got {
		seen[id]++
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("row %s appeared %d times across the walk (must be exactly once)", id, n)
		}
	}
	assert.ElementsMatch(t, want, got, "the walk must visit exactly the rows one unpaged query returns")
	assert.Equal(t, want, got, "the walk must visit them in the same order, too")
}

func dealIDs(ds []domain.Deal) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}

func companyIDs(cs []domain.Company) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func contactIDs(cs []domain.Contact) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func recordIDs(rs []domain.CustomObjectRecord) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// ============================================================
// Deals
// ============================================================

// The headline case: the default ordering on the path the /deals page actually
// uses. Hand-written expectation, so it does not merely agree with whatever the
// implementation does today.
func TestPagination_Deals_DefaultOrderIsExactAndComplete(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingDeals(t, db, org)
	repo := NewDealRepository(db)

	got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
		ds, next, err := repo.List(context.Background(), org, domain.DealFilter{Limit: pagingLimit, Cursor: cursor})
		require.NoError(t, err)
		require.LessOrEqual(t, len(ds), pagingLimit, "a page must never exceed the requested limit")
		return dealIDs(ds), next
	})

	// created_at DESC, then id DESC inside each tie. Pre-fix this walk returned
	// A, F, A, B, A — three copies of A and no E, C or D at all.
	//
	// H before G is the assertion that fails if the id tiebreaker is dropped from
	// orderClause: they tie on created_at and were inserted G-then-H, so without the
	// tiebreaker Postgres returns them that way round.
	assertFullWalk(t, got, []uuid.UUID{pDealA, pDealF, pDealB, pDealE, pDealC, pDealD, pDealH, pDealG, pDealI})
}

// title is ordered differently from every other deal column, so this fails if the
// ORDER BY quietly ignores sort_by (or if the cursor is bound to the wrong column).
func TestPagination_Deals_SortByTitleIsHonoured(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingDeals(t, db, org)
	repo := NewDealRepository(db)

	got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
		ds, next, err := repo.List(context.Background(), org, domain.DealFilter{
			Limit: pagingLimit, Cursor: cursor, SortBy: "title", SortOrder: "desc",
		})
		require.NoError(t, err)
		return dealIDs(ds), next
	})

	// titles are t1..t9 on A, F, B, E, C, D, G, H, I respectively ⇒ DESC is
	// I, H, G, D, C, E, B, F, A.
	assertFullWalk(t, got, []uuid.UUID{pDealI, pDealH, pDealG, pDealD, pDealC, pDealE, pDealB, pDealF, pDealA})
}

// value and probability are NULLABLE in production (`NUMERIC(15,2) DEFAULT 0`,
// `INT DEFAULT 0 CHECK (…)` — DEFAULT, not NOT NULL, and no boot guard adds one),
// and domain.Deal holds them as float64/int, which cannot carry a NULL: it scans
// back as 0. So a cursor minted on a NULL-valued row says 0 whatever the column
// held, and the ORDER BY must sort that row as 0 too — anything else compares the
// next page against a value the row never had.
//
// Measured on this exact fixture (9 deals, 3 of them NULL, limit 2), against real
// Postgres, for all three candidate whitelists:
//
//	nullable:false (as shipped)  DESC 2 of 9 rows — page 2 is EMPTY, the walk stops
//	                             ASC  6 of 9 rows — the NULL deals never appear
//	nullable:true                DESC 2 of 9 — UNCHANGED. The null arms switch on a
//	                             nil cursor value, and a float64 field cannot
//	                             produce one, so they are dead code here.
//	                             ASC  NEVER TERMINATES — the "…OR value IS NULL" arm
//	                             re-admits the entire non-null head every time the
//	                             cursor is inside the null block, so "Load more"
//	                             re-appends D C E B F A G H forever and row I is
//	                             still never reached. Strictly worse than the bug.
//	nullAs:"0" (COALESCE)        9 of 9 both ways, each row exactly once
//
// Which is why the whitelist carries nullAs and not nullable. The hand-written
// expectations below also pin the resulting PLACEMENT: a NULL value sorts as 0, so
// a deal the UI already renders as $0 no longer leads a highest-value-first list.
func TestPagination_Deals_NullNumericColumnsWalkExactlyOnce(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingDeals(t, db, org)
	repo := NewDealRepository(db)

	var nulls int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM deals WHERE value IS NULL AND probability IS NULL`).Scan(&nulls).Error)
	require.EqualValues(t, 3, nulls, "the fixture must contain enough NULL deals to straddle a page")

	walk := func(sortBy, order string) []uuid.UUID {
		return walkPages(t, func(cursor string) ([]uuid.UUID, string) {
			ds, next, err := repo.List(context.Background(), org, domain.DealFilter{
				Limit: pagingLimit, Cursor: cursor, SortBy: sortBy, SortOrder: order,
			})
			require.NoError(t, err)
			return dealIDs(ds), next
		})
	}

	for _, sortBy := range []string{"value", "probability"} {
		t.Run(sortBy+"/desc", func(t *testing.T) {
			// 600 | 400,400 | 200,200 | 100 | 0,0,0 — the NULL block last, id DESC in it.
			assertFullWalk(t, walk(sortBy, "desc"),
				[]uuid.UUID{pDealA, pDealF, pDealB, pDealE, pDealC, pDealD, pDealI, pDealH, pDealG})
		})
		t.Run(sortBy+"/asc", func(t *testing.T) {
			// The exact reverse, ties included: id ASC inside each of them.
			assertFullWalk(t, walk(sortBy, "asc"),
				[]uuid.UUID{pDealG, pDealH, pDealI, pDealD, pDealC, pDealE, pDealB, pDealF, pDealA})
		})
	}
}

// The matrix. sort_by has never been set together with a cursor by any live
// caller — DealFilter simply never receives both — so this is guarding a
// combination that is unreachable today and would be a silent regression the day
// somebody wires sort into the object registry.
func TestPagination_Deals_WalkMatchesUnpagedForEverySort(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingDeals(t, db, org)
	repo := NewDealRepository(db)

	for _, sortBy := range []string{"", "created_at", "value", "probability", "title"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sortBy+"/"+order, func(t *testing.T) {
				// One big page is the ground truth for the ORDERING; the walk must agree
				// with it, which is what makes the pagination correct rather than merely
				// self-consistent.
				full, next, err := repo.List(context.Background(), org, domain.DealFilter{
					Limit: 50, SortBy: sortBy, SortOrder: order,
				})
				require.NoError(t, err)
				require.Empty(t, next, "50 > 9 rows, so there is no second page")
				require.Len(t, full, 9)

				got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
					ds, nextC, err := repo.List(context.Background(), org, domain.DealFilter{
						Limit: pagingLimit, Cursor: cursor, SortBy: sortBy, SortOrder: order,
					})
					require.NoError(t, err)
					return dealIDs(ds), nextC
				})
				assertFullWalk(t, got, dealIDs(full))
			})
		}
	}
}

// A cursor in the PREVIOUS wire format (bare base64 UUID) — or any other rubbish —
// must serve page 1, not 500. The old decode path only caught base64 errors, so a
// body that survived base64 went straight into `id < ?`; a non-UUID body reached
// Postgres as SQLSTATE 22P02 and a "Load more" click became a server error. Both
// formats are in flight across a deploy, in both directions.
func TestPagination_Deals_UndecodableCursorServesPageOne(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingDeals(t, db, org)
	repo := NewDealRepository(db)

	page1, _, err := repo.List(context.Background(), org, domain.DealFilter{Limit: pagingLimit})
	require.NoError(t, err)

	for name, cursor := range map[string]string{
		"legacy bare uuid":     base64.StdEncoding.EncodeToString([]byte(pDealF.String())),
		"legacy non-uuid body": base64.StdEncoding.EncodeToString([]byte("not-a-uuid-at-all")),
		"not base64":           "%%%not base64%%%",
		"empty-ish":            "k1:",
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := repo.List(context.Background(), org, domain.DealFilter{Limit: pagingLimit, Cursor: cursor})
			require.NoError(t, err, "a malformed cursor must not reach SQL")
			assert.Equal(t, dealIDs(page1), dealIDs(got), "a malformed cursor must serve page 1")
		})
	}
}

// ============================================================
// Companies
// ============================================================

// /objects/company renders the same ObjectListView as /deals and hit the same
// defect. CompanyFilter exposes no sort, so newest-first is the only ordering.
func TestPagination_Companies_WalkIsExactAndComplete(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingCompanies(t, db, org)
	repo := NewCompanyRepository(db)

	got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
		cs, next, err := repo.List(context.Background(), org, domain.CompanyFilter{Limit: pagingLimit, Cursor: cursor})
		require.NoError(t, err)
		require.LessOrEqual(t, len(cs), pagingLimit)
		return companyIDs(cs), next
	})

	assertFullWalk(t, got, []uuid.UUID{pCompA, pCompF, pCompB, pCompE, pCompC, pCompD})
}

// ============================================================
// Contacts
// ============================================================

func TestPagination_Contacts_DefaultOrderIsExactAndComplete(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingContacts(t, db, org)
	repo := NewContactRepository(db)

	got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
		cs, next, err := repo.List(context.Background(), org, domain.ContactFilter{Limit: pagingLimit, Cursor: cursor})
		require.NoError(t, err)
		return contactIDs(cs), next
	})

	assertFullWalk(t, got, []uuid.UUID{pContA, pContF, pContB, pContE, pContC, pContD})
}

// email is the app's one NULLABLE sort column. Postgres places NULLs FIRST under
// DESC and LAST under ASC by default, and orderClause emits no explicit NULLS
// clause, so the predicate has to assume exactly that. A tuple comparison against
// NULL yields NULL rather than true: get this wrong and the three address-less
// contacts vanish from every page after the first, which is a data-loss-shaped bug
// that no status code and no per-page assertion would ever surface.
func TestPagination_Contacts_NullableSortColumnKeepsNullRows(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingContacts(t, db, org)
	repo := NewContactRepository(db)

	walk := func(order string) []uuid.UUID {
		return walkPages(t, func(cursor string) ([]uuid.UUID, string) {
			cs, next, err := repo.List(context.Background(), org, domain.ContactFilter{
				Limit: pagingLimit, Cursor: cursor, SortBy: "email", SortOrder: order,
			})
			require.NoError(t, err)
			return contactIDs(cs), next
		})
	}

	// DESC ⇒ NULLS FIRST, id DESC inside the null block: C, B, A, then f@, e@, d@.
	assertFullWalk(t, walk("desc"), []uuid.UUID{pContC, pContB, pContA, pContF, pContE, pContD})
	// ASC ⇒ NULLS LAST, id ASC inside the null block: d@, e@, f@, then A, B, C.
	assertFullWalk(t, walk("asc"), []uuid.UUID{pContD, pContE, pContF, pContA, pContB, pContC})
}

func TestPagination_Contacts_WalkMatchesUnpagedForEverySort(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingContacts(t, db, org)
	repo := NewContactRepository(db)

	for _, sortBy := range []string{"", "created_at", "name", "email"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sortBy+"/"+order, func(t *testing.T) {
				full, next, err := repo.List(context.Background(), org, domain.ContactFilter{
					Limit: 50, SortBy: sortBy, SortOrder: order,
				})
				require.NoError(t, err)
				require.Empty(t, next)
				require.Len(t, full, 6)

				got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
					cs, nextC, err := repo.List(context.Background(), org, domain.ContactFilter{
						Limit: pagingLimit, Cursor: cursor, SortBy: sortBy, SortOrder: order,
					})
					require.NoError(t, err)
					return contactIDs(cs), nextC
				})
				assertFullWalk(t, got, contactIDs(full))
			})
		}
	}
}

// A cursor minted under one ordering must not be applied under another: the two
// comparators would interleave and the page would be arbitrary. The cursor carries
// its sort key and direction precisely so the mismatch is detectable, and the
// caller is sent back to page 1 rather than served nonsense.
func TestPagination_Contacts_CursorFromAnotherSortServesPageOne(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingContacts(t, db, org)
	repo := NewContactRepository(db)

	_, cursor, err := repo.List(context.Background(), org, domain.ContactFilter{
		Limit: pagingLimit, SortBy: "created_at", SortOrder: "desc",
	})
	require.NoError(t, err)
	require.NotEmpty(t, cursor)

	for _, tc := range []struct{ sortBy, order string }{
		{"email", "desc"},
		{"name", "desc"},
		{"created_at", "asc"},
	} {
		t.Run(tc.sortBy+"/"+tc.order, func(t *testing.T) {
			page1, _, err := repo.List(context.Background(), org, domain.ContactFilter{
				Limit: pagingLimit, SortBy: tc.sortBy, SortOrder: tc.order,
			})
			require.NoError(t, err)

			got, _, err := repo.List(context.Background(), org, domain.ContactFilter{
				Limit: pagingLimit, Cursor: cursor, SortBy: tc.sortBy, SortOrder: tc.order,
			})
			require.NoError(t, err)
			assert.Equal(t, contactIDs(page1), contactIDs(got),
				"a cursor minted under created_at DESC must not advance a %s %s listing", tc.sortBy, tc.order)
		})
	}
}

// ============================================================
// Custom object records
// ============================================================

// /objects/<custom-slug> renders the SAME ObjectListView as /deals and
// /objects/company, and R4.1 taught the frontend to page it — but ListRecords still
// pages with OFFSET and ordered by created_at alone, which is not a total order.
// created_at defaults to NOW(), i.e. transaction-start time, so a bulk import, the
// industry-template seed or an automation create loop writes a whole block of
// records sharing one created_at to the microsecond. Page 1 and page 2 are separate
// queries with different LIMIT+OFFSET and Postgres is free to break those ties
// differently in each, which returns one record twice and another never.
//
// This asserts the walk against a single unpaged read, so it catches both halves of
// that: a duplicate, and a row that no page ever returns.
//
// NOTE: this is a tie-determinism test, not a keyset test. ListRecords still uses
// OFFSET, so a concurrent insert or delete between two page requests still shifts
// every later page. That is a known remaining gap, deliberately not closed here.
func TestPagination_CustomObjectRecords_TiedCreatedAtWalkIsExactAndComplete(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	defID := uuid.New()
	seedPagingRecords(t, db, org, defID)
	repo := NewCustomObjectRepository(db)

	const slug = "widget" // no row scope on a bare context, so any slug reads the same

	list := func(limit, offset int) []domain.CustomObjectRecord {
		recs, total, err := repo.ListRecords(context.Background(), org, defID, slug,
			domain.RecordFilter{Limit: limit, Offset: offset})
		require.NoError(t, err)
		require.EqualValues(t, len(pRecs), total)
		return recs
	}

	// Ground truth for the ORDERING: one query, no offset.
	full := list(100, 0)
	require.Len(t, full, len(pRecs))

	got := []uuid.UUID{}
	for offset := 0; offset < len(pRecs)+pagingLimit; offset += pagingLimit {
		page := list(pagingLimit, offset)
		got = append(got, recordIDs(page)...)
		if len(page) < pagingLimit {
			break
		}
	}

	assertFullWalk(t, got, recordIDs(full))
}

// ============================================================
// Schema drift
// ============================================================

// The whitelists in deal_repository.go, company_repository.go and
// contact_repository.go each carry a per-column nullability flag, and getting it
// wrong is silent: the ORDER BY still runs, the page still returns rows, and only a
// list whose cursor lands on or after a NULL loses data. It was wrong on shipped
// code — dealSortColumns said NOT NULL, with a comment asserting it, while the
// migration says `value NUMERIC(15,2) DEFAULT 0`.
//
// A comment cannot hold that invariant, so this test does. It reads
// information_schema, which is the fixture's own DDL — on its own that only proves
// "the whitelist agrees with the fixture", which is exactly the circle that hid the
// bug, because the fixture ALSO said NOT NULL. So it asserts against the migration
// text too. Fixture, whitelist and the DDL production actually ran are then pinned
// to each other, and breaking any one of the three fails here.
//
// It asserts on handlesNull(), not on `nullable`: a nullable column may be answered
// either by the predicate's null arms or by a nullAs COALESCE. What is never
// acceptable is a nullable column with neither.
func TestKeysetWhitelists_AgreeWithSchemaNullability(t *testing.T) {
	db, done := setupPagination(t)
	defer done()

	whitelists := []struct {
		table string
		cols  map[string]keysetColumn
	}{
		{"deals", dealSortColumns},
		{"companies", companySortColumns},
		{"contacts", contactSortColumns},
	}

	for _, w := range whitelists {
		for sortKey, col := range w.cols {
			prefix, name, ok := strings.Cut(col.col, ".")
			require.True(t, ok, "whitelist column %q must be table-qualified", col.col)
			require.Equal(t, w.table, prefix, "sort key %q is qualified with the wrong table", sortKey)

			t.Run(w.table+"."+name, func(t *testing.T) {
				var isNullable string
				require.NoError(t, db.Raw(
					`SELECT is_nullable FROM information_schema.columns
					  WHERE table_schema = 'public' AND table_name = ? AND column_name = ?`,
					w.table, name).Scan(&isNullable).Error)
				require.NotEmpty(t, isNullable, "no such column %s.%s in the fixture schema", w.table, name)

				fixtureNullable := isNullable == "YES"
				assert.Equal(t, migrationNullable(t, w.table, name), fixtureNullable,
					"the fixture DDL for %s.%s disagrees with the migration production ran; "+
						"a fixture stricter than production is how the deals NULL bug stayed invisible", w.table, name)
				assert.Equal(t, fixtureNullable, col.handlesNull(),
					"%s.%s is nullable=%v in the schema, but its whitelist entry %s handle NULLs "+
						"(set nullable, or nullAs when the Go field cannot carry a NULL)",
					w.table, name, fixtureNullable, map[bool]string{true: "does", false: "does not"}[col.handlesNull()])
			})
		}
	}
}

// migrationNullable reports whether the migration that CREATEs `table` declares
// `column` without NOT NULL. Deliberately crude — it reads the one file that
// creates these three tables and looks at the line for the column — because the
// alternative is trusting a comment, and a comment is what was wrong.
func migrationNullable(t *testing.T, table, column string) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000002_schema.up.sql"))
	require.NoError(t, err, "the schema migration must be readable from the package dir")

	_, after, ok := strings.Cut(string(raw), "CREATE TABLE "+table+" (")
	require.True(t, ok, "migration has no CREATE TABLE %s", table)
	body, _, ok := strings.Cut(after, "\n);")
	require.True(t, ok, "CREATE TABLE %s is not terminated", table)

	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != column {
			continue
		}
		return !strings.Contains(strings.ToUpper(line), "NOT NULL")
	}
	t.Fatalf("migration's CREATE TABLE %s declares no column %q", table, column)
	return false
}
