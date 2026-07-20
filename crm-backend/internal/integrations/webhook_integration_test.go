package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"
	"crm-backend/internal/integrations/envelope"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// webhookStack wires the full L5.3 receive→process path against real Postgres and
// a fake Graph.
type webhookStack struct {
	db      *gorm.DB
	repo    *Repository
	conn    *ConnectionService
	ingest  *LeadIngestService
	graph   *fakeGraph
	writer  *recordingWriter
	handler *WebhookHandler
	router  *gin.Engine
	proc    *webhookProcessor
}

func newWebhookStack(t *testing.T, db *gorm.DB) *webhookStack {
	t.Helper()
	repo := NewRepository(db)
	ring, err := envelope.ParseKeyring(testKey)
	require.NoError(t, err)
	codec := envelope.NewCodec(ring)

	graph := newFakeGraph(t, "app-secret")
	prov := graph.provider("app123")
	reg := NewRegistry()
	reg.Register(prov)

	conn := NewConnectionService(repo, codec, reg, "https://api.example", "https://app.example", nil)
	writer := &recordingWriter{}
	logger := slog.New(slog.NewTextHandler(nopWriter{}, nil))
	ingest := NewLeadIngestService(repo, writer, &stubMatcher{}, contactSchema(), noFieldDefs{}, stubMembers{}, nil, logger)

	h := NewWebhookHandler(repo, conn, "verify-token", nil, logger)
	router := gin.New()
	h.RegisterRoutes(router)

	return &webhookStack{
		db: db, repo: repo, conn: conn, ingest: ingest, graph: graph, writer: writer,
		handler: h, router: router,
		proc: &webhookProcessor{repo: repo, conn: conn, ingest: ingest, logger: logger},
	}
}

// connectPage inserts a live connection with a sealed page token.
func (s *webhookStack) connectPage(t *testing.T, orgID uuid.UUID, pageID string) *IntegrationConnection {
	t.Helper()
	id := uuid.New()
	sealed, kv, err := s.conn.sealCredentials(orgID, id, Credentials{AccessToken: "tok-" + pageID})
	require.NoError(t, err)
	c := &IntegrationConnection{
		ID: id, OrgID: orgID, Provider: ProviderKeyFacebook, ExternalAccountID: pageID,
		ExternalAccountLabel: "Page " + pageID, EncryptedCredentials: sealed, KeyVersion: kv,
		Status: ConnStatusConnected,
	}
	require.NoError(t, s.repo.InsertConnection(context.Background(), c))
	return c
}

// enableForm creates a facebook_form source for a connection + form id.
func (s *webhookStack) enableForm(t *testing.T, orgID, connID uuid.UUID, formID string) *LeadSource {
	t.Helper()
	cfg, _ := json.Marshal(map[string]any{"facebook": map[string]any{"form_id": formID}})
	src := &LeadSource{
		OrgID: orgID, Kind: KindFacebookForm, Name: "FB form " + formID,
		TargetSlug: "contact", UpdatePolicy: UpdatePolicyFillBlankOnly, Status: SourceStatusActive,
		MatchFields: datatypes.JSON(`["email"]`),
		// Map the FB field name onto the contact field so a contact is actually written.
		FieldMap: datatypes.JSON(`{"email":{"target_key":"email"}}`),
		Config:   datatypes.JSON(cfg),
	}
	// CreateConnectionSource (not CreateSource) so token_hash is NULL — a page-backed
	// source has no bearer key, exactly as the enable-form handler creates it.
	require.NoError(t, s.repo.CreateConnectionSource(context.Background(), src))
	// connection_id is deliberately not on the LeadSource struct (see the migration),
	// so stamp it with a targeted update — the same way the handler does.
	require.NoError(t, s.repo.SetSourceConnection(context.Background(), orgID, src.ID, connID))
	return src
}

