package repository

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// setupPermissions stands up the minimal prerequisites the 000017 FKs need
// (organizations, roles, users, custom_object_defs), runs the real up migration,
// then inserts one org and the five system roles. Returns the org id, a
// role-name → id map, and the repository. Mirrors applyRegistrySchema
// (object_registry_repository_test.go).
func setupPermissions(t *testing.T) (orgID uuid.UUID, roleIDs map[string]uuid.UUID, repo domain.PermissionRepository) {
	t.Helper()
	db, cleanup := startPostgres(t)
	t.Cleanup(cleanup)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), full_name varchar, email varchar)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS roles (id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid, name varchar NOT NULL, is_system boolean NOT NULL DEFAULT false)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid NOT NULL, slug varchar NOT NULL, deleted_at timestamptz)`).Error)

	runMigrationFile(t, db, "000017_object_security.up.sql")

	orgID = uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)

	roleIDs = map[string]uuid.UUID{}
	for _, name := range []string{domain.RoleOwner, domain.RoleAdmin, domain.RoleManager, domain.RoleSales, domain.RoleViewer} {
		id := uuid.New()
		require.NoError(t, db.Exec(`INSERT INTO roles (id, org_id, name, is_system) VALUES (?, NULL, ?, true)`, id, name).Error)
		roleIDs[name] = id
	}

	return orgID, roleIDs, NewPermissionRepository(db)
}

func TestMigration000017_UpDownRoundTrip(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS roles (id uuid PRIMARY KEY DEFAULT uuid_generate_v4(), org_id uuid, name varchar, is_system boolean)`).Error)

	runMigrationFile(t, db, "000017_object_security.up.sql")
	require.True(t, tableExists(t, db, "object_permissions"))
	require.True(t, tableExists(t, db, "object_audit"))

	runMigrationFile(t, db, "000017_object_security.down.sql")
	require.False(t, tableExists(t, db, "object_permissions"))
	require.False(t, tableExists(t, db, "object_audit"))

	// Re-up is self-consistent.
	runMigrationFile(t, db, "000017_object_security.up.sql")
	require.True(t, tableExists(t, db, "object_permissions"))
	require.True(t, tableExists(t, db, "object_audit"))
}

func TestEnsureDefaults_SeedsLegacyMatrix_AndIsIdempotent(t *testing.T) {
	orgID, _, repo := setupPermissions(t)
	ctx := context.Background()

	require.NoError(t, repo.EnsureDefaults(ctx, orgID))

	access, err := repo.LoadOrgAccess(ctx, orgID)
	require.NoError(t, err)

	// Every system role gets read on every system object; the matrix mirrors the
	// legacy RequireRole gates exactly.
	require.True(t, access[domain.RoleViewer]["deal"].Read, "viewer reads deals")
	require.False(t, access[domain.RoleViewer]["deal"].Create, "viewer can't create")
	require.True(t, access[domain.RoleSales]["contact"].Edit, "sales edits contacts")
	require.False(t, access[domain.RoleSales]["contact"].Delete, "sales can't delete")
	require.True(t, access[domain.RoleManager]["company"].Delete, "manager deletes companies")
	require.True(t, access[domain.RoleAdmin]["deal"].Delete, "admin deletes deals")

	// Idempotent: a second pass adds nothing.
	before, err := repo.ListPermissions(ctx, orgID)
	require.NoError(t, err)
	require.NoError(t, repo.EnsureDefaults(ctx, orgID))
	after, err := repo.ListPermissions(ctx, orgID)
	require.NoError(t, err)
	require.Equal(t, len(before), len(after), "re-seed must not duplicate rows")
	require.Equal(t, 5*len(permSystemObjectSlugs), len(after), "5 roles × 3 system objects")
}

func TestEnsureDefaults_CoversCustomObjects_AndRespectsLockdown(t *testing.T) {
	orgID, roleIDs, repo := setupPermissions(t)
	ctx := context.Background()

	// A pre-existing custom object: the seed must cover it too (uniform path UX
	// is non-breaking at rollout).
	db := repo.(*permissionRepository).db
	require.NoError(t, db.Exec(`INSERT INTO custom_object_defs (org_id, slug) VALUES (?, 'project')`, orgID).Error)

	require.NoError(t, repo.EnsureDefaults(ctx, orgID))
	access, err := repo.LoadOrgAccess(ctx, orgID)
	require.NoError(t, err)
	require.True(t, access[domain.RoleSales]["project"].Create, "sales can create projects by default")

	// An admin locks viewers fully out of project (explicit all-false row).
	require.NoError(t, repo.UpsertPermission(ctx, domain.ObjectPermission{
		OrgID: orgID, RoleID: roleIDs[domain.RoleViewer], ObjectSlug: "project",
	}))

	// Re-seeding must NOT resurrect viewer's default read — the explicit denial
	// survives because the object already has rows.
	require.NoError(t, repo.EnsureDefaults(ctx, orgID))
	access, err = repo.LoadOrgAccess(ctx, orgID)
	require.NoError(t, err)
	require.False(t, access[domain.RoleViewer]["project"].Read, "explicit lock-down must survive re-seed")
}

