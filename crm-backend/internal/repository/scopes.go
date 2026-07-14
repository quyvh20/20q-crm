package repository

import (
	"context"

	"crm-backend/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ctxKey string

const (
	// ctxKeyScope carries the caller's row-scope ('own' | 'team' | 'all'), resolved
	// from the role's data_scope (P3). Row-scope is data-driven, so any role —
	// system or custom — can be row-scoped without a hardcoded name check.
	ctxKeyScope  ctxKey = "data_scope_scope"
	ctxKeyUserID ctxKey = "data_scope_user_id"
	// ctxKeyRoleID carries the caller's ROLE, needed since U6.2 because a record
	// can be shared to a role. Absent (uuid.Nil) simply matches no role-targeted
	// share — fail closed.
	ctxKeyRoleID ctxKey = "data_scope_role_id"
)

// WithCallerScope carries the caller's row scope, user id and role id so the
// repositories can restrict a row-scoped role to the records it owns, its
// teammates own (scope 'team'), or that are shared to it (as a user, via its
// role, or via a group).
func WithCallerScope(ctx context.Context, scope string, userID, roleID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyScope, scope)
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	ctx = context.WithValue(ctx, ctxKeyRoleID, roleID)
	return ctx
}

// WithDataScope is WithCallerScope without a role identity — the caller matches
// user- and group-targeted shares but no role-targeted ones. Kept for in-process
// callers that have no role to hand.
func WithDataScope(ctx context.Context, scope string, userID uuid.UUID) context.Context {
	return WithCallerScope(ctx, scope, userID, uuid.Nil)
}

func extractDataScope(ctx context.Context) (scope string, userID uuid.UUID, ok bool) {
	s, u, _, ok := extractCallerScope(ctx)
	return s, u, ok
}

func extractCallerScope(ctx context.Context) (scope string, userID, roleID uuid.UUID, ok bool) {
	s, sOk := ctx.Value(ctxKeyScope).(string)
	u, uOk := ctx.Value(ctxKeyUserID).(uuid.UUID)
	if !sOk || !uOk {
		return "", uuid.Nil, uuid.Nil, false
	}
	r, _ := ctx.Value(ctxKeyRoleID).(uuid.UUID) // absent → uuid.Nil, matches no role share
	return s, u, r, true
}

// accessArgsFromCtx builds the predicate args for the caller on ctx. ok=false
// means there is no caller scope on the context at all — a trusted in-process
// call (seeder, worker, unit test), which is unrestricted by design.
func accessArgsFromCtx(ctx context.Context, orgID uuid.UUID, table, recordType string, requireEdit bool) (RecordAccessArgs, bool) {
	scope, userID, roleID, ok := extractCallerScope(ctx)
	if !ok {
		return RecordAccessArgs{}, false
	}
	return RecordAccessArgs{
		Table:       table,
		RecordType:  recordType,
		OrgID:       orgID,
		Scope:       scope,
		UserID:      userID,
		RoleID:      roleID,
		RequireEdit: requireEdit,
	}, true
}

// applyScopeFromCtx restricts a READ to the rows the caller may see. recordType is
// the record_shares discriminator (the object slug) and must be passed explicitly:
// all custom objects share one table, so it cannot be inferred.
func applyScopeFromCtx(db *gorm.DB, ctx context.Context, orgID uuid.UUID, table, recordType string) *gorm.DB {
	db = db.Where(table+".org_id = ?", orgID)
	args, ok := accessArgsFromCtx(ctx, orgID, table, recordType, false)
	if !ok {
		return db
	}
	sql, sqlArgs := RecordAccessPredicate(args)
	if sql == "" {
		return db // 'all' scope — org filter is the whole restriction
	}
	return db.Where(sql, sqlArgs...)
}

// applyWriteScopeFromCtx is applyScopeFromCtx for MUTATIONS: a row-scoped caller
// may write records they own (or a teammate's, under 'team' scope) and records
// shared to them at level 'edit'. A 'view' share used to be silently writable
// (U0.4).
func applyWriteScopeFromCtx(db *gorm.DB, ctx context.Context, orgID uuid.UUID, table, recordType string) *gorm.DB {
	db = db.Where(table+".org_id = ?", orgID)
	args, ok := accessArgsFromCtx(ctx, orgID, table, recordType, true)
	if !ok {
		return db
	}
	sql, sqlArgs := RecordAccessPredicate(args)
	if sql == "" {
		return db
	}
	return db.Where(sql, sqlArgs...)
}

// requireWriteVisible enforces write-level row scope for one record before an
// unscoped Save/Updates: a row-scoped caller may write a record they own (or a
// teammate's, under 'team') or one shared to them at level 'edit' — read
// visibility alone is NOT write access (U0.4). No-op for 'all'-scoped callers and
// trusted contexts.
//
// The guard is "is the caller NOT all-scoped", not "is the caller own-scoped":
// the latter shape is how a new scope value silently arrives unenforced.
func requireWriteVisible(db *gorm.DB, ctx context.Context, orgID uuid.UUID, table, recordType string, id uuid.UUID) error {
	scope, _, _, ok := extractCallerScope(ctx)
	if !ok || scope == domain.DataScopeAll {
		return nil
	}
	var n int64
	err := applyWriteScopeFromCtx(db.WithContext(ctx).Table(table), ctx, orgID, table, recordType).
		Where(table+".id = ? AND "+table+".deleted_at IS NULL", id).
		Count(&n).Error
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrRecordNotWritable
	}
	return nil
}
