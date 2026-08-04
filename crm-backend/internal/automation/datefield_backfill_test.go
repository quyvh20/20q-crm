package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// datefield_backfill_test.go covers R4.4: activating a date_field workflow must arm
// the records that ALREADY exist, and re-arming must converge rather than accumulate.
//
// Every DB assertion checks the SET of record ids, not a count. A count passes
// whenever the backfill arms the right NUMBER of wrong records — which is exactly
// what a mis-shaped payload map, an off-by-one cap, or a missing test-lead filter
// produces.

// ── Pure logic (no DB — runs in CI's -short unit job too) ─────────────────────

func TestIsBackfillTestLeadContact(t *testing.T) {
	real := "someone@example.com"
	testLead := "crm-test-" + uuid.NewString() + "@lead-test.invalid"
	upper := "CRM-TEST-x@LEAD-TEST.INVALID"

	assert.False(t, isBackfillTestLeadContact(nil), "a contact with no email is a real contact")
	assert.False(t, isBackfillTestLeadContact(&real))
	assert.True(t, isBackfillTestLeadContact(&testLead))
	assert.True(t, isBackfillTestLeadContact(&upper),
		"IsTestContactEmail normalizes case; the backfill must inherit that, not re-implement it")
}

// TestDateFieldReconcileNeededOnUpdate pins the guard that keeps UpdateWorkflow from
// re-scanning the whole object on every save. UpdateWorkflow fires on renames and
// step edits too, so an unguarded hook there is a table scan behind the save button.
func TestDateFieldReconcileNeededOnUpdate(t *testing.T) {
	dfTrigger := func(object, field string, offset int) datatypes.JSON {
		raw, err := json.Marshal(map[string]any{"type": TriggerDateField, "params": map[string]any{
			"object": object, "field": field, "offset_days": offset, "at_time": "09:00", "timezone": "UTC",
		}})
		require.NoError(t, err)
		return datatypes.JSON(raw)
	}
	wfWith := func(trigger datatypes.JSON, active bool) *Workflow {
		return &Workflow{ID: uuid.New(), OrgID: uuid.New(), Trigger: trigger, IsActive: active}
	}
	base := dfTrigger("deal", "deal.expected_close_at", -3)

	t.Run("unchanged params → no reconcile", func(t *testing.T) {
		assert.False(t, dateFieldReconcileNeededOnUpdate(base, wfWith(base, true)))
	})
	t.Run("offset changed → reconcile", func(t *testing.T) {
		next := dfTrigger("deal", "deal.expected_close_at", -10)
		assert.True(t, dateFieldReconcileNeededOnUpdate(base, wfWith(next, true)))
	})
	t.Run("object changed → reconcile", func(t *testing.T) {
		next := dfTrigger("contact", "contact.custom_fields.renewal", -3)
		assert.True(t, dateFieldReconcileNeededOnUpdate(base, wfWith(next, true)),
			"the object-change orphan is the whole reason this path exists")
	})
	t.Run("became date_field → reconcile", func(t *testing.T) {
		prev := datatypes.JSON(`{"type":"contact_created"}`)
		assert.True(t, dateFieldReconcileNeededOnUpdate(prev, wfWith(base, true)))
	})
	t.Run("inactive → no reconcile", func(t *testing.T) {
		next := dfTrigger("deal", "deal.expected_close_at", -10)
		assert.False(t, dateFieldReconcileNeededOnUpdate(base, wfWith(next, false)),
			"armWorkflowTimers already cancels an inactive date_field workflow's timers")
	})
	t.Run("left date_field → no reconcile", func(t *testing.T) {
		next := datatypes.JSON(`{"type":"contact_created"}`)
		assert.False(t, dateFieldReconcileNeededOnUpdate(base, wfWith(next, true)))
	})
}

// ── DB-backed (Docker-gated) ──────────────────────────────────────────────────

