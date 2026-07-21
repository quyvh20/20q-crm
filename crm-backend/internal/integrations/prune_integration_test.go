package integrations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The retention sweep is the only thing in the system that erases customer data on a
// timer, and it is a bulk destructive rewrite of a hot table. Every clause of its
// predicate is asserted against real Postgres, in both directions — what it erases
// AND what it must leave alone, since a sweep that is too greedy destroys evidence
// and one that is too shy leaves personal data forever.

func seedContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, deleted bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	var del any
	if deleted {
		del = time.Now()
	}
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, email, deleted_at) VALUES (?, ?, ?, ?)`,
		id, orgID, "subject@example.com", del).Error)
	return id
}

type pruneRow struct {
	Raw        string
	Ctx        string
	Quarantine string
	Consent    *string
	RedactedAt *time.Time
}

func readPruneRow(t *testing.T, db *gorm.DB, id uuid.UUID) pruneRow {
	t.Helper()
	var r pruneRow
	require.NoError(t, db.Raw(`
		SELECT raw_payload::text AS raw, context::text AS ctx,
		       quarantined_fields::text AS quarantine, consent::text AS consent,
		       redacted_at
		  FROM integration_events WHERE id = ?`, id).Scan(&r).Error)
	return r
}

// seedLedgerRow writes a delivery carrying the full set of things a subject supplies.
func seedLedgerRow(t *testing.T, db *gorm.DB, orgID uuid.UUID, age time.Duration, mut func(*IntegrationEvent)) uuid.UUID {
	t.Helper()
	at := time.Now().Add(-age)
	e := &IntegrationEvent{
		ID:                uuid.New(),
		OrgID:             orgID,
		Status:            EventStatusQuarantined,
		ResultSlug:        "contact",
		RawPayload:        []byte(`{"email":"subject@example.com","phone":"+15551234567"}`),
		Context:           []byte(`{"form_id":"F1","page_id":"P1","gclid":"CJ-abc","utm_source":"ads","referrer_url":"https://x.test"}`),
		QuarantinedFields: []byte(`{"message":"I need help with a private matter"}`),
		CreatedAt:         at,
		ProcessedAt:       &at,
	}
	mut(e)
	require.NoError(t, db.Create(e).Error)
	return e.ID
}

const oldEnough = 120 * 24 * time.Hour

func TestPrune_ErasesAnExpiredOrphanCompletely(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	id := seedLedgerRow(t, db, orgID, oldEnough, func(*IntegrationEvent) {})

	n, err := repo.PruneExpiredPayloads(context.Background(), time.Now().Add(-ledgerRetention), 100)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	got := readPruneRow(t, db, id)
	require.Equal(t, "{}", got.Raw)
	require.Equal(t, "{}", got.Quarantine,
		"quarantined_fields holds the free text a visitor typed — nothing redacted it before this")
	require.NotNil(t, got.RedactedAt, "a redacted row must be distinguishable from one that stored nothing")

	// Context is PROJECTED, not blanked: the identifying keys go, the provider routing
	// ids stay so the retry path can still resolve which form the lead belonged to.
	require.Contains(t, got.Ctx, "form_id")
	require.Contains(t, got.Ctx, "page_id")
	require.NotContains(t, got.Ctx, "gclid")
	require.NotContains(t, got.Ctx, "utm_source")
	require.NotContains(t, got.Ctx, "referrer_url")
}

// THE RETRY GUARD. Blanking context would make every retry answer "this lead's form
// is not enabled" forever — on a form the admin had just enabled — destroying the one
// recovery path the retry button exists to provide.
func TestPrune_KeepsTheRoutingKeyTheRetryPathReads(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	conn := uuid.New()
	id := seedLedgerRow(t, db, orgID, oldEnough, func(e *IntegrationEvent) { e.ConnectionID = &conn })
	_, err := repo.PruneExpiredPayloads(context.Background(), time.Now().Add(-ledgerRetention), 100)
	require.NoError(t, err)

	var ev IntegrationEvent
	require.NoError(t, db.First(&ev, "id = ?", id).Error)
	require.Equal(t, "F1", stringOf(readContext(ev.Context)["form_id"]),
		"the retry pre-flight resolves the form from here; losing it makes recovery permanently impossible")
	require.Equal(t, RetryModeRefetch, classifyRetry(&ev).Mode,
		"a pruned orphan must still be retryable — the provider still has the lead")
}

func TestPrune_LeavesRecentAndInFlightRowsAlone(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	recent := seedLedgerRow(t, db, orgID, 2*24*time.Hour, func(*IntegrationEvent) {})
	// Non-terminal: its payload is the input a worker is about to use, and
	// FinishEvent's wholesale Save could write it straight back over a redaction.
	pending := seedLedgerRow(t, db, orgID, oldEnough, func(e *IntegrationEvent) {
		e.Status = EventStatusPending
		e.ProcessedAt = nil
	})
	// A terminal-success row with no record is the "incomplete" class the delivery log
	// tells an admin to open the payload of and re-send. Emptying it makes our own
	// instruction a dead end.
	incomplete := seedLedgerRow(t, db, orgID, oldEnough, func(e *IntegrationEvent) {
		e.Status = EventStatusProcessed
	})

	_, err := repo.PruneExpiredPayloads(context.Background(), time.Now().Add(-ledgerRetention), 100)
	require.NoError(t, err)

	for name, id := range map[string]uuid.UUID{"recent": recent, "pending": pending, "incomplete": incomplete} {
		got := readPruneRow(t, db, id)
		require.Contains(t, got.Raw, "subject@example.com", "%s must be untouched", name)
		require.Nil(t, got.RedactedAt, "%s must not be marked redacted", name)
	}
}

// ARM 2. Contact deletion redacts best-effort and only LOGS on failure, and a second
// delete cannot re-trigger it — so a missed redaction is permanent. Scoping the sweep
// to orphans alone would have excluded exactly the rows where erasure was promised and
// did not happen.
func TestPrune_RepairsAnErasureThatNeverRan(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	gone := seedContact(t, db, orgID, true)   // contact deleted, redaction never ran
	alive := seedContact(t, db, orgID, false) // still a customer

	// Recent on purpose: this arm is a repair, not retention, so age must not gate it.
	orphaned := seedLedgerRow(t, db, orgID, time.Hour, func(e *IntegrationEvent) {
		e.Status = EventStatusProcessed
		e.ResultRecordID = &gone
	})
	live := seedLedgerRow(t, db, orgID, time.Hour, func(e *IntegrationEvent) {
		e.Status = EventStatusProcessed
		e.ResultRecordID = &alive
	})

	n, err := repo.PruneExpiredPayloads(context.Background(), time.Now().Add(-ledgerRetention), 100)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	got := readPruneRow(t, db, orphaned)
	require.Equal(t, "{}", got.Raw, "the subject's contact is gone; their payload must go with it")
	require.NotNil(t, got.RedactedAt)

	kept := readPruneRow(t, db, live)
	require.Contains(t, kept.Raw, "subject@example.com",
		"a live customer's delivery must never be swept — it is erasable on request and may hold consent evidence")
	require.Nil(t, kept.RedactedAt)
}

// The consent invariant is DEFENDED rather than assumed. An orphan "cannot" carry an
// envelope — consent is written only after a record exists — but FinishEvent's
// wholesale Save can null a committed result_record_id while consent, being unmapped,
// survives. When the invariant holds this writes nothing; when it breaks the residue
// is erased and the tombstone says an envelope existed and was erased.
func TestPrune_TombstonesConsentResidueInsteadOfTrustingTheInvariant(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	withConsent := seedLedgerRow(t, db, orgID, oldEnough, func(*IntegrationEvent) {})
	_, err := repo.SetEventConsent(ctx, withConsent, []byte(`{"basis":"consent","text":"I agree"}`))
	require.NoError(t, err)
	plain := seedLedgerRow(t, db, orgID, oldEnough, func(*IntegrationEvent) {})

	_, err = repo.PruneExpiredPayloads(ctx, time.Now().Add(-ledgerRetention), 100)
	require.NoError(t, err)

	got := readPruneRow(t, db, withConsent)
	require.NotNil(t, got.Consent)
	require.Contains(t, *got.Consent, `"redacted": true`)

	// NULL must stay NULL: a tombstone on a row that never had an envelope would
	// assert an erasure that never happened.
	require.Nil(t, readPruneRow(t, db, plain).Consent)
}

// The batching loop terminates. The predicate keys on redacted_at rather than on
// payload shape precisely because the sweep RETAINS some context keys — a
// shape-based guard would re-match those rows forever and a LIMIT batch would always
// come back full.
func TestPrune_IsIdempotentAndTerminates(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	for i := 0; i < 5; i++ {
		seedLedgerRow(t, db, orgID, oldEnough, func(*IntegrationEvent) {})
	}
	cutoff := time.Now().Add(-ledgerRetention)

	first, err := repo.PruneExpiredPayloads(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Equal(t, int64(5), first)

	second, err := repo.PruneExpiredPayloads(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Equal(t, int64(0), second, "a second sweep must find nothing, or the loop never ends")
}

func TestPrune_LockIsExclusiveAndReleased(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	inner := make(chan bool, 1)
	got, err := repo.WithLedgerPruneLock(ctx, func() error {
		// A second attempt WHILE the first holds it must decline rather than sweep in
		// parallel — several replicas rewriting the same backlog multiplies the write
		// load on a hot table to no purpose.
		second, serr := repo.WithLedgerPruneLock(ctx, func() error { return nil })
		require.NoError(t, serr)
		inner <- second
		return nil
	})
	require.NoError(t, err)
	require.True(t, got)
	require.False(t, <-inner, "the lock must be exclusive")

	// And released, or no replica ever sweeps again.
	again, err := repo.WithLedgerPruneLock(ctx, func() error { return nil })
	require.NoError(t, err)
	require.True(t, again, "the lock must be released when the sweep finishes")
}

// The shipped contact-keyed redactors gained quarantined_fields in this slice — it
// held subject-supplied VALUES and nothing erased it, while the UI already told the
// admin "what the person supplied is gone".
func TestRedactForRecord_AlsoErasesQuarantinedFields(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	rec := seedContact(t, db, orgID, false)
	id := seedLedgerRow(t, db, orgID, time.Hour, func(e *IntegrationEvent) {
		e.Status = EventStatusProcessed
		e.ResultRecordID = &rec
	})

	require.NoError(t, repo.RedactForRecord(context.Background(), orgID, rec))
	got := readPruneRow(t, db, id)
	require.Equal(t, "{}", got.Quarantine,
		"the free text a visitor typed into an unmapped field is theirs too")
	require.NotNil(t, got.RedactedAt)
}
