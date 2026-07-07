package automation

import (
	"context"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// actor.go holds the P8 "run-as-creator" actor model (plan §3.5, decision §5.2).
//
// Before P8 the executors wrote records via direct SQL with no caller on the
// context, so PermissionUseCase treated every automation write as a trusted
// in-process call and allowed it — meaning an own-scope role holding
// workflows.manage could edit ANY record in the org by authoring a workflow,
// escalating past its OLS/FLS/own-scope. P8 closes that: the engine resolves the
// workflow's author into a full domain.Caller and attaches it to the execution
// context, so the record-writing executors enforce the author's OLS + FLS +
// own-scope and attribute the P5a audit trail to them — exactly as REST does.
//
// The engine's own bookkeeping (locking, action logs, run-status writes) keeps
// running on the plain engine context; only the ACTION side-effects run under the
// author's identity, so a restricted author can never corrupt the run's own
// records.

// CallerResolver resolves a workflow author's full identity (role, role id, data
// scope, owner flag) so actions can run as the author. Injected via
// WithCallerResolver; when nil (unit tests) the engine attaches no caller and
// actions keep the legacy trusted-in-process behavior — which is consistent with
// a nil authorizer (both must be wired for enforcement to bite).
type CallerResolver interface {
	ResolveCaller(ctx context.Context, orgID, userID uuid.UUID) (domain.Caller, error)
}

// actorContext returns the context an action executes under. With a resolver
// wired it carries the workflow author's Caller (WithCallerIdentity), so the
// record-writing executors enforce OLS/FLS/own-scope against the author's role
// and stamp the audit actor. Fail-closed: an unknown workflow or an unresolvable
// author (departed/suspended member) yields a RESTRICTED caller — a real user id
// for audit attribution but no role identity, so OLS default-denies every record
// write while non-writing actions (email/webhook/delay) still run.
func (e *Engine) actorContext(ctx context.Context, run *WorkflowRun) context.Context {
	if e.callerResolver == nil {
		return ctx // no resolver (unit tests): legacy trusted behavior, matches nil authz
	}

	wf, err := e.repo.GetWorkflowByID(ctx, run.OrgID, run.WorkflowID)
	if err != nil || wf == nil {
		e.logger.Warn("automation: workflow not found for actor resolution — running restricted",
			"run_id", run.ID.String(),
			"workflow_id", run.WorkflowID.String(),
		)
		return domain.WithCallerIdentity(ctx, restrictedCaller(uuid.Nil))
	}

	caller, err := e.callerResolver.ResolveCaller(ctx, run.OrgID, wf.CreatedBy)
	if err != nil {
		// run-as-creator: no resolvable creator ⇒ no authority. Fail closed so a
		// workflow authored by a since-removed/suspended member can't keep running
		// record writes with the old god-mode behavior.
		e.logger.Warn("automation: workflow author unresolved — running restricted (record writes denied)",
			"run_id", run.ID.String(),
			"workflow_id", wf.ID.String(),
			"created_by", wf.CreatedBy.String(),
			"error", err.Error(),
		)
		return domain.WithCallerIdentity(ctx, restrictedCaller(wf.CreatedBy))
	}

	return domain.WithCallerIdentity(ctx, caller)
}

// restrictedCaller is the fail-closed actor: it carries a real user id (so an
// attempted write is still audit-attributable) but no role identity, so
// PermissionUseCase.resolveRoleID fails and Authorize/HasCapability default-deny.
// DataScope is set to 'own' as defense-in-depth in case a future code path reads
// it before OLS runs.
func restrictedCaller(userID uuid.UUID) domain.Caller {
	return domain.Caller{UserID: userID, DataScope: domain.DataScopeOwn}
}

// ownScopeAllows reports whether an own-scoped author may act on a record,
// mirroring the read-layer owned-OR-shared rule (repository/scopes.go) so
// automation can't mutate a record the author could never see. Only meaningful for
// contacts/deals (the objects with owner_user_id + record_shares): table is the
// SQL table ("contacts"/"deals"), recordType the record_shares discriminator
// ("contact"/"deal"). KEEP IN SYNC with repository/scopes.go applyScopeFromCtx.
func ownScopeAllows(ctx context.Context, db *gorm.DB, orgID uuid.UUID, table, recordType string, recordID, userID uuid.UUID) (bool, error) {
	var allowed bool
	q := "SELECT EXISTS(SELECT 1 FROM " + table + " t WHERE t.id = ? AND t.org_id = ? AND " +
		"(t.owner_user_id = ? OR EXISTS (SELECT 1 FROM record_shares rs WHERE rs.record_id = t.id AND rs.record_type = ? AND rs.grantee_user_id = ?)))"
	err := db.WithContext(ctx).Raw(q, recordID, orgID, userID, recordType, userID).Scan(&allowed).Error
	return allowed, err
}
