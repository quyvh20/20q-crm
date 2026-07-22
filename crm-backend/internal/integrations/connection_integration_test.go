package integrations

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"testing"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// errTestSubscribe is a sentinel a fakeProvider returns to simulate a failed
// leadgen subscription.
var errTestSubscribe = errors.New("subscribe failed (test)")

// These exercise the full connect flow against real Postgres, including the
// partial-unique claim index and the single-use custody guarantees — the parts
// the unit tests cannot reach because they are enforced by the schema. They skip
// in -short / when Docker is absent (newIntegrationsTestDB handles that).

func newTestConnStack(t *testing.T, db *gorm.DB, keyHex string) (*Repository, *ConnectionService, *fakeProvider) {
	t.Helper()
	repo := NewRepository(db)
	ring, err := envelope.ParseKeyring(keyHex)
	require.NoError(t, err)
	reg := NewRegistry()
	fp := &fakeProvider{
		info: ProviderInfo{Key: "fake", Label: "Fake", SupportsWebhooks: true},
		accounts: []Account{
			{ID: "acct1", Label: "Account One", Credentials: Credentials{AccessToken: "tok-acct1"}},
			{ID: "acct2", Label: "Account Two", Credentials: Credentials{AccessToken: "tok-acct2"}},
		},
	}
	reg.Register(fp)
	svc := NewConnectionService(repo, envelope.NewCodec(ring), reg, "https://api.example", "https://app.example", nil)
	return repo, svc, fp
}

// testKey is a deterministic 32-byte base64 key for the tests. Distinct from the
// zero key so a "wrong key" test has a real alternative.
const testKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE="

func stateFromAuthURL(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	require.NoError(t, err)
	state := u.Query().Get("state")
	require.NotEmpty(t, state, "auth URL must carry a state")
	return state
}

func TestConnectFlow_EndToEnd(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, svc, fp := newTestConnStack(t, db, testKey)

	org := seedOrg(t, db)
	user := uuid.New()

	// 1. Initiate.
	authURL, err := svc.StartConnect(ctx, org, user, "fake", "/settings/integrations")
	require.NoError(t, err)
	state := stateFromAuthURL(t, authURL)
	require.Contains(t, authURL, url.QueryEscape("https://api.example/api/integrations/providers/fake/callback"))

	// 2. Callback — org/user come from the state row, and the framework passes the
	//    code + the exact redirect URI it built at initiate.
	selToken, err := svc.HandleCallback(ctx, "fake", "auth-code-xyz", state)
	require.NoError(t, err)
	require.NotEmpty(t, selToken)
	require.Equal(t, "auth-code-xyz", fp.lastCode)
	require.Equal(t, "https://api.example/api/integrations/providers/fake/callback", fp.lastRedirect)

	// 3. Candidates — token-free, and only the owning caller may read them.
	provider, choices, err := svc.Candidates(ctx, org, user, selToken)
	require.NoError(t, err)
	require.Equal(t, "fake", provider)
	require.Len(t, choices, 2)
	require.Equal(t, "acct1", choices[0].ID)

	// 4. Select — a connection is created and its credentials open under its own id.
	conn, err := svc.SelectAccount(ctx, org, user, selToken, "acct1")
	require.NoError(t, err)
	require.Equal(t, "acct1", conn.ExternalAccountID)
	require.Equal(t, ConnStatusConnected, conn.Status)

	creds, err := svc.openCredentials(conn)
	require.NoError(t, err)
	require.Equal(t, "tok-acct1", creds.AccessToken)

	// The credential is NEVER stored in cleartext.
	var stored string
	require.NoError(t, db.Raw(`SELECT encrypted_credentials FROM integration_connections WHERE id = ?`, conn.ID).Scan(&stored).Error)
	require.NotContains(t, stored, "tok-acct1")

	// State is single-use: replaying it fails.
	_, err = svc.HandleCallback(ctx, "fake", "auth-code-xyz", state)
	require.Error(t, err)

	// The selection token was consumed by select; a second select fails.
	_, err = svc.SelectAccount(ctx, org, user, selToken, "acct1")
	require.Error(t, err)
}

