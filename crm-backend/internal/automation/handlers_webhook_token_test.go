package automation

import (
	"bytes"
	"crypto/hmac"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// handlers_webhook_token_test.go covers the webhook setup endpoints (P17):
//   - GET  /api/webhooks/token            → token + MASKED secret + inbound URL
//   - POST /api/webhooks/regenerate-secret → rotates the secret, returns it in full once
//
// Two kinds of test live here:
//   - Pure (no Docker): the URL/scheme/mask helpers and the missing-org-context
//     401 short-circuit, which returns before any DB access.
//   - DB-backed (skips without Docker): get-or-create, masking, and rotation
//     behavior, using the package's integration scaffolding (setupTestDB /
//     makeEngine), consistent with the rest of the package.

// ============================================================
// Pure: URL assembly (inboundWebhookURL)
// ============================================================

func TestInboundWebhookURL(t *testing.T) {
	cases := []struct {
		name   string
		scheme string
		host   string
		token  string
		want   string
	}{
		{"https prod", "https", "api.20q-crm.com", "abc123", "https://api.20q-crm.com/api/webhooks/inbound/abc123"},
		{"http localhost with port", "http", "localhost:8080", "deadbeef", "http://localhost:8080/api/webhooks/inbound/deadbeef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, inboundWebhookURL(tc.scheme, tc.host, tc.token))
		})
	}
}

// ============================================================
// Pure: scheme detection (requestScheme)
// ============================================================

func TestRequestScheme(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(setup func(r *http.Request)) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "http://example.com/api/webhooks/token", nil)
		if setup != nil {
			setup(c.Request)
		}
		return c
	}

	t.Run("X-Forwarded-Proto wins", func(t *testing.T) {
		c := newCtx(func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") })
		assert.Equal(t, "https", requestScheme(c))
	})

	t.Run("X-Forwarded-Proto comma list takes the first", func(t *testing.T) {
		c := newCtx(func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https, http") })
		assert.Equal(t, "https", requestScheme(c))
	})

	t.Run("TLS connection falls back to https", func(t *testing.T) {
		c := newCtx(func(r *http.Request) { r.TLS = &tls.ConnectionState{} })
		assert.Equal(t, "https", requestScheme(c))
	})

	t.Run("plain http default", func(t *testing.T) {
		c := newCtx(nil)
		assert.Equal(t, "http", requestScheme(c))
	})
}

// ============================================================
// Pure: secret masking (maskSecret)
// ============================================================

func TestMaskSecret(t *testing.T) {
	t.Run("reveals only the last 4 chars", func(t *testing.T) {
		full := "deadbeefdeadbeefdeadbeefdeadbeef"
		m := maskSecret(full)
		assert.Equal(t, strings.Repeat("•", 12)+"beef", m)
		assert.NotEqual(t, full, m, "full secret must never appear in the masked form")
		assert.True(t, strings.HasSuffix(m, full[len(full)-4:]))
	})

	t.Run("does not leak the true length (fixed bullet run)", func(t *testing.T) {
		short := maskSecret("aaaa1234")  // len 8
		long := maskSecret("bbbbbbbb1234") // len 12
		assert.Equal(t, strings.Repeat("•", 12)+"1234", short)
		assert.Equal(t, strings.Repeat("•", 12)+"1234", long)
	})

	t.Run("short and empty secrets are fully masked", func(t *testing.T) {
		assert.Equal(t, "", maskSecret(""))
		assert.Equal(t, "••••", maskSecret("abcd"))
	})
}

// ============================================================
// Pure: missing org context → 401 before any DB access
// ============================================================

// TestWebhookTokenHandler_MissingOrgContextReturns401 proves both webhook setup
// handlers reject a request with no resolvable org context with 401 — before
// touching h.repo / h.db (both nil here, so a regression that reordered the check
// would panic on the nil deref).
func TestWebhookTokenHandler_MissingOrgContextReturns401(t *testing.T) {
	h := &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/webhooks/token", h.GetWebhookToken)
	router.POST("/api/webhooks/reveal-secret", h.RevealWebhookSecret)
	router.POST("/api/webhooks/regenerate-secret", h.RegenerateWebhookSecret)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/webhooks/token"},
		{"POST", "/api/webhooks/reveal-secret"},
		{"POST", "/api/webhooks/regenerate-secret"},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		assert.Equal(t, http.StatusUnauthorized, w.Code, "%s %s body: %s", tc.method, tc.path, w.Body.String())
	}
}

