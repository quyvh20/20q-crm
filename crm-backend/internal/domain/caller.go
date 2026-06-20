package domain

import (
	"context"

	"github.com/google/uuid"
)

// Caller is the authenticated principal behind a request: their role name (the
// same string the auth middleware resolves from the JWT / org_users.role_id) and
// user id. RecordService reads it from the context to enforce Object-Level
// Security (which role) and to stamp the audit actor (which user).
//
// It is carried on the context — not threaded through every method signature —
// because the existing data-scope plumbing already works that way
// (repository.WithDataScope) and RecordService already receives the context. The
// auth middleware sets it for every protected route, so any user-facing record
// request has a caller. A request WITHOUT a caller is therefore an in-process,
// trusted call (automation, AI, a seed/migration) and OLS treats it as such.
type Caller struct {
	Role   string
	UserID uuid.UUID
}

type callerCtxKey struct{}

// WithCaller returns a context carrying the caller identity. Called once by the
// auth middleware after the role is resolved.
func WithCaller(ctx context.Context, role string, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, Caller{Role: role, UserID: userID})
}

// CallerFromContext returns the caller and true when one is present. A missing
// caller (ok=false) marks a trusted in-process call — see Caller's doc.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerCtxKey{}).(Caller)
	return c, ok
}