// dfbSeedDeals creates the deals fixture in the shape dealAutomationMap reads.
// Named dfb* throughout so nothing here collides with the package's other fixtures.
func dfbSeedDeals(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT DEFAULT '',
		value NUMERIC DEFAULT 0,
		probability INT DEFAULT 0,
		stage_id UUID,
		contact_id UUID,
		company_id UUID,
		owner_user_id UUID,
		is_won BOOLEAN NOT NULL DEFAULT FALSE,
		is_lost BOOLEAN NOT NULL DEFAULT FALSE,
		expected_close_at TIMESTAMPTZ,
		closed_at TIMESTAMPTZ,
		custom_fields JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
}

// dfbDeal inserts a deal with the given expected_close_at (nil → NULL).
func dfbDeal(t *testing.T, db *gorm.DB, orgID uuid.UUID, closeAt *time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, expected_close_at) VALUES (?, ?, ?, ?)`,
		id, orgID, "deal-"+id.String()[:8], closeAt).Error)
	return id
}

// dfbCompleteContacts closes a gap in setupTestDB's contacts fixture: it omits
// company_id, which domain.Contact and production both have and which
// contactAutomationMap reads. CREATE TABLE IF NOT EXISTS never ADDS a column, so the
// only way to reconcile the fixture with production is an ALTER. Without it the
// contact scan fails on a column that exists everywhere but here — a fixture that
// disagrees with production, which is the failure the setupTestDB comments warn about.
func dfbCompleteContacts(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)
}

// dfbContact inserts a contact carrying a custom_fields blob.
func dfbContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, email, customFields string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, email, custom_fields) VALUES (?, ?, ?, ?, ?::jsonb)`,
		id, orgID, "c-"+id.String()[:8], email, customFields).Error)
	return id
}

// dfbTimerRecordIDs returns the set of record ids the workflow currently has pending
// date_field timers for, read out of each timer's payload — the same place
// fireTimerRun reads it. Asserting on ids rather than a count is the point: a bare
// count is green whenever the backfill arms the right number of WRONG records.
func dfbTimerRecordIDs(t *testing.T, db *gorm.DB, wfID uuid.UUID) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, timer := range pendingDateFieldTimers(t, db, wfID) {
		var payload map[string]any
		require.NoError(t, json.Unmarshal(timer.Payload, &payload))
		id, _ := payload["entity_id"].(string)
		require.NotEmpty(t, id, "every armed timer must carry entity_id — fireTimerRun reads it")
		out[id] = true
	}
	return out
}

func dfbIDSet(ids ...uuid.UUID) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id.String()] = true
	}
	return out
}

// dfbCaptureLogs swaps the engine's logger for one writing JSON into buf. Returns the
// buffer so a test can assert on what was actually logged (the no-silent-caps rule).
func dfbCaptureLogs(engine *Engine) *bytes.Buffer {
	var buf bytes.Buffer
	engine.logger = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &buf
}

// TestDateFieldBackfill_ArmsPreExistingRecords is the headline: activation must arm
// records that already existed, which is what write-driven materialization never did.
func TestDateFieldBackfill_ArmsPreExistingRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 30)
	d1 := dfbDeal(t, db, orgID, &closeAt)
	d2 := dfbDeal(t, db, orgID, &closeAt)
	d3 := dfbDeal(t, db, orgID, &closeAt)
	// A deal in ANOTHER org must not be armed — the scan takes an explicit org_id
	// because it deliberately bypasses RecordService (and therefore OLS).
	otherOrg := dfbDeal(t, db, uuid.New(), &closeAt)

	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)

	stats, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Scanned)
	assert.Equal(t, 3, stats.Armed)
	assert.Zero(t, stats.Truncated)

	got := dfbTimerRecordIDs(t, db, wf.ID)
	assert.Equal(t, dfbIDSet(d1, d2, d3), got, "exactly the org's three deals")
	assert.NotContains(t, got, otherOrg.String(), "another org's deal must never be armed")

	// The occurrence itself must be usable: future, and offset -3 from the close date.
	timers := pendingDateFieldTimers(t, db, wf.ID)
	require.Len(t, timers, 3)
	for _, timer := range timers {
		assert.True(t, timer.FireAt.After(time.Now()), "fires in the future")
		assert.True(t, timer.FireAt.Before(closeAt), "offset -3 fires before the close date")
	}
}

