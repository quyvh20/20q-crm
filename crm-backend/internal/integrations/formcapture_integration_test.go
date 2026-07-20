package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The form-embed route end to end against real Postgres. The CORS assertions are
// the point: they are the only thing standing between "a customer's form works"
// and "any website may read authenticated CRM data", and neither failure is
// visible to a same-origin test or to curl.

func seedFormSource(t *testing.T, db *gorm.DB, origins []string) (*Repository, *FormSource) {
	t.Helper()
	repo := NewRepository(db)
	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)

	cfg := FormConfig{
		Enabled:  true,
		Honeypot: "company_website",
		Fields: []FormField{
			{Name: "email", Label: "Email", Type: "email", Required: true},
			{Name: "first_name", Label: "First name", Type: "text"},
		},
	}
	confJSON, err := MergeFormConfig(nil, cfg)
	require.NoError(t, err)

	s := &LeadSource{
		OrgID: orgID, Kind: KindFormEmbed, Name: "Website contact form", TargetSlug: "contact",
		UpdatePolicy: UpdatePolicyFillBlankOnly,
		MatchFields:  datatypes.JSON(`["email"]`),
		FieldMap:     datatypes.JSON(`{}`),
		Config:       confJSON,
		Status:       SourceStatusActive,
		TokenHash:    HashLeadKey("crm_lead_form_bearer"),
	}
	require.NoError(t, repo.CreateSource(context.Background(), s))

	tok, err := GeneratePublicToken()
	require.NoError(t, err)
	require.NoError(t, repo.SetPublicToken(context.Background(), orgID, s.ID, tok))
	if origins != nil {
		require.NoError(t, repo.SetAllowedOrigins(context.Background(), orgID, s.ID, origins))
	}

	found, err := repo.FindFormSourceByPublicToken(context.Background(), tok)
	require.NoError(t, err)
	require.NotNil(t, found)
	return repo, found
}

func formRouter(t *testing.T, repo *Repository, w RecordWriter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ingest := NewLeadIngestService(repo, w, &stubMatcher{}, contactSchema(), noFieldDefs{}, stubMembers{}, nil, logger)
	h := NewHandler(repo, ingest, allowingAuthorizer{}, stubMembers{}, contactSchema(), nil,
		NewRateLimiter(nil, 10000, 0), NewRateLimiter(nil, 10000, 0), logger)

	r := gin.New()
	r.POST(FormCapturePrefix+"/:public_token", h.formCORS, h.CaptureForm)
	r.OPTIONS(FormCapturePrefix+"/:public_token", h.formCORS, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	return r
}

func submit(t *testing.T, r *gin.Engine, method, token, origin, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, FormCapturePrefix+"/"+token, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	r.ServeHTTP(rec, req)
	return rec
}

const goodSubmission = `{"fields":{"email":"ada@example.com","first_name":"Ada"},
	"context":{"page_url":"https://customer.com/pricing?utm_source=x&utm_campaign=y","referrer":"https://google.com/"}}`

// TestFormRoute_CORS is the security unit: allowed submits, disallowed is blocked
// BEFORE anything is written, and no response ever grants credentials.
func TestFormRoute_CORS(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, []string{"https://customer.com"})
	w := &recordingWriter{}
	r := formRouter(t, repo, w)

	t.Run("allowed origin: submits, echoes ACAO, and never grants credentials", func(t *testing.T) {
		rec := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com", goodSubmission)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, "https://customer.com", rec.Header().Get("Access-Control-Allow-Origin"))
		// The header that would turn this into a cross-origin read of authenticated
		// CRM data. Absent, not "false".
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"),
			"a credentials grant to a customer origin is the vulnerability this route exists to avoid")
		require.Contains(t, rec.Header().Values("Vary"), "Origin")
		require.Equal(t, 1, w.creates)
	})

	t.Run("disallowed origin: 403, no ACAO, and NOTHING written", func(t *testing.T) {
		before := w.creates
		var eventsBefore int64
		require.NoError(t, db.Raw(`SELECT count(*) FROM integration_events WHERE source_id = ?`, src.ID).Scan(&eventsBefore).Error)

		rec := submit(t, r, http.MethodPost, src.PublicToken, "https://evil.example", goodSubmission)

		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		require.Equal(t, before, w.creates, "a refused origin must not write a contact")

		var eventsAfter int64
		require.NoError(t, db.Raw(`SELECT count(*) FROM integration_events WHERE source_id = ?`, src.ID).Scan(&eventsAfter).Error)
		require.Equal(t, eventsBefore, eventsAfter,
			"'blocked' must mean nothing was stored — a ledger row would make the claim false")
	})

	t.Run("preflight: 204 with the method allowance, only for an allowed origin", func(t *testing.T) {
		rec := submit(t, r, http.MethodOptions, src.PublicToken, "https://customer.com", "")
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Equal(t, "https://customer.com", rec.Header().Get("Access-Control-Allow-Origin"))
		require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))

		rec = submit(t, r, http.MethodOptions, src.PublicToken, "https://evil.example", "")
		require.Equal(t, http.StatusNoContent, rec.Code)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"),
			"a disallowed origin must learn nothing from the preflight")
	})

	t.Run("an unknown token answers exactly like a disallowed origin", func(t *testing.T) {
		// Distinguishable answers here would be a live-token oracle.
		known := submit(t, r, http.MethodOptions, src.PublicToken, "https://evil.example", "")
		unknown := submit(t, r, http.MethodOptions, "not-a-real-token", "https://evil.example", "")
		require.Equal(t, known.Code, unknown.Code)
		require.Equal(t, known.Header().Get("Access-Control-Allow-Origin"), unknown.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("no Origin header passes through — CORS is not an authentication check", func(t *testing.T) {
		before := w.creates
		rec := submit(t, r, http.MethodPost, src.PublicToken, "", goodSubmission)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Greater(t, w.creates, before,
			"a script sends no Origin and is unaffected — the honest limit of an allowlist")
	})
}

// A source with no origins configured is the state of every freshly created one.
// It must deny, not allow.
func TestFormRoute_EmptyAllowlistDeniesBrowsers(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, nil)
	w := &recordingWriter{}
	r := formRouter(t, repo, w)

	rec := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com", goodSubmission)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Zero(t, w.creates)
}

func TestFormRoute_HoneypotIsSilent(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, []string{"https://customer.com"})
	w := &recordingWriter{}
	r := formRouter(t, repo, w)

	clean := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com", goodSubmission)
	trapped := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com",
		`{"fields":{"email":"bot@spam.example","company_website":"http://spam.example"}}`)

	// A bot that can tell it was caught just adapts, so the response must be
	// byte-identical to a success.
	require.Equal(t, http.StatusOK, trapped.Code)
	require.JSONEq(t, clean.Body.String(), trapped.Body.String(),
		"a caught bot must not be able to distinguish the response")
	require.Equal(t, 1, w.creates, "the honeypot submission must not write a contact")

	var status, errText string
	require.NoError(t, db.Raw(
		`SELECT status, error FROM integration_events WHERE source_id = ? ORDER BY created_at DESC LIMIT 1`,
		src.ID).Row().Scan(&status, &errText))
	require.Equal(t, EventStatusQuarantined, status)
	require.Contains(t, errText, "honeypot")
}

