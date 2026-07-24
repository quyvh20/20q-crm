package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/integrations/envelope"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// buildSignedRequest forges a Meta signed_request the way Meta itself would: HMAC-256
// of the base64url PAYLOAD STRING keyed by the app secret, joined `sig.payload`.
func buildSignedRequest(t *testing.T, appSecret string, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	payloadB64 := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write([]byte(payloadB64))
	sigB64 := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return sigB64 + "." + payloadB64
}

// The signed_request verifier is the whole security boundary of the deletion
// callback: it runs unauthenticated and decides whether a stranger can trigger a
// data purge. A forged signature must be refused.
func TestFacebookParseDeletionRequest(t *testing.T) {
	const secret = "super-secret"
	p := NewFacebookProvider("app123", secret, "", nil)

	got, err := p.ParseDeletionRequest(buildSignedRequest(t, secret, map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": "user-42",
	}))
	require.NoError(t, err)
	require.Equal(t, "user-42", got, "a valid signed_request yields its user_id")

	// Signed with a DIFFERENT secret — the HMAC must not match ours.
	_, err = p.ParseDeletionRequest(buildSignedRequest(t, "wrong-secret", map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": "user-42",
	}))
	require.Error(t, err, "a forged signature must be rejected")

	// Correctly signed but the wrong algorithm is refused (Meta always sends HMAC-SHA256).
	_, err = p.ParseDeletionRequest(buildSignedRequest(t, secret, map[string]any{
		"algorithm": "PLAINTEXT", "user_id": "user-42",
	}))
	require.Error(t, err, "an unexpected algorithm must be rejected")

	// No user_id — nothing to erase, and a silent success would hide that.
	_, err = p.ParseDeletionRequest(buildSignedRequest(t, secret, map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": "",
	}))
	require.Error(t, err, "a missing user_id must be rejected")

	// Structurally malformed (no dot) is refused before any HMAC work.
	_, err = p.ParseDeletionRequest("not-a-signed-request")
	require.Error(t, err)

	// The unimplemented default refuses the capability, so a provider without a
	// deletion callback can never be talked into a purge.
	_, err = UnimplementedProvider{}.ParseDeletionRequest("anything")
	require.ErrorIs(t, err, ErrProviderCapabilityUnsupported)
}

// The status page reflects a caller-controlled query param, so it must be
// HTML-escaped — otherwise it is a reflected-XSS on a public route.
func TestDeletionStatus_EscapesCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ConnectionHandler{}
	r := gin.New()
	r.GET("/api/integrations/data-deletion/status", h.DeletionStatus)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/data-deletion/status?code=%3Cscript%3Ealert(1)%3C/script%3E", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "<script>", "the reflected code must be HTML-escaped")
	require.Contains(t, w.Body.String(), "&lt;script&gt;")
}

