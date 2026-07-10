package repository

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestNotificationCursor_Roundtrip is a pure test: the opaque cursor must decode
// back to the exact (created_at, id) it encoded so keyset paging walks correctly.
func TestNotificationCursor_Roundtrip(t *testing.T) {
	id := uuid.New()
	// A non-round nanosecond time to catch truncation.
	ts := time.Unix(1_700_000_000, 123_456_789).UTC()

	cur := encodeNotificationCursor(ts, id)
	require.NotEmpty(t, cur)

	gotTS, gotID, ok := decodeNotificationCursor(cur)
	require.True(t, ok)
	require.Equal(t, id, gotID)
	require.True(t, ts.Equal(gotTS), "want %v got %v", ts, gotTS)
}

func TestNotificationCursor_Malformed(t *testing.T) {
	for _, bad := range []string{"", "not-base64!!", "Zm9vfGJhcg" /* "foo|bar" */, "MTIz" /* "123", no pipe */} {
		_, _, ok := decodeNotificationCursor(bad)
		require.False(t, ok, "cursor %q should be rejected", bad)
	}
}

// applyNotificationSchema creates the FK prerequisites (uuid-ossp + minimal
// organizations/users), runs the real 000036 migration, and inserts one org and
// two users. Returns (orgID, userA, userB) so tests can assert per-user scoping.
func applyNotificationSchema(t *testing.T, db *gorm.DB) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id UUID PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY DEFAULT uuid_generate_v4())`).Error)
	runMigrationFile(t, db, "000036_notifications.up.sql")

	orgID := uuid.New()
	userA := uuid.New()
	userB := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id) VALUES (?)`, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO users (id) VALUES (?), (?)`, userA, userB).Error)
	return orgID, userA, userB
}

func TestNotificationRepository_Integration(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()

	orgID, userA, userB := applyNotificationSchema(t, db)
	repo := NewNotificationRepository(db)
	ctx := context.Background()

	// Seed 5 notifications for userA with strictly increasing created_at so paging
	// order is deterministic; one for userB to prove scoping.
	base := time.Now().Add(-time.Hour)
	ids := make([]uuid.UUID, 0, 5)
	for i := 0; i < 5; i++ {
		n := &domain.Notification{OrgID: orgID, UserID: userA, Type: "automation", Title: "n"}
		require.NoError(t, repo.Create(ctx, n))
		// Force distinct, ordered timestamps (Create defaults created_at to NOW()).
		require.NoError(t, db.Exec(`UPDATE notifications SET created_at = ? WHERE id = ?`, base.Add(time.Duration(i)*time.Minute), n.ID).Error)
		ids = append(ids, n.ID)
	}
	require.NoError(t, repo.Create(ctx, &domain.Notification{OrgID: orgID, UserID: userB, Title: "other"}))

	// Unread count is per-user.
	cA, err := repo.UnreadCount(ctx, orgID, userA)
	require.NoError(t, err)
	require.EqualValues(t, 5, cA)
	cB, err := repo.UnreadCount(ctx, orgID, userB)
	require.NoError(t, err)
	require.EqualValues(t, 1, cB)

	// Page 1 (limit 2) → newest first: ids[4], ids[3]. NextCursor present.
	page1, next1, err := repo.List(ctx, orgID, userA, domain.NotificationListInput{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, ids[4], page1[0].ID)
	require.Equal(t, ids[3], page1[1].ID)
	require.NotEmpty(t, next1)

	// Page 2 continues strictly past the cursor: ids[2], ids[1].
	page2, next2, err := repo.List(ctx, orgID, userA, domain.NotificationListInput{Limit: 2, Cursor: next1})
	require.NoError(t, err)
	require.Len(t, page2, 2)
	require.Equal(t, ids[2], page2[0].ID)
	require.Equal(t, ids[1], page2[1].ID)
	require.NotEmpty(t, next2)

	// Page 3 is the last (1 row), no further cursor.
	page3, next3, err := repo.List(ctx, orgID, userA, domain.NotificationListInput{Limit: 2, Cursor: next2})
	require.NoError(t, err)
	require.Len(t, page3, 1)
	require.Equal(t, ids[0], page3[0].ID)
	require.Empty(t, next3)

	// Mark one read → unread drops, unread-only filter excludes it.
	require.NoError(t, repo.MarkRead(ctx, orgID, userA, ids[4]))
	cA, err = repo.UnreadCount(ctx, orgID, userA)
	require.NoError(t, err)
	require.EqualValues(t, 4, cA)
	unreadPage, _, err := repo.List(ctx, orgID, userA, domain.NotificationListInput{Limit: 10, UnreadOnly: true})
	require.NoError(t, err)
	require.Len(t, unreadPage, 4)
	for _, n := range unreadPage {
		require.NotEqual(t, ids[4], n.ID)
	}

	// A user cannot mark another user's notification read (scoped no-op).
	require.NoError(t, repo.MarkRead(ctx, orgID, userB, ids[3]))
	cA, err = repo.UnreadCount(ctx, orgID, userA)
	require.NoError(t, err)
	require.EqualValues(t, 4, cA, "userB marking userA's row must be a no-op")

	// Mark all read for userA → 0 unread; return count is the number flipped.
	marked, err := repo.MarkAllRead(ctx, orgID, userA)
	require.NoError(t, err)
	require.EqualValues(t, 4, marked)
	cA, err = repo.UnreadCount(ctx, orgID, userA)
	require.NoError(t, err)
	require.EqualValues(t, 0, cA)

	// Retention sweep removes only rows older than the cutoff.
	require.NoError(t, db.Exec(`UPDATE notifications SET created_at = ? WHERE id = ?`, time.Now().Add(-100*24*time.Hour), ids[0]).Error)
	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(-90*24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
}