// post signs and delivers a webhook body, returning the HTTP status.
func (s *webhookStack) post(body []byte) int {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/facebook/webhook", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", signFB("app-secret", body))
	s.router.ServeHTTP(w, r)
	return w.Code
}

func (s *webhookStack) eventByLeadgen(t *testing.T, leadgenID string) *IntegrationEvent {
	t.Helper()
	var ev IntegrationEvent
	err := s.db.Where("provider_event_id = ?", leadgenID).First(&ev).Error
	if err != nil {
		return nil
	}
	return &ev
}

func TestWebhook_ReceiveAndProcess_EndToEnd(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	s.enableForm(t, org, c.ID, "form1")

	// 1. Receive — signed → 200, and a pending event lands.
	require.Equal(t, http.StatusOK, s.post(leadgenBody("page1", "form1", "L1")))
	ev := s.eventByLeadgen(t, "L1")
	require.NotNil(t, ev)
	require.Equal(t, EventStatusPending, ev.Status)
	require.NotNil(t, ev.ConnectionID)

	// 2. Process — the worker fetches the lead and writes a contact.
	s.proc.drain(ctx)
	ev = s.eventByLeadgen(t, "L1")
	require.Equal(t, EventStatusProcessed, ev.Status)
	require.NotNil(t, ev.ResultRecordID)
	require.Equal(t, 1, s.writer.creates, "the fetched lead must become a contact")

	// 3. Redelivery (Meta retries for ~36h) dedups on leadgen_id — no second event,
	//    no second contact.
	require.Equal(t, http.StatusOK, s.post(leadgenBody("page1", "form1", "L1")))
	var count int64
	require.NoError(t, s.db.Model(&IntegrationEvent{}).Where("provider_event_id = ?", "L1").Count(&count).Error)
	require.Equal(t, int64(1), count, "a redelivery must not create a second event")
	s.proc.drain(ctx)
	require.Equal(t, 1, s.writer.creates, "a redelivery must not create a second contact")
}

func TestWebhook_UnsignedRejected(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	s := newWebhookStack(t, db)

	w := httptest.NewRecorder()
	body := leadgenBody("page1", "form1", "L9")
	r := httptest.NewRequest(http.MethodPost, "/api/integrations/facebook/webhook", bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", signFB("WRONG-secret", body))
	s.router.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Nil(t, s.eventByLeadgen(t, "L9"), "an unsigned delivery must not be enqueued")
}

func TestWebhook_UnknownPageDropped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	s := newWebhookStack(t, db)
	// No connection for page-unknown. Signed → 200 (we ack Meta), but nothing enqueued
	// (there is no live workspace to attribute it to — never the old workspace).
	require.Equal(t, http.StatusOK, s.post(leadgenBody("page-unknown", "form1", "L2")))
	require.Nil(t, s.eventByLeadgen(t, "L2"))
}

func TestWebhook_NoFormQuarantines(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	s.connectPage(t, org, "page1")
	// The page is connected but the form is NOT enabled (no facebook_form source).
	require.Equal(t, http.StatusOK, s.post(leadgenBody("page1", "form-unenabled", "L3")))
	s.proc.drain(ctx)
	ev := s.eventByLeadgen(t, "L3")
	require.NotNil(t, ev)
	require.Equal(t, EventStatusQuarantined, ev.Status, "a lead for an un-enabled form must quarantine, not loop")
	require.Equal(t, 0, s.writer.creates)
}

func TestWebhook_TokenDeathFlipsConnectionAndFails(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	s.enableForm(t, org, c.ID, "form1")

	// The page token is dead — the leadgen fetch returns 400 (permanent).
	s.graph.leadStatus = 400

	require.Equal(t, http.StatusOK, s.post(leadgenBody("page1", "form1", "L4")))
	s.proc.drain(ctx)

	ev := s.eventByLeadgen(t, "L4")
	require.Equal(t, EventStatusFailed, ev.Status, "a permanent fetch failure fails the delivery")
	require.Equal(t, 0, s.writer.creates)

	updated, err := s.repo.GetConnection(ctx, org, c.ID)
	require.NoError(t, err)
	require.Equal(t, ConnStatusError, updated.Status, "a dead token must flip the connection to error (loud, not silent lead loss)")
}

