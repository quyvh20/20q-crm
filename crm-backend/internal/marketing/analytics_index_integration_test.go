package marketing_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// campaignRollupIndexDDL must stay character-identical to the R6.2 boot guard in
// cmd/server/main.go and to
// migrations/000073_marketing_email_events_campaign_rollup.up.sql. Three files, one
// index; this test is the only thing that would notice them drifting apart.
const campaignRollupIndexDDL = `CREATE INDEX IF NOT EXISTS idx_marketing_email_events_campaign_rollup
	ON marketing_email_events(org_id, campaign_id, event_type, email_normalized)`

// explainLines returns the planner's output for q as one string per line.
func explainLines(t *testing.T, db *gorm.DB, q string, args ...interface{}) string {
	t.Helper()
	rows, err := db.Raw("EXPLAIN "+q, args...).Rows()
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		out = append(out, line)
	}
	require.NoError(t, rows.Err())
	return strings.Join(out, "\n")
}

// seedRollupEvents loads enough events that a full scan is genuinely more expensive than
// an index probe — the planner will happily seq-scan a hundred rows whatever indexes
// exist, so a small fixture would make this test pass for the wrong reason.
func seedRollupEvents(t *testing.T, db *gorm.DB, org, camp uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(`
		INSERT INTO marketing_email_events
			(org_id, svix_id, event_type, email_normalized, campaign_id, occurred_at)
		SELECT ?, 'svix_' || c.n || '_' || s.rn || '_' || e.kind,
		       e.kind,
		       'user' || s.rn || '.c' || c.n || '@example.com',
		       CASE WHEN c.n = 0 THEN ?::uuid ELSE ?::uuid END,
		       NOW() + make_interval(secs => e.offs)
		FROM generate_series(0, 19) AS c(n)
		CROSS JOIN generate_series(1, 250) AS s(rn)
		CROSS JOIN (VALUES ('delivered', 0), ('opened', 60), ('clicked', 90)) AS e(kind, offs)`,
		org, camp, uuid.New()).Error)
	require.NoError(t, db.Exec(`ANALYZE marketing_email_events`).Error)
}

