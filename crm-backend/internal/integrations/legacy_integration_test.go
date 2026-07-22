package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// LegacyCapture lends the legacy automation webhook the platform's ledger, owner
// routing and health signal WITHOUT taking over its write. These tests run against a
// real Postgres because every one of them is a claim about what the database ends up
// holding.

func newLegacyCapture(t *testing.T, db *gorm.DB) (*LegacyCapture, *Repository) {
	t.Helper()
	repo := NewRepository(db)
	ingest := NewLeadIngestService(repo, &recordingWriter{}, &stubMatcher{}, contactSchema(),
		noFieldDefs{}, stubMembers{}, nil, slog.Default())
	return NewLegacyCapture(repo, ingest, slog.Default()), repo
}

func legacySource(t *testing.T, repo *Repository, orgID uuid.UUID) *LeadSource {
	t.Helper()
	src, err := repo.FindSourceByKind(context.Background(), orgID, KindWebhookInbound)
	require.NoError(t, err)
	return src
}

// The row has to appear on its own: nobody creates it, and the Integrations page has
// to have something to configure.
func TestLegacyCapture_CreatesTheSourceOnFirstDelivery(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	require.Nil(t, legacySource(t, repo, orgID), "precondition: no source yet")

	id, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "a@example.com"})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, id)
	require.Nil(t, cap.ResolveOwner(context.Background(), orgID, id),
		"an unconfigured source routes to nobody, and says so by returning nil")

	src := legacySource(t, repo, orgID)
	require.NotNil(t, src)
	require.Equal(t, KindWebhookInbound, src.Kind)
	// The two settings that preserve the legacy contract rather than the platform's
	// defaults: legacy overwrote every field it was sent, and never had a daily cap.
	require.Equal(t, UpdatePolicyOverwrite, src.UpdatePolicy)
	require.EqualValues(t, 0, src.DailyCap, "a cap the legacy endpoint never had would start refusing live traffic")
}

// token_hash must stay NULL. FindSourceByTokenHash has no kind filter, so a key here
// would silently open a second capture-API ingress into the org — and an empty string
// would collide across the second such row in the fleet on the UNIQUE index.
func TestLegacyCapture_SourceHasNoBearerCredential(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)
	_, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "a@example.com"})
	require.NoError(t, err)

	src := legacySource(t, repo, orgID)
	var tokenHash *string
	require.NoError(t, db.Raw("SELECT token_hash FROM lead_sources WHERE id = ?", src.ID).Scan(&tokenHash).Error)
	require.Nil(t, tokenHash, "the legacy source must carry no bearer key at all, not even an empty one")
}

// Every delivery must reuse the one row, or the ledger and the owner rotation split
// across duplicates that look identical in the UI.
func TestLegacyCapture_ReusesTheSameSourceAcrossDeliveries(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, _ := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	for i := 0; i < 3; i++ {
		_, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "a@example.com"})
		require.NoError(t, err)
	}
	var sources int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM lead_sources WHERE org_id = ? AND kind = ?",
		orgID, KindWebhookInbound).Scan(&sources).Error)
	require.EqualValues(t, 1, sources)
}

// The delivery log's whole value here: the raw payload is stored BEFORE the write, so
// a lead whose write failed is still recoverable.
func TestLegacyCapture_LedgerHoldsTheRawPayloadAndTheOutcome(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, _ := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	payload := map[string]any{"email": "led@example.com", "budget": "50k"}
	id, err := cap.BeginDelivery(context.Background(), orgID, payload)
	require.NoError(t, err)

	var raw []byte
	var status string
	require.NoError(t, db.Raw("SELECT raw_payload, status FROM integration_events WHERE id = ?", id).
		Row().Scan(&raw, &status))
	require.Equal(t, EventStatusProcessing, status,
		"a synchronous delivery must never be `pending`, or the async claim loop would re-run a write that already happened")
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "50k", got["budget"], "the payload is stored verbatim, including keys the CRM has no field for")

	contactID := uuid.New()
	cap.FinishDelivery(context.Background(), orgID, id, contactID, true, nil)

	var outcome, resultSlug string
	var resultID uuid.UUID
	require.NoError(t, db.Raw("SELECT status, outcome, result_slug, result_record_id FROM integration_events WHERE id = ?", id).
		Row().Scan(&status, &outcome, &resultSlug, &resultID))
	require.Equal(t, EventStatusProcessed, status)
	require.Equal(t, OutcomeCreated, outcome)
	require.Equal(t, "contact", resultSlug)
	require.Equal(t, contactID, resultID, "the ledger must point at the record it produced")
}

