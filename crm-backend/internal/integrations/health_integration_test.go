package integrations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The L6.1 health statements are the kind that ONLY real Postgres validates: each is
// a single UPDATE with a `prev` CTE whose whole purpose is to report the transition it
// just performed. A Go-level fake would assert the value we made up. These run the
// shipped SQL and check the edge, because the edge is the thing notifications hang off
// — a statement that silently always returns false is an alarm that never fires, and a
// statement that always returns true is a notification storm. Both look identical from
// Go.

func seedHealthConn(t *testing.T, db *gorm.DB, orgID uuid.UUID, status string, failures int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO integration_connections
		    (id, org_id, provider, external_account_id, external_account_label,
		     encrypted_credentials, key_version, status, consecutive_failures)
		VALUES (?, ?, 'fake', ?, 'Page One', 'sealed', 1, ?, ?)`,
		id, orgID, "acct-"+id.String()[:8], status, failures).Error)
	return id
}

func connRow(t *testing.T, db *gorm.DB, id uuid.UUID) (status string, failures int, synced *string) {
	t.Helper()
	var row struct {
		Status              string
		ConsecutiveFailures int
		LastSyncedAt        *string
	}
	require.NoError(t, db.Raw(
		`SELECT status, consecutive_failures, last_synced_at::text AS last_synced_at
		   FROM integration_connections WHERE id = ?`, id).Scan(&row).Error)
	return row.Status, row.ConsecutiveFailures, row.LastSyncedAt
}

// TestTouchSourceUsed_HealEdgeFiresExactlyOnce pins the recovery edge.
//
// The control half is the load-bearing one: an assertion that `healed` is true on the
// un-flip would pass just as well against a statement that returns true on EVERY
// success — which would mail every admin a "working again" notice on every lead
// forever. The second call is what proves the edge is an edge.
func TestTouchSourceUsed_HealEdgeFiresExactlyOnce(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	src := seedSource(t, repo, orgID).ID
	require.NoError(t, db.Exec(
		`UPDATE lead_sources SET status = 'error', consecutive_failures = 10 WHERE id = ?`, src).Error)

	healed, err := repo.TouchSourceUsed(ctx, src)
	require.NoError(t, err)
	require.True(t, healed, "the delivery that un-flips an error badge IS the recovery edge")

	healed, err = repo.TouchSourceUsed(ctx, src)
	require.NoError(t, err)
	require.False(t, healed, "a source that was already active did not recover — announcing it again would notify on every lead forever")

	var status string
	var failures int
	require.NoError(t, db.Raw(`SELECT status, consecutive_failures FROM lead_sources WHERE id = ?`, src).
		Row().Scan(&status, &failures))
	require.Equal(t, SourceStatusActive, status)
	require.Equal(t, 0, failures)
}

// TestTouchSourceUsed_NeverResurrectsDisabled pins the pre-existing rule the CTE
// rewrite had to preserve: an admin's explicit disable is not machine-reversible.
func TestTouchSourceUsed_NeverResurrectsDisabled(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)

	src := seedSource(t, repo, orgID).ID
	require.NoError(t, db.Exec(`UPDATE lead_sources SET status = 'disabled' WHERE id = ?`, src).Error)

	healed, err := repo.TouchSourceUsed(context.Background(), src)
	require.NoError(t, err)
	require.False(t, healed)

	var status string
	require.NoError(t, db.Raw(`SELECT status FROM lead_sources WHERE id = ?`, src).Row().Scan(&status))
	require.Equal(t, SourceStatusDisabled, status, "a disabled source must stay disabled")
}

// TestBumpConnectionFailure_RetryableDegradesAndCaps is the point of the whole
// connection half: sustained throttling must become visible, and must NOT become
// "reconnect your account".
func TestBumpConnectionFailure_RetryableDegradesAndCaps(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)
	conn := seedHealthConn(t, db, orgID, ConnStatusConnected, 0)

	// Below the threshold nothing is announced — a single throttled fetch is noise.
	for i := 1; i < connDegradeThreshold; i++ {
		band, err := repo.BumpConnectionFailure(ctx, orgID, conn, false, "throttled")
		require.NoError(t, err)
		require.Empty(t, band, "no transition before the threshold (failure %d)", i)
	}

	band, err := repo.BumpConnectionFailure(ctx, orgID, conn, false, "throttled")
	require.NoError(t, err)
	require.Equal(t, ConnStatusDegraded, band, "crossing the threshold is the announcement edge")

	status, failures, _ := connRow(t, db, conn)
	require.Equal(t, ConnStatusDegraded, status)
	require.Equal(t, connDegradeThreshold, failures)

	// The cap. Retryable failures must never escalate past degraded, because `error`
	// is the band whose copy tells the admin to reconnect — and a Graph outage is not
	// a credential problem. Ten more throttles must not change that.
	for i := 0; i < 10; i++ {
		band, err := repo.BumpConnectionFailure(ctx, orgID, conn, false, "throttled")
		require.NoError(t, err)
		require.Empty(t, band, "degraded is terminal for retryable failures — no re-announcement, no escalation")
	}
	status, _, _ = connRow(t, db, conn)
	require.Equal(t, ConnStatusDegraded, status, "a provider outage must never be reported as a dead credential")
}

// TestBumpConnectionFailure_PermanentErrorsImmediately — a dead token is dead now.
func TestBumpConnectionFailure_PermanentErrorsImmediately(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	conn := seedHealthConn(t, db, orgID, ConnStatusConnected, 0)
	band, err := repo.BumpConnectionFailure(ctx, orgID, conn, true, "token rejected")
	require.NoError(t, err)
	require.Equal(t, ConnStatusError, band, "no threshold for a permanent failure")

	// Already flagged: no second announcement, or a flapping token pages on every delivery.
	band, err = repo.BumpConnectionFailure(ctx, orgID, conn, true, "token rejected")
	require.NoError(t, err)
	require.Empty(t, band)

	// A degraded connection CAN still escalate to error — the cap is on the retryable
	// class, not on the row.
	conn2 := seedHealthConn(t, db, orgID, ConnStatusDegraded, connDegradeThreshold)
	band, err = repo.BumpConnectionFailure(ctx, orgID, conn2, true, "token rejected")
	require.NoError(t, err)
	require.Equal(t, ConnStatusError, band, "a real credential death must escalate out of degraded")
}

// TestBumpConnectionFailure_IsOrgScoped — the edge functions are reachable from a
// handler, so a missing org predicate would be a cross-tenant write.
func TestBumpConnectionFailure_IsOrgScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)
	conn := seedHealthConn(t, db, orgA, ConnStatusConnected, 0)

	band, err := repo.BumpConnectionFailure(context.Background(), orgB, conn, true, "token rejected")
	require.NoError(t, err)
	require.Empty(t, band)

	status, failures, _ := connRow(t, db, conn)
	require.Equal(t, ConnStatusConnected, status, "another org must not be able to flip this connection")
	require.Equal(t, 0, failures)
}

// TestEaseConnectionHealth_HealsAndDoesNotStampSync covers both halves of the ease
// contract, including the one it deliberately does NOT do.
func TestEaseConnectionHealth_HealsAndDoesNotStampSync(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	for _, from := range []string{ConnStatusDegraded, ConnStatusError} {
		conn := seedHealthConn(t, db, orgID, from, 7)
		healed, err := repo.EaseConnectionHealth(ctx, orgID, conn)
		require.NoError(t, err)
		require.True(t, healed, "recovering from %s is an edge", from)

		status, failures, synced := connRow(t, db, conn)
		require.Equal(t, ConnStatusConnected, status)
		require.Equal(t, 0, failures)
		// The whole reason last_synced_at is not written here: the heal fires after
		// FetchLead but BEFORE form resolution, so a connection quarantining 100% of
		// its deliveries reaches this line on every one. Stamping a sync time would
		// render the most confident green the UI has over a pipe producing no records.
		require.Nil(t, synced, "easing is not evidence that a lead became a record")

		healed, err = repo.EaseConnectionHealth(ctx, orgID, conn)
		require.NoError(t, err)
		require.False(t, healed, "an already-connected connection did not recover")
	}
}

// TestMarkConnectionSynced_StampsTerminalSuccess — the column L5 declared and no code
// path had ever written, so every connection card in the fleet read "never synced".
func TestMarkConnectionSynced_StampsTerminalSuccess(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgID := seedOrg(t, db)
	conn := seedHealthConn(t, db, orgID, ConnStatusConnected, 0)

	_, _, synced := connRow(t, db, conn)
	require.Nil(t, synced)

	require.NoError(t, repo.MarkConnectionSynced(context.Background(), conn))
	_, _, synced = connRow(t, db, conn)
	require.NotNil(t, synced, "a delivery that became a record is what last_synced_at means")
}
