package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The google_ads route end to end against real Postgres: URL resolution, key
// verification, the ledger rows each failure leaves, dedupe by lead_id, the test
// coercion, and the failure counter. The RecordService side stays faked — what is
// under test here is the route's contract with Google, not the write path L2
// already pins.

// seedGoogleSource creates a google_ads source the way CreateSource does: bearer
// key AND google credentials, seeded map, email+phone matching.
func seedGoogleSource(t *testing.T, db *gorm.DB) (repo *Repository, src *GoogleSource, googleKey string) {
	t.Helper()
	repo = NewRepository(db)
	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)

	seedJSON, _ := json.Marshal(googleSeedFieldMap())
	s := &LeadSource{
		OrgID: orgID, Kind: KindGoogleAds, Name: "Spring Google Form", TargetSlug: "contact",
		UpdatePolicy: UpdatePolicyFillBlankOnly,
		MatchFields:  datatypes.JSON(`["email","phone"]`),
		FieldMap:     datatypes.JSON(seedJSON),
		Config:       datatypes.JSON(`{}`),
		Status:       SourceStatusActive,
		TokenHash:    HashLeadKey("crm_lead_test_bearer"),
	}
	require.NoError(t, repo.CreateSource(context.Background(), s))

	pub, err := GeneratePublicToken()
	require.NoError(t, err)
	plaintext, hash, err := GenerateGoogleKey()
	require.NoError(t, err)
	require.NoError(t, repo.SetGoogleCredentials(context.Background(), orgID, s.ID, pub, hash))

	found, err := repo.FindSourceByPublicToken(context.Background(), pub)
	require.NoError(t, err)
	require.NotNil(t, found, "the URL token must resolve")
	require.Equal(t, s.ID, found.ID)
	require.Equal(t, pub, found.PublicToken)
	return repo, found, plaintext
}

// googleTestHandler wires the real repo + real ingest pipeline with a fake writer.
func googleTestHandler(t *testing.T, db *gorm.DB, repo *Repository, w RecordWriter, m ContactMatcher) *Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if m == nil {
		m = &stubMatcher{}
	}
	ingest := NewLeadIngestService(repo, w, m, contactSchema(), noFieldDefs{}, stubMembers{}, nil, logger)
	return NewHandler(repo, ingest, allowingAuthorizer{}, stubMembers{}, contactSchema(), nil,
		NewRateLimiter(nil, 10000, 0), NewRateLimiter(nil, 10000, 0), logger)
}

// allowingAuthorizer satisfies domain.RecordAuthorizer for routes that never hit
// the management path in these tests.
type allowingAuthorizer struct{}

func (allowingAuthorizer) Authorize(context.Context, uuid.UUID, string, domain.RecordAction) error {
	return nil
}
func (allowingAuthorizer) Audit(context.Context, domain.AuditEntry) {}
func (allowingAuthorizer) FieldMask(context.Context, uuid.UUID, string) domain.FieldMask {
	return domain.FieldMask{}
}