func TestWebhook_RateLimitRetriesNotFlips(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	s.enableForm(t, org, c.ID, "form1")

	// Graph returns a page-level rate limit — HTTP 400, error code 32 (NOT 429). This
	// must be treated as TRANSIENT: retry the delivery, never flip the healthy token.
	s.graph.leadStatus = 400
	s.graph.leadErrorCode = 32

	require.Equal(t, http.StatusOK, s.post(leadgenBody("page1", "form1", "L5")))
	s.proc.drain(ctx)

	ev := s.eventByLeadgen(t, "L5")
	require.Equal(t, EventStatusPending, ev.Status, "a rate-limited fetch must be re-pended, not failed")
	updated, err := s.repo.GetConnection(ctx, org, c.ID)
	require.NoError(t, err)
	require.Equal(t, ConnStatusConnected, updated.Status, "a rate limit must NOT flip a healthy connection to error")

	// When the throttle clears, a later drain succeeds and writes the contact.
	s.graph.leadStatus = 0
	s.proc.drain(ctx)
	ev = s.eventByLeadgen(t, "L5")
	require.Equal(t, EventStatusProcessed, ev.Status)
	require.Equal(t, 1, s.writer.creates)
	require.Empty(t, ev.Error, "a delivery that ultimately succeeded must not carry a stale retry error")
}

func TestWebhook_TransientWriteErrorRetries(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	s := newWebhookStack(t, db)
	// Swap in a writer that fails the contact create with a raw (transient-looking)
	// error, then recovers — the write path must retry, not lose the lead.
	fw := &flakyWriter{failFirst: 1}
	s.ingest = NewLeadIngestService(s.repo, fw, &stubMatcher{}, contactSchema(), noFieldDefs{}, stubMembers{}, nil,
		slog.New(slog.NewTextHandler(nopWriter{}, nil)))
	s.proc.ingest = s.ingest

	org := seedOrg(t, db)
	c := s.connectPage(t, org, "page1")
	s.enableForm(t, org, c.ID, "form1")

	require.Equal(t, http.StatusOK, s.post(leadgenBody("page1", "form1", "L6")))
	s.proc.drain(ctx) // attempt 1: write fails transiently → re-pended
	ev := s.eventByLeadgen(t, "L6")
	require.Equal(t, EventStatusPending, ev.Status, "a transient write failure must re-pend, not fail terminally")
	updated, err := s.repo.GetConnection(ctx, org, c.ID)
	require.NoError(t, err)
	require.Equal(t, ConnStatusConnected, updated.Status, "a transient WRITE failure must never flip the connection")

	s.proc.drain(ctx) // attempt 2: write succeeds
	ev = s.eventByLeadgen(t, "L6")
	require.Equal(t, EventStatusProcessed, ev.Status)
	require.Equal(t, 1, fw.creates)
}

// flakyWriter fails the first N creates with a raw error, then succeeds.
type flakyWriter struct {
	failFirst int
	calls     int
	creates   int
}

func (w *flakyWriter) Create(_ context.Context, _, _ uuid.UUID, slug string, _ domain.RecordWriteInput) (*domain.UniformRecord, error) {
	w.calls++
	if w.calls <= w.failFirst {
		return nil, errTransientWrite
	}
	w.creates++
	return &domain.UniformRecord{ID: uuid.New(), Object: slug}, nil
}

func (w *flakyWriter) Update(_ context.Context, _ uuid.UUID, slug string, id uuid.UUID, _ domain.RecordWriteInput) (*domain.UniformRecord, error) {
	return &domain.UniformRecord{ID: id, Object: slug}, nil
}

var errTransientWrite = errors.New("write failed transiently (test)")

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