// TestDateFieldBackfill_TimerPayloadMatchesLiveArming pins the payload shape. A
// backfilled timer is fired by the SAME fireTimerRun as a write-armed one, so a
// payload that differs produces a run with no eval context and conditions that fail
// closed — silently, days later.
func TestDateFieldBackfill_TimerPayloadMatchesLiveArming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 30)
	dealID := dfbDeal(t, db, orgID, &closeAt)
	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)

	_, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	timers := pendingDateFieldTimers(t, db, wf.ID)
	require.Len(t, timers, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(timers[0].Payload, &payload))
	assert.Equal(t, dealID.String(), payload["entity_id"])

	deal, ok := payload["deal"].(map[string]any)
	require.True(t, ok, "the record snapshot must live under the object slug, as materializeDateFieldTimers writes it")
	assert.Equal(t, dealID.String(), deal["id"])
	assert.NotEmpty(t, deal["expected_close_at"], "RFC3339 close date, the layout parseDateValue tries first")

	trig, ok := payload["trigger"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, TriggerDateField, trig["type"])
	assert.Equal(t, "deal.expected_close_at", trig["field"])
	assert.NotEmpty(t, trig["fire_at"])

	// The dedupe key must be the one the live path would have produced, or a
	// subsequent write to the record arms a SECOND timer for the same occurrence.
	assert.Equal(t, dateFieldDedupeKey(dealID.String(), timers[0].FireAt), timers[0].DedupeKey)
}

