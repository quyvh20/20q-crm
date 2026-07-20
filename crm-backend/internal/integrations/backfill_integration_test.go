package integrations

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// backfillHandler builds a source Handler wired to the webhook stack's connection
// service, for driving runBackfill.
func (s *webhookStack) backfillHandler(t *testing.T) *Handler {
	t.Helper()
	h := NewHandler(s.repo, s.ingest, allowingAuthorizer{}, stubMembers{}, contactSchema(), nil,
		NewRateLimiter(nil, 0, 0), NewRateLimiter(nil, 0, 0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h.WithConnections(s.conn)
}

func TestFacebook_ListForms(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app")
	forms, err := p.ListForms(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"})
	require.NoError(t, err)
	require.Len(t, forms, 2)
	require.Equal(t, "form1", forms[0].ID)
	require.Equal(t, "Contact Form", forms[0].Name)
}

func TestFacebook_Backfill(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app")
	leads, next, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"}, "form1", "")
	require.NoError(t, err)
	require.Len(t, leads, 1)
	require.Equal(t, "BL1", leads[0].ProviderEventID, "the leadgen id must ride the lead for dedupe")
	require.Equal(t, "past@example.com", leads[0].Fields["email"])
	require.NotEmpty(t, next, "a first page with more must return a cursor")

	// The next page is empty → no cursor.
	leads2, next2, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"}, Credentials{AccessToken: "tok"}, "form1", next)
	require.NoError(t, err)
	require.Len(t, leads2, 0)
	require.Empty(t, next2)
}

func TestForms_EnableCreatesFacebookFormSource(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")

	// Enable form1 by creating its source the way the handler does.
	src := s.enableForm(t, org, c.ID, "form1")
	require.Equal(t, KindFacebookForm, src.Kind)

	// It is resolvable by (connection, form_id) — exactly what the webhook processor uses.
	got, err := s.repo.FindFacebookFormSource(ctx, c.ID, "form1")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, src.ID, got.ID)

	// And it carries NO bearer token (token_hash NULL) — a page-backed source has no key.
	var hash *string
	require.NoError(t, db.Raw(`SELECT token_hash FROM lead_sources WHERE id = ?`, src.ID).Scan(&hash).Error)
	require.Nil(t, hash, "a facebook_form source must have a NULL token_hash (no bearer credential)")

	// EnabledFormIDs reports it.
	enabled, err := s.repo.EnabledFormIDs(ctx, org, c.ID)
	require.NoError(t, err)
	require.Equal(t, src.ID, enabled["form1"])
}

func TestBackfill_ImportsHistoricalLeadsExactlyOnce(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	src := s.enableForm(t, org, c.ID, "form1")

	// A Handler wired with the connection service, so runBackfill can resolve creds.
	h := s.backfillHandler(t)

	// Run the backfill synchronously (the exported handler launches a goroutine; the
	// unexported runBackfill is the unit under test).
	h.runBackfill(ctx, src, false)

	require.Equal(t, 1, s.writer.creates, "the one historical lead must be imported as a contact")

	// The imported delivery is recorded, connection-scoped, deduped on BL1.
	ev := s.eventByLeadgen(t, "BL1")
	require.NotNil(t, ev)
	require.Equal(t, EventStatusProcessed, ev.Status)

	// Re-running imports NOTHING new — dedupe on leadgen_id makes backfill exactly-once.
	h.runBackfill(ctx, src, false)
	require.Equal(t, 1, s.writer.creates, "a second backfill must not re-import the same lead")
}

func TestBackfill_RecoversATransientlyFailedLeadOnReRun(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	// A writer that fails the first create with a raw (transient) error, then succeeds.
	fw := &flakyWriter{failFirst: 1}
	s.ingest = NewLeadIngestService(s.repo, fw, &stubMatcher{}, contactSchema(), noFieldDefs{}, stubMembers{}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	src := s.enableForm(t, org, c.ID, "form1")
	h := s.backfillHandler(t)

	// Run 1: the write fails transiently → the delivery is recorded FAILED, no contact.
	h.runBackfill(ctx, src, false)
	require.Equal(t, 0, fw.creates)
	ev := s.eventByLeadgen(t, "BL1")
	require.Equal(t, EventStatusFailed, ev.Status)

	// Run 2: the re-run must RECOVER the failed lead (not skip it on the dedupe index)
	// — backfill is these historical leads' only entry.
	h.runBackfill(ctx, src, false)
	require.Equal(t, 1, fw.creates, "a re-run must recover a transiently-failed backfill lead")
	ev = s.eventByLeadgen(t, "BL1")
	require.Equal(t, EventStatusProcessed, ev.Status)
	require.Empty(t, ev.Error, "a recovered delivery must not keep its prior failure error")
}

func TestBackfill_DedupesAgainstAWebhookDeliveredLead(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	src := s.enableForm(t, org, c.ID, "form1")

	// Simulate BL1 having already arrived by webhook: a connection-scoped event exists.
	leadgen := "BL1"
	existing := &IntegrationEvent{
		OrgID: org, ConnectionID: &c.ID, ProviderEventID: &leadgen,
		Status: EventStatusProcessed, RawPayload: datatypes.JSON(`{}`),
	}
	rid := uuid.New()
	existing.ResultRecordID = &rid
	inserted, err := s.repo.InsertEventDeduped(ctx, existing)
	require.NoError(t, err)
	require.True(t, inserted)

	h := s.backfillHandler(t)
	h.runBackfill(ctx, src, false)

	// The webhook already handled BL1; backfill must NOT write it a second time.
	require.Equal(t, 0, s.writer.creates, "backfill must dedupe against a lead already received by webhook")
}

func TestCreateSource_RejectsFacebookForm(t *testing.T) {
	// A facebook_form source cannot be minted via the generic New-source flow. The
	// guard returns before any DB access, so a nil repo is fine (mirrors the
	// test-lead handler test's shape).
	gin.SetMode(gin.TestMode)
	h := NewHandler(nil, nil, allowingAuthorizer{}, stubMembers{}, contactSchema(), nil,
		NewRateLimiter(nil, 0, 0), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := gin.New()
	router.POST("/sources", func(c *gin.Context) {
		c.Set("org_id", uuid.New())
		c.Set("user_id", uuid.New())
		h.CreateSource(c)
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/sources", strings.NewReader(`{"name":"x","kind":"facebook_form"}`))
	r.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