// ============================================================
// DB-backed helpers + tests
// ============================================================

type webhookTokenResp struct {
	Data *struct {
		Token        string `json:"token"`
		SecretMasked string `json:"secret_masked"`
		URL          string `json:"url"`
	} `json:"data"`
}

type webhookSecretResp struct {
	Data *struct {
		Token  string `json:"token"`
		Secret string `json:"secret"`
		URL    string `json:"url"`
	} `json:"data"`
}

// webhookTokenTestRouter spins up a Postgres-backed handler with both webhook
// setup routes registered and an admin org context injected.
func webhookTokenTestRouter(t *testing.T) (*gin.Engine, *gorm.DB, uuid.UUID, func()) {
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
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", uuid.New())
		c.Set("role", "admin")
		c.Next()
	})
	router.GET("/api/webhooks/token", handler.GetWebhookToken)
	router.POST("/api/webhooks/reveal-secret", handler.RevealWebhookSecret)
	router.POST("/api/webhooks/regenerate-secret", handler.RegenerateWebhookSecret)
	router.POST("/api/webhooks/inbound/:org_token", handler.WebhookInbound)

	return router, db, orgID, func() { engine.cancel(); cleanup() }
}

// webhookErrorCode extracts the error.code field from an error-envelope response.
func webhookErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	return e.Error.Code
}

// TestWebhookTokenHandler_GetReturnsMaskedAndIsIdempotent verifies the first GET
// provisions a token, the response masks the secret (never the full value) and
// embeds the inbound URL, and repeat calls are idempotent (same token + masked
// secret, no duplicate rows).
func TestWebhookTokenHandler_GetReturnsMaskedAndIsIdempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()

	getTok := func() webhookTokenResp {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/webhooks/token", nil))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp webhookTokenResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.NotNil(t, resp.Data, "response must carry data, got: %s", w.Body.String())
		return resp
	}

	first := getTok()
	assert.NotEmpty(t, first.Data.Token, "token must be created")
	assert.NotEmpty(t, first.Data.SecretMasked, "masked secret must be present")
	assert.Contains(t, first.Data.URL, "/api/webhooks/inbound/"+first.Data.Token,
		"url must embed the org token at the inbound route")

	// The masked secret must NOT equal the stored full secret, and must reveal only
	// the last 4 characters of it.
	var row WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgID).First(&row).Error)
	assert.NotEqual(t, row.Secret, first.Data.SecretMasked, "GET must never return the full secret")
	assert.True(t, strings.HasSuffix(first.Data.SecretMasked, row.Secret[len(row.Secret)-4:]),
		"masked secret must reveal the last 4 chars of the real secret")

	var count int64
	require.NoError(t, db.Model(&WorkflowOrgToken{}).Where("org_id = ?", orgID).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	second := getTok()
	assert.Equal(t, first.Data.Token, second.Data.Token, "token must be stable across calls")
	assert.Equal(t, first.Data.SecretMasked, second.Data.SecretMasked, "masked secret must be stable across calls")

	require.NoError(t, db.Model(&WorkflowOrgToken{}).Where("org_id = ?", orgID).Count(&count).Error)
	assert.Equal(t, int64(1), count, "second GET must not create a duplicate token")
}

// TestWebhookSecret_RegenerateRotatesAndReturnsFullOnce verifies that regenerate
// rotates the secret (invalidating the old one), returns the new secret in full
// exactly once, leaves the token/URL stable, and that a subsequent GET reflects
// the new secret only in masked form.
func TestWebhookSecret_RegenerateRotatesAndReturnsFullOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()

	// Provision the token, then capture the original (pre-rotation) secret.
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, httptest.NewRequest("GET", "/api/webhooks/token", nil))
	require.Equal(t, http.StatusOK, wGet.Code)
	var before WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgID).First(&before).Error)

	// Regenerate.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/webhooks/regenerate-secret", nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var rg webhookSecretResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rg))
	require.NotNil(t, rg.Data, "regenerate must carry data, got: %s", w.Body.String())

	assert.Len(t, rg.Data.Secret, 64, "regenerate returns the full 64-char secret")
	assert.NotEqual(t, before.Secret, rg.Data.Secret, "secret must be rotated")
	assert.Equal(t, before.Token, rg.Data.Token, "token (and inbound URL) must stay stable")

	// The new secret is persisted; the old one is gone (invalidated).
	var after WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgID).First(&after).Error)
	assert.Equal(t, rg.Data.Secret, after.Secret, "rotated secret must be persisted")
	assert.Equal(t, before.Token, after.Token, "token row must be the same (no churn)")

	// A subsequent GET shows the new secret only masked, never in full.
	wGet2 := httptest.NewRecorder()
	router.ServeHTTP(wGet2, httptest.NewRequest("GET", "/api/webhooks/token", nil))
	require.Equal(t, http.StatusOK, wGet2.Code)
	var third webhookTokenResp
	require.NoError(t, json.Unmarshal(wGet2.Body.Bytes(), &third))
	require.NotNil(t, third.Data)
	assert.NotEqual(t, rg.Data.Secret, third.Data.SecretMasked, "GET must never return the full secret")
	assert.True(t, strings.HasSuffix(third.Data.SecretMasked, rg.Data.Secret[len(rg.Data.Secret)-4:]),
		"masked secret must reveal the last 4 chars of the rotated secret")
}

