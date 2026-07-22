package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The inbound legacy webhook (POST /api/webhooks/inbound/:org_token) has always
// answered 200 whether or not it wrote anything, because every error from its raw-SQL
// upsert was discarded. These tests pin the three ways that lost real data, against a
// real Postgres — none of them is expressible without one, since all three are about
// what the DATABASE does (a unique-violation race, a jsonb merge, a failed statement).
//
// Every test here drives the shipped HTTP handler rather than the helper, so what is
// pinned is the endpoint's observable behaviour and not an internal signature.

// postLead sends an inbound delivery and returns the recorder. Signature verification
// is skipped the way the rest of the package's webhook tests skip it; these tests are
// about the write, and the signature path has its own coverage.
func postLead(t *testing.T, router *gin.Engine, token string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/inbound/"+token, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// seedOrgToken provisions the org's inbound token directly, which is what the setup
// endpoint would have done.
func seedOrgToken(t *testing.T, db *gorm.DB, orgID uuid.UUID) string {
	t.Helper()
	tok := GenerateToken(32)
	require.NoError(t, db.Create(&WorkflowOrgToken{
		OrgID: orgID, Token: tok, Secret: GenerateToken(64), CreatedAt: time.Now(),
	}).Error)
	return tok
}

func contactRow(t *testing.T, db *gorm.DB, orgID uuid.UUID, email string) (uuid.UUID, map[string]any, time.Time) {
	t.Helper()
	var row struct {
		ID           uuid.UUID `gorm:"column:id"`
		CustomFields []byte    `gorm:"column:custom_fields"`
		UpdatedAt    time.Time `gorm:"column:updated_at"`
	}
	require.NoError(t, db.Raw(
		"SELECT id, custom_fields, updated_at FROM contacts WHERE org_id = ? AND email = ? AND deleted_at IS NULL",
		orgID, email).Scan(&row).Error)
	cf := map[string]any{}
	if len(row.CustomFields) > 0 {
		require.NoError(t, json.Unmarshal(row.CustomFields, &cf))
	}
	return row.ID, cf, row.UpdatedAt
}

// A delivery carrying one custom field used to REPLACE the whole custom_fields blob
// with just that key, destroying every other custom field on the contact — including
// values a human had typed into the UI. The blast radius was the customer's own data,
// and the endpoint reported success.
func TestWebhookInbound_MergesCustomFieldsRatherThanReplacingThem(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	w := postLead(t, router, tok, map[string]any{"email": "merge@example.com", "utm_source": "google", "tier": "gold"})
	require.Equal(t, http.StatusOK, w.Code)

	// A second delivery carrying a DIFFERENT unknown key.
	w = postLead(t, router, tok, map[string]any{"email": "merge@example.com", "campaign": "spring"})
	require.Equal(t, http.StatusOK, w.Code)

	_, cf, _ := contactRow(t, db, orgID, "merge@example.com")
	require.Equal(t, "google", cf["utm_source"], "the first delivery's custom field must survive the second")
	require.Equal(t, "gold", cf["tier"], "every prior custom field must survive, not just the ones resent")
	require.Equal(t, "spring", cf["campaign"], "the new key must be written")
}

// The same statement must also let a later delivery CORRECT a value, or "merge"
// would have quietly become "first write wins" — the opposite failure.
func TestWebhookInbound_MergeLetsALaterDeliveryOverwriteTheSameKey(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "ow@example.com", "tier": "silver"}).Code)
	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "ow@example.com", "tier": "gold"}).Code)

	_, cf, _ := contactRow(t, db, orgID, "ow@example.com")
	require.Equal(t, "gold", cf["tier"], "the newer delivery must win for a key it resends")
}

// raceLockKey is the advisory lock the injected trigger parks on.
const raceLockKey = 987654321