func postGoogle(t *testing.T, h *Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/capture/google-ads/:public_token", h.CaptureGoogleAds)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/capture/google-ads/"+token, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

func googleBody(key, leadID string, isTest bool, cols ...googleColumn) string {
	b, _ := json.Marshal(map[string]any{
		"lead_id":          leadID,
		"user_column_data": cols,
		"google_key":       key,
		"is_test":          isTest,
		"campaign_id":      543212345,
		"gcl_id":           "Cj0Kfixture",
	})
	return string(b)
}

var emailCol = googleColumn{ColumnID: "EMAIL", Value: "ada@example.com"}
var nameCol = googleColumn{ColumnID: "FULL_NAME", Value: "Ada Lovelace"}
var phoneCol = googleColumn{ColumnID: "PHONE_NUMBER", Value: "1-650-555-0123"}

// TestGoogleRoute_DoneWhen is the plan's Done-when, in order: a test post lands a
// `test` event + record; a fixture lead creates an attributed contact; replaying
// the same lead_id yields duplicate; a wrong google_key 401s and logs failed.
func TestGoogleRoute_DoneWhen(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src, key := seedGoogleSource(t, db)
	writer := &recordingWriter{}
	h := googleTestHandler(t, db, repo, writer, nil)
	ctx := context.Background()

	// 1. "Send test data": 200 {}, event badged test, contact written SYNTHETIC.
	rec := postGoogle(t, h, src.PublicToken, googleBody(key, "TeSter-123-ID", true, emailCol, nameCol, phoneCol))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{}`, rec.Body.String(), "Google's documented success body is {}")
	require.Equal(t, 1, writer.creates)

	events, err := repo.ListEvents(ctx, src.OrgID, &src.ID, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, EventStatusTest, events[0].Status, "the advertiser must find their test in the log")
	require.Contains(t, string(events[0].RawPayload), "ada@example.com",
		"the raw payload keeps Google's data for diagnosis")

	// A second test click converges rather than erroring or accumulating: same
	// synthetic identity, so it must not create a second record. (The stub matcher
	// returns no match, so convergence here shows as a second honest create being
	// prevented by the pipeline reaching the update path is covered in unit tests;
	// what THIS asserts is that click 2 is a 200, not the unique-collision 500 the
	// uncoerced design produced.)
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "TeSter-456-ID", true, emailCol, nameCol))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	// 2. A production fixture lead creates an attributed contact.
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "prod-lead-1", false, emailCol, nameCol))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	events, err = repo.ListEvents(ctx, src.OrgID, &src.ID, 10)
	require.NoError(t, err)
	require.Equal(t, EventStatusProcessed, events[0].Status)
	require.Equal(t, OutcomeCreated, events[0].Outcome)
	require.Contains(t, string(events[0].Context), "Cj0Kfixture", "gcl_id must ride the delivery context")

	// 3. Replaying the same lead_id answers 200 without re-running.
	before := writer.creates
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "prod-lead-1", false, emailCol, nameCol))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, before, writer.creates, "a replay must not write a second record")

	// 4. Wrong google_key: 401, {"message":...}, and a failed row for recovery.
	rec = postGoogle(t, h, src.PublicToken, googleBody("crm_gads_wrong", "prod-lead-2", false, emailCol))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), `"message"`)

	events, err = repo.ListEvents(ctx, src.OrgID, &src.ID, 10)
	require.NoError(t, err)
	require.Equal(t, EventStatusFailed, events[0].Status, "a key mismatch must be ledgered, not just refused")
	require.Contains(t, events[0].Error, "webhook key", "the row must explain itself")
	require.NotContains(t, string(events[0].RawPayload), "crm_gads_wrong",
		"the received key must never be stored")
	// The failure counter must NOT move on a pre-auth failure — it is forgeable.
	var failures int
	require.NoError(t, db.Raw(`SELECT consecutive_failures FROM lead_sources WHERE id = ?`, src.ID).Scan(&failures).Error)
	require.Zero(t, failures, "a bad key is attacker-forgeable and must not flip a healthy source")
}

// TestGoogleRoute_CapQuarantine pins the accept-and-quarantine contract: 200 to
// Google, nothing written, a replayable row with the recovery text.
func TestGoogleRoute_CapQuarantine(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src, key := seedGoogleSource(t, db)
	writer := &recordingWriter{}
	h := googleTestHandler(t, db, repo, writer, nil)
	ctx := context.Background()

	// Exhaust the cap: cap=1, then one real lead.
	require.NoError(t, db.Exec(`UPDATE lead_sources SET daily_cap = 1 WHERE id = ?`, src.ID).Error)
	rec := postGoogle(t, h, src.PublicToken, googleBody(key, "lead-1", false, emailCol))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, writer.creates)

	// The next lead: 200 — Google must not drop it — but NOT written.
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "lead-2", false, emailCol))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, writer.creates, "a capped lead must not write")

	events, err := repo.ListEvents(ctx, src.OrgID, &src.ID, 10)
	require.NoError(t, err)
	require.Equal(t, EventStatusQuarantined, events[0].Status)
	require.Contains(t, events[0].Error, "batch endpoint", "the row must document its own recovery")

	// The quarantined row is REPLAYABLE: the same lead_id through Ingest (as the
	// batch endpoint would send it) re-runs in place and writes for real.
	res, err := h.ingest.Ingest(ctx, &src.LeadSource, RawLead{
		Fields:          map[string]any{"EMAIL": "ada@example.com"},
		ProviderEventID: "lead-2",
	})
	require.NoError(t, err)
	require.Equal(t, OutcomeCreated, res.Outcome)
	require.Equal(t, 2, writer.creates, "the replay must write the quarantined lead")

	// A TEST lead bypasses the cap rather than quarantining: a quarantined test
	// replayed later would run as a REAL lead (TestOrigin is not persisted) and
	// write a deal against the synthetic contact into the forecast.
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "test-past-cap", true, emailCol))
	require.Equal(t, http.StatusOK, rec.Code)
	events, err = repo.ListEvents(ctx, src.OrgID, &src.ID, 10)
	require.NoError(t, err)
	require.Equal(t, EventStatusTest, events[0].Status, "a test past the cap must still run as a test")
}

// TestGoogleRoute_UnknownAndRevoked pins the 401 family: unknown token, disabled
// source — and that `error` status deliberately KEEPS accepting.
func TestGoogleRoute_UnknownAndRevoked(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src, key := seedGoogleSource(t, db)
	writer := &recordingWriter{}
	h := googleTestHandler(t, db, repo, writer, nil)
	ctx := context.Background()

	rec := postGoogle(t, h, "not-a-token", googleBody(key, "x", false, emailCol))
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	require.NoError(t, repo.SetSourceStatus(ctx, src.OrgID, src.ID, SourceStatusDisabled))
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "y", false, emailCol))
	require.Equal(t, http.StatusUnauthorized, rec.Code, "disabled is an admin's explicit revocation")

	// `error` is a badge, not a gate: refusing while flagged would drop leads
	// unledgered during the exact window someone is fixing the source.
	require.NoError(t, repo.SetSourceStatus(ctx, src.OrgID, src.ID, SourceStatusActive))
	require.NoError(t, db.Exec(`UPDATE lead_sources SET status = 'error' WHERE id = ?`, src.ID).Error)
	rec = postGoogle(t, h, src.PublicToken, googleBody(key, "z", false, emailCol))
	require.Equal(t, http.StatusOK, rec.Code, "an error-flagged source must keep accepting")

	// And the success just now must have healed the flag.
	var status string
	require.NoError(t, db.Raw(`SELECT status FROM lead_sources WHERE id = ?`, src.ID).Scan(&status).Error)
	require.Equal(t, SourceStatusActive, status, "a real success self-heals the error badge")
}

// TestIncrementSourceFailure_FlipsOnceAtThreshold pins the counter's atomicity
// contract: the flip happens exactly at the threshold, only from active.
func TestIncrementSourceFailure_FlipsOnceAtThreshold(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src, _ := seedGoogleSource(t, db)
	ctx := context.Background()

	flippedCount := 0
	for i := 0; i < errorFlipThreshold+3; i++ {
		flipped, err := repo.IncrementSourceFailure(ctx, src.ID)
		require.NoError(t, err)
		if flipped {
			flippedCount++
		}
	}
	require.Equal(t, 1, flippedCount, "the flip must report exactly once")

	var status string
	require.NoError(t, db.Raw(`SELECT status FROM lead_sources WHERE id = ?`, src.ID).Scan(&status).Error)
	require.Equal(t, SourceStatusError, status)

	// The flip must never resurrect a disabled source.
	require.NoError(t, repo.SetSourceStatus(ctx, src.OrgID, src.ID, SourceStatusDisabled))
	for i := 0; i < errorFlipThreshold+1; i++ {
		_, err := repo.IncrementSourceFailure(ctx, src.ID)
		require.NoError(t, err)
	}
	require.NoError(t, db.Raw(`SELECT status FROM lead_sources WHERE id = ?`, src.ID).Scan(&status).Error)
	require.Equal(t, SourceStatusDisabled, status, "failures must not resurrect an admin's disable")
}

// TestUpdateSource_DoesNotClobberMachineColumns pins the Save omission: an admin
// edit riding a stale struct must not un-flip an error badge or wipe the counter.
func TestUpdateSource_DoesNotClobberMachineColumns(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src, _ := seedGoogleSource(t, db)
	ctx := context.Background()

	// The machine writes while the admin's page is open...
	for i := 0; i < errorFlipThreshold; i++ {
		_, err := repo.IncrementSourceFailure(ctx, src.ID)
		require.NoError(t, err)
	}
	// ...and the admin renames from a struct read before any of it.
	stale := src.LeadSource
	stale.Status = SourceStatusActive
	stale.ConsecutiveFailures = 0
	stale.Name = "renamed mid-storm"
	require.NoError(t, repo.UpdateSource(ctx, &stale))

	var status string
	var failures int
	require.NoError(t, db.Raw(`SELECT status FROM lead_sources WHERE id = ?`, src.ID).Scan(&status).Error)
	require.NoError(t, db.Raw(`SELECT consecutive_failures FROM lead_sources WHERE id = ?`, src.ID).Scan(&failures).Error)
	require.Equal(t, SourceStatusError, status, "a rename must not un-flip the badge")
	require.Equal(t, errorFlipThreshold, failures, "a rename must not wipe the counter")

	var name string
	require.NoError(t, db.Raw(`SELECT name FROM lead_sources WHERE id = ?`, src.ID).Scan(&name).Error)
	require.Equal(t, "renamed mid-storm", name, "the rename itself must still land")
}

// TestFindSourceByPublicToken_Revocation mirrors the bearer lookup's contract.
func TestFindSourceByPublicToken_Revocation(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src, _ := seedGoogleSource(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SoftDeleteSource(ctx, src.OrgID, src.ID))
	found, err := repo.FindSourceByPublicToken(ctx, src.PublicToken)
	require.NoError(t, err)
	require.Nil(t, found, "retiring a source must kill its URL immediately")
}

// The google_key prefix must never satisfy the bearer gate — the two credential
// classes must not meet.
func TestGoogleKeyIsNotALeadKey(t *testing.T) {
	key, _, err := GenerateGoogleKey()
	require.NoError(t, err)
	require.False(t, IsLeadKey(key), "a google_key pasted into a Bearer header must fail, not half-work")
	require.True(t, strings.HasPrefix(key, GoogleKeyPrefix))
}
