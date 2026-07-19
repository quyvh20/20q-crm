package repository

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OrgMemberReader is the membership slice the integrations package needs: the
// single-user lookup it already used, plus a BATCHED liveness check for owner
// routing.
//
// It EMBEDS domain.AuthRepository rather than widening that interface. Adding one
// method to a ~40-method port would force every hand-written fake in the test suite
// to grow a stub for a need only this one caller has; embedding inherits GetOrgUser
// whole and adds the new method beside it.
type OrgMemberReader struct {
	domain.AuthRepository
	db *gorm.DB
}

// NewOrgMemberReader wraps the auth repository with batched membership reads.
func NewOrgMemberReader(db *gorm.DB, auth domain.AuthRepository) *OrgMemberReader {
	return &OrgMemberReader{AuthRepository: auth, db: db}
}

// activeMemberSQL is the SQL twin of integrations.IsLiveMember. The two encode one
// rule — "may this person be handed a lead" — in two languages, so they are named
// as twins in both places and tested against one shared fixture set.
//
// `deleted_at IS NULL` is written out by hand deliberately: OrgUser.DeletedAt is a
// plain *time.Time, NOT gorm.DeletedAt, so GORM applies no soft-delete scope and an
// omitted leg would silently match soft-deleted rows. The real protection against a
// removed member is row ABSENCE (member removal hard-deletes the org_users row);
// this leg is cheap defence and keeps the two twins textually parallel.
const activeMemberSQL = `SELECT user_id FROM org_users
	WHERE org_id = ? AND user_id IN ? AND status = 'active' AND deleted_at IS NULL`

// ActiveMemberIDs returns which of the given users are live members of the org, in
// ONE round trip.
//
// Batched rather than N GetOrgUser calls because this runs on the public capture hot
// path: GetOrgUser Preloads Role, so N members would cost 2N queries plus N role
// hydrations nobody reads. org_users' primary key is (user_id, org_id), so the
// IN-list is one index probe per id.
//
// ERROR CONTRACT — the caller must fail OPEN. A returned error means "unknown", NOT
// "nobody is active": treating a DB blip as a dead pool would unown real leads, and
// an unowned contact is invisible to own-scoped reps. Do not "simplify" a caller
// into `if err != nil { return nil }`.
func (r *OrgMemberReader) ActiveMemberIDs(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var ids []uuid.UUID
	if err := r.db.WithContext(ctx).Raw(activeMemberSQL, orgID, userIDs).Scan(&ids).Error; err != nil {
		return nil, err
	}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}
