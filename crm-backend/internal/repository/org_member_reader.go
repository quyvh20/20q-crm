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

// ActiveMemberIDs returns which of the given users are live members of the org, in
// ONE round trip.
//
// The rule and the query live in member_liveness.go, shared with automation's
// assignment routing and twinned with domain.OrgUser.IsLive. This method exists to
// satisfy integrations.MemberChecker, which is a port over a struct rather than a
// bare function — it is the binding, not a second implementation.
//
// ERROR CONTRACT — for THIS caller (lead capture) an error means "unknown" and the
// caller must fail OPEN: treating a DB blip as a dead pool would unown real leads,
// and an unowned contact is invisible to own-scoped reps. Do not "simplify" a caller
// into `if err != nil { return nil }`. Automation's assignment path deliberately
// takes the opposite polarity; see member_liveness.go.
func (r *OrgMemberReader) ActiveMemberIDs(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	return ActiveMemberIDs(ctx, r.db, orgID, userIDs)
}
