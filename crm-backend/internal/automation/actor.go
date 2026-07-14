package automation

import (
	"context"
	"fmt"

	"crm-backend/internal/domain"
	"crm-backend/internal/repository"

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
		return withActorScope(ctx, restrictedCaller(wf.CreatedBy), run.OrgID)
	}

	return withActorScope(ctx, caller, run.OrgID)
}

// withActorScope attaches the author's identity AND their repository row scope.
//
// Both halves matter. WithCallerIdentity alone drives the executors' explicit
// authorization probes, but any action that goes through a REPOSITORY — a
// RecordService-backed read like find_records/enroll_records — filters rows off the
// repository context instead. With no scope on that context the repositories treat
// the call as a trusted in-process one and return the whole org, so an own-scoped
// author could enumerate every record in the workspace through a workflow. The
// explicit probes then become defense in depth rather than the only line.
func withActorScope(ctx context.Context, caller domain.Caller, orgID uuid.UUID) context.Context {
	ctx = domain.WithCallerIdentity(ctx, caller)
	return repository.WithCallerScope(ctx, caller.DataScope, caller.UserID, caller.RoleID)
}

// restrictedCaller is the fail-closed actor: it carries a real user id (so an
// attempted write is still audit-attributable) but no role identity, so
// PermissionUseCase.resolveRoleID fails and Authorize/HasCapability default-deny.
// DataScope is set to 'own' as defense-in-depth in case a future code path reads
// it before OLS runs.
func restrictedCaller(userID uuid.UUID) domain.Caller {
	return domain.Caller{UserID: userID, DataScope: domain.DataScopeOwn}
}

// rowScopeAllows reports whether a row-scoped author ('own' or 'team') may act on
// a record. It calls the SAME predicate builder the repositories use
// (repository.RecordAccessPredicate), so automation can never reach a record the
// author could not reach through the UI — and so a new scope value or a new share
// target lands here automatically instead of via a "keep in sync" comment, which
// is what this function used to carry.
//
// requireEdit distinguishes reads from writes: a 'view' share grants visibility
// only. Before U6 automation used the READ rule to authorize WRITES, so a
// view-shared record was silently writable by a workflow (the U0.4 bug, still
// alive on this path).
func rowScopeAllows(ctx context.Context, db *gorm.DB, orgID uuid.UUID, table, recordType string, recordID uuid.UUID, caller domain.Caller, requireEdit bool) (bool, error) {
	pred, args := repository.RecordAccessPredicate(repository.RecordAccessArgs{
		Table:       "t",
		RecordType:  recordType,
		OrgID:       orgID,
		Scope:       caller.DataScope,
		UserID:      caller.UserID,
		RoleID:      caller.RoleID,
		RequireEdit: requireEdit,
	})
	if pred == "" {
		return true, nil // 'all' scope — no row restriction
	}
	var allowed bool
	q := "SELECT EXISTS(SELECT 1 FROM " + table + " t WHERE t.id = ? AND t.org_id = ? AND " + pred + ")"
	full := append([]any{recordID, orgID}, args...)
	err := db.WithContext(ctx).Raw(q, full...).Scan(&allowed).Error
	return allowed, err
}

// authorizeLinkedRecordRead enforces the workflow author's read access (OLS +
// own-scope) on the contact/deal a task or activity is being attached to.
// Creating a follow-up artifact against a record the author cannot see would
// leak its existence and bypass the P8 actor model — deny instead. A nil authz
// (unit tests) is a no-op, matching the other executors.
func authorizeLinkedRecordRead(ctx context.Context, db *gorm.DB, authz domain.RecordAuthorizer, orgID uuid.UUID, contactID, dealID *uuid.UUID) error {
	if authz == nil {
		return nil
	}
	caller, hasCaller := domain.CallerFromContext(ctx)
	check := func(slug string, id *uuid.UUID) error {
		if id == nil {
			return nil
		}
		if err := authz.Authorize(ctx, orgID, slug, domain.ActionRead); err != nil {
			return err
		}
		// 'all' scope (and the owner role) reaches every row; anything narrower goes
		// through the row predicate. Testing for "is all" rather than "is own" is what
		// keeps a newly added scope enforced by default.
		if !hasCaller || caller.IsOwner || caller.DataScope == domain.DataScopeAll {
			return nil
		}
		allowed, err := rowScopeAllows(ctx, db, orgID, slug+"s", slug, *id, caller, false)
		if err != nil {
			return fmt.Errorf("row-scope check failed for %s: %w", slug, err)
		}
		if !allowed {
			return fmt.Errorf("your role may only reference %s records you can access", slug)
		}
		return nil
	}
	if err := check("contact", contactID); err != nil {
		return err
	}
	return check("deal", dealID)
}
