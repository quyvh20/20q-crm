package repository

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func newDigestTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx,
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
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	// org_users is the membership-liveness source ListDailyDigestDue now joins.
	require.NoError(t, db.Exec(`CREATE TABLE org_users (
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		status VARCHAR(50) NOT NULL DEFAULT 'active',
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id)
	)`).Error)
	require.NoError(t, db.AutoMigrate(&domain.NotificationPreference{}))
	return db
}

// A preference row outlives its membership — neither workspace deletion nor member
// offboarding prunes notification_preferences — so ListDailyDigestDue must exclude a
// member who is no longer live, or a departed owner (and suspended/removed members)
// receive one last digest. This pins the liveness gate against real SQL; drop the
// LiveMemberExists clause and the three non-live rows all come back.
func TestListDailyDigestDue_ExcludesNonLiveMembers(t *testing.T) {
	db := newDigestTestDB(t)
	repo := NewNotificationPreferenceRepository(db)
	ctx := context.Background()
	org := uuid.New()

	addMember := func(u uuid.UUID, status string, deleted bool) {
		var del any
		if deleted {
			del = time.Now()
		}
		require.NoError(t, db.Exec(
			`INSERT INTO org_users (user_id, org_id, status, deleted_at) VALUES (?, ?, ?, ?)`,
			u, org, status, del).Error)
	}
	addDailyPref := func(u uuid.UUID) {
		require.NoError(t, db.Create(&domain.NotificationPreference{
			OrgID:       org,
			UserID:      u,
			EmailDigest: domain.DigestDaily,
			Overrides:   map[string]domain.ChannelPref{},
		}).Error)
	}

	live := uuid.New()
	suspended := uuid.New()
	deletedOrg := uuid.New()
	removed := uuid.New()

	addMember(live, "active", false)
	addMember(suspended, "suspended", false)
	addMember(deletedOrg, "deleted", true) // SoftDeleteOrganization stamps both status and deleted_at
	// removed: no org_users row at all — removal hard-deletes it

	for _, u := range []uuid.UUID{live, suspended, deletedOrg, removed} {
		addDailyPref(u)
	}

	due, err := repo.ListDailyDigestDue(ctx, time.Now())
	require.NoError(t, err)

	got := map[uuid.UUID]bool{}
	for _, p := range due {
		got[p.UserID] = true
	}
	require.True(t, got[live], "a live member's daily digest must be due")
	require.False(t, got[suspended], "a suspended member must not receive a digest")
	require.False(t, got[deletedOrg], "a deleted workspace's member must not receive a digest")
	require.False(t, got[removed], "a removed member (no org_users row) must not receive a digest")
}
