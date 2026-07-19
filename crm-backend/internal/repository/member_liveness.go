package repository

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Membership liveness: may this person be handed work?
//
// One rule, expressed twice — here in SQL and as domain.OrgUser.IsLive in Go —
// because some callers hold a loaded row and others need to filter a list without
// loading anything. The twins are pinned to a shared fixture set in
// member_liveness_test.go, which is what keeps them from drifting apart.
//
// This is deliberately the STRICTEST predicate in the codebase. Several older
// call sites check only `status = 'active'` and omit deleted_at (see
// auth_repository's list/count helpers); those are load-bearing for other features
// and are not swept in here, but anything deciding who RECEIVES work should use
// this pair rather than hand-rolling a third variant.

// ActiveMemberSQL selects the live members from a candidate list.
//
// Both legs matter and they catch different exits:
//   - status = 'active' excludes a SUSPENDED member, whose row survives intact.
//   - deleted_at IS NULL catches org-level soft delete. It is written out by hand
//     because OrgUser.DeletedAt is a plain *time.Time, not gorm.DeletedAt, so GORM
//     adds no soft-delete scope and an omitted leg would silently match tombstones.
//
// A REMOVED member matches neither leg — removal hard-deletes the org_users row, so
// they simply fail to appear in the result.
const ActiveMemberSQL = `SELECT user_id FROM org_users
	WHERE org_id = ? AND user_id IN ? AND status = 'active' AND deleted_at IS NULL`

// ActiveMemberIDs returns which of the given users are live members of the org, in
// ONE round trip.
//
// Batched rather than N point lookups because callers check a whole pool per record,
// sometimes on a public hot path: the obvious GetOrgUser loop Preloads Role, so N
// members would cost 2N queries plus N role hydrations nobody reads. org_users'
// primary key is (user_id, org_id), so the IN-list is one index probe per id.
//
// ERROR CONTRACT — an error means "unknown", NOT "nobody is active". Callers must
// decide their own polarity deliberately and the two in this codebase differ, for
// reasons documented at each: lead capture fails OPEN (a refused one-shot webhook
// loses the lead outright), automation assignment fails CLOSED (the run is retried
// and surfaced in run history). Do not "simplify" either into the other.
func ActiveMemberIDs(ctx context.Context, db *gorm.DB, orgID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	live := make(map[uuid.UUID]bool, len(userIDs))
	if len(userIDs) == 0 {
		// Postgres rejects `IN ()`. An empty probe is a legitimate caller state (an
		// empty pool), not an error.
		return live, nil
	}
	var ids []uuid.UUID
	if err := db.WithContext(ctx).Raw(ActiveMemberSQL, orgID, userIDs).Scan(&ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		live[id] = true
	}
	return live, nil
}