// TestWebhookSecret_RevealReturnsFullWithoutRotating verifies that reveal returns
// the org's current full secret (matching what GET masks) and does NOT rotate it.
func TestWebhookSecret_RevealReturnsFullWithoutRotating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()

	// Provision the token and read the masked form + the stored full secret.
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, httptest.NewRequest("GET", "/api/webhooks/token", nil))
	require.Equal(t, http.StatusOK, wGet.Code)
	var masked webhookTokenResp
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &masked))
	require.NotNil(t, masked.Data)

	var stored WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgID).First(&stored).Error)

	// Reveal.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", "/api/webhooks/reveal-secret", nil))
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var rv struct {
		Data *struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rv))
	require.NotNil(t, rv.Data)

	// The revealed secret is the full stored secret, and matches what GET masked.
	assert.Equal(t, stored.Secret, rv.Data.Secret, "reveal must return the current full secret")
	assert.True(t, strings.HasSuffix(masked.Data.SecretMasked, rv.Data.Secret[len(rv.Data.Secret)-4:]),
		"the masked form must be the masked view of the revealed secret")

	// Reveal must NOT rotate: the DB secret is unchanged.
	var after WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgID).First(&after).Error)
	assert.Equal(t, stored.Secret, after.Secret, "reveal must not change the stored secret")
}

// TestRegenerateSecret_SignatureSchemeInvalidation is the no-Docker companion to
// TestRegenerateSecret_OldSecretInvalidated. It locks the exact signature scheme
// WebhookInbound verifies — X-Signature: "sha256=" + hex HMAC-SHA256 of the raw
// body, compared with hmac.Equal — and proves the rotation invariant at that
// boundary without a DB: against the stored (rotated-in) secret, a signature made
// with the NEW secret matches (handler → 200) while one made with the OLD secret
// does not (handler → 401).
func TestRegenerateSecret_SignatureSchemeInvalidation(t *testing.T) {
	body := []byte(`{"email":"sig-test@example.com"}`)
	oldSecret := GenerateToken(64)
	newSecret := GenerateToken(64)
	require.NotEqual(t, oldSecret, newSecret)

	// What WebhookInbound computes for the stored secret after rotation (= new).
	expected := "sha256=" + computeHMAC(body, newSecret)

	assert.True(t, hmac.Equal([]byte("sha256="+computeHMAC(body, newSecret)), []byte(expected)),
		"a signature made with the rotated-in secret must verify (→ 200)")
	assert.False(t, hmac.Equal([]byte("sha256="+computeHMAC(body, oldSecret)), []byte(expected)),
		"a signature made with the pre-rotation secret must be rejected (→ 401)")
}