// armInsertRace parks the NEXT inbound INSERT so the test can commit a competing row
// underneath it, deterministically reproducing a lost duplicate-email race.
//
// The competitor cannot be written by the trigger itself: an INSERT that violates a
// unique index rolls the whole statement back, taking any row the trigger wrote with
// it, so the recovery re-read would find nothing (verified — that was the first
// attempt, and it produced a 500). The competitor has to be COMMITTED on another
// connection, which means controlling the interleave rather than simulating it.
//
// So the trigger blocks on an advisory lock the test already holds. The handler's
// SELECT has missed by then; the test commits the winner and releases the lock; the
// handler's INSERT proceeds into a genuine 23505 against a row that really is there.
//
// Forcing the interleave rather than running a wall-clock race is the point: the
// concurrent test below stays green with the fix reverted, because on a fast machine
// the deliveries serialize and no conflict ever occurs.
func armInsertRace(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(fmt.Sprintf(`
		CREATE OR REPLACE FUNCTION park_on_race_lock() RETURNS trigger AS $fn$
		BEGIN
			-- The winner is the test's own competitor row, inserted while the handler
			-- is parked. It must sail through: parking it too would deadlock the test
			-- against the lock it is itself holding.
			IF NEW.first_name <> 'Winner' THEN
				PERFORM pg_advisory_lock(%d);
				PERFORM pg_advisory_unlock(%d);
			END IF;
			RETURN NEW;
		END $fn$ LANGUAGE plpgsql`, raceLockKey, raceLockKey)).Error)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER park_on_race_lock_trg BEFORE INSERT ON contacts
		FOR EACH ROW EXECUTE FUNCTION park_on_race_lock()`).Error)
	t.Cleanup(func() { db.Exec("DROP TRIGGER IF EXISTS park_on_race_lock_trg ON contacts") })
}

// The bug this whole change exists for. A delivery that loses the insert race used to
// get its 23505 discarded and answer 200 with a uuid that was never inserted — so the
// sender recorded a contact id belonging to no row, and a contact_created workflow
// enrolled against that phantom.
func TestWebhookInbound_LosingTheInsertRaceUpdatesTheWinnerRatherThanLyingAbout200(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	// Hold the lock on a connection of our own, so the trigger parks rather than
	// passing straight through.
	holder, err := db.DB()
	require.NoError(t, err)
	conn, err := holder.Conn(t.Context())
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(t.Context(), fmt.Sprintf("SELECT pg_advisory_lock(%d)", raceLockKey))
	require.NoError(t, err)

	armInsertRace(t, db)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- postLead(t, router, tok, map[string]any{"email": "raced@example.com", "last_name": "Loser"})
	}()

	// Give the handler time to run its SELECT and park inside the INSERT trigger, then
	// commit the winner and let it through.
	time.Sleep(400 * time.Millisecond)
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email, created_at, updated_at)
		 VALUES (gen_random_uuid(), ?, 'Winner', '', 'raced@example.com', NOW(), NOW())`, orgID).Error)
	_, err = conn.ExecContext(t.Context(), fmt.Sprintf("SELECT pg_advisory_unlock(%d)", raceLockKey))
	require.NoError(t, err)

	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the parked delivery never completed")
	}
	require.Equal(t, http.StatusOK, w.Code, "losing a race is recoverable, not a failure")

	var body struct {
		ContactID string `json:"contact_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM contacts WHERE org_id = ? AND email = ?", orgID, "raced@example.com").Scan(&count).Error)
	require.EqualValues(t, 1, count, "the race must leave exactly one contact")

	realID, _, _ := contactRow(t, db, orgID, "raced@example.com")
	require.Equal(t, realID.String(), body.ContactID,
		"the response must name the contact that exists, not the uuid the losing insert minted")

	// And the losing delivery's data must reach the winner's row rather than being
	// dropped on the floor — recovery means the lead is applied, not merely survived.
	var lastName string
	require.NoError(t, db.Raw("SELECT last_name FROM contacts WHERE id = ?", realID).Scan(&lastName).Error)
	require.Equal(t, "Loser", lastName, "the losing delivery's fields must be applied to the winning row")
}

// A genuine wall-clock race, kept as a property test: whatever the interleaving, the
// endpoint must converge on one contact and every response must name it. NOTE this
// one is probabilistic about which branch it exercises — on a fast machine the
// deliveries can serialize and no conflict occurs at all, which is why the
// deterministic test above exists rather than this standing in for it.
func TestWebhookInbound_ConcurrentFirstDeliveriesYieldOneContact(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	const n = 6
	codes := make([]int, n)
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := postLead(t, router, tok, map[string]any{
				"email": "race@example.com", "first_name": fmt.Sprintf("R%d", i),
			})
			codes[i] = w.Code
			var body struct {
				ContactID string `json:"contact_id"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			ids[i] = body.ContactID
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		require.Equal(t, http.StatusOK, code, "delivery %d must be accepted", i)
	}

	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM contacts WHERE org_id = ? AND email = ?", orgID, "race@example.com").Scan(&count).Error)
	require.EqualValues(t, 1, count, "concurrent first-time deliveries must produce exactly one contact")

	// Every response must name the contact that actually exists. This is the half the
	// shipped code got wrong: it answered with a uuid it had minted locally and then
	// failed to insert, so the id in the body belonged to no row at all.
	realID, _, _ := contactRow(t, db, orgID, "race@example.com")
	for i, id := range ids {
		require.Equal(t, realID.String(), id, "delivery %d reported a contact id that is not the stored contact", i)
	}
}

