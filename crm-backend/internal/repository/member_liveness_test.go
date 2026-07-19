package repository

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The liveness rule exists twice — ActiveMemberSQL and domain.OrgUser.IsLive — so
// that callers holding a loaded row and callers filtering a list can ask the same
// question. Two expressions of one rule drift silently unless something forces them
// through the same cases, which is what this file is.
//
// livenessCases is that shared fixture set. Each row is a real membership state the
// product can produce.
var livenessCases = []struct {
	name   string
	status string
	// deleted marks the org_users row's deleted_at (org-level soft delete).
	deleted bool
	// absent models member REMOVAL, which hard-deletes the row rather than
	// tombstoning it — so there is no row for either twin to inspect.
	absent bool
	want   bool
}{
	{name: "active member", status: domain.StatusActive, want: true},
	{name: "suspended member", status: domain.StatusSuspended, want: false},
	{name: "invited but not yet joined", status: domain.StatusInvited, want: false},
	{name: "status deleted", status: domain.StatusDeleted, want: false},
	{name: "active but soft-deleted row", status: domain.StatusActive, deleted: true, want: false},
	{name: "suspended and soft-deleted", status: domain.StatusSuspended, deleted: true, want: false},
	{name: "removed member (no row at all)", absent: true, want: false},
}

func TestOrgUserIsLive_GoHalf(t *testing.T) {
	for _, c := range livenessCases {
		t.Run(c.name, func(t *testing.T) {
			if c.absent {
				// A nil receiver is how the Go half sees a removed member, and it
				// must answer false rather than panic.
				var ou *domain.OrgUser
				assert.False(t, ou.IsLive())
				return
			}
			ou := &domain.OrgUser{UserID: uuid.New(), OrgID: uuid.New(), Status: c.status}
			if c.deleted {
				now := time.Now()
				ou.DeletedAt = &now
			}
			assert.Equal(t, c.want, ou.IsLive())
		})
	}
}

func TestActiveMemberSQL_AgreesWithIsLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
		tcpostgres.WithSQLDriver("pgx"),
	)
	if err != nil {
		t.Skipf("Docker not available — skipping integration test: %v", err)
	}
	defer func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("warning: failed to terminate container: %v", err)
		}
	}()

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE TABLE org_users (
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		role_id UUID,
		status VARCHAR(50) NOT NULL DEFAULT 'active',
		joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id)
	)`).Error)

	orgID := uuid.New()
	probe := make([]uuid.UUID, 0, len(livenessCases))
	wantLive := map[uuid.UUID]bool{}

	for _, c := range livenessCases {
		userID := uuid.New()
		probe = append(probe, userID)
		wantLive[userID] = c.want

		if c.absent {
			continue // removal hard-deletes the row; nothing to insert
		}
		var deletedAt *time.Time
		if c.deleted {
			now := time.Now()
			deletedAt = &now
		}
		require.NoError(t, db.Exec(
			`INSERT INTO org_users (user_id, org_id, role_id, status, deleted_at) VALUES (?, ?, ?, ?, ?)`,
			userID, orgID, uuid.New(), c.status, deletedAt,
		).Error)
	}

	got, err := ActiveMemberIDs(ctx, db, orgID, probe)
	require.NoError(t, err)

	for i, c := range livenessCases {
		userID := probe[i]
		assert.Equal(t, c.want, got[userID],
			"SQL half disagrees with the Go half on %q — the twins have drifted", c.name)
	}

	// Same question, other direction: a member of a DIFFERENT org is not live here,
	// however healthy their own membership is.
	otherOrg := uuid.New()
	stranger := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO org_users (user_id, org_id, role_id, status) VALUES (?, ?, ?, 'active')`,
		stranger, otherOrg, uuid.New(),
	).Error)

	got, err = ActiveMemberIDs(ctx, db, orgID, []uuid.UUID{stranger})
	require.NoError(t, err)
	assert.False(t, got[stranger], "membership must be scoped to the org being asked about")
}

func TestActiveMemberIDs_EmptyProbeIsNotAnError(t *testing.T) {
	// Postgres rejects `IN ()`, so the empty case must short-circuit before it ever
	// reaches the driver — a nil DB here proves no query is issued.
	live, err := ActiveMemberIDs(context.Background(), nil, uuid.New(), nil)
	require.NoError(t, err)
	assert.Empty(t, live)
}
