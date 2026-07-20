package integrations

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// Owner routing decides whether a captured lead reaches a human at all. These tests
// assert SEQUENCES rather than distributions on purpose: "each member got about a
// third" passes identically for a uniform random picker, which is exactly what the
// most likely wrong implementation produces.

func liveSet(ids ...uuid.UUID) map[uuid.UUID]bool {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// names renders a decision sequence as letters, so a failure reads as
// "got ABCABC, want ABCABC" instead of a wall of UUIDs.
func rotate(t *testing.T, n int, pool []uuid.UUID, live map[uuid.UUID]bool, fallback *uuid.UUID, label map[uuid.UUID]string) string {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		d := pickOwner(int64(i), pool, live, fallback)
		if d.OwnerID == nil {
			b.WriteString("-")
			continue
		}
		b.WriteString(label[*d.OwnerID])
	}
	return b.String()
}

// TestPickOwner_RotatesInOrder pins the actual contract: consecutive leads walk the
// pool in the admin's chosen order and wrap. The order is admin-visible in the UI,
// so it has to be the order the code uses.
func TestPickOwner_RotatesInOrder(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	pool := []uuid.UUID{a, b, c}
	label := map[uuid.UUID]string{a: "A", b: "B", c: "C"}

	got := rotate(t, 6, pool, liveSet(a, b, c), nil, label)
	if got != "ABCABC" {
		t.Errorf("rotation = %s, want ABCABC — a distribution assertion would not have caught this", got)
	}
}

// TestPickOwner_FiltersBeforeModulo is the fairness property that a naive
// implementation gets wrong in a way nobody notices.
//
// Filter-then-modulo on [A,B,C] with B suspended gives A,C,A,C — an even split of
// the live members. Modulo-then-skip-forward gives A,C,C,A,C,C, where C silently
// absorbs B's entire share and A is under-served forever.
func TestPickOwner_FiltersBeforeModulo(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	pool := []uuid.UUID{a, b, c}
	label := map[uuid.UUID]string{a: "A", b: "B", c: "C"}

	got := rotate(t, 6, pool, liveSet(a, c), nil, label) // B suspended
	if got != "ACACAC" {
		t.Errorf("rotation with a suspended member = %s, want ACACAC (modulo-then-skip would give ACCACC)", got)
	}
}

// TestPickOwner_SuspendedMemberNeverReceives is the blunt statement of the same
// rule: a suspended rep must never be handed a lead, whatever the ticket.
func TestPickOwner_SuspendedMemberNeverReceives(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	pool := []uuid.UUID{a, b}

	for i := 0; i < 20; i++ {
		d := pickOwner(int64(i), pool, liveSet(a), nil) // b suspended
		if d.OwnerID == nil || *d.OwnerID != a {
			t.Fatalf("ticket %d routed to a suspended member (or nobody): %v", i, d.OwnerID)
		}
	}
}

// TestPickOwner_DeadPoolFallsBackToTheFallbackOwner: the whole rotation being
// suspended must not cost the lead — that is what the fallback is for.
func TestPickOwner_DeadPoolFallsBackToTheFallbackOwner(t *testing.T) {
	a, b, fb := uuid.New(), uuid.New(), uuid.New()

	d := pickOwner(0, []uuid.UUID{a, b}, liveSet(fb), &fb)

	if d.OwnerID == nil || *d.OwnerID != fb {
		t.Fatalf("a dead rotation must fall back to the fallback owner, got %v", d.OwnerID)
	}
	if d.Note == "" {
		t.Error("the fallback must be explained on the delivery, or it looks like the rotation is working")
	}
}

// TestPickOwner_DeadPoolAndDeadFallbackIsUnownedAndLoud.
//
// Unowned is the last rung, never a failed lead: a 4xx to a one-shot Make or
// Facebook delivery usually means the lead exists nowhere, whereas an unowned
// contact exists and any all-scope admin can find it. But it is invisible to
// own-scoped reps, so it must be said out loud in both directions.
func TestPickOwner_DeadPoolAndDeadFallbackIsUnownedAndLoud(t *testing.T) {
	a, fb := uuid.New(), uuid.New()

	d := pickOwner(0, []uuid.UUID{a}, liveSet(), &fb) // nobody is live

	if d.OwnerID != nil {
		t.Fatalf("a suspended fallback must not be stamped — a record that looks assigned is worse than one that looks unassigned, got %v", d.OwnerID)
	}
	if d.Note == "" || d.Warning == "" {
		t.Errorf("an unowned lead needs BOTH a ledger note and a caller warning: note=%q warning=%q", d.Note, d.Warning)
	}
}

// TestPickOwner_NeverPanicsOnAnEmptyLiveSet. A panic here would strand the delivery
// in `processing`, and its Idempotency-Key would then 409 forever — the retry
// mechanism that exists to make delivery safe becoming permanent loss.
func TestPickOwner_NeverPanicsOnAnEmptyLiveSet(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("pickOwner panicked on an all-dead pool: %v", r)
		}
	}()
	if d := pickOwner(7, []uuid.UUID{uuid.New()}, liveSet(), nil); d.OwnerID != nil {
		t.Error("an all-dead pool with no fallback must be unowned")
	}
	if d := pickOwner(3, nil, liveSet(), nil); d.OwnerID != nil {
		t.Error("an empty pool must be unowned")
	}
}

// TestNonNegMod pins the guard against a negative ticket. Go's % keeps the sign of
// the dividend, so a wrapped cursor would index out of range and panic on the
// capture path.
func TestNonNegMod(t *testing.T) {
	if got := nonNegMod(-1, 3); got < 0 || got > 2 {
		t.Errorf("nonNegMod(-1,3) = %d, want an in-range index", got)
	}
	if got := nonNegMod(5, 0); got != 0 {
		t.Errorf("nonNegMod with an empty pool must not divide by zero, got %d", got)
	}
}

// The liveness rule's tests moved with the rule: repository/member_liveness_test.go
// runs both halves (domain.OrgUser.IsLive and repository.ActiveMemberSQL) through
// one fixture set, and covers three states this package's copy did not — invited,
// status-deleted, and suspended-AND-soft-deleted.

// TestParsePoolUUIDs_DegradesRatherThanFails: routing config being wrong is not a
// reason to drop a customer's lead.
func TestParsePoolUUIDs_DegradesRatherThanFails(t *testing.T) {
	id := uuid.New()
	cases := map[string]int{
		`[]`:                             0,
		`{}`:                             0, // an object, not an array
		`null`:                           0,
		`["not-a-uuid"]`:                 0,
		`["` + id.String() + `"]`:        1,
		`["nope","` + id.String() + `"]`: 1,
	}
	for raw, want := range cases {
		if got := len(parsePoolUUIDs(datatypes.JSON(raw))); got != want {
			t.Errorf("parsePoolUUIDs(%s) returned %d ids, want %d", raw, got, want)
		}
	}
}

// TestJoinNotes: a delivery can legitimately carry two explanations at once (an
// ambiguous-phone lead routed into a dead pool). Dropping either deletes a
// disclosure someone needs.
func TestJoinNotes(t *testing.T) {
	if got := joinNotes("", "b"); got != "b" {
		t.Errorf(`joinNotes("","b") = %q`, got)
	}
	if got := joinNotes("a", ""); got != "a" {
		t.Errorf(`joinNotes("a","") = %q`, got)
	}
	got := joinNotes("phone was ambiguous", "rotation was empty")
	if !strings.Contains(got, "phone was ambiguous") || !strings.Contains(got, "rotation was empty") {
		t.Errorf("both notes must survive the join, got %q", got)
	}
}
