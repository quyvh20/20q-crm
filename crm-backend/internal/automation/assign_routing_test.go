package automation

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rotation tests assert an exact SEQUENCE, never a distribution.
//
// This is the point of the file. "Each of the three got roughly a third" is true of
// a uniform random picker, of a least-loaded heuristic, and of real round-robin
// alike — so a distribution assertion cannot tell a working rotation from the broken
// one this code replaced. Only the order distinguishes them.

// fixedPool returns three ids in a deterministic, readable order so a failure names
// which member came out of turn.
func fixedPool() (a, b, c uuid.UUID, pool []uuid.UUID) {
	a = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	c = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	return a, b, c, []uuid.UUID{a, b, c}
}

// allLive marks every id in the pool as an active member.
func allLive(pool []uuid.UUID) map[uuid.UUID]bool {
	live := make(map[uuid.UUID]bool, len(pool))
	for _, id := range pool {
		live[id] = true
	}
	return live
}

// sequence draws n consecutive tickets, which is what the persisted cursor feeds
// pickFromPool across runs.
func sequence(t *testing.T, pool []uuid.UUID, live map[uuid.UUID]bool, n int) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		picked, ok := pickFromPool(int64(i), pool, live)
		require.True(t, ok, "ticket %d: expected a pick from a pool with live members", i)
		out = append(out, picked)
	}
	return out
}

func TestPickFromPool_ExactRotationSequence(t *testing.T) {
	a, b, c, pool := fixedPool()

	got := sequence(t, pool, allLive(pool), 7)

	// Every member takes exactly one turn before anyone takes a second.
	assert.Equal(t, []uuid.UUID{a, b, c, a, b, c, a}, got,
		"round_robin must rotate in pool order; a least-loaded or random picker fails here while still passing a distribution check")
}

func TestPickFromPool_SuspendedMemberNeverReceivesAndShareSplitsEvenly(t *testing.T) {
	a, b, c, pool := fixedPool()
	live := allLive(pool)
	delete(live, b) // B suspended, or removed from the workspace entirely

	got := sequence(t, pool, live, 6)

	for i, picked := range got {
		assert.NotEqual(t, b, picked, "ticket %d assigned to a member who is not active", i)
	}

	// The alternation is the real assertion. Taking the modulo over the FULL pool
	// and skipping forward past B would yield A,C,C,A,C,C — C quietly absorbing B's
	// entire share. Filtering before the modulo gives a clean 50/50.
	assert.Equal(t, []uuid.UUID{a, c, a, c, a, c}, got,
		"a suspended member's share must be split evenly, not handed to whoever follows them in the pool")
}

func TestPickFromPool_StableAcrossRepeatedEvaluation(t *testing.T) {
	_, _, _, pool := fixedPool()
	live := allLive(pool)

	want := sequence(t, pool, live, 6)

	// Ranging the liveness MAP instead of the pool slice would pass a single run and
	// fail here, because Go randomizes map iteration order. Repeating the draw is
	// what turns that into a deterministic failure rather than a flake.
	for i := 0; i < 50; i++ {
		assert.Equal(t, want, sequence(t, pool, live, 6),
			"rotation must not depend on map iteration order")
	}
}

func TestPickFromPool_NoLiveMembers(t *testing.T) {
	_, _, _, pool := fixedPool()

	picked, ok := pickFromPool(0, pool, map[uuid.UUID]bool{})

	assert.False(t, ok, "a pool where everyone has left must report failure, not guess")
	assert.Equal(t, uuid.Nil, picked)
}

func TestPickFromPool_EmptyPoolDoesNotPanic(t *testing.T) {
	// `n % 0` panics in Go, and a panic in an executor takes down the worker rather
	// than failing one step.
	require.NotPanics(t, func() {
		_, ok := pickFromPool(3, nil, map[uuid.UUID]bool{})
		assert.False(t, ok)
	})
}

func TestPickFromPool_NegativeTicketStaysInRange(t *testing.T) {
	a, _, _, pool := fixedPool()

	// Go's % keeps the sign of the dividend, so a negative cursor would index out of
	// bounds and panic on the assignment path.
	require.NotPanics(t, func() {
		picked, ok := pickFromPool(-1, pool, allLive(pool))
		require.True(t, ok)
		assert.Contains(t, pool, picked)
	})

	picked, ok := pickFromPool(-3, pool, allLive(pool))
	require.True(t, ok)
	assert.Equal(t, a, picked, "-3 over a pool of 3 lands back on the first member")
}

func TestPickFromPool_SingleLiveMemberAlwaysWins(t *testing.T) {
	a, b, c, pool := fixedPool()
	live := map[uuid.UUID]bool{c: true}

	for ticket := int64(0); ticket < 4; ticket++ {
		picked, ok := pickFromPool(ticket, pool, live)
		require.True(t, ok)
		assert.Equal(t, c, picked)
		assert.NotEqual(t, a, picked)
		assert.NotEqual(t, b, picked)
	}
}

func TestDedupeUUIDs_CollapsesRepeatsPreservingOrder(t *testing.T) {
	a, b, c, _ := fixedPool()

	got := dedupeUUIDs([]uuid.UUID{b, a, b, c, a})

	// First-seen order, so a duplicated id cannot silently double that person's
	// share of the rotation.
	assert.Equal(t, []uuid.UUID{b, a, c}, got)
}

func TestNonNegMod(t *testing.T) {
	cases := []struct {
		name string
		n    int64
		m    int
		want int
	}{
		{"zero", 0, 3, 0},
		{"wraps", 4, 3, 1},
		{"negative wraps forward", -1, 3, 2},
		{"negative multiple", -3, 3, 0},
		{"zero modulus is safe", 5, 0, 0},
		{"negative modulus is safe", 5, -2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, nonNegMod(c.n, c.m))
		})
	}
}

func TestOpenRecordFilter_OnlyDealsHaveAnOpenClosedAxis(t *testing.T) {
	// A rep with 500 closed-won deals is not busy, so closed work must not count
	// against them. Contacts have no such axis.
	assert.Contains(t, openRecordFilter("deal"), "is_won = false")
	assert.Contains(t, openRecordFilter("deal"), "is_lost = false")
	assert.Empty(t, openRecordFilter("contact"))
}

func TestLeastLoadedSQL_DrivesFromMembershipNotRecords(t *testing.T) {
	sql := leastLoadedSQL("contacts", openRecordFilter("contact"))

	// Driving FROM org_users is what makes a zero-record member eligible and what
	// keeps a non-member out. The old query grouped the record table instead.
	assert.Contains(t, sql, "FROM org_users ou")
	assert.Contains(t, sql, "LEFT JOIN contacts t")
	assert.Contains(t, sql, "ou.status = 'active'")
	assert.Contains(t, sql, "ou.deleted_at IS NULL")
	assert.Contains(t, sql, "t.deleted_at IS NULL")
	assert.Contains(t, sql, "ORDER BY cnt ASC, ou.user_id ASC")

	// The legacy denormalized column is a stale snapshot of a multi-org user's first
	// org. Selecting candidates from it was how a non-member could be assigned.
	assert.NotContains(t, sql, "FROM users")
}
