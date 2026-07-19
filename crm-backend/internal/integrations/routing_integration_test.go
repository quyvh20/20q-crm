package integrations

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The cursor's whole reason to exist is correctness under concurrency, which is the
// one property a unit test cannot show. These run against real Postgres.

func seedPooledSource(t *testing.T, db *gorm.DB, pool []uuid.UUID) (*Repository, uuid.UUID, uuid.UUID) {
	t.Helper()
	repo := NewRepository(db)
	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)

	src := &LeadSource{
		OrgID: orgID, Kind: KindAPI, Name: "rotation", TargetSlug: "contact",
		UpdatePolicy: UpdatePolicyFillBlankOnly,
		MatchFields:  datatypes.JSON(`["email"]`),
		FieldMap:     datatypes.JSON(`{}`),
		Config:       datatypes.JSON(`{}`),
		Status:       SourceStatusActive,
	}
	require.NoError(t, repo.CreateSource(context.Background(), src))

	if pool != nil {
		raw := "["
		for i, id := range pool {
			if i > 0 {
				raw += ","
			}
			raw += `"` + id.String() + `"`
		}
		raw += "]"
		require.NoError(t, repo.SetOwnerPool(context.Background(), orgID, src.ID, datatypes.JSON(raw)))
	}
	return repo, orgID, src.ID
}

// TestNextOwnerTicket_ConcurrentLeadsNeverCollide is the property the atomic
// UPDATE...RETURNING exists for. A read-modify-write in Go would hand several
// simultaneous leads the same ticket, and therefore the same rep — silently.
func TestNextOwnerTicket_ConcurrentLeadsNeverCollide(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, orgID, srcID := seedPooledSource(t, db, []uuid.UUID{uuid.New(), uuid.New(), uuid.New()})

	const n = 40
	tickets := make([]int64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tk, _, ok, err := repo.NextOwnerTicket(context.Background(), orgID, srcID)
			if err == nil && ok {
				tickets[i] = tk
			} else {
				tickets[i] = -1
			}
		}(i)
	}
	wg.Wait()

	seen := map[int64]bool{}
	for _, tk := range tickets {
		require.GreaterOrEqual(t, tk, int64(0), "every concurrent lead must get a ticket")
		assert.False(t, seen[tk], "ticket %d was handed out twice — two leads would share one rep", tk)
		seen[tk] = true
	}
	assert.Len(t, seen, n, "expected %d distinct tickets", n)
}

// TestNextOwnerTicket_PoolLessSourceIsUntouched pins the enable-by-cost property:
// the ~100% of sources that never configure a rotation must not pay a row write on
// every captured lead.
func TestNextOwnerTicket_PoolLessSourceIsUntouched(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, orgID, srcID := seedPooledSource(t, db, nil) // owner_pool defaults to []

	_, _, ok, err := repo.NextOwnerTicket(context.Background(), orgID, srcID)
	require.NoError(t, err)
	assert.False(t, ok, "a source with no rotation must report 'not pooled'")

	var cursor int64
	require.NoError(t, db.Raw(`SELECT owner_cursor FROM lead_sources WHERE id = ?`, srcID).Scan(&cursor).Error)
	assert.Zero(t, cursor, "a pool-less source must not have been written to at all")
}

// TestPeekOwnerTicket_DoesNotConsumeATurn: a test lead reports who the next real
// lead would go to without spending that rep's turn on a contact the admin is told
// to delete.
func TestPeekOwnerTicket_DoesNotConsumeATurn(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, orgID, srcID := seedPooledSource(t, db, []uuid.UUID{uuid.New(), uuid.New()})
	ctx := context.Background()

	first, _, ok, err := repo.PeekOwnerTicket(ctx, orgID, srcID)
	require.NoError(t, err)
	require.True(t, ok)
	second, _, _, err := repo.PeekOwnerTicket(ctx, orgID, srcID)
	require.NoError(t, err)

	assert.Equal(t, first, second, "peeking twice must return the same turn")

	taken, _, _, err := repo.NextOwnerTicket(ctx, orgID, srcID)
	require.NoError(t, err)
	assert.Equal(t, first, taken, "the peeked turn must be the one the next real lead actually takes")
}

// TestUpdateSource_DoesNotRewindTheCursor is the regression test for the decision to
// keep owner_cursor OFF the GORM model.
//
// UpdateSource is db.Save, which writes every mapped column from a struct read at
// the start of the request. If the cursor were a field, an admin renaming a source
// would write back the value as it stood at page load — rewinding the rotation by
// however many leads landed in between, silently.
func TestUpdateSource_DoesNotRewindTheCursor(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, orgID, srcID := seedPooledSource(t, db, []uuid.UUID{uuid.New(), uuid.New()})
	ctx := context.Background()

	src, err := repo.GetSource(ctx, orgID, srcID) // the admin opens the page
	require.NoError(t, err)

	_, _, _, err = repo.NextOwnerTicket(ctx, orgID, srcID) // a lead lands meanwhile
	require.NoError(t, err)

	src.Name = "renamed"
	require.NoError(t, repo.UpdateSource(ctx, src)) // the admin saves

	next, _, _, err := repo.NextOwnerTicket(ctx, orgID, srcID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), next, "an unrelated edit must not rewind the rotation")
}

// TestRemoveFromOwnerPools prunes a departing member while leaving the admin's
// ordering of everyone else intact.
func TestRemoveFromOwnerPools(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	a, leaver, c := uuid.New(), uuid.New(), uuid.New()
	repo, orgID, srcID := seedPooledSource(t, db, []uuid.UUID{a, leaver, c})
	ctx := context.Background()

	require.NoError(t, repo.RemoveFromOwnerPools(ctx, orgID, leaver))

	raw, err := repo.GetOwnerPool(ctx, orgID, srcID)
	require.NoError(t, err)
	got := parsePoolUUIDs(raw)

	require.Len(t, got, 2, "the departing member must be gone")
	assert.Equal(t, []uuid.UUID{a, c}, got, "the remaining order must survive the prune")
}

