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
	// Role is the role NAME — display/audit vocabulary only. Authorization keys
	// off RoleID exclusively (the P5 name-fallback bridge was deleted in P9); a
	// caller with no RoleID resolves to no grants and is default-denied.
	Role   string
	UserID uuid.UUID
	// RoleID is the caller's role identity (R1 re-key). Names are tenant-editable
	// vocabulary — a rename or duplicate must never change what a role can do, so
	// every authorization layer (OLS/FLS/capabilities/layouts) looks up grants by
	// this id. uuid.Nil means "not resolved": since the P9 bridge removal there is
	// no name fallback, so an unresolved role is default-denied everywhere.
	RoleID uuid.UUID
	// IsOwner marks the god-mode principal, resolved from roles.is_owner (never
	// the name string) by the auth middleware. Every owner bypass keys off this.
	IsOwner bool
	// DataScope is the caller's row visibility ('own' | 'all'), resolved from the
	// role's data_scope by the auth middleware (same value threaded to the repos via
	// repository.WithDataScope). Empty on a name-only bridge caller. The AI reads it
	// to fork own-scope and shape its persona (P7); REST relies on the repo-layer
	// scope, so it doesn't need to read this.
	DataScope string
}

type callerCtxKey struct{}

// WithCallerIdentity returns a context carrying the full caller identity —
// role id + owner flag included (P5). Set once by the auth middleware after the
// membership is resolved.
func WithCallerIdentity(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, c)
}

// CallerFromContext returns the caller and true when one is present. A missing
// caller (ok=false) marks a trusted in-process call — see Caller's doc.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerCtxKey{}).(Caller)
	return c, ok
}

type requestMetaCtxKey struct{}

// WithRequestMeta carries the request's transport detail (IP, User-Agent) on the
// context so the admin usecases (workspace/role/permission) can stamp an
// auth_events row (P4) without threading RequestMeta through every method — the
// same pattern WithCallerIdentity uses. Set once by the auth middleware.
func WithRequestMeta(ctx context.Context, meta RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaCtxKey{}, meta)
}

// RequestMetaFromContext returns the request meta and true when present.
func RequestMetaFromContext(ctx context.Context) (RequestMeta, bool) {
	m, ok := ctx.Value(requestMetaCtxKey{}).(RequestMeta)
	return m, ok
}
