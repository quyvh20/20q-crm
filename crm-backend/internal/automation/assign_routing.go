package automation

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Assignment routing: which human an automation hands a record to.
//
// The failure this file exists to prevent is silent. An assignment to someone who
// has left the workspace looks correct in every list — the record has an owner, so
// nobody triages it — while the person named cannot open it. Nothing errors, and
// own-scoped teammates cannot see the record at all.
//
// Kept deliberately separate from internal/integrations/routing.go, which solves the
// same problem for inbound lead capture. See the fail-closed note on
// nextAssignTicket for why the two differ where they differ.

// activeMemberSQL is this package's liveness rule, hoisted out of the two inline
// copies that already encode it (handlers.go, handlers_email_templates.go).
//
// Both halves matter and they catch different exits:
//   - status = 'active' excludes a SUSPENDED member, whose row survives intact.
//   - deleted_at IS NULL is belt-and-braces. RemoveMember hard-deletes the row
//     (workspace_usecase.go), so a removed member usually has no row to match at
//     all; the column is still set by org-level soft delete.
//
// This is the strictest predicate in the codebase and the one the automation
// package already used to build the pool picker's user list. That agreement is the
// point: the builder UI must not offer a person the executor would then refuse.
const activeMemberSQL = `SELECT user_id FROM org_users
	WHERE org_id = ? AND user_id IN ? AND status = 'active' AND deleted_at IS NULL`

// activeMemberIDs answers "which of these people may be handed work right now" in
// one round trip, returning a set for O(1) lookup during the pick.
func activeMemberIDs(ctx context.Context, db *gorm.DB, orgID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	live := make(map[uuid.UUID]bool, len(userIDs))
	if len(userIDs) == 0 {
		// Postgres rejects `IN ()`. An empty probe is a legitimate caller state
		// (an empty pool), not an error.
		return live, nil
	}
	var ids []uuid.UUID
	if err := db.WithContext(ctx).Raw(activeMemberSQL, orgID, userIDs).Scan(&ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		live[id] = true
	}
	return live, nil
}

// dedupeUUIDs collapses repeats while preserving first-seen order.
//
// The pool picker emits checkboxes and so cannot produce a duplicate, but an
// AI-drafted or hand-edited workflow can. A repeated id would silently double that
// person's share of the rotation, which reads as a fairness bug nobody can find by
// looking at the builder.
func dedupeUUIDs(in []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(in))
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// pickFromPool is the rotation's decision logic, kept pure so its fairness can be
// tested as an exact SEQUENCE rather than as a distribution.
//
// That distinction is the whole reason this is a separate function: "each member got
// roughly a third" passes identically for a uniform random picker, which is exactly
// what the subtly-wrong version of this code produces (see the filter note below).
//
// Returns ok=false when nobody in the pool is live — the caller decides what to do
// about it, because "assign nobody" and "fail the step" are different products.
func pickFromPool(ticket int64, pool []uuid.UUID, live map[uuid.UUID]bool) (uuid.UUID, bool) {
	// Filter FIRST, then modulo. Ranging the liveness MAP instead of this slice
	// would be the subtle killer: Go randomizes map iteration order, so the
	// rotation would decay into a uniform random draw and the persisted cursor
	// would be decorative.
	//
	// Filtering first is also what makes a suspension fair. Pool [A,B,C] with B
	// suspended alternates A,C,A,C — a clean 50/50. Taking the modulo over the full
	// pool and then skipping forward past B yields A,C,C,A,C,C: C silently absorbs
	// B's entire share.
	liveMembers := make([]uuid.UUID, 0, len(pool))
	for _, id := range pool {
		if live[id] {
			liveMembers = append(liveMembers, id)
		}
	}

	// This emptiness check MUST stay adjacent to the modulo below it: `n % 0` is a
	// Go divide-by-zero panic, and a panic inside an executor takes down the run's
	// worker rather than failing one step.
	if len(liveMembers) == 0 {
		return uuid.Nil, false
	}
	return liveMembers[nonNegMod(ticket, len(liveMembers))], true
}