// TestSourcesRoutingTo names what an offboarding admin needs to see: reassigning a
// leaver's existing records says nothing about the leads still arriving for them.
func TestSourcesRoutingTo(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	rep := uuid.New()
	repo, orgID, _ := seedPooledSource(t, db, []uuid.UUID{rep})
	ctx := context.Background()

	names, err := repo.SourcesRoutingTo(ctx, orgID, rep)
	require.NoError(t, err)
	assert.Equal(t, []string{"rotation"}, names)

	none, err := repo.SourcesRoutingTo(ctx, orgID, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none, "someone in no rotation must produce no warning")
}

// TestEventAssignedOwner_SurvivesForARetry: the ticket belongs to the DELIVERY, not
// the attempt. Without this an Idempotency-Key retry takes a second ticket, and a
// form where every other lead fails hands one rep everything while the ledger looks
// green.
func TestEventAssignedOwner_SurvivesForARetry(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, orgID, srcID := seedPooledSource(t, db, []uuid.UUID{uuid.New()})
	ctx := context.Background()

	ev := &IntegrationEvent{OrgID: orgID, SourceID: &srcID, Status: EventStatusProcessing, Attempts: 1}
	_, err := repo.InsertEventDeduped(ctx, ev)
	require.NoError(t, err)

	owner := uuid.New()
	require.NoError(t, repo.SetEventAssignedOwner(ctx, ev.ID, &owner))

	got, err := repo.GetEventAssignedOwner(ctx, ev.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "a retry must be able to reuse the first attempt's rep")
	assert.Equal(t, owner, *got)
}

// ── Consent (DB-backed) ──────────────────────────────────────────────────────

// TestConsent_StoredAndErasable is the invariant the storage ordering exists for.
//
// Erasure is contact-keyed, so an envelope must only ever exist on a delivery that
// produced a record — otherwise no erasure request could reach it. And erasure must
// leave a TOMBSTONE rather than NULL: a blanked row would make the ledger assert that
// no consent was ever obtained, which is a different and false claim.
func TestConsent_StoredAndErasable(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	recordID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO contacts (id, org_id, email) VALUES (?, ?, ?)`,
		recordID, orgID, "subject@customer-example.com").Error)

	ev := &IntegrationEvent{OrgID: orgID, SourceID: &src.ID, Status: EventStatusProcessing, Attempts: 1}
	_, err := repo.InsertEventDeduped(ctx, ev)
	require.NoError(t, err)
	ev.ResultRecordID = &recordID
	ev.Status = EventStatusProcessed
	require.NoError(t, repo.FinishEvent(ctx, ev))

	rec := parseConsent(map[string]any{
		"basis": "consent", "text": "I agree to be contacted", "captured_at": "2026-07-19T10:04:00Z",
	})
	n, err := repo.SetEventConsent(ctx, ev.ID, rec.Envelope)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "the envelope must land on exactly one delivery")

	stored, err := repo.ConsentForEvents(ctx, []uuid.UUID{ev.ID})
	require.NoError(t, err)
	require.Contains(t, string(stored[ev.ID]), "I agree to be contacted", "stored verbatim")

	// FinishEvent runs AFTER the consent write in the real pipeline; prove a later
	// Save cannot blank it (this is why the column is unmapped).
	require.NoError(t, repo.FinishEvent(ctx, ev))
	after, err := repo.ConsentForEvents(ctx, []uuid.UUID{ev.ID})
	require.NoError(t, err)
	require.Contains(t, string(after[ev.ID]), "I agree", "a later FinishEvent must not erase the envelope")

	// Erasure: the subject asks to be forgotten.
	require.NoError(t, repo.RedactForRecord(ctx, orgID, recordID))

	var raw, ctxCol, consent string
	require.NoError(t, db.Raw(`SELECT raw_payload::text, context::text, COALESCE(consent::text,'NULL') FROM integration_events WHERE id = ?`, ev.ID).
		Row().Scan(&raw, &ctxCol, &consent))

	assert.Equal(t, "{}", raw, "the payload the subject supplied must be gone")
	assert.Equal(t, "{}", ctxCol, "the capture context must be gone")
	assert.NotContains(t, consent, "I agree", "the consent wording must be gone")
	assert.Contains(t, consent, "redacted", "a tombstone, not NULL — the ledger must not claim consent was never obtained")

	// The delivery itself survives: it is how a customer answers "what happened to
	// this lead" long after the person is gone.
	var status string
	require.NoError(t, db.Raw(`SELECT status FROM integration_events WHERE id = ?`, ev.ID).Row().Scan(&status))
	assert.Equal(t, EventStatusProcessed, status)
}

// TestConsent_NeverStoredWithoutARecord: an envelope on a row with no
// result_record_id is PII that contact-keyed erasure can never reach.
func TestConsent_NeverStoredWithoutARecord(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	src := seedSource(t, repo, orgID)

	// A delivery that failed before producing a record — the shape failEvent leaves.
	ev := &IntegrationEvent{OrgID: orgID, SourceID: &src.ID, Status: EventStatusFailed, Attempts: 1}
	_, err := repo.InsertEventDeduped(ctx, ev)
	require.NoError(t, err)

	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM integration_events
		WHERE consent IS NOT NULL AND result_record_id IS NULL`).Scan(&n).Error)
	assert.Zero(t, n, "consent on a record-less delivery is unerasable PII")
}