// A failed delivery has to be visible as a failure, or the log shows a source that is
// losing every lead as healthy.
func TestLegacyCapture_FailedDeliveryIsRecordedAndCounted(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	id, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "bad@example.com"})
	require.NoError(t, err)
	cap.FinishDelivery(context.Background(), orgID, id, uuid.Nil, false, errors.New("insert exploded"))

	var status, errText string
	require.NoError(t, db.Raw("SELECT status, error FROM integration_events WHERE id = ?", id).
		Row().Scan(&status, &errText))
	require.Equal(t, EventStatusFailed, status)
	require.Contains(t, errText, "insert exploded")

	src := legacySource(t, repo, orgID)
	require.Equal(t, 1, src.ConsecutiveFailures, "a post-authentication failure must move the health counter")
}

// The counter must also come back down, or one bad afternoon leaves the badge red
// forever.
func TestLegacyCapture_SuccessClearsTheFailureCounter(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	id, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "x@example.com"})
	require.NoError(t, err)
	cap.FinishDelivery(context.Background(), orgID, id, uuid.Nil, false, errors.New("boom"))
	require.Equal(t, 1, legacySource(t, repo, orgID).ConsecutiveFailures)

	id2, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "x@example.com"})
	require.NoError(t, err)
	cap.FinishDelivery(context.Background(), orgID, id2, uuid.New(), false, nil)

	src := legacySource(t, repo, orgID)
	require.Equal(t, 0, src.ConsecutiveFailures, "a real success self-heals the badge")
	require.NotNil(t, src.LastUsedAt, "and stamps when the source last worked")
}

// Once an admin configures a rotation, the same ladder every other channel uses must
// hand out owners here too — that is the entire point of the slice.
func TestLegacyCapture_RoutesToAConfiguredRotation(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	_, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "seed@example.com"})
	require.NoError(t, err)
	src := legacySource(t, repo, orgID)

	rep := uuid.New()
	pool, _ := json.Marshal([]string{rep.String()})
	require.NoError(t, repo.SetOwnerPool(context.Background(), orgID, src.ID, datatypes.JSON(pool)))

	id, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "routed@example.com"})
	require.NoError(t, err)
	owner := cap.ResolveOwner(context.Background(), orgID, id)
	require.NotNil(t, owner, "a configured rotation must produce an owner")
	require.Equal(t, rep, *owner)

	// The routing decision must reach the ledger, not just the caller: this endpoint's
	// 200 body is frozen, so the delivery row is the ONLY place an admin can learn who
	// a lead was assigned to — or that it was assigned to nobody.
	var assigned uuid.UUID
	require.NoError(t, db.Raw("SELECT assigned_owner_id FROM integration_events WHERE id = ?", id).
		Row().Scan(&assigned))
	require.Equal(t, rep, assigned)
}

// The ticket is CONSUMING, so it must be taken only where ownership is actually
// stamped. Resolving it for a delivery that turns out to be an update burns a rep's
// turn on a lead that never gets an owner — with a steady one-new-in-three mix and a
// three-person rotation, every new lead then lands on the same rep while the UI shows
// a healthy three-way split. ingest.go calls this "this feature's worst bug"; review
// caught it here.
func TestLegacyCapture_OpeningADeliveryDoesNotBurnARotationTicket(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	_, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "seed@example.com"})
	require.NoError(t, err)
	src := legacySource(t, repo, orgID)
	pool, _ := json.Marshal([]string{uuid.NewString(), uuid.NewString(), uuid.NewString()})
	require.NoError(t, repo.SetOwnerPool(context.Background(), orgID, src.ID, datatypes.JSON(pool)))

	cursorNow := func() int64 {
		var c int64
		require.NoError(t, db.Raw("SELECT owner_cursor FROM lead_sources WHERE id = ?", src.ID).Row().Scan(&c))
		return c
	}
	before := cursorNow()

	// Five deliveries that never create a contact — the ordinary shape of legacy
	// traffic, where the same address submits again and again.
	for i := 0; i < 5; i++ {
		_, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "again@example.com"})
		require.NoError(t, err)
	}
	require.Equal(t, before, cursorNow(),
		"opening a delivery must not consume a rotation turn; only a create may")

	// And the create branch does take exactly one.
	id, err := cap.BeginDelivery(context.Background(), orgID, map[string]any{"email": "new@example.com"})
	require.NoError(t, err)
	require.NotNil(t, cap.ResolveOwner(context.Background(), orgID, id))
	require.Equal(t, before+1, cursorNow(), "a create takes exactly one turn")
}

// Closing a delivery that was never opened must be a no-op rather than a panic or a
// spurious health signal: BeginDelivery is allowed to degrade, and the caller reports
// what it got.
func TestLegacyCapture_FinishIgnoresADeliveryThatNeverOpened(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	cap, repo := newLegacyCapture(t, db)
	orgID := seedOrg(t, db)

	cap.FinishDelivery(context.Background(), orgID, uuid.Nil, uuid.New(), true, nil)
	require.Nil(t, legacySource(t, repo, orgID),
		"a delivery that never opened must not even bring the source into existence")
}