func TestConnectFlow_SubscribesWebhookProvider(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo, svc, fp := newTestConnStack(t, db, testKey) // fp.info.SupportsWebhooks == true
	org := seedOrg(t, db)

	conn := connectOnce(t, ctx, svc, org, uuid.New(), "acct1")
	require.Equal(t, 1, fp.subscribeCalls, "a webhook-capable provider must be subscribed once on connect")

	fresh, err := repo.GetConnection(ctx, org, conn.ID)
	require.NoError(t, err)
	require.True(t, ViewOfConnection(fresh).Subscribed, "a successful subscribe must be recorded on the connection")
}

func TestConnectFlow_SubscribeFailureDoesNotUndoConnection(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo, svc, fp := newTestConnStack(t, db, testKey)
	fp.subscribeErr = errTestSubscribe
	org := seedOrg(t, db)

	conn := connectOnce(t, ctx, svc, org, uuid.New(), "acct1")
	// The connection still exists (the credential is stored) ...
	require.Equal(t, ConnStatusConnected, conn.Status)
	fresh, err := repo.GetConnection(ctx, org, conn.ID)
	require.NoError(t, err)
	// ... but it is flagged not-subscribed with a reason, so the card can warn.
	view := ViewOfConnection(fresh)
	require.False(t, view.Subscribed, "a failed subscribe must not read as subscribed")
	require.NotEmpty(t, view.LastError, "a failed subscribe must leave a reason on the connection")
}

func TestConnectFlow_ReconnectUpserts(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, svc, fp := newTestConnStack(t, db, testKey)
	org := seedOrg(t, db)
	user := uuid.New()

	first := connectOnce(t, ctx, svc, org, user, "acct1")

	// Refresh the account's credentials and reconnect the same page in the same org.
	fp.accounts[0].Credentials.AccessToken = "tok-acct1-rotated"
	second := connectOnce(t, ctx, svc, org, user, "acct1")

	require.Equal(t, first.ID, second.ID, "same-org reconnect must upsert the same row, not create a second")

	creds, err := svc.openCredentials(second)
	require.NoError(t, err)
	require.Equal(t, "tok-acct1-rotated", creds.AccessToken, "reconnect must refresh the stored credential")
}

func TestConnectFlow_CrossOrgClaimRefused(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, svc, _ := newTestConnStack(t, db, testKey)

	orgA := seedOrg(t, db)
	orgB := seedOrg(t, db)
	userA := uuid.New()
	userB := uuid.New()

	connectOnce(t, ctx, svc, orgA, userA, "acct1")

	// Org B runs the whole flow and tries to select the same page → refused.
	authURL, err := svc.StartConnect(ctx, orgB, userB, "fake", "")
	require.NoError(t, err)
	selToken, err := svc.HandleCallback(ctx, "fake", "code", stateFromAuthURL(t, authURL))
	require.NoError(t, err)
	_, err = svc.SelectAccount(ctx, orgB, userB, selToken, "acct1")
	require.ErrorIs(t, err, ErrAccountClaimedElsewhere)
}

func TestConnectFlow_CrossOrgReclaimAfterDisconnect(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, svc, _ := newTestConnStack(t, db, testKey)

	orgA := seedOrg(t, db)
	orgB := seedOrg(t, db)

	connA := connectOnce(t, ctx, svc, orgA, uuid.New(), "acct1")
	require.NoError(t, svc.Disconnect(ctx, orgA, connA.ID))

	// After A disconnects (claim released), B can claim the page.
	connB := connectOnce(t, ctx, svc, orgB, uuid.New(), "acct1")
	require.Equal(t, orgB, connB.OrgID)
	require.NotEqual(t, connA.ID, connB.ID)
}

