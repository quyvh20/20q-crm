package repository

import (
	"context"

	"crm-backend/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ctxKey string

const (
	// ctxKeyScope carries the caller's row-scope ('own' | 'all'), resolved from the
	// role's data_scope (P3). Row-scope is now data-driven, so any role — system or
	// custom — can be own-scoped without a hardcoded name check.
	ctxKeyScope  ctxKey = "data_scope_scope"
	ctxKeyUserID ctxKey = "data_scope_user_id"
)

// WithDataScope carries the caller's data scope ('own'|'all') and user id so the
// repositories can restrict 'own'-scoped roles to their owned + shared records.
func WithDataScope(ctx context.Context, scope string, userID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyScope, scope)
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	return ctx
}

func extractDataScope(ctx context.Context) (scope string, userID uuid.UUID, ok bool) {
	s, sOk := ctx.Value(ctxKeyScope).(string)
	u, uOk := ctx.Value(ctxKeyUserID).(uuid.UUID)
	if !sOk || !uOk {
		return "", uuid.Nil, false
	}
	return s, u, true
}

func DataScope(orgID uuid.UUID, scope string, userID uuid.UUID) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if scope == domain.DataScopeOwn {
			return db.Where("org_id = ? AND owner_user_id = ?", orgID, userID)
		}
		return db.Where("org_id = ?", orgID)
	}
}

func applyScopeFromCtx(db *gorm.DB, ctx context.Context, orgID uuid.UUID, table string) *gorm.DB {
	scope, userID, ok := extractDataScope(ctx)
	if ok && scope == domain.DataScopeOwn {
		// Enforce 'own' scope + the record_shares fallback (owned OR shared to me).
		recordType := "contact"
		if table == "deals" {
			recordType = "deal"
		}
		return db.Where(table+".org_id = ? AND ("+table+".owner_user_id = ? OR EXISTS (SELECT 1 FROM record_shares rs WHERE rs.record_id = "+table+".id AND rs.record_type = ? AND rs.grantee_user_id = ?))", orgID, userID, recordType, userID)
	}
	return db.Where(table+".org_id = ?", orgID)
}
