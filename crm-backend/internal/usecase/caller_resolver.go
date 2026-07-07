package usecase

import (
	"context"
	"net/http"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// callerResolver resolves the full identity of an arbitrary (org, user) pair into
// a domain.Caller — the SAME identity the auth middleware builds per request
// (role name, role id, data scope, owner flag), but from just the ids, with no
// HTTP request in flight.
//
// P8 uses it for the automation "run-as-creator" actor model: a background
// worker executing a workflow has no request context, so it resolves the
// workflow author here and attaches the resulting Caller to the execution
// context. That makes OLS/FLS/own-scope + the audit trail bind an automation
// write exactly as they bind a REST write — closing the "an own-scope role with
// workflows.manage escalates past its OLS by authoring a workflow" hole (§3.5).
//
// It mirrors the middleware's DB path (authRepo.GetOrgUser → domain.IsOwnerRole)
// rather than the Redis session cache, because the author is not the request
// caller and has no live session to read.
type callerResolver struct {
	authRepo domain.AuthRepository
}

// NewCallerResolver builds the run-as-creator identity resolver over the auth
// repository (the org_users membership + preloaded role).
func NewCallerResolver(authRepo domain.AuthRepository) *callerResolver {
	return &callerResolver{authRepo: authRepo}
}

// ResolveCaller returns the full Caller identity for a (org, user) membership, or
// an error when the user is not an ACTIVE member of the org (removed, suspended,
// or never joined) or the role can't be resolved. Fail-closed by design: an
// unresolved author must NOT run with god-mode — the caller (the engine) treats
// the error as "deny record writes" rather than silently widening access.
func (r *callerResolver) ResolveCaller(ctx context.Context, orgID, userID uuid.UUID) (domain.Caller, error) {
	if userID == uuid.Nil {
		return domain.Caller{}, domain.NewAppError(http.StatusForbidden, "cannot resolve caller: no user id")
	}
	ou, err := r.authRepo.GetOrgUser(ctx, userID, orgID)
	if err != nil {
		return domain.Caller{}, err
	}
	if ou == nil || ou.Role == nil {
		return domain.Caller{}, domain.NewAppError(http.StatusForbidden, "cannot resolve caller: not a member of the org")
	}
	if ou.Status != domain.StatusActive {
		return domain.Caller{}, domain.NewAppError(http.StatusForbidden, "cannot resolve caller: membership is not active")
	}
	return domain.Caller{
		Role:      ou.Role.Name,
		UserID:    userID,
		RoleID:    ou.RoleID,
		IsOwner:   domain.IsOwnerRole(ou.Role),
		DataScope: ou.Role.DataScope,
	}, nil
}
