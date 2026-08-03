package integrations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// oauth_artifact_sweeper_test.go covers Repository.PurgeExpiredOAuthArtifacts
// (connection_repository.go:466) — the 30-minute background job (reaper.go:75) that HARD
// DELETEs from integration_oauth_states and integration_pending_connections across every
// org, with a 1-hour grace past expiry/consumption (oauthSweepRetain, reaper.go:38). It
// shipped with zero tests.
//
// The rows it deletes are live-handshake state: an integration_oauth_states row is what
// the provider callback matches `state` against, and an integration_pending_connections
// row holds the exchanged provider token (envelope-sealed) between the callback and the
// admin picking an account. Sweep one of those a few minutes early and a user in the
// middle of connecting Facebook gets "invalid state" and has to start over — with no
// error logged anywhere, because from the callback's point of view the state simply
// never existed.

// oauthArtifactFixture is one row in either custody table.
type oauthArtifactFixture struct {
	id         uuid.UUID
	label      string
	expiresIn  time.Duration // relative to now; negative = already expired
	consumedIn *time.Duration
	survives   bool
}

func consumedAgo(d time.Duration) *time.Duration { neg := -d; return &neg }

// insertOAuthState inserts an integration_oauth_states row. state_hash is UNIQUE, so it
// is derived from the row id.
func insertOAuthState(t *testing.T, db *gorm.DB, orgID uuid.UUID, f oauthArtifactFixture) {
	t.Helper()
	var consumed any
	if f.consumedIn != nil {
		consumed = time.Now().Add(*f.consumedIn)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO integration_oauth_states (id, org_id, user_id, provider, state_hash, code_verifier, expires_at, consumed_at)
		VALUES (?, ?, uuid_generate_v4(), 'facebook', ?, 'sealed-pkce-verifier', ?, ?)`,
		f.id, orgID, "state-"+f.id.String(), time.Now().Add(f.expiresIn), consumed).Error)
}

// insertPendingConnection inserts an integration_pending_connections row.
// selection_token_hash is UNIQUE, so it is derived from the row id.
func insertPendingConnection(t *testing.T, db *gorm.DB, orgID uuid.UUID, f oauthArtifactFixture, token string) {
	t.Helper()
	var consumed any
	if f.consumedIn != nil {
		consumed = time.Now().Add(*f.consumedIn)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO integration_pending_connections
			(id, org_id, user_id, provider, encrypted_token, candidate_accounts, selection_token_hash, expires_at, consumed_at)
		VALUES (?, ?, uuid_generate_v4(), 'facebook', ?, '[]'::jsonb, ?, ?, ?)`,
		f.id, orgID, token, "sel-"+f.id.String(), time.Now().Add(f.expiresIn), consumed).Error)
}

