package repository

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// setupLinks creates the prerequisites the 000016 migration needs (the uuid-ossp
// extension plus minimal organizations/users tables for its FKs), runs the real
// up migration, and returns the repository plus an org + user to attribute edges
// to.
func setupLinks(t *testing.T) (repo domain.LinkRepository, orgID, userID uuid.UUID, cleanup func()) {
	t.Helper()
	db, done := startPostgres(t)

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	runMigrationFile(t, db, "000016_object_links.up.sql")

	orgID = uuid.New()
	userID = uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO users (id) VALUES (?)`, userID).Error)

	return NewLinkRepository(db), orgID, userID, done
}

// TestMigration000016_UpDownRoundTrip proves .down drops cleanly and .up is
// re-runnable (up → down → up).
func TestMigration000016_UpDownRoundTrip(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (id uuid PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)

	runMigrationFile(t, db, "000016_object_links.up.sql")
	require.True(t, tableExists(t, db, "object_links"), "object_links should exist after up")

	runMigrationFile(t, db, "000016_object_links.down.sql")
	require.False(t, tableExists(t, db, "object_links"), "object_links should be gone after down")

	runMigrationFile(t, db, "000016_object_links.up.sql")
	require.True(t, tableExists(t, db, "object_links"), "object_links should exist after re-up")
}

func TestObjectLinkRepository_CreateFindAndUniqueness(t *testing.T) {
	repo, orgID, userID, cleanup := setupLinks(t)
	defer cleanup()
	ctx := context.Background()

	projID := uuid.New()
	companyID := uuid.New()
	edge := &domain.ObjectLink{
		OrgID: orgID, FromSlug: "project", FromID: projID,
		ToSlug: "company", ToID: companyID, RelationKey: "account", CreatedBy: &userID,
	}
	require.NoError(t, repo.Create(ctx, edge))
	require.NotEqual(t, uuid.Nil, edge.ID)

	found, err := repo.FindEdge(ctx, orgID, "project", projID, "account", "company", companyID)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, edge.ID, found.ID)

	// A second identical active edge violates the partial unique index.
	dup := &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: projID, ToSlug: "company", ToID: companyID, RelationKey: "account"}
	require.Error(t, repo.Create(ctx, dup), "duplicate active edge must be rejected")

	// After unlinking, the same edge can be re-created (soft-deletes leave the index).
	ok, err := repo.SoftDelete(ctx, orgID, edge.ID)
	require.NoError(t, err)
	require.True(t, ok)

	gone, err := repo.FindEdge(ctx, orgID, "project", projID, "account", "company", companyID)
	require.NoError(t, err)
	require.Nil(t, gone, "soft-deleted edge must not be found")

	relink := &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: projID, ToSlug: "company", ToID: companyID, RelationKey: "account"}
	require.NoError(t, repo.Create(ctx, relink), "re-link after unlink should be allowed")
}

func TestObjectLinkRepository_ListFrom(t *testing.T) {
	repo, orgID, _, cleanup := setupLinks(t)
	defer cleanup()
	ctx := context.Background()

	projID := uuid.New()
	require.NoError(t, repo.Create(ctx, &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: projID, ToSlug: "company", ToID: uuid.New(), RelationKey: "account"}))
	require.NoError(t, repo.Create(ctx, &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: projID, ToSlug: "tag", ToID: uuid.New(), RelationKey: "tags"}))
	// A different record's edge must not leak in.
	require.NoError(t, repo.Create(ctx, &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: uuid.New(), ToSlug: "company", ToID: uuid.New(), RelationKey: "account"}))

	links, err := repo.ListFrom(ctx, orgID, "project", projID)
	require.NoError(t, err)
	require.Len(t, links, 2, "ListFrom should return only the record's own outgoing edges")
}

func TestObjectLinkRepository_CascadeSoftDelete(t *testing.T) {
	repo, orgID, _, cleanup := setupLinks(t)
	defer cleanup()
	ctx := context.Background()

	companyID := uuid.New()
	projID := uuid.New()
	taskID := uuid.New()
	otherID := uuid.New()

	// company appears as a `to` endpoint twice, and never as a `from`.
	require.NoError(t, repo.Create(ctx, &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: projID, ToSlug: "company", ToID: companyID, RelationKey: "account"}))
	require.NoError(t, repo.Create(ctx, &domain.ObjectLink{OrgID: orgID, FromSlug: "task", FromID: taskID, ToSlug: "company", ToID: companyID, RelationKey: "account"}))
	// An unrelated edge that must survive.
	require.NoError(t, repo.Create(ctx, &domain.ObjectLink{OrgID: orgID, FromSlug: "project", FromID: projID, ToSlug: "other", ToID: otherID, RelationKey: "ref"}))

	require.NoError(t, repo.CascadeSoftDelete(ctx, orgID, "company", companyID))

	projLinks, err := repo.ListFrom(ctx, orgID, "project", projID)
	require.NoError(t, err)
	require.Len(t, projLinks, 1, "only the company edge should be cascaded from project")
	require.Equal(t, "other", projLinks[0].ToSlug)

	taskLinks, err := repo.ListFrom(ctx, orgID, "task", taskID)
	require.NoError(t, err)
	require.Empty(t, taskLinks, "the task→company edge should be cascaded too")
}

func TestObjectLinkRepository_ContactTagsBridge(t *testing.T) {
	repo, _, _, cleanup := setupLinks(t)
	defer cleanup()
	ctx := context.Background()

	// Minimal contact_tags join (the real one comes from migration 000002).
	db := repo.(*objectLinkRepository).db
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS contact_tags (contact_id uuid NOT NULL, tag_id uuid NOT NULL, PRIMARY KEY (contact_id, tag_id))`).Error)

	contactID := uuid.New()
	tagID := uuid.New()

	require.NoError(t, repo.AddContactTag(ctx, contactID, tagID))
	require.NoError(t, repo.AddContactTag(ctx, contactID, tagID), "ON CONFLICT DO NOTHING — re-add is a no-op")

	ids, err := repo.ListContactTagIDs(ctx, contactID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tagID}, ids)

	removed, err := repo.RemoveContactTag(ctx, contactID, tagID)
	require.NoError(t, err)
	require.True(t, removed)

	ids, err = repo.ListContactTagIDs(ctx, contactID)
	require.NoError(t, err)
	require.Empty(t, ids)
}