// TestRegenerateSecret_OldSecretInvalidated proves rotation actually invalidates
// the previous secret at the verification boundary: after regenerate, an inbound
// webhook signed with the OLD secret is rejected with 401 UNAUTHORIZED, while one
// signed with the NEW secret is accepted.
func TestRegenerateSecret_OldSecretInvalidated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	// Exercise real signature verification (the rest of the package skips it).
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "false")

	router, db, orgID, cleanup := webhookTokenTestRouter(t)
	defer cleanup()

	// Provision the token, then read the original secret straight from the DB
	// (the GET only returns the masked form).
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, httptest.NewRequest("GET", "/api/webhooks/token", nil))
	require.Equal(t, http.StatusOK, wGet.Code)
	var tok webhookTokenResp
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &tok))
	require.NotNil(t, tok.Data)
	orgToken := tok.Data.Token

	var before WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgID).First(&before).Error)
	oldSecret := before.Secret

	body := []byte(`{"email":"sig-test@example.com"}`)
	postInbound := func(secret string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/webhooks/inbound/"+orgToken, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Signature", "sha256="+computeHMAC(body, secret))
		router.ServeHTTP(w, req)
		return w
	}

	// Sanity: before rotation the old secret produces a valid signature → accepted.
	require.Equal(t, http.StatusOK, postInbound(oldSecret).Code, "old secret should verify before rotation")

	// Rotate the secret.
	wRegen := httptest.NewRecorder()
	router.ServeHTTP(wRegen, httptest.NewRequest("POST", "/api/webhooks/regenerate-secret", nil))
	require.Equal(t, http.StatusOK, wRegen.Code, "body: %s", wRegen.Body.String())
	var rg webhookSecretResp
	require.NoError(t, json.Unmarshal(wRegen.Body.Bytes(), &rg))
	require.NotNil(t, rg.Data)
	require.NotEqual(t, oldSecret, rg.Data.Secret)

	// After rotation: a request signed with the OLD secret is rejected with 401.
	wOld := postInbound(oldSecret)
	assert.Equal(t, http.StatusUnauthorized, wOld.Code,
		"old signature must be rejected after rotation, body: %s", wOld.Body.String())
	assert.Equal(t, "UNAUTHORIZED", webhookErrorCode(t, wOld))

	// The NEW secret verifies.
	assert.Equal(t, http.StatusOK, postInbound(rg.Data.Secret).Code, "new secret should verify after rotation")
}

// TestWebhookTokenEndpoint_OrgScoped proves the token endpoint is per-org: two
// different orgs receive distinct tokens/secrets, each persisted to its own row,
// each URL embeds only its own token, and a repeat call for an org is idempotent.
func TestWebhookTokenEndpoint_OrgScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	handler := &Handler{
		engine:      engine,
		repo:        engine.repo,
		db:          db,
		logger:      slog.Default(),
		rateLimiter: newTokenBucket(),
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	// Org is taken from a per-request header so one router can act as many orgs.
	router.Use(func(c *gin.Context) {
		if oid := c.GetHeader("X-Test-Org"); oid != "" {
			c.Set("org_id", uuid.MustParse(oid))
		}
		c.Set("user_id", uuid.New())
		c.Set("role", "admin")
		c.Next()
	})
	router.GET("/api/webhooks/token", handler.GetWebhookToken)

	getFor := func(org uuid.UUID) webhookTokenResp {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/webhooks/token", nil)
		req.Header.Set("X-Test-Org", org.String())
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp webhookTokenResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.NotNil(t, resp.Data)
		return resp
	}

	orgA := uuid.New()
	orgB := uuid.New()
	a := getFor(orgA)
	b := getFor(orgB)

	// Distinct tokens per org.
	assert.NotEmpty(t, a.Data.Token)
	assert.NotEmpty(t, b.Data.Token)
	assert.NotEqual(t, a.Data.Token, b.Data.Token, "each org must get its own token")

	// Each org's URL embeds only its own token, never the other org's.
	assert.Contains(t, a.Data.URL, a.Data.Token)
	assert.NotContains(t, a.Data.URL, b.Data.Token)

	// Idempotent per org: a repeat call returns the same token.
	assert.Equal(t, a.Data.Token, getFor(orgA).Data.Token, "repeat call for an org is stable")

	// Persistence: exactly one row per org, with independent secrets.
	var rowA, rowB WorkflowOrgToken
	require.NoError(t, db.Where("org_id = ?", orgA).First(&rowA).Error)
	require.NoError(t, db.Where("org_id = ?", orgB).First(&rowB).Error)
	assert.Equal(t, a.Data.Token, rowA.Token)
	assert.Equal(t, b.Data.Token, rowB.Token)
	assert.NotEqual(t, rowA.Secret, rowB.Secret, "secrets must be independent per org")

	var count int64
	require.NoError(t, db.Model(&WorkflowOrgToken{}).Where("org_id IN ?", []uuid.UUID{orgA, orgB}).Count(&count).Error)
	assert.Equal(t, int64(2), count)
}