// The declared field list is what the credential is on every other route: it is
// what stops a stranger writing arbitrary keys into the ledger the admin reads.
func TestFormRoute_UndeclaredFieldsAreDropped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, []string{"https://customer.com"})
	w := &recordingWriter{}
	r := formRouter(t, repo, w)

	rec := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com",
		`{"fields":{"email":"ada@example.com","owner_user_id":"11111111-1111-1111-1111-111111111111","<script>":"x"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var raw string
	require.NoError(t, db.Raw(
		`SELECT raw_payload::text FROM integration_events WHERE source_id = ? ORDER BY created_at DESC LIMIT 1`,
		src.ID).Scan(&raw).Error)
	require.NotContains(t, raw, "<script>",
		"an undeclared key must never reach raw_payload — the mapping UI samples it for suggestions")
	require.NotContains(t, raw, "owner_user_id")
}

// The context is rebuilt server-side, so a caller cannot write arbitrary JSON onto
// the ledger row — but the two keys the snippet legitimately sends must survive.
func TestFormRoute_ContextIsRebuiltNotPassedThrough(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, []string{"https://customer.com"})
	r := formRouter(t, repo, &recordingWriter{})

	rec := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com",
		`{"fields":{"email":"ada@example.com"},"context":{"page_url":"https://customer.com/p?utm_source=x","referrer":"https://google.com/","injected":"nope"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var ctxJSON string
	require.NoError(t, db.Raw(
		`SELECT context::text FROM integration_events WHERE source_id = ? ORDER BY created_at DESC LIMIT 1`,
		src.ID).Scan(&ctxJSON).Error)
	require.Contains(t, ctxJSON, "utm_source=x", "the page URL carries the UTMs and must survive")
	require.Contains(t, ctxJSON, "google.com")
	require.NotContains(t, ctxJSON, "injected", "only the two keys we read may land on the ledger")
}

// A malformed submission is the failure you most need to see from a snippet running
// on someone else's site, and it is the one nobody is watching a response for.
func TestFormRoute_MalformedBodyLeavesEvidence(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, []string{"https://customer.com"})
	r := formRouter(t, repo, &recordingWriter{})

	rec := submit(t, r, http.MethodPost, src.PublicToken, "https://customer.com", `{"fields":{`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	// The page must be able to READ the error — a cross-origin failure with no ACAO
	// shows only "network error" to whoever is debugging their own website.
	require.Equal(t, "https://customer.com", rec.Header().Get("Access-Control-Allow-Origin"))

	var n int64
	require.NoError(t, db.Raw(
		`SELECT count(*) FROM integration_events WHERE source_id = ? AND status = ?`,
		src.ID, EventStatusQuarantined).Scan(&n).Error)
	require.EqualValues(t, 1, n, "a malformed submission must leave the admin evidence")
}

// public_token is one namespace across kinds, so each capture route must refuse the
// other's tokens — otherwise a form token (public by construction) POSTed at the
// Google route plants a false "webhook key mismatch" row in that org's ledger.
func TestFormToken_RejectedByTheGoogleRoute(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo, src := seedFormSource(t, db, []string{"https://customer.com"})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ingest := NewLeadIngestService(repo, &recordingWriter{}, &stubMatcher{}, contactSchema(), noFieldDefs{}, stubMembers{}, nil, logger)
	h := NewHandler(repo, ingest, allowingAuthorizer{}, stubMembers{}, contactSchema(), nil,
		NewRateLimiter(nil, 10000, 0), NewRateLimiter(nil, 10000, 0), logger)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/capture/google-ads/:public_token", h.CaptureGoogleAds)

	rec := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"lead_id": "x", "google_key": "guess", "user_column_data": []any{}})
	req := httptest.NewRequest(http.MethodPost, "/api/capture/google-ads/"+src.PublicToken, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM integration_events WHERE source_id = ?`, src.ID).Scan(&n).Error)
	require.Zero(t, n, "a form token at the Google route must plant no ledger row at all")
}