func TestWriteAndListAudit(t *testing.T) {
	orgID, _, repo := setupPermissions(t)
	ctx := context.Background()
	db := repo.(*permissionRepository).db

	actorID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO users (id, full_name, email) VALUES (?, 'Jane Doe', 'jane@example.com')`, actorID).Error)

	recID := uuid.New()
	base := time.Now().UTC().Truncate(time.Second)

	// A create (with actor) then a later delete (no actor). Explicit timestamps so
	// the newest-first ordering is deterministic rather than racing on NOW().
	require.NoError(t, repo.WriteAudit(ctx, &domain.ObjectAudit{
		OrgID: orgID, ObjectSlug: "deal", RecordID: recID, ActorID: &actorID,
		Action: "create", Changes: domain.JSON(`{"title":{"new":"Acme deal"}}`),
		CreatedAt: base.Add(-time.Minute),
	}))
	require.NoError(t, repo.WriteAudit(ctx, &domain.ObjectAudit{
		OrgID: orgID, ObjectSlug: "deal", RecordID: recID,
		Action: "delete", Changes: domain.JSON(`{}`),
		CreatedAt: base,
	}))

	views, err := repo.ListAudit(ctx, orgID, "deal", recID, 100)
	require.NoError(t, err)
	require.Len(t, views, 2)

	// Newest first: the delete, then the create.
	require.Equal(t, "delete", views[0].Action)
	require.Equal(t, "", views[0].ActorName, "delete had no actor → empty name (LEFT JOIN)")

	require.Equal(t, "create", views[1].Action)
	require.Equal(t, "Jane Doe", views[1].ActorName, "actor name resolved via the users join")
	require.NotNil(t, views[1].ActorID)
	require.Equal(t, actorID, *views[1].ActorID)
	// The jsonb diff round-trips into a map.
	cell, ok := views[1].Changes["title"].(map[string]interface{})
	require.True(t, ok, "changes should decode to a field→diff map")
	require.Equal(t, "Acme deal", cell["new"])

	// Scoped to the record: a different record id sees nothing.
	other, err := repo.ListAudit(ctx, orgID, "deal", uuid.New(), 100)
	require.NoError(t, err)
	require.Len(t, other, 0)
}

func TestUpsertPermission_InsertThenUpdate(t *testing.T) {
	orgID, roleIDs, repo := setupPermissions(t)
	ctx := context.Background()

	// Insert.
	require.NoError(t, repo.UpsertPermission(ctx, domain.ObjectPermission{
		OrgID: orgID, RoleID: roleIDs[domain.RoleViewer], ObjectSlug: "deal", CanRead: true,
	}))
	access, err := repo.LoadOrgAccess(ctx, orgID)
	require.NoError(t, err)
	require.True(t, access[domain.RoleViewer]["deal"].Read)
	require.False(t, access[domain.RoleViewer]["deal"].Edit)

	// Update the same cell (grant edit, revoke read).
	require.NoError(t, repo.UpsertPermission(ctx, domain.ObjectPermission{
		OrgID: orgID, RoleID: roleIDs[domain.RoleViewer], ObjectSlug: "deal", CanEdit: true,
	}))
	access, err = repo.LoadOrgAccess(ctx, orgID)
	require.NoError(t, err)
	require.False(t, access[domain.RoleViewer]["deal"].Read, "update replaced the bits")
	require.True(t, access[domain.RoleViewer]["deal"].Edit)

	// Still one row for the cell (upsert, not insert-twice).
	perms, err := repo.ListPermissions(ctx, orgID)
	require.NoError(t, err)
	count := 0
	for _, p := range perms {
		if p.RoleID == roleIDs[domain.RoleViewer] && p.ObjectSlug == "deal" {
			count++
		}
	}
	require.Equal(t, 1, count, "upsert must not create a duplicate cell")
}