func artifactExists(t *testing.T, db *gorm.DB, table string, id uuid.UUID) bool {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM `+table+` WHERE id = ?`, id).Scan(&n).Error)
	return n > 0
}

// TestPurgeExpiredOAuthArtifacts_DeletesConsumedAndExpiredOnly walks the full matrix
// against BOTH tables, since the two DELETEs are separate statements that were written
// twice and could drift apart.
//
// The predicate is a two-clause OR — `(consumed_at IS NOT NULL AND consumed_at < cutoff)
// OR expires_at < cutoff` — and each clause is exercised in isolation: the consumed rows
// carry a FUTURE expires_at so only the consumption clause can select them, and the
// expired rows carry a NULL consumed_at so only the expiry clause can.
func TestPurgeExpiredOAuthArtifacts_DeletesConsumedAndExpiredOnly(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	org := seedOrg(t, db)

	fixtures := []oauthArtifactFixture{
		// Consumption clause, isolated: expiry is in the future for all three.
		{id: uuid.New(), label: "consumed 2h ago", expiresIn: 10 * time.Minute, consumedIn: consumedAgo(2 * time.Hour), survives: false},
		{id: uuid.New(), label: "consumed 1min ago", expiresIn: 10 * time.Minute, consumedIn: consumedAgo(time.Minute), survives: true},
		{id: uuid.New(), label: "never consumed, still valid", expiresIn: 10 * time.Minute, survives: true},
		// Expiry clause, isolated: consumed_at is NULL for both.
		{id: uuid.New(), label: "expired 2h ago", expiresIn: -2 * time.Hour, survives: false},
		{id: uuid.New(), label: "expired 5min ago, inside the grace", expiresIn: -5 * time.Minute, survives: true},
	}
	for _, f := range fixtures {
		insertOAuthState(t, db, org, f)
		insertPendingConnection(t, db, org, f, "sealed-token")
	}

	require.NoError(t, repo.PurgeExpiredOAuthArtifacts(context.Background(), time.Hour))

	for _, f := range fixtures {
		for _, table := range []string{"integration_oauth_states", "integration_pending_connections"} {
			assert.Equal(t, f.survives, artifactExists(t, db, table, f.id), "%s: %s", table, f.label)
		}
	}

	// The task brief listed "expires_at NULL" as a case. It cannot exist: both tables
	// declare expires_at NOT NULL (migration 000049, lines 90 and 114), so the
	// `expires_at < cutoff` clause can never be NULL-blind. Asserting the constraint is
	// the honest version of that case — if the column is ever made nullable, rows with a
	// NULL expiry become immortal and this fails.
	for _, table := range []string{"integration_oauth_states", "integration_pending_connections"} {
		var nullable string
		require.NoError(t, db.Raw(`SELECT is_nullable FROM information_schema.columns
			WHERE table_name = ? AND column_name = 'expires_at'`, table).Scan(&nullable).Error)
		assert.Equal(t, "NO", nullable,
			"%s.expires_at must stay NOT NULL — a NULL expiry would never satisfy `expires_at < cutoff` and the row would never be swept", table)
	}
}

// TestPurgeExpiredOAuthArtifacts_NeverTouchesAnInFlightHandshake is the user-visible
// failure this job could cause.
//
// The row is a pending connection ten minutes from expiry that nobody has consumed: an
// admin has finished the provider consent screen and is looking at the account picker.
// Deleting it breaks the flow with "invalid state" (or an expired selection token) and
// the exchanged provider token in encrypted_token is lost with it, forcing a full
// re-consent. So this asserts both that the row is still there AND that its ciphertext is
// byte-identical — a sweep that "cleaned up" secrets in place would be just as fatal and
// far harder to spot.
func TestPurgeExpiredOAuthArtifacts_NeverTouchesAnInFlightHandshake(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	org := seedOrg(t, db)

	const sealed = "v1.YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo.c2VhbGVkLXRva2Vu"
	pending := oauthArtifactFixture{id: uuid.New(), expiresIn: 10 * time.Minute}
	insertPendingConnection(t, db, org, pending, sealed)
	state := oauthArtifactFixture{id: uuid.New(), expiresIn: 10 * time.Minute}
	insertOAuthState(t, db, org, state)

	require.NoError(t, repo.PurgeExpiredOAuthArtifacts(context.Background(), time.Hour))

	require.True(t, artifactExists(t, db, "integration_pending_connections", pending.id),
		"an unconsumed, unexpired pending connection is a live OAuth flow")
	require.True(t, artifactExists(t, db, "integration_oauth_states", state.id),
		"and so is its unconsumed state row")

	var token string
	require.NoError(t, db.Raw(`SELECT encrypted_token FROM integration_pending_connections WHERE id = ?`, pending.id).Scan(&token).Error)
	assert.Equal(t, sealed, token, "the sealed provider token must be untouched, not just the row")

	var verifier string
	require.NoError(t, db.Raw(`SELECT code_verifier FROM integration_oauth_states WHERE id = ?`, state.id).Scan(&verifier).Error)
	assert.Equal(t, "sealed-pkce-verifier", verifier, "the PKCE verifier must survive intact for the callback to use")
}

// TestPurgeExpiredOAuthArtifacts_BoundaryRowSurvives pins the direction of both
// comparisons at strict `<`: a row that fell out of validity just INSIDE the grace stays.
//
// Both timestamps are stamped from a reference read immediately before the call, so the
// only drift against the pruner's own time.Now() (connection_repository.go:467, no
// injectable clock) is the latency of the UPDATEs — milliseconds. The ±1s band absorbs
// that. A flipped comparison, or a grace measured in the wrong unit, fails here.
func TestPurgeExpiredOAuthArtifacts_BoundaryRowSurvives(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	org := seedOrg(t, db)
	const retain = time.Hour

	edgeExpiry := oauthArtifactFixture{id: uuid.New(), expiresIn: time.Hour}
	pastExpiry := oauthArtifactFixture{id: uuid.New(), expiresIn: time.Hour}
	edgeConsumed := oauthArtifactFixture{id: uuid.New(), expiresIn: time.Hour, consumedIn: consumedAgo(time.Minute)}
	pastConsumed := oauthArtifactFixture{id: uuid.New(), expiresIn: time.Hour, consumedIn: consumedAgo(time.Minute)}
	for _, f := range []oauthArtifactFixture{edgeExpiry, pastExpiry, edgeConsumed, pastConsumed} {
		insertOAuthState(t, db, org, f)
		insertPendingConnection(t, db, org, f, "sealed-token")
	}

	ref := time.Now()
	stamp := func(col string, id uuid.UUID, at time.Time) {
		for _, table := range []string{"integration_oauth_states", "integration_pending_connections"} {
			require.NoError(t, db.Exec(`UPDATE `+table+` SET `+col+` = ? WHERE id = ?`, at, id).Error)
		}
	}
	stamp("expires_at", edgeExpiry.id, ref.Add(-retain).Add(time.Second))
	stamp("expires_at", pastExpiry.id, ref.Add(-retain).Add(-time.Second))
	stamp("consumed_at", edgeConsumed.id, ref.Add(-retain).Add(time.Second))
	stamp("consumed_at", pastConsumed.id, ref.Add(-retain).Add(-time.Second))

	require.NoError(t, repo.PurgeExpiredOAuthArtifacts(context.Background(), retain))

	for _, table := range []string{"integration_oauth_states", "integration_pending_connections"} {
		assert.True(t, artifactExists(t, db, table, edgeExpiry.id), "%s: expiry just inside the grace survives", table)
		assert.False(t, artifactExists(t, db, table, pastExpiry.id), "%s: expiry just outside the grace is swept", table)
		assert.True(t, artifactExists(t, db, table, edgeConsumed.id), "%s: consumption just inside the grace survives", table)
		assert.False(t, artifactExists(t, db, table, pastConsumed.id), "%s: consumption just outside the grace is swept", table)
	}
}

// TestPurgeExpiredOAuthArtifacts_SweepsBothTablesAndBothOrgs pins that this job is
// fleet-wide and covers both custody tables.
//
// PurgeExpiredOAuthArtifacts takes no orgID — unlike its sibling
// PurgeOAuthArtifactsForOrg, which the workspace teardown uses and which IS org-scoped
// (see TestPurgeOAuthArtifacts_IsOrgScoped). Confusing the two in either direction is a
// live hazard: an org filter leaking into the sweeper leaves every other org's tables
// growing, and dropping the filter from the teardown version would delete other
// workspaces' in-flight handshakes. This test is the sweeper half of that pair.
func TestPurgeExpiredOAuthArtifacts_SweepsBothTablesAndBothOrgs(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)

	type row struct {
		org   uuid.UUID
		stale oauthArtifactFixture
		live  oauthArtifactFixture
	}
	rows := []row{
		{org: orgA,
			stale: oauthArtifactFixture{id: uuid.New(), expiresIn: -3 * time.Hour},
			live:  oauthArtifactFixture{id: uuid.New(), expiresIn: 10 * time.Minute}},
		{org: orgB,
			stale: oauthArtifactFixture{id: uuid.New(), expiresIn: -3 * time.Hour},
			live:  oauthArtifactFixture{id: uuid.New(), expiresIn: 10 * time.Minute}},
	}
	for _, r := range rows {
		for _, f := range []oauthArtifactFixture{r.stale, r.live} {
			insertOAuthState(t, db, r.org, f)
			insertPendingConnection(t, db, r.org, f, "sealed-token")
		}
	}

	require.NoError(t, repo.PurgeExpiredOAuthArtifacts(context.Background(), time.Hour))

	for _, table := range []string{"integration_oauth_states", "integration_pending_connections"} {
		for _, r := range rows {
			assert.False(t, artifactExists(t, db, table, r.stale.id),
				"%s: the stale row must go in EVERY org, not just the first one", table)
			assert.True(t, artifactExists(t, db, table, r.live.id),
				"%s: the live row must stay in every org", table)
		}
		var total int64
		require.NoError(t, db.Raw(`SELECT count(*) FROM `+table).Scan(&total).Error)
		assert.Equal(t, int64(2), total, "%s: exactly the two live rows remain", table)
	}
}
