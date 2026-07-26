package automation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// TestCountRunsByEnrollment_ScopesToTag proves the per-enrollment progress rollup counts
// ONLY the runs tagged with that enrollment id (via the trigger_context jsonb filter),
// excluding a sibling enrollment of the same workflow and the workflow's own untagged
// natural-trigger runs — the fix for the workflow-wide-conflation review finding. This
// exercises the `trigger_context->>? = ?` SQL against real Postgres.
func TestCountRunsByEnrollment_ScopesToTag(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	e := &Engine{db: db}
	ctx := context.Background()
	org, wf := uuid.New(), uuid.New()
	enrollA, enrollB := uuid.New(), uuid.New()

	mk := func(status string, enrollID uuid.UUID, tagged bool) {
		tc := map[string]any{"contact": map[string]any{"id": uuid.NewString()}}
		if tagged {
			tc[marketingEnrollmentKey] = enrollID.String()
		}
		raw, _ := json.Marshal(tc)
		run := &WorkflowRun{
			ID: uuid.New(), OrgID: org, WorkflowID: wf, WorkflowVersion: 1,
			Status: status, TriggerContext: datatypes.JSON(raw), IdempotencyKey: uuid.NewString(),
		}
		require.NoError(t, db.Create(run).Error)
	}
	mk("completed", enrollA, true)
	mk("waiting", enrollA, true)
	mk("completed", enrollB, true)   // sibling enrollment, same workflow
	mk("completed", uuid.Nil, false) // untagged natural-trigger run

	a, err := e.CountRunsByEnrollment(ctx, org, wf, enrollA)
	require.NoError(t, err)
	assert.Equal(t, int64(1), a["completed"])
	assert.Equal(t, int64(1), a["waiting"])
	assert.Len(t, a, 2, "only enrollment A's runs — not B's, not the untagged run")

	b, err := e.CountRunsByEnrollment(ctx, org, wf, enrollB)
	require.NoError(t, err)
	assert.Equal(t, int64(1), b["completed"])
	assert.Len(t, b, 1)

	// A different org sees none of it (explicit org scope).
	none, err := e.CountRunsByEnrollment(ctx, uuid.New(), wf, enrollA)
	require.NoError(t, err)
	assert.Len(t, none, 0)
}
