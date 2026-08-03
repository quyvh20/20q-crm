package repository

import (
	"context"
	"sync"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ConsumeTOTPStep is the compare-and-swap that makes a TOTP code single-use. Its
// whole value is in the WHERE clause and in the atomicity, neither of which a fake
// can express — so it is tested against a real database or not at all.

func setupTOTPUser(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	// deleted_at and updated_at are required, not decorative: domain.User carries
	// gorm.DeletedAt so Model(&domain.User{}) appends a soft-delete predicate, and
	// GORM auto-stamps updated_at on every Update. Without either the statement
	// fails with 42703. The real table has both.
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		email          TEXT,
		totp_last_step BIGINT,
		created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at     TIMESTAMPTZ
	)`).Error)

	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO users (id, email) VALUES (?, ?)`, id, "t@example.com").Error)
	return id
}

func TestConsumeTOTPStep_DB(t *testing.T) {
	db, cleanup := startPostgres(t)
	defer cleanup()
	repo := &authRepository{db: db}
	ctx := context.Background()

	t.Run("first claim of a step wins", func(t *testing.T) {
		id := setupTOTPUser(t, db)
		ok, err := repo.ConsumeTOTPStep(ctx, id, 1000)
		require.NoError(t, err)
		require.True(t, ok, "an unspent step must be claimable")
	})

	// The replay.
	t.Run("the same step cannot be claimed twice", func(t *testing.T) {
		id := setupTOTPUser(t, db)
		first, err := repo.ConsumeTOTPStep(ctx, id, 2000)
		require.NoError(t, err)
		require.True(t, first)

		second, err := repo.ConsumeTOTPStep(ctx, id, 2000)
		require.NoError(t, err)
		require.False(t, second, "replaying the same step must be refused")
	})

	// A code from an EARLIER step, replayed after a later one was spent.
	t.Run("an older step cannot be claimed", func(t *testing.T) {
		id := setupTOTPUser(t, db)
		_, err := repo.ConsumeTOTPStep(ctx, id, 3000)
		require.NoError(t, err)

		older, err := repo.ConsumeTOTPStep(ctx, id, 2999)
		require.NoError(t, err)
		require.False(t, older, "a step below the last spent must be refused")
	})

	t.Run("a later step is claimable", func(t *testing.T) {
		id := setupTOTPUser(t, db)
		_, err := repo.ConsumeTOTPStep(ctx, id, 4000)
		require.NoError(t, err)

		next, err := repo.ConsumeTOTPStep(ctx, id, 4001)
		require.NoError(t, err)
		require.True(t, next, "the next step must still be usable — otherwise the user is locked out")
	})

	// Why it is one statement rather than read-then-write: a code is valid for a
	// whole 30-second window, so two logins can present it simultaneously.
	t.Run("concurrent claims of one step produce exactly one winner", func(t *testing.T) {
		id := setupTOTPUser(t, db)

		const racers = 8
		var wg sync.WaitGroup
		results := make([]bool, racers)
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func(i int) {
				defer wg.Done()
				ok, err := repo.ConsumeTOTPStep(ctx, id, 5000)
				if err == nil {
					results[i] = ok
				}
			}(i)
		}
		wg.Wait()

		won := 0
		for _, r := range results {
			if r {
				won++
			}
		}
		require.Equal(t, 1, won, "exactly one concurrent claim may succeed; got %d", won)
	})

	t.Run("claiming does not affect another user", func(t *testing.T) {
		a := setupTOTPUser(t, db)
		b := setupTOTPUser(t, db)

		_, err := repo.ConsumeTOTPStep(ctx, a, 6000)
		require.NoError(t, err)

		ok, err := repo.ConsumeTOTPStep(ctx, b, 6000)
		require.NoError(t, err)
		require.True(t, ok, "one user's spent step must not block another's")
	})

	t.Run("a NULL last step is treated as nothing spent", func(t *testing.T) {
		id := setupTOTPUser(t, db)
		var lastStep *int64
		require.NoError(t, db.Raw(`SELECT totp_last_step FROM users WHERE id = ?`, id).Scan(&lastStep).Error)
		require.Nil(t, lastStep, "a fresh user starts with no step spent")

		// Existing users all look like this until their next login, so a negative or
		// zero step must still be claimable rather than locking them out.
		ok, err := repo.ConsumeTOTPStep(ctx, id, 1)
		require.NoError(t, err)
		require.True(t, ok)
	})
}

var _ = domain.User{}