// TestCampaignRollupIndex_IsUsedAndIsLoadBearing pins the R6.2 index to the three queries
// it exists for. It is a performance guard written as a correctness test: the queries
// return the same answers with or without the index, so nothing else in the suite would
// notice it disappearing — and what it prevents (CampaignAnalytics' correlated LATERAL
// probe degenerating into one full table scan per open event, measured at 43 s on 590k
// events) is a production timeout, not a wrong number.
//
// Each case asserts BOTH directions: the plan uses the index when it exists, AND falls
// back to a sequential scan when it is dropped. Without the second half this test would
// still pass if the index stopped being load-bearing.
func TestCampaignRollupIndex_IsUsedAndIsLoadBearing(t *testing.T) {
	db := newCampaignTestDB(t)
	createEventsTable(t, db)
	org, camp := uuid.New(), uuid.New()
	seedRollupEvents(t, db, org, camp)

	const idxName = "idx_marketing_email_events_campaign_rollup"

	// The boot guard is CREATE INDEX IF NOT EXISTS and runs on EVERY pod boot, so it has
	// to be a no-op the second time rather than an error.
	require.NoError(t, db.Exec(campaignRollupIndexDDL).Error, "first run creates the index")
	require.NoError(t, db.Exec(campaignRollupIndexDDL).Error, "boot guard must be idempotent")

	var valid bool
	require.NoError(t, db.Raw(`SELECT i.indisvalid FROM pg_class c
		JOIN pg_index i ON i.indexrelid = c.oid WHERE c.relname = ?`, idxName).
		Scan(&valid).Error)
	assert.True(t, valid, "the index must be VALID — an invalid index is never used by the "+
		"planner and CREATE INDEX IF NOT EXISTS will not repair it")

	require.NoError(t, db.Exec(`ANALYZE marketing_email_events`).Error)

	// The three queries this index exists for, each in the exact shape its repository
	// method issues it (analytics_repository.go and ab_repository.go).
	cases := []struct {
		name string
		q    string
		args []interface{}
	}{
		{
			name: "CampaignAnalytics rollup",
			q: `SELECT event_type, COUNT(*), COUNT(DISTINCT email_normalized)
			    FROM marketing_email_events
			    WHERE org_id = ? AND campaign_id = ? GROUP BY event_type`,
			args: []interface{}{org, camp},
		},
		{
			// The expensive one: a correlated subquery per open event.
			name: "CampaignAnalytics MPP LATERAL probe",
			q: `SELECT COUNT(DISTINCT o.email_normalized)
			    FROM marketing_email_events o
			    LEFT JOIN LATERAL (
			        SELECT MIN(d.occurred_at) AS delivered_at
			        FROM marketing_email_events d
			        WHERE d.org_id = o.org_id AND d.campaign_id = o.campaign_id
			          AND d.email_normalized = o.email_normalized
			          AND d.event_type = 'delivered'
			    ) d ON TRUE
			    WHERE o.org_id = ? AND o.campaign_id = ? AND o.event_type = 'opened'
			      AND (d.delivered_at IS NULL OR o.occurred_at IS NULL
			           OR o.occurred_at > d.delivered_at + make_interval(secs => 10))`,
			args: []interface{}{org, camp},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainLines(t, db, tc.q, tc.args...)
			assert.Contains(t, plan, idxName,
				"query must be served by the R6.2 rollup index, got plan:\n%s", plan)
		})
	}

	// Now remove the control and prove the plans actually depended on it. If these
	// assertions stop holding, the assertions above have stopped meaning anything.
	require.NoError(t, db.Exec(`DROP INDEX `+idxName).Error)
	require.NoError(t, db.Exec(`ANALYZE marketing_email_events`).Error)

	for _, tc := range cases {
		t.Run(tc.name+" degrades without the index", func(t *testing.T) {
			plan := explainLines(t, db, tc.q, tc.args...)
			assert.NotContains(t, plan, idxName)
			assert.Contains(t, plan, "Seq Scan",
				"without the index these queries fall back to full scans — if this no "+
					"longer holds, another index has started covering "+
					"(org_id, campaign_id, event_type, email_normalized) and the guard "+
					"above is no longer proving anything. Plan:\n%s", plan)
		})
	}
}

// TestCampaignRollupIndex_DoesNotDuplicateOrgCreated guards the one claim R6.2 rejected
// from the plan: that the dashboard's org+time-window query needed a new index. It does
// not — DeliverabilityRates is already served by idx_marketing_email_events_org_created,
// and a second index on the same leading columns would be write amplification for
// nothing.
func TestCampaignRollupIndex_DoesNotDuplicateOrgCreated(t *testing.T) {
	db := newCampaignTestDB(t)
	createEventsTable(t, db)
	org, camp := uuid.New(), uuid.New()
	seedRollupEvents(t, db, org, camp)

	require.NoError(t, db.Exec(`CREATE INDEX IF NOT EXISTS idx_marketing_email_events_org_created
		ON marketing_email_events(org_id, created_at)`).Error)
	require.NoError(t, db.Exec(campaignRollupIndexDDL).Error)
	require.NoError(t, db.Exec(`ANALYZE marketing_email_events`).Error)

	// DeliverabilityRates' shape (events_repository.go): org + a created_at window.
	plan := explainLines(t, db, `
		SELECT COUNT(*) FILTER (WHERE event_type = 'delivered'),
		       COUNT(*) FILTER (WHERE event_type = 'complained')
		FROM marketing_email_events
		WHERE org_id = ? AND created_at >= NOW() - make_interval(secs => ?)`,
		org, 3600)

	assert.NotContains(t, plan, "idx_marketing_email_events_campaign_rollup",
		"the rollup index must not be what serves the org+time dashboard query — that is "+
			"idx_marketing_email_events_org_created's job. Plan:\n%s", plan)
}
