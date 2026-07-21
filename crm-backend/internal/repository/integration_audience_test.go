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

// The recipient query is the one piece of L6.1 that is silent when it is wrong: it
// returns a shorter list, every notification it does send looks correct, and nothing
// anywhere reports that somebody was left out. So the cases it must get right are
// pinned against real SQL rather than reasoned about.
//
// The owner case is the whole reason this file exists. The owner role deliberately
// holds NO role_permissions rows — it bypasses capability checks so an empty or
// half-seeded permission table cannot lock an owner out of their own workspace — so
// the natural query ("roles granting integrations.manage") silently excludes the one
// person guaranteed to care that lead capture stopped. That omission would pass every
// test written against a seeded org.

func newAudienceTestDB(t *testing.T) *gorm.DB {
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

	require.NoError(t, db.Exec(`CREATE TABLE org_users (
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		role_id UUID,
		status VARCHAR(50) NOT NULL DEFAULT 'active',
		joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id)
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE roles (
		id UUID PRIMARY KEY,
		org_id UUID,
		name VARCHAR(255) NOT NULL,
		is_system BOOLEAN NOT NULL DEFAULT FALSE,
		is_owner BOOLEAN NOT NULL DEFAULT FALSE
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE role_permissions (
		role_id UUID NOT NULL,
		permission_code VARCHAR(255) NOT NULL,
		org_id UUID,
		PRIMARY KEY (role_id, permission_code)
	)`).Error)
	return db
}

func addRole(t *testing.T, db *gorm.DB, name string, isSystem, isOwner bool, caps ...string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO roles (id, name, is_system, is_owner) VALUES (?, ?, ?, ?)`,
		id, name, isSystem, isOwner).Error)
	for _, c := range caps {
		require.NoError(t, db.Exec(
			`INSERT INTO role_permissions (role_id, permission_code) VALUES (?, ?)`, id, c).Error)
	}
	return id
}

func addMember(t *testing.T, db *gorm.DB, orgID, roleID uuid.UUID, status string, deleted bool) uuid.UUID {
	t.Helper()
	userID := uuid.New()
	var deletedAt *time.Time
	if deleted {
		now := time.Now()
		deletedAt = &now
	}
	require.NoError(t, db.Exec(
		`INSERT INTO org_users (user_id, org_id, role_id, status, deleted_at) VALUES (?, ?, ?, ?, ?)`,
		userID, orgID, roleID, status, deletedAt).Error)
	return userID
}

func TestIntegrationAdmins_IncludesOwnerAndCapabilityHolders(t *testing.T) {
	db := newAudienceTestDB(t)
	r := NewIntegrationAudienceReader(db)
	orgID := uuid.New()

	// The owner role holds NO capability rows, by design.
	ownerRole := addRole(t, db, domain.RoleOwner, true, true)
	// A role that was created before the is_owner backfill: identified by the
	// (is_system, name) fallback that domain.IsOwnerRole also honours.
	legacyOwnerRole := addRole(t, db, domain.RoleOwner, true, false)
	adminRole := addRole(t, db, "admin", true, false, domain.CapIntegrationsManage, domain.CapMembersManage)
	repRole := addRole(t, db, "sales_rep", true, false, domain.CapMembersInvite)

	owner := addMember(t, db, orgID, ownerRole, domain.StatusActive, false)
	legacyOwner := addMember(t, db, orgID, legacyOwnerRole, domain.StatusActive, false)
	admin := addMember(t, db, orgID, adminRole, domain.StatusActive, false)
	rep := addMember(t, db, orgID, repRole, domain.StatusActive, false)

	got, err := r.IntegrationAdmins(context.Background(), orgID)
	require.NoError(t, err)

	require.ElementsMatch(t, []uuid.UUID{owner, legacyOwner, admin}, got)
	require.NotContains(t, got, rep, "a role without integrations.manage must not be notified")
}

func TestIntegrationAdmins_ExcludesNonLiveMembers(t *testing.T) {
	db := newAudienceTestDB(t)
	r := NewIntegrationAudienceReader(db)
	orgID := uuid.New()
	adminRole := addRole(t, db, "admin", true, false, domain.CapIntegrationsManage)

	active := addMember(t, db, orgID, adminRole, domain.StatusActive, false)
	// Every one of these holds the capability, and every one of them must be skipped.
	// ListMembersByOrgID applies no filter at all, so a fan-out built on it would mail
	// all four.
	addMember(t, db, orgID, adminRole, domain.StatusSuspended, false)
	addMember(t, db, orgID, adminRole, domain.StatusInvited, false)
	addMember(t, db, orgID, adminRole, domain.StatusDeleted, false)
	addMember(t, db, orgID, adminRole, domain.StatusActive, true) // org soft-deleted

	got, err := r.IntegrationAdmins(context.Background(), orgID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{active}, got)
}

// TestIntegrationAdmins_DeletedWorkspaceHasNoAudience — workspace deletion stamps
// org_users.deleted_at on every member, so a deleted workspace resolves to an empty
// audience without the query knowing anything about organizations.
func TestIntegrationAdmins_DeletedWorkspaceHasNoAudience(t *testing.T) {
	db := newAudienceTestDB(t)
	r := NewIntegrationAudienceReader(db)
	orgID := uuid.New()
	adminRole := addRole(t, db, "admin", true, false, domain.CapIntegrationsManage)
	addMember(t, db, orgID, adminRole, domain.StatusActive, false)

	// What SoftDeleteOrganization does to org_users.
	require.NoError(t, db.Exec(
		`UPDATE org_users SET status = 'deleted', deleted_at = NOW() WHERE org_id = ?`, orgID).Error)

	got, err := r.IntegrationAdmins(context.Background(), orgID)
	require.NoError(t, err)
	require.Empty(t, got)
}

// TestIntegrationAdmins_IsOrgScoped — a notification fan-out that crossed orgs would
// leak one workspace's source names into another's notification bell.
func TestIntegrationAdmins_IsOrgScoped(t *testing.T) {
	db := newAudienceTestDB(t)
	r := NewIntegrationAudienceReader(db)
	orgA, orgB := uuid.New(), uuid.New()
	adminRole := addRole(t, db, "admin", true, false, domain.CapIntegrationsManage)

	inA := addMember(t, db, orgA, adminRole, domain.StatusActive, false)
	addMember(t, db, orgB, adminRole, domain.StatusActive, false)

	got, err := r.IntegrationAdmins(context.Background(), orgA)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{inA}, got)
}
