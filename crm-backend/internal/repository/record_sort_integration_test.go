package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================
// R7.3 — the sort menu the API advertises, driven against real Postgres
// ============================================================
//
// domain.recordSortFields is the list of orderings the registry route accepts and
// the UI renders headers from. The repositories reach an ordering through their
// own keyset whitelists (deal_repository.go, contact_repository.go), which speak a
// DIFFERENT vocabulary — contacts' "first_name" column is "name" there — and which
// silently fall back to created_at for anything they do not recognise.
//
// So a mismatch between the two tables is invisible in every way that matters: no
// error, no empty page, just a list that is not sorted the way the header says it
// is, and a sort arrow drawn on a column that is not driving the order. These two
// tests are what stop that drift, one statically and one against real SQL.

// The static half. It reads both tables directly, so it fails on the drift itself
// rather than on a downstream symptom, and it names the exact missing entry.
func TestRecordSortDeclaration_MatchesKeysetWhitelists(t *testing.T) {
	whitelists := map[string]map[string]keysetColumn{
		"contact": contactSortColumns,
		"deal":    dealSortColumns,
	}

	for slug, allowed := range whitelists {
		keys := domain.SortableRecordFields(slug)
		require.NotEmpty(t, keys, "%s advertises no sortable fields", slug)

		for _, key := range keys {
			filterKey := domain.RecordSortFilterKey(slug, key)
			require.NotEmpty(t, filterKey, "%s advertises %q with no filter-key translation", slug, key)
			assert.Contains(t, allowed, filterKey,
				"%s advertises sort key %q, which translates to filter key %q, "+
					"but that is not in the repository's keyset whitelist — the ORDER BY would "+
					"silently fall back to created_at while the UI showed a %q sort",
				slug, key, filterKey, key)
		}
	}

	// The default must be reachable too, or "no sort requested" and "sort by the
	// default" would be two different queries.
	for slug, allowed := range whitelists {
		assert.Contains(t, allowed, domain.RecordSortFilterKey(slug, domain.DefaultRecordSortBy),
			"%s cannot order by the default sort key", slug)
	}
}

// The dynamic half: every advertised ordering, in both directions, paged to
// exhaustion against real Postgres over the R4 fixtures — which are built for
// exactly this (explicit non-random ids, sort-column ties straddling page
// boundaries, and NULL sort values: three NULL emails on contacts, three NULL
// value/probability deals).
//
// Ground truth is a single unpaged read under the same ordering, so this asserts
// the walk is CORRECT rather than merely self-consistent: assertFullWalk checks
// every row appears exactly once, the set matches, and the order matches.
func TestPagination_AdvertisedSorts_WalkExactlyOnce(t *testing.T) {
	db, done := setupPagination(t)
	defer done()
	org := seedPagingOrg(t, db)
	seedPagingContacts(t, db, org)
	seedPagingDeals(t, db, org)

	contactRepo := NewContactRepository(db)
	dealRepo := NewDealRepository(db)

	// Each object's list, expressed in the CLIENT sort vocabulary, so the
	// translation is inside the code under test rather than duplicated here.
	lists := map[string]func(key, order, cursor string, limit int) ([]uuid.UUID, string){
		"contact": func(key, order, cursor string, limit int) ([]uuid.UUID, string) {
			cs, next, err := contactRepo.List(context.Background(), org, domain.ContactFilter{
				Limit: limit, Cursor: cursor,
				SortBy:    domain.RecordSortFilterKey("contact", key),
				SortOrder: order,
			})
			require.NoError(t, err)
			require.LessOrEqual(t, len(cs), limit, "a page must never exceed the requested limit")
			return contactIDs(cs), next
		},
		"deal": func(key, order, cursor string, limit int) ([]uuid.UUID, string) {
			ds, next, err := dealRepo.List(context.Background(), org, domain.DealFilter{
				Limit: limit, Cursor: cursor,
				SortBy:    domain.RecordSortFilterKey("deal", key),
				SortOrder: order,
			})
			require.NoError(t, err)
			require.LessOrEqual(t, len(ds), limit, "a page must never exceed the requested limit")
			return dealIDs(ds), next
		},
	}

	for slug, list := range lists {
		for _, key := range domain.SortableRecordFields(slug) {
			for _, order := range []string{"asc", "desc"} {
				t.Run(slug+"/"+key+"/"+order, func(t *testing.T) {
					full, next := list(key, order, "", 50)
					require.Empty(t, next, "50 exceeds the seeded row count, so there is no second page")
					require.NotEmpty(t, full, "the fixture must seed rows for %s", slug)

					got := walkPages(t, func(cursor string) ([]uuid.UUID, string) {
						return list(key, order, cursor, pagingLimit)
					})
					assertFullWalk(t, got, full)
				})
			}
		}
	}
}
