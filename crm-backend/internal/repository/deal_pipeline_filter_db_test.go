package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDealList_PipelineFilterIsAColumnPredicate_DB pins the R9.3 board filter to
// the REAL `deals.pipeline_id` column.
//
// It exists because of the exact bug it would have caught. The kanban sends the
// board id as a generic list filter, and any key the deal adapter does not bind
// explicitly falls through to CustomFilters, where it is executed as
// `deals.custom_fields ->> 'pipeline_id' = '<uuid>'`. `pipeline_id` is a column,
// never a custom-fields key, so that predicate matches NOTHING — and the failure
// is silent: no error, just an empty list. Every deal board in every org would
// have rendered its columns and zero cards.
//
// A test that only asserted "filtering by board A returns A's deals" would have
// passed against the broken version if A had no deals, so this asserts BOTH
// directions: A's deal is returned, and B's is not.
func TestDealList_PipelineFilterIsAColumnPredicate_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupPagination(t)
	defer cleanup()

	orgID := seedPagingOrg(t, db)
	boardA, boardB := uuid.New(), uuid.New()
	dealA, dealB := uuid.New(), uuid.New()

	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, pipeline_id) VALUES (?, ?, 'on board A', ?)`,
		dealA, orgID, boardA).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, pipeline_id) VALUES (?, ?, 'on board B', ?)`,
		dealB, orgID, boardB).Error)

	repo := NewDealRepository(db)
	got, _, err := repo.List(context.Background(), orgID, domain.DealFilter{PipelineID: &boardA})
	require.NoError(t, err)

	ids := make([]uuid.UUID, 0, len(got))
	for _, d := range got {
		ids = append(ids, d.ID)
	}
	assert.Contains(t, ids, dealA, "the filtered board's deal must come back — an empty result is the failure mode this pins")
	assert.NotContains(t, ids, dealB, "another board's deal must not leak into a scoped list")
	assert.Len(t, got, 1)
}