// A numeric phone is a real payload shape (plenty of senders emit 5551234567
// unquoted). It used to fail the INSERT on the varchar column, and because the error
// was discarded the whole lead vanished behind a 200.
func TestWebhookInbound_NumericPhoneIsStoredRatherThanLosingTheLead(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	w := postLead(t, router, tok, map[string]any{"email": "num@example.com", "phone": 5551234567})
	require.Equal(t, http.StatusOK, w.Code)

	var phone string
	require.NoError(t, db.Raw("SELECT phone FROM contacts WHERE org_id = ? AND email = ?", orgID, "num@example.com").Scan(&phone).Error)
	require.Equal(t, "5551234567", phone, "a numeric phone must be stored, not rendered in scientific notation and not dropped")
}

// The control for every test above: when the write genuinely cannot happen, the
// endpoint must SAY so. A 200 here is worse than uninformative — it enrolls workflows
// against a contact that does not exist and tells a retrying sender not to retry.
func TestWebhookInbound_AnswersFiveHundredWhenTheWriteFails(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	// Take the table away for the duration: a forced, deterministic write failure that
	// does not depend on guessing a constraint the fixture might not have.
	require.NoError(t, db.Exec("ALTER TABLE contacts RENAME TO contacts_hidden").Error)
	t.Cleanup(func() { db.Exec("ALTER TABLE contacts_hidden RENAME TO contacts") })

	w := postLead(t, router, tok, map[string]any{"email": "boom@example.com"})
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a failed write must not be reported as an accepted lead")
	require.Equal(t, "INTERNAL_ERROR", webhookErrorCode(t, w))
}

// updated_at is set explicitly because Table()+map supplies GORM no model schema, so
// nothing bumps it — a staleness that is invisible until someone sorts a list by it.
func TestWebhookInbound_UpdateBumpsUpdatedAt(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "ts@example.com"}).Code)
	_, _, before := contactRow(t, db, orgID, "ts@example.com")

	time.Sleep(10 * time.Millisecond)
	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "ts@example.com", "first_name": "Later"}).Code)

	_, _, after := contactRow(t, db, orgID, "ts@example.com")
	require.True(t, after.After(before), "an inbound update must bump updated_at (before=%s after=%s)", before, after)
}

// ── L7.3: the skip flag is dev-scoped ──────────────────────────────────────

// inboundRouterWithHandler is webhookTokenTestRouter's sibling for the tests that
// need to vary APP_ENV: it hands back the handler so one container can serve every
// case, rather than paying for a Postgres per sub-test.
func inboundRouterWithHandler(t *testing.T) (*gin.Engine, *Handler, *gorm.DB, uuid.UUID, func()) {
	t.Helper()
	db, cleanup := setupTestDB(t)
	orgID := uuid.New()
	engine := makeEngine(db, map[string]ActionExecutor{})
	handler := &Handler{
		engine:      engine,
		repo:        engine.repo,
		db:          db,
		logger:      slog.Default(),
		rateLimiter: newTokenBucket(),
		capChecker:  capAllow{},
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/webhooks/inbound/:org_token", handler.WebhookInbound)
	return router, handler, db, orgID, func() { engine.cancel(); cleanup() }
}

// The control that makes the gate mean anything. WEBHOOK_SKIP_SIGNATURE used to be a
// bare os.Getenv read, so setting it ANYWHERE disabled HMAC verification for every
// org's public endpoint at once — no log line, no UI indication, and no record that
// the variable exists (it is in no config struct, no BindEnv, and neither
// .env.example). A production deploy that inherited it from a dev shell handed anyone
// who could read an org token — it travels in the URL, and the URL is written to the
// access log — the ability to create contacts and fire workflows in that workspace.
func TestWebhookInbound_SkipSignatureIsIgnoredOutsideDevAndTest(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	for _, env := range []string{"", "production", "prod", "staging", "Development", "TEST"} {
		t.Run("APP_ENV="+env, func(t *testing.T) {
			handler.SetAppEnv(env)
			w := postLead(t, router, tok, map[string]any{"email": "unsigned@example.com"})
			require.Equal(t, http.StatusUnauthorized, w.Code,
				"an unsigned body must be refused when APP_ENV is %q", env)
			require.Equal(t, "UNAUTHORIZED", webhookErrorCode(t, w))
		})
	}
}

// The positive half: the hatch must still work where it is meant to, or every
// fixture-driven webhook test in this package silently starts testing nothing.
func TestWebhookInbound_SkipSignatureStillWorksInDevAndTest(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)

	for _, env := range []string{"development", "test"} {
		t.Run("APP_ENV="+env, func(t *testing.T) {
			handler.SetAppEnv(env)
			w := postLead(t, router, tok, map[string]any{"email": env + "@example.com"})
			require.Equal(t, http.StatusOK, w.Code, "the dev escape hatch must still work in %q", env)
		})
	}
}