func TestConnectFlow_CustodyWrongUserRejected(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, svc, _ := newTestConnStack(t, db, testKey)
	org := seedOrg(t, db)
	starter := uuid.New()
	attacker := uuid.New()

	authURL, err := svc.StartConnect(ctx, org, starter, "fake", "")
	require.NoError(t, err)
	selToken, err := svc.HandleCallback(ctx, "fake", "code", stateFromAuthURL(t, authURL))
	require.NoError(t, err)

	// A different user in the same org must not read the candidates or select.
	_, _, err = svc.Candidates(ctx, org, attacker, selToken)
	require.Error(t, err)
	_, err = svc.SelectAccount(ctx, org, attacker, selToken, "acct1")
	require.Error(t, err)

	// The legitimate starter still can (peek did not consume).
	_, err = svc.SelectAccount(ctx, org, starter, selToken, "acct1")
	require.NoError(t, err)
}

func TestConnectFlow_Canary(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	_, svc, _ := newTestConnStack(t, db, testKey)
	org := seedOrg(t, db)

	// Empty install passes.
	require.NoError(t, svc.Canary(ctx))

	connectOnce(t, ctx, svc, org, uuid.New(), "acct1")
	require.NoError(t, svc.Canary(ctx), "the configured key must open the row it just wrote")

	// A DIFFERENT key must fail the canary loudly — this is the rotated-variable /
	// dropped-keyring detection.
	otherRing, err := envelope.ParseKeyring(base64OfByte(2))
	require.NoError(t, err)
	otherSvc := NewConnectionService(NewRepository(db), envelope.NewCodec(otherRing), NewRegistry(), "", "", nil)
	require.Error(t, otherSvc.Canary(ctx), "a wrong key must not silently pass")
}

func TestConnectFlow_UnknownProvider(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	_, svc, _ := newTestConnStack(t, db, testKey)
	// Deliberately a name no adapter will ever claim. This read "tiktok" until L7.5
	// shipped one — at which point the test still passed (it builds its own empty
	// registry) while no longer testing what its message says.
	_, err := svc.StartConnect(context.Background(), seedOrg(t, db), uuid.New(), "no-such-provider", "")
	require.Error(t, err, "an unregistered provider must be refused")
}

func TestConnectFlow_PKCEVerifierRoundTrips(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	ctx := context.Background()
	repo := NewRepository(db)
	ring, err := envelope.ParseKeyring(testKey)
	require.NoError(t, err)
	reg := NewRegistry()
	fp := &fakeProvider{
		info:     ProviderInfo{Key: "pkce", Label: "PKCE", UsesPKCE: true},
		accounts: []Account{{ID: "a", Label: "A", Credentials: Credentials{AccessToken: "t"}}},
	}
	reg.Register(fp)
	svc := NewConnectionService(repo, envelope.NewCodec(ring), reg, "https://api.example", "https://app.example", nil)

	org := seedOrg(t, db)
	authURL, err := svc.StartConnect(ctx, org, uuid.New(), "pkce", "")
	require.NoError(t, err)
	require.Contains(t, authURL, "code_challenge=")

	_, err = svc.HandleCallback(ctx, "pkce", "code", stateFromAuthURL(t, authURL))
	require.NoError(t, err)
	require.NotEmpty(t, fp.lastVerifier, "the sealed PKCE verifier must be decrypted and handed to the provider")
}

// connectOnce runs the whole connect flow and returns the resulting connection.
func connectOnce(t *testing.T, ctx context.Context, svc *ConnectionService, org, user uuid.UUID, accountID string) *IntegrationConnection {
	t.Helper()
	authURL, err := svc.StartConnect(ctx, org, user, "fake", "")
	require.NoError(t, err)
	selToken, err := svc.HandleCallback(ctx, "fake", "code", stateFromAuthURL(t, authURL))
	require.NoError(t, err)
	conn, err := svc.SelectAccount(ctx, org, user, selToken, accountID)
	require.NoError(t, err)
	return conn
}

// base64OfByte builds a 32-byte key whose first byte is b, so tests can produce a
// second, definitely-different key.
func base64OfByte(b byte) string {
	raw := make([]byte, envelope.KeySize)
	raw[0] = b
	return base64.StdEncoding.EncodeToString(raw)
}
