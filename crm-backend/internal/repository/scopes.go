package repository

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ctxKey string

const (
	ctxKeyRole   ctxKey = "data_scope_role"
	ctxKeyUserID ctxKey = "data_scope_user_id"
)

func WithDataScope(ctx context.Context, role string, userID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, ctxKeyRole, role)
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	return ctx
}

func extractDataScope(ctx context.Context) (role string, userID uuid.UUID, ok bool) {
	r, rOk := ctx.Value(ctxKeyRole).(string)
	u, uOk := ctx.Value(ctxKeyUserID).(uuid.UUID)
	if !rOk || !uOk {
		return "", uuid.Nil, false
	}
	return r, u, true
}

func DataScope(orgID uuid.UUID, role string, userID uuid.UUID) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if role == "sales" {
			return db.Where("org_id = ? AND owner_user_id = ?", orgID, userID)
		}
		return db.Where("org_id = ?", orgID)
	}
}

func applyScopeFromCtx(db *gorm.DB, ctx context.Context, orgID uuid.UUID, table string) *gorm.DB {
	role, userID, ok := extractDataScope(ctx)
	if ok && role == "sales" {
		return db.Where(table+".org_id = ? AND "+table+".owner_user_id = ?", orgID, userID)
	}
	return db.Where(table+".org_id = ?", orgID)
}