// And the flag must still be required: a dev environment alone is not permission to
// accept unsigned traffic.
func TestWebhookInbound_DevEnvAloneDoesNotSkipTheSignature(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	tok := seedOrgToken(t, db, orgID)
	handler.SetAppEnv("development")

	w := postLead(t, router, tok, map[string]any{"email": "nodev@example.com"})
	require.Equal(t, http.StatusUnauthorized, w.Code,
		"without the flag, a dev environment must still verify signatures")
}

// ── L7.2b: the borrowed lead-platform bookkeeping ──────────────────────────

// fakeLeadCapture stands in for internal/integrations.LegacyCapture. The port is
// declared with primitives precisely so this is possible without importing it.
type fakeLeadCapture struct {
	owner *uuid.UUID
	begin int
	// what FinishDelivery was told
	finishedContact uuid.UUID
	finishedCreated bool
	finishedCause   error
	finishCalls     int
	resolveCalls    int
	beginErr        error
}

func (f *fakeLeadCapture) BeginDelivery(_ context.Context, _ uuid.UUID, _ map[string]any) (uuid.UUID, error) {
	f.begin++
	if f.beginErr != nil {
		return uuid.Nil, f.beginErr
	}
	return uuid.New(), nil
}

// resolveCalls counts how many times routing was asked for an owner. It is the whole
// point of the split: a delivery that turns out to be an UPDATE must never ask,
// because the rotation ticket is consuming.
func (f *fakeLeadCapture) ResolveOwner(_ context.Context, _, _ uuid.UUID) *uuid.UUID {
	f.resolveCalls++
	return f.owner
}

func (f *fakeLeadCapture) FinishDelivery(_ context.Context, _, deliveryID, contactID uuid.UUID, created bool, cause error) {
	if deliveryID == uuid.Nil {
		return
	}
	f.finishCalls++
	f.finishedContact, f.finishedCreated, f.finishedCause = contactID, created, cause
}

func contactOwner(t *testing.T, db *gorm.DB, orgID uuid.UUID, email string) *uuid.UUID {
	t.Helper()
	var row struct {
		OwnerUserID *uuid.UUID `gorm:"column:owner_user_id"`
	}
	require.NoError(t, db.Raw(
		"SELECT owner_user_id FROM contacts WHERE org_id = ? AND email = ? AND deleted_at IS NULL",
		orgID, email).Scan(&row).Error)
	return row.OwnerUserID
}

// The complaint this whole lead platform opens with: the legacy endpoint writes no
// owner, so own-scoped reps have never been able to see a single lead it produced.
func TestWebhookInbound_StampsTheRoutedOwnerOnANewContact(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	handler.SetAppEnv("test")
	tok := seedOrgToken(t, db, orgID)

	rep := uuid.New()
	cap := &fakeLeadCapture{owner: &rep}
	handler.SetLeadCapture(cap)

	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "owned@example.com"}).Code)

	got := contactOwner(t, db, orgID, "owned@example.com")
	require.NotNil(t, got, "a routed lead must be assigned to someone")
	require.Equal(t, rep, *got)
	require.Equal(t, 1, cap.begin)
	require.Equal(t, 1, cap.finishCalls)
	require.True(t, cap.finishedCreated, "a first delivery is a create")
	require.NoError(t, cap.finishedCause)
}