// A missing signed_request is a 400 before any service work, so the handler tolerates
// a nil service on that path.
func TestDataDeletion_MissingSignedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &ConnectionHandler{}
	r := gin.New()
	r.POST("/api/integrations/providers/:provider/data-deletion", h.DataDeletion)

	req := httptest.NewRequest(http.MethodPost, "/api/integrations/providers/facebook/data-deletion", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// StampExternalUserID → FindConnectionsByExternalUser is the capture-and-match SQL the
// whole callback stands on; it references the ALTER-added external_user_id column, so
// it must run against the shipped migration.
func TestStampAndFindByExternalUser(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := NewRepository(db)
	org := seedOrg(t, db)

	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO integration_connections
		(id, org_id, provider, external_account_id, encrypted_credentials, status)
		VALUES (?, ?, 'facebook', 'page-1', 'blob', 'connected')`, id, org).Error)

	require.NoError(t, repo.StampExternalUserID(ctx, id, "fbuser-A"))

	found, err := repo.FindConnectionsByExternalUser(ctx, "facebook", "fbuser-A")
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, id, found[0].ID)
	require.Equal(t, "blob", found[0].EncryptedCredentials, "the query must load the credential blob for the teardown loop")

	none, err := repo.FindConnectionsByExternalUser(ctx, "facebook", "fbuser-B")
	require.NoError(t, err)
	require.Empty(t, none)

	// Stamping empty NULLs the id (the reconnect-with-failed-capture fix): the row must
	// no longer match the prior user, so a stale binding cannot over-delete a live token.
	require.NoError(t, repo.StampExternalUserID(ctx, id, ""))
	cleared, err := repo.FindConnectionsByExternalUser(ctx, "facebook", "fbuser-A")
	require.NoError(t, err)
	require.Empty(t, cleared, "an empty stamp must clear the binding")
}

// End to end: a verified deletion request purges exactly the requesting user's
// connections and leaves a bystander's untouched.
func TestHandleDataDeletion_PurgesTheUsersConnections(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()

	repo := NewRepository(db)
	ring, err := envelope.ParseKeyring(testKey)
	require.NoError(t, err)
	reg := NewRegistry()
	const appSecret = "test-app-secret"
	reg.Register(NewFacebookProvider("app123", appSecret, "", NewHTTPClient(nil)))
	svc := NewConnectionService(repo, envelope.NewCodec(ring), reg, "https://api.example", "https://app.example", nil)

	org := seedOrg(t, db)
	insertConn := func(page, extUser string) uuid.UUID {
		id := uuid.New()
		require.NoError(t, db.Exec(`INSERT INTO integration_connections
			(id, org_id, provider, external_account_id, encrypted_credentials, external_user_id, status)
			VALUES (?, ?, 'facebook', ?, 'sealed-blob', ?, 'connected')`, id, org, page, extUser).Error)
		return id
	}
	// A connection this user DISCONNECTED earlier: soft-deleted, but a bare soft-delete
	// leaves the sealed token and external_user_id intact — so erasure must still reach
	// it, exactly the gap the review caught.
	insertConnDeleted := func(page, extUser string) uuid.UUID {
		id := uuid.New()
		require.NoError(t, db.Exec(`INSERT INTO integration_connections
			(id, org_id, provider, external_account_id, encrypted_credentials, external_user_id, status, deleted_at)
			VALUES (?, ?, 'facebook', ?, 'sealed-blob', ?, 'connected', NOW())`, id, org, page, extUser).Error)
		return id
	}
	victim1 := insertConn("page-1", "fbuser-A")
	victim2 := insertConn("page-2", "fbuser-A")
	victimDeleted := insertConnDeleted("page-0", "fbuser-A")
	bystander := insertConn("page-3", "fbuser-B")

	code, err := svc.HandleDataDeletion(ctx, "facebook", buildSignedRequest(t, appSecret, map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": "fbuser-A",
	}))
	require.NoError(t, err)
	require.NotEmpty(t, code, "a confirmation code is returned to the provider")

	state := func(id uuid.UUID) (creds string, extNull, deleted bool) {
		require.NoError(t, db.Raw(
			`SELECT encrypted_credentials, external_user_id IS NULL, deleted_at IS NOT NULL
			   FROM integration_connections WHERE id = ?`, id).Row().Scan(&creds, &extNull, &deleted))
		return
	}

	for _, id := range []uuid.UUID{victim1, victim2, victimDeleted} {
		creds, extNull, deleted := state(id)
		require.Equal(t, "", creds, "the sealed credential must be destroyed, including on a pre-disconnected row")
		require.True(t, extNull, "external_user_id must be NULLed once the login data is erased")
		require.True(t, deleted, "the connection must be soft-deleted / claim released")
	}

	creds, extNull, deleted := state(bystander)
	require.Equal(t, "sealed-blob", creds, "another user's credential must be untouched")
	require.False(t, extNull)
	require.False(t, deleted)

	// A request that verifies but matches no connection is still a success (nothing to erase).
	code2, err := svc.HandleDataDeletion(ctx, "facebook", buildSignedRequest(t, appSecret, map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": "nobody",
	}))
	require.NoError(t, err)
	require.NotEmpty(t, code2)

	// A forged request never reaches a purge — it is a 400-class error.
	_, err = svc.HandleDataDeletion(ctx, "facebook", buildSignedRequest(t, "not-the-secret", map[string]any{
		"algorithm": "HMAC-SHA256", "user_id": "fbuser-B",
	}))
	require.Error(t, err)
	// And the bystander is STILL untouched after the forged attempt.
	creds, _, deleted = state(bystander)
	require.Equal(t, "sealed-blob", creds)
	require.False(t, deleted)

	require.False(t, strings.HasPrefix(svc.DeletionStatusURL(code), "/"), "status URL is absolute, config-derived")
	require.Contains(t, svc.DeletionStatusURL(code), "https://api.example/api/integrations/data-deletion/status?code=")
}