// TestDateFieldBackfill_SkipsUnusableDates: NULL, past and unparseable dates must arm
// nothing at all — not a row with a zero/past fire_at that the scanner picks up on its
// very next pass and fires immediately for every record in the org.
func TestDateFieldBackfill_SkipsUnusableDates(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	past := time.Now().AddDate(0, 0, -30)
	dfbDeal(t, db, orgID, nil)   // NULL date
	dfbDeal(t, db, orgID, &past) // already elapsed
	// Unparseable: a custom-field date holding junk, read through the nested path.
	junkID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO deals (id, org_id, title, custom_fields) VALUES (?, ?, 'junk', '{"renewal_date":"not a date"}'::jsonb)`,
		junkID, orgID).Error)

	nullWF := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)
	stats, err := engine.reconcileDateFieldTimers(ctx, nullWF)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Scanned)
	assert.Equal(t, 0, stats.Armed)
	assert.Equal(t, 3, stats.SkippedNoFutureDate)
	assert.Empty(t, pendingDateFieldTimers(t, db, nullWF.ID))

	junkWF := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.custom_fields.renewal_date", -3, true)
	stats, err = engine.reconcileDateFieldTimers(ctx, junkWF)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Armed, "an unparseable date arms nothing")
	assert.Empty(t, pendingDateFieldTimers(t, db, junkWF.ID))

	// And no spurious rows anywhere in the table.
	var total int64
	require.NoError(t, db.Model(&AutomationTimer{}).Where("kind = ?", TimerKindDateField).Count(&total).Error)
	assert.Zero(t, total)
}

// TestDateFieldBackfill_OnOffOnConverges: the reconcile is idempotent by construction
// because it cancels unconditionally before arming. Without that, a moved-date dedupe
// key differs from the armed one and OnConflict never fires, so on→off→on would leave
// 2N timers and every record would be paged twice.
func TestDateFieldBackfill_OnOffOnConverges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 30)
	d1 := dfbDeal(t, db, orgID, &closeAt)
	d2 := dfbDeal(t, db, orgID, &closeAt)
	want := dfbIDSet(d1, d2)

	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)

	// ON
	_, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	require.Equal(t, want, dfbTimerRecordIDs(t, db, wf.ID))

	// OFF — the same call serves deactivation: cancel, arm nothing.
	wf.IsActive = false
	stats, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Armed)
	assert.Empty(t, pendingDateFieldTimers(t, db, wf.ID))

	// ON again
	wf.IsActive = true
	_, err = engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	assert.Equal(t, want, dfbTimerRecordIDs(t, db, wf.ID))
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 2, "N, never 2N")

	// A redundant re-activation (double-click, retry) must also converge.
	_, err = engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 2)
}

// TestDateFieldBackfill_ExcludesTestLeadContacts is the substitute for write-time
// silence, which a backfill cannot read: isAutomationSilenced lives on a live write's
// payload and there is deliberately nothing on the record to look at. The test lead's
// persisted @lead-test.invalid identity is the one thing that survives, so the scan
// filters on it — otherwise the backfill re-arms precisely the timers
// WithAutomationSilenced exists to prevent ("no date_field timer armed to page a rep
// next week about a contact that does not exist").
//
// The real contact is the load-bearing control: without it a backfill that armed
// NOTHING would pass this test and look like a working filter.
func TestDateFieldBackfill_ExcludesTestLeadContacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbCompleteContacts(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	renewal := time.Now().AddDate(0, 0, 45).Format("2006-01-02")
	cf := fmt.Sprintf(`{"renewal_date":%q}`, renewal)

	real1 := dfbContact(t, db, orgID, "real1@example.com", cf)
	real2 := dfbContact(t, db, orgID, "real2@example.com", cf)
	testLead := dfbContact(t, db, orgID, "crm-test-"+uuid.NewString()+"@lead-test.invalid", cf)

	wf := createDateFieldWF(t, engine.repo, orgID, "contact", "contact.custom_fields.renewal_date", -7, true)

	stats, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.ExcludedTestLeads)
	assert.Equal(t, 2, stats.Armed)

	// Scanned is PRE-filter, on this path as on every other. It used to be built from
	// the KEPT contacts, so `scanned` in the completion log meant "rows read" for deals
	// and "rows kept" for contacts — two different numbers under one name, with nothing
	// asserting either. Pinning the identity here is what keeps them one thing.
	assert.Equal(t, 3, stats.Scanned,
		"the excluded test lead is counted in Scanned too — Scanned and ExcludedTestLeads are independent numbers")
	assert.Equal(t, 0, stats.SkippedNoFutureDate)
	assert.Equal(t, stats.Scanned, stats.Armed+stats.ExcludedTestLeads+stats.SkippedNoFutureDate,
		"the counters must add up, or the completion log is lying about one of them")

	got := dfbTimerRecordIDs(t, db, wf.ID)
	assert.Equal(t, dfbIDSet(real1, real2), got,
		"real contacts armed (the control), the synthetic test lead excluded")
	assert.NotContains(t, got, testLead.String())
}

// TestDateFieldBackfill_ParamsChangeRearmsAtNewConfig covers the first pre-existing
// bug this reconcile closes: armWorkflowTimers only cancels when the workflow is NOT
// an active date_field workflow, so changing the offset on a LIVE workflow left every
// armed timer pinned to the old moment, firing forever at a time nobody configured.
func TestDateFieldBackfill_ParamsChangeRearmsAtNewConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 60)
	dealID := dfbDeal(t, db, orgID, &closeAt)

	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)
	_, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	before := pendingDateFieldTimers(t, db, wf.ID)
	require.Len(t, before, 1)
	oldFireAt := before[0].FireAt
	oldKey := before[0].DedupeKey

	// The admin changes "3 days before" to "30 days before" while it stays active.
	prevTrigger := wf.Trigger
	trig, err := json.Marshal(map[string]any{"type": TriggerDateField, "params": map[string]any{
		"object": "deal", "field": "deal.expected_close_at", "offset_days": -30,
		"at_time": "09:00", "timezone": "UTC",
	}})
	require.NoError(t, err)
	wf.Trigger = datatypes.JSON(trig)
	require.True(t, dateFieldReconcileNeededOnUpdate(prevTrigger, wf),
		"UpdateWorkflow must recognise an offset change as needing a reconcile")

	_, err = engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)

	after := pendingDateFieldTimers(t, db, wf.ID)
	require.Len(t, after, 1, "one occurrence, not two — the old-config timer is gone")
	assert.Equal(t, dfbIDSet(dealID), dfbTimerRecordIDs(t, db, wf.ID))
	assert.NotEqual(t, oldKey, after[0].DedupeKey, "a new offset is a new occurrence")
	assert.True(t, after[0].FireAt.Before(oldFireAt), "-30 fires earlier than -3")

	var stale int64
	require.NoError(t, db.Model(&AutomationTimer{}).
		Where("workflow_id = ? AND dedupe_key = ?", wf.ID, oldKey).Count(&stale).Error)
	assert.Zero(t, stale, "the old-config timer must not survive the params change")
}

// TestDateFieldBackfill_ObjectChangeLeavesNoOrphans covers the second, worse
// pre-existing bug — and this one FAILED before R4.4.
//
// The FE explicitly supports switching a date_field trigger's object (deal → contact).
// When that happened on an ACTIVE workflow, nothing ever revisited the old object's
// timers: ActiveDateFieldWorkflowsForObject filters on the NEW slug, materialize's own
// `p.Object != slug` guard is a second filter, and armWorkflowTimers does not cancel an
// active date_field workflow. The scheduler then fired them anyway, because it only
// checks wf.IsActive. The unconditional cancel in the reconcile is what closes it.
func TestDateFieldBackfill_ObjectChangeLeavesNoOrphans(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	dfbCompleteContacts(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 60)
	dealID := dfbDeal(t, db, orgID, &closeAt)
	renewal := time.Now().AddDate(0, 0, 45).Format("2006-01-02")
	contactID := dfbContact(t, db, orgID, "real@example.com", fmt.Sprintf(`{"renewal_date":%q}`, renewal))

	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)
	_, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	require.Equal(t, dfbIDSet(dealID), dfbTimerRecordIDs(t, db, wf.ID))

	// Repoint the live workflow at contacts.
	prevTrigger := wf.Trigger
	trig, err := json.Marshal(map[string]any{"type": TriggerDateField, "params": map[string]any{
		"object": "contact", "field": "contact.custom_fields.renewal_date", "offset_days": -7,
		"at_time": "09:00", "timezone": "UTC",
	}})
	require.NoError(t, err)
	wf.Trigger = datatypes.JSON(trig)
	require.True(t, dateFieldReconcileNeededOnUpdate(prevTrigger, wf),
		"UpdateWorkflow must recognise an object change as needing a reconcile")

	_, err = engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)

	got := dfbTimerRecordIDs(t, db, wf.ID)
	assert.Equal(t, dfbIDSet(contactID), got, "only the NEW object's records are armed")
	assert.NotContains(t, got, dealID.String(),
		"the old object's timer is an orphan nothing else would ever cancel, and the scheduler fires it on wf.IsActive alone")
}

// TestDateFieldBackfill_CapTruncationIsExactAndLogged pins the no-silent-caps rule:
// when the scan cap bites, the number of records left UNARMED is exact and appears in
// the log. A cap nobody can see reads as "the workflow is on" while most of the book
// is never armed.
func TestDateFieldBackfill_CapTruncationIsExactAndLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	logs := dfbCaptureLogs(engine)
	ctx := context.Background()
	orgID := uuid.New()

	// Exercise the boundary without inserting 10001 rows.
	restore := dateFieldBackfillScanLimit
	dateFieldBackfillScanLimit = 4
	t.Cleanup(func() { dateFieldBackfillScanLimit = restore })

	closeAt := time.Now().AddDate(0, 0, 30)
	const total = 7
	for i := 0; i < total; i++ {
		dfbDeal(t, db, orgID, &closeAt)
	}

	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)
	stats, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)

	assert.Equal(t, 4, stats.Scanned)
	assert.Equal(t, 4, stats.Armed)
	assert.Equal(t, int64(total-4), stats.Truncated, "exact, not 'at least'")
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 4)

	out := logs.String()
	assert.Contains(t, out, "hit its scan cap", "the truncation must be logged, not swallowed")
	assert.Contains(t, out, `"unexamined_records":3`, "and it must carry the exact count")
}

// TestDateFieldBackfill_CustomObjectFlatFieldPath: custom objects have no nested
// "custom_fields" wrapper — customToUniform unmarshals the data blob FLAT and
// fireEvent lays it straight under the slug, so the trigger path is "<slug>.<key>".
// Reproducing the wrong shape here resolves to nil and arms nothing, silently.
func TestDateFieldBackfill_CustomObjectFlatFieldPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	createObjectRegistry(db)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_records (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		object_def_id UUID NOT NULL,
		display_name TEXT DEFAULT '',
		data JSONB DEFAULT '{}',
		owner_user_id UUID,
		created_by UUID,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)

	defID := seedCustomObject(db, orgID, "subscription", "Subscription", "📦")
	otherDefID := seedCustomObject(db, orgID, "asset", "Asset", "🔧")
	renewal := time.Now().AddDate(0, 0, 40).Format("2006-01-02")

	mk := func(defID uuid.UUID) uuid.UUID {
		id := uuid.New()
		require.NoError(t, db.Exec(
			`INSERT INTO custom_object_records (id, org_id, object_def_id, display_name, data) VALUES (?, ?, ?, ?, ?::jsonb)`,
			id, orgID, defID, "rec", fmt.Sprintf(`{"renews_on":%q}`, renewal)).Error)
		return id
	}
	sub1 := mk(defID)
	sub2 := mk(defID)
	asset := mk(otherDefID) // a different custom object: must not be armed

	wf := createDateFieldWF(t, engine.repo, orgID, "subscription", "subscription.renews_on", -14, true)
	stats, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Armed)

	got := dfbTimerRecordIDs(t, db, wf.ID)
	assert.Equal(t, dfbIDSet(sub1, sub2), got)
	assert.NotContains(t, got, asset.String(), "the slug join must scope the scan to ONE custom object")
}

// ── The concurrency window ────────────────────────────────────────────────────

// dfbInjectAfterScan registers a ONE-SHOT gorm Query callback that runs `inject`
// immediately after the first SELECT against `table` completes, and removes itself at
// test cleanup.
//
// This is the only way to sit deterministically inside the reconcile: the interesting
// instant is "the scan has read its snapshot", which is not observable from outside.
// `inject` runs on the engine's own *gorm.DB, i.e. a DIFFERENT pooled connection from
// the reconcile's open transaction — a genuine concurrent write, not a nested one.
func dfbInjectAfterScan(t *testing.T, db *gorm.DB, table string, inject func()) {
	t.Helper()
	const name = "dfb:inject_after_scan"
	fired := false
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if fired || tx.Statement.Table != table {
			return
		}
		fired = true
		inject()
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(name) })
}

// TestDateFieldBackfill_ConcurrentlyArmedTimerSurvivesTheScan is the ordering test.
//
// The reconcile DELETEs the workflow's whole pending timer set and re-INSERTs only
// what its scan returned. If the scan is taken BEFORE the delete, everything armed in
// between is destroyed and never replaced: an inbound lead arrives during a reconcile,
// ingest arms its renewal timer, the delete wipes it, and that contact is never paged
// — permanently, because nothing rescans. ON CONFLICT DO NOTHING does not help; it
// only stops a duplicate key from aborting the batch.
//
// DELETE → SCAN → INSERT closes it: a record the scan does not return committed after
// the scan, hence after the delete, so its own live write arms a timer that the delete
// has already run past.
//
// The injected write is the real live path (materializeDateFieldTimers), not a
// hand-rolled INSERT, so this also pins that the two paths agree on the dedupe key —
// a disagreement would show up here as TWO timers for one record.
func TestDateFieldBackfill_ConcurrentlyArmedTimerSurvivesTheScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 30)
	existing := dfbDeal(t, db, orgID, &closeAt)

	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)

	// The record that arrives DURING the reconcile: created and armed by its own write,
	// after the scan has taken its snapshot, so the reconcile's INSERT list cannot
	// contain it.
	var late uuid.UUID
	dfbInjectAfterScan(t, db, "deals", func() {
		late = dfbDeal(t, db, orgID, &closeAt)
		require.NoError(t, engine.materializeDateFieldTimers(ctx, orgID, "deal_created",
			dealEventPayload(late.String(), closeAt.Format(time.RFC3339))))
	})

	stats, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, late,
		"the injection never ran — without it this test proves nothing about the window")

	// The reconcile itself only ever saw the one pre-existing deal.
	assert.Equal(t, 1, stats.Scanned)
	assert.Equal(t, 1, stats.Armed)

	got := dfbTimerRecordIDs(t, db, wf.ID)
	assert.Equal(t, dfbIDSet(existing, late), got,
		"the timer armed mid-reconcile must survive: it is the only one that record will ever get")
	assert.Len(t, pendingDateFieldTimers(t, db, wf.ID), 2,
		"one timer per record — the backfill and the live write must agree on the dedupe key")
}

// TestDateFieldBackfill_FailedScanLeavesTimersArmed: the cancel and the scan are in ONE
// transaction, so a scan that blows up rolls the cancel back. Without that, a bad
// object slug (or a dropped connection) would silently disarm a live workflow — worse
// than the missing backfill this whole file exists to add.
func TestDateFieldBackfill_FailedScanLeavesTimersArmed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 30)
	dealID := dfbDeal(t, db, orgID, &closeAt)
	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, true)

	_, err := engine.reconcileDateFieldTimers(ctx, wf)
	require.NoError(t, err)
	require.Equal(t, dfbIDSet(dealID), dfbTimerRecordIDs(t, db, wf.ID), "control: armed")

	// Repoint at a custom object that does not exist — the join finds no object_defs
	// row, and object_defs itself is absent from the fixture, so the scan errors.
	trig, err := json.Marshal(map[string]any{"type": TriggerDateField, "params": map[string]any{
		"object": "nonexistent_object", "field": "nonexistent_object.renews_on",
		"offset_days": -7, "at_time": "09:00", "timezone": "UTC",
	}})
	require.NoError(t, err)
	broken := *wf
	broken.Trigger = datatypes.JSON(trig)

	stats, err := engine.reconcileDateFieldTimers(ctx, &broken)
	require.Error(t, err, "a scan against a missing table must surface, not be swallowed")
	assert.Equal(t, dateFieldBackfillStats{}, stats,
		"a rolled-back reconcile did nothing, so it must report nothing")

	assert.Equal(t, dfbIDSet(dealID), dfbTimerRecordIDs(t, db, wf.ID),
		"the transaction rolled back, so the cancel must have rolled back with it")
}

// ── The user-visible path: POST /api/workflows/:id/toggle ─────────────────────

// dfbToggleRouter injects org/user/role context the way the auth middleware does, then
// registers only the toggle route.
func dfbToggleRouter(h *Handler, orgID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", uuid.New())
		c.Set("role", "admin")
		c.Next()
	})
	router.POST("/api/workflows/:id/toggle", h.ToggleWorkflow)
	return router
}

// dfbToggle issues POST /api/workflows/:id/toggle and asserts a 200.
func dfbToggle(t *testing.T, router *gin.Engine, wfID uuid.UUID) {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/workflows/"+wfID.String()+"/toggle", nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// dfbWaitForTimerRecordIDs polls until the workflow's pending date_field timer set
// equals want, then returns. Bounded, and it fails with the ids it actually saw.
//
// The reconcile ToggleWorkflow kicks off is asynchronous (`go` + context.Background(),
// because the response is written long before a 10k-record scan finishes), so a fixed
// sleep is either flaky or slow. Polling with a hard deadline is neither, and a timeout
// here is a real failure — never a skip.
func dfbWaitForTimerRecordIDs(t *testing.T, db *gorm.DB, wfID uuid.UUID, want map[string]bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var got map[string]bool
	for {
		got = dfbTimerRecordIDs(t, db, wfID)
		if assert.ObjectsAreEqual(want, got) {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.Equal(t, want, got, "timed out waiting for the async date_field reconcile after the toggle")
}

// TestDateFieldBackfill_ToggleHandlerArmsPreExistingRecords drives the ONE route that
// can switch a date_field workflow on.
//
// Every other test in this file calls reconcileDateFieldTimers directly, which asserts
// nothing about whether ToggleWorkflow actually calls it — and scheduleDateFieldReconcile
// opens with a nil-guard early return, so a handler wired without an engine would answer
// 200, report the workflow active, and arm nothing. There is no later symptom: the
// records are simply never paged. This is the test that fails when that wiring breaks.
func TestDateFieldBackfill_ToggleHandlerArmsPreExistingRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	dfbSeedDeals(t, db)
	engine := makeEngine(db, nil)
	defer engine.cancel()
	orgID := uuid.New()

	closeAt := time.Now().AddDate(0, 0, 30)
	d1 := dfbDeal(t, db, orgID, &closeAt)
	d2 := dfbDeal(t, db, orgID, &closeAt)
	otherOrg := dfbDeal(t, db, uuid.New(), &closeAt)

	// Created INACTIVE on purpose: the toggle is what activates it, which is exactly
	// the moment R4.4 exists for.
	wf := createDateFieldWF(t, engine.repo, orgID, "deal", "deal.expected_close_at", -3, false)

	// capAllow because NewHandler panics on a nil capChecker; the toggle route itself
	// does not consult it.
	handler := NewHandler(engine, db, handlerRunNowDiscardLogger(), capAllow{}, nil, nil, nil)
	router := dfbToggleRouter(handler, orgID)

	dfbToggle(t, router, wf.ID)

	dfbWaitForTimerRecordIDs(t, db, wf.ID, dfbIDSet(d1, d2))
	assert.NotContains(t, dfbTimerRecordIDs(t, db, wf.ID), otherOrg.String(),
		"another org's deal must never be armed by a toggle")

	reloaded, err := engine.repo.GetWorkflowByID(context.Background(), orgID, wf.ID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.True(t, reloaded.IsActive, "control: the toggle really did activate the workflow")

	// And toggling back OFF must disarm, through the same route — otherwise a
	// deactivated workflow keeps paging reps from timers armed while it was live.
	dfbToggle(t, router, wf.ID)
	dfbWaitForTimerRecordIDs(t, db, wf.ID, map[string]bool{})
}

// TestScheduleDateFieldReconcile_MisWiringIsLoggedNotSilent pins the nil-guard's log.
// The guard is correct — it must not panic — but returning in silence turned a wiring
// regression into an invisible one: 200 on the route, "active" in the UI, zero timers,
// no evidence anywhere. The log line is the evidence.
func TestScheduleDateFieldReconcile_MisWiringIsLoggedNotSilent(t *testing.T) {
	var buf bytes.Buffer
	h := &Handler{logger: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}

	h.scheduleDateFieldReconcile(&Workflow{ID: uuid.New(), IsActive: true})

	out := buf.String()
	assert.Contains(t, out, "date_field backfill NOT scheduled",
		"a handler with no engine must SAY it armed nothing")
	assert.Contains(t, out, `"has_engine":false`)
}

// TestDateFieldBackfill_NonDateFieldWorkflowIsUntouched: the reconcile is hooked
// unconditionally in ToggleWorkflow, so it runs for every workflow in the product. It
// must be a no-op for the ones it does not own — in particular it must not delete a
// schedule workflow's timers.
func TestDateFieldBackfill_NonDateFieldWorkflowIsUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	engine := makeEngine(db, nil)
	defer engine.cancel()
	ctx := context.Background()
	orgID := uuid.New()

	schedWF := createScheduleWF(t, engine.repo, orgID, "0 9 * * *", "UTC", true)
	require.NoError(t, engine.repo.ArmScheduleTimer(ctx, schedWF, time.Now()))
	armed, err := engine.repo.HasPendingTimer(ctx, schedWF.ID, TimerKindSchedule)
	require.NoError(t, err)
	require.True(t, armed, "control: the schedule timer is armed")

	stats, err := engine.reconcileDateFieldTimers(ctx, schedWF)
	require.NoError(t, err)
	assert.Equal(t, dateFieldBackfillStats{}, stats)

	armed, err = engine.repo.HasPendingTimer(ctx, schedWF.ID, TimerKindSchedule)
	require.NoError(t, err)
	assert.True(t, armed, "a schedule workflow's timer must survive the date_field reconcile")
}