// The rule the ingest path documents and this one now has to hold too: an owner is
// stamped on CREATE only. On the update path a present-but-null owner means UNASSIGN,
// so writing it on a resubmission would silently strip whichever rep had since been
// given the record.
func TestWebhookInbound_ResubmissionDoesNotRestampTheOwner(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	handler.SetAppEnv("test")
	tok := seedOrgToken(t, db, orgID)

	first := uuid.New()
	cap := &fakeLeadCapture{owner: &first}
	handler.SetLeadCapture(cap)
	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "keep@example.com"}).Code)

	// A human reassigns the contact to someone else.
	handOver := uuid.New()
	require.NoError(t, db.Exec("UPDATE contacts SET owner_user_id = ? WHERE org_id = ? AND email = ?",
		handOver, orgID, "keep@example.com").Error)

	// The rotation would now hand the lead to a third rep — and must not.
	third := uuid.New()
	cap.owner = &third
	require.Equal(t, http.StatusOK,
		postLead(t, router, tok, map[string]any{"email": "keep@example.com", "first_name": "Again"}).Code)

	got := contactOwner(t, db, orgID, "keep@example.com")
	require.NotNil(t, got)
	require.Equal(t, handOver, *got, "a resubmission must not move the record away from its current owner")
	require.False(t, cap.finishedCreated, "the second delivery is an update")
	// The other half, and the one review caught: an update must not even ASK for an
	// owner. The rotation ticket is consuming, so asking burns a rep's turn on a lead
	// that gets nobody — and resubmissions are this endpoint's ordinary traffic.
	require.Equal(t, 1, cap.resolveCalls,
		"only the create should have consulted routing; an update asking burns a rotation turn")
}

// The bookkeeping is additive and must never be able to revoke the lead. A capture
// that cannot open a delivery leaves the endpoint behaving exactly as it did before
// L7.2b existed.
func TestWebhookInbound_BookkeepingFailureStillWritesTheLead(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	handler.SetAppEnv("test")
	tok := seedOrgToken(t, db, orgID)
	handler.SetLeadCapture(&fakeLeadCapture{beginErr: errors.New("ledger down")})

	w := postLead(t, router, tok, map[string]any{"email": "degraded@example.com"})
	require.Equal(t, http.StatusOK, w.Code, "a ledger failure must not cost the lead")

	var count int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM contacts WHERE org_id = ? AND email = ?",
		orgID, "degraded@example.com").Scan(&count).Error)
	require.EqualValues(t, 1, count)
	require.Nil(t, contactOwner(t, db, orgID, "degraded@example.com"), "no owner is honest when routing never ran")
}

// A nil port is the pre-L7.2b world, and it must still work: this endpoint predates
// the lead platform and cannot be made to depend on it.
func TestWebhookInbound_WorksWithNoLeadCaptureAtAll(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	handler.SetAppEnv("test")
	tok := seedOrgToken(t, db, orgID)

	require.Equal(t, http.StatusOK, postLead(t, router, tok, map[string]any{"email": "nocap@example.com"}).Code)
	require.Nil(t, contactOwner(t, db, orgID, "nocap@example.com"))
}

// A failed write must reach the ledger as a failure, or the delivery log would show
// a source that is quietly losing every lead as perfectly healthy.
func TestWebhookInbound_ReportsAFailedWriteToTheLedger(t *testing.T) {
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	router, handler, db, orgID, cleanup := inboundRouterWithHandler(t)
	defer cleanup()
	handler.SetAppEnv("test")
	tok := seedOrgToken(t, db, orgID)
	cap := &fakeLeadCapture{}
	handler.SetLeadCapture(cap)

	require.NoError(t, db.Exec("ALTER TABLE contacts RENAME TO contacts_hidden").Error)
	t.Cleanup(func() { db.Exec("ALTER TABLE contacts_hidden RENAME TO contacts") })

	require.Equal(t, http.StatusInternalServerError,
		postLead(t, router, tok, map[string]any{"email": "boom2@example.com"}).Code)
	require.Equal(t, 1, cap.finishCalls, "a failed delivery must still be closed")
	require.Error(t, cap.finishedCause, "the ledger must record WHY it failed")
}
