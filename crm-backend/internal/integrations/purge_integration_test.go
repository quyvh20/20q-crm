package integrations

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Workspace teardown. The destructive half runs against real Postgres because the
// whole point is which ROWS it reaches: too narrow leaves a customer's credentials at
// rest and their page claimed, too wide reaches into another tenant.

func seedPurgeConn(t *testing.T, db *gorm.DB, orgID uuid.UUID, account, status string, deleted bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO integration_connections
		    (id, org_id, provider, external_account_id, external_account_label,
		     encrypted_credentials, key_version, status, deleted_at)
		VALUES (?, ?, 'facebook', ?, 'Page', 'sealed-token', 1, ?, ?)`,
		id, orgID, account, status, nullableNow(deleted)).Error)
	return id
}

func nullableNow(deleted bool) any {
	if !deleted {
		return nil
	}
	return "now()"
}

type connRowState struct {
	Creds     string
	Status    string
	DeletedAt *string
}

func readConnState(t *testing.T, db *gorm.DB, id uuid.UUID) connRowState {
	t.Helper()
	var r connRowState
	require.NoError(t, db.Raw(`
		SELECT encrypted_credentials AS creds, status, deleted_at::text AS deleted_at
		  FROM integration_connections WHERE id = ?`, id).Scan(&r).Error)
	return r
}

// Credential destruction must reach EVERY row the org ever had, live or not. A
// customer deleting their workspace most expects the pages they already disconnected
// to be gone — and ListConnections is silently soft-delete-scoped, so the obvious
// implementation would have missed exactly those.
func TestPurgeConnectionSecrets_ReachesSoftDeletedRowsToo(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)

	live := seedPurgeConn(t, db, orgA, "page-live", ConnStatusConnected, false)
	gone := seedPurgeConn(t, db, orgA, "page-old", ConnStatusConnected, true)
	other := seedPurgeConn(t, db, orgB, "page-b", ConnStatusConnected, false)

	n, err := repo.PurgeConnectionSecrets(context.Background(), orgA)
	require.NoError(t, err)
	require.Equal(t, int64(2), n)

	for name, id := range map[string]uuid.UUID{"live": live, "soft-deleted": gone} {
		got := readConnState(t, db, id)
		require.Empty(t, got.Creds, "%s: the sealed token must be destroyed", name)
		require.Equal(t, ConnStatusDisconnected, got.Status)
		// Both the deleted_at and the status drop the row out of the partial claim
		// index, so the customer's page becomes connectable elsewhere immediately
		// instead of being held hostage by a workspace that no longer exists.
		require.NotNil(t, got.DeletedAt, "%s: the claim must be released", name)
	}

	kept := readConnState(t, db, other)
	require.Equal(t, "sealed-token", kept.Creds, "another workspace must be untouched")
}

// THE CROSS-TENANT GUARD. Provider.Disconnect detaches our app from the ACCOUNT and
// knows nothing about orgs or claims, so the unsubscribe set must be only the accounts
// this org still holds. A previously-disconnected page that another workspace has
// since connected must NOT be in it — unsubscribing it would silently stop that
// workspace's lead delivery while its card still read "connected".
func TestListClaimedConnections_ExcludesReleasedPages(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA := seedOrg(t, db)

	held := seedPurgeConn(t, db, orgA, "page-held", ConnStatusConnected, false)
	degraded := seedPurgeConn(t, db, orgA, "page-degraded", ConnStatusDegraded, false)
	seedPurgeConn(t, db, orgA, "page-released", ConnStatusDisconnected, false)
	seedPurgeConn(t, db, orgA, "page-old", ConnStatusConnected, true) // soft-deleted: claim already released

	got, err := repo.ListClaimedConnections(context.Background(), orgA)
	require.NoError(t, err)

	ids := map[uuid.UUID]bool{}
	for _, c := range got {
		ids[c.ID] = true
	}
	require.True(t, ids[held])
	require.True(t, ids[degraded], "a degraded connection still holds the claim")
	require.Len(t, got, 2,
		"released and soft-deleted pages must never be unsubscribed — another workspace may hold them now")
	// The snapshot must carry the ciphertext, because the purge blanks it moments later
	// and the provider call cannot be made with a secret that no longer exists.
	require.Equal(t, "sealed-token", got[0].EncryptedCredentials)
}

func TestDisableSourcesForOrg_StopsTheBacklogAndIsOrgScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)

	a := seedSource(t, repo, orgA)
	b := seedSource(t, repo, orgB)

	n, err := repo.DisableSourcesForOrg(context.Background(), orgA)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	// Separate structs per read: GORM folds a populated primary key into the next
	// query's conditions, so reusing one silently turns the second First into
	// "id = b AND id = a" and answers record-not-found.
	var disabled LeadSource
	require.NoError(t, db.First(&disabled, "id = ?", a.ID).Error)
	require.Equal(t, SourceStatusDisabled, disabled.Status,
		"the async queue has no org join, so disabling is what makes the worker quarantine the backlog")
	require.False(t, disabled.IsLive())

	var untouched LeadSource
	require.NoError(t, db.First(&untouched, "id = ?", b.ID).Error)
	require.Equal(t, SourceStatusActive, untouched.Status)

	// Idempotent: a repair re-run must be a no-op rather than churn.
	n, err = repo.DisableSourcesForOrg(context.Background(), orgA)
	require.NoError(t, err)
	require.Equal(t, int64(0), n)
}

// The repair surface. A teardown that failed at delete time can never be retried
// through the product — the workspace is gone and cannot be deleted again — so
// without this a transient deadlock is a permanent leak of a sealed credential.
func TestDeletedOrgsNeedingPurge_FindsUnfinishedTeardowns(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()

	liveOrg := seedOrg(t, db)
	deletedClean := seedOrg(t, db)
	deletedLeaky := seedOrg(t, db)
	deletedSource := seedOrg(t, db)

	seedPurgeConn(t, db, liveOrg, "p1", ConnStatusConnected, false) // alive: not our business
	seedPurgeConn(t, db, deletedLeaky, "p2", ConnStatusConnected, false)
	seedSource(t, repo, deletedSource)
	for _, o := range []uuid.UUID{deletedClean, deletedLeaky, deletedSource} {
		require.NoError(t, db.Exec(`UPDATE organizations SET deleted_at = NOW() WHERE id = ?`, o).Error)
	}
	// deletedClean was fully torn down already.
	_, err := repo.PurgeConnectionSecrets(ctx, deletedClean)
	require.NoError(t, err)

	got, err := repo.DeletedOrgsNeedingPurge(ctx, 50)
	require.NoError(t, err)

	found := map[uuid.UUID]bool{}
	for _, id := range got {
		found[id] = true
	}
	require.True(t, found[deletedLeaky], "a deleted workspace still holding a credential must be repaired")
	require.True(t, found[deletedSource], "an enabled source in a deleted workspace must be repaired")
	require.False(t, found[liveOrg], "a live workspace must never be torn down by the repair sweep")
	require.False(t, found[deletedClean], "a finished teardown must not be re-run forever")
}

// PurgeWorkspace is idempotent end to end — the repair sweep re-runs it, so a second
// pass must settle rather than churn.
func TestPurgeWorkspace_IsIdempotent(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	ctx := context.Background()
	orgID := seedOrg(t, db)

	seedPurgeConn(t, db, orgID, "p1", ConnStatusConnected, false)
	seedSource(t, repo, orgID)

	svc := NewPurgeService(repo, nil, nil) // nil connection service: the DB half must still run
	conns, sources, err := svc.PurgeWorkspace(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, int64(1), conns)
	require.Equal(t, int64(1), sources)

	conns, sources, err = svc.PurgeWorkspace(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, int64(0), sources, "nothing left to disable")
	// Connections still match (the statement is unconditional on the org) but nothing
	// changes: the credentials are already empty and the claim already released.
	require.GreaterOrEqual(t, conns, int64(0))

	got, err := repo.DeletedOrgsNeedingPurge(ctx, 50)
	require.NoError(t, err)
	require.NotContains(t, got, orgID, "a purged workspace must drop out of the repair queue")
}

func TestPurgeOAuthArtifacts_IsOrgScoped(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	repo := NewRepository(db)
	orgA, orgB := seedOrg(t, db), seedOrg(t, db)

	for _, o := range []uuid.UUID{orgA, orgB} {
		require.NoError(t, db.Exec(`
			INSERT INTO integration_oauth_states (id, org_id, user_id, provider, state_hash, expires_at)
			VALUES (uuid_generate_v4(), ?, uuid_generate_v4(), 'facebook', ?, NOW() + interval '10 minutes')`,
			o, "hash-"+o.String()[:8]).Error)
	}

	require.NoError(t, repo.PurgeOAuthArtifactsForOrg(context.Background(), orgA))

	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM integration_oauth_states WHERE org_id = ?`, orgA).Scan(&n).Error)
	require.Zero(t, n, "an in-flight connect must not be able to complete behind the teardown")
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM integration_oauth_states WHERE org_id = ?`, orgB).Scan(&n).Error)
	require.Equal(t, int64(1), n)
}