// nonNegMod keeps the index in range even if a cursor ever went negative. Go's %
// preserves the sign of the dividend, so a negative ticket would index out of
// bounds and panic.
func nonNegMod(n int64, m int) int {
	if m <= 0 {
		return 0
	}
	i := int(n % int64(m))
	if i < 0 {
		i += m
	}
	return i
}

// nextAssignTicketSQL atomically claims this step's place in the rotation.
//
// The UPSERT is what makes rotation correct under concurrency: two runs firing at
// once get two different tickets, because the increment happens inside the row lock
// rather than in Go after a read. A read-then-write cursor — or deriving the ticket
// by COUNTing prior action logs — hands both runs the same number and therefore the
// same assignee, which is the bug this whole change is about.
//
// First call for a step returns 0, so a fresh rotation starts at pool[0].
const nextAssignTicketSQL = `INSERT INTO automation_assign_cursors
	(org_id, workflow_id, action_id, ticket, updated_at)
	VALUES (?, ?, ?, 0, now())
	ON CONFLICT (org_id, workflow_id, action_id)
	DO UPDATE SET ticket = automation_assign_cursors.ticket + 1, updated_at = now()
	RETURNING ticket`

// nextAssignTicket claims the next turn for one assign_user step.
//
// Keyed by action_id as well as workflow_id because a workflow may hold several
// assign_user steps (a branch per region, say), and each needs its own independent
// rotation. Sharing one cursor across them would interleave their turns and starve
// whichever branch fires less often.
//
// Retry note: a step that fails after claiming its ticket burns that turn. The
// rotation stays fair — it simply skips a slot — so this is deliberately not made
// run-idempotent. The caller keeps the claim as late as it can to narrow the window.
func nextAssignTicket(ctx context.Context, db *gorm.DB, orgID, workflowID uuid.UUID, actionID string) (int64, error) {
	var ticket int64
	err := db.WithContext(ctx).
		Raw(nextAssignTicketSQL, orgID, workflowID, actionID).
		Scan(&ticket).Error
	if err != nil {
		return 0, err
	}
	return ticket, nil
}

// leastLoadedSQL finds the live member carrying the fewest open records.
//
// Driving FROM org_users rather than from the record table is the correction, and it
// fixes two bugs at once:
//
//   - It bounds candidates to actual members. The previous query fell back to
//     `users WHERE org_id = ?` — the legacy denormalized column, which for a
//     multi-org user is a stale snapshot of their FIRST org. It could hand a record
//     to someone with no membership in this org at all.
//   - It makes a member with ZERO records eligible. Grouping the record table could
//     only ever return people who already owned something, so a new hire was
//     invisible to "least loaded" until they somehow acquired their first record —
//     inverting the feature for exactly the person it should favour.
//
// The LEFT JOIN carries org_id and the soft-delete filter in its ON clause, not in
// WHERE: moving either up would turn the outer join back into an inner one and
// re-hide the zero-record member.
//
// Ties break on user_id so the result is a stable sequence a test can pin, rather
// than whatever order the planner happens to return.
func leastLoadedSQL(table, openFilter string) string {
	return fmt.Sprintf(`SELECT ou.user_id AS owner_user_id, COUNT(t.id) AS cnt
		FROM org_users ou
		LEFT JOIN %s t
			ON t.owner_user_id = ou.user_id
			AND t.org_id = ?
			AND t.deleted_at IS NULL%s
		WHERE ou.org_id = ? AND ou.status = 'active' AND ou.deleted_at IS NULL
		GROUP BY ou.user_id
		ORDER BY cnt ASC, ou.user_id ASC
		LIMIT 1`, table, openFilter)
}

// openRecordFilter narrows "load" to work still in play.
//
// Counting a rep's whole history is not a workload measure: someone with 500
// closed-won deals is not busy, they are successful, and the old unfiltered count
// permanently routed new work away from the team's best closers. Contacts have no
// open/closed axis, so their count is all live rows.
func openRecordFilter(entity string) string {
	if entity == "deal" {
		return "\n\t\t\tAND t.is_won = false AND t.is_lost = false"
	}
	return ""
}
