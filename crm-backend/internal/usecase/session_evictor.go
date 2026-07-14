package usecase

import (
	"context"
	"strconv"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// SessionCacheKey is the middleware's per-(user, org) session-cache key. Shared
// between the middleware (read/write) and every eviction site so the key format
// can never drift between them.
//
// v2 (P5 identity re-key): the value gains role_id + is_owner, so the KEY is
// bumped rather than appending to the v1 value — the old parser (SplitN(val,
// ":", 4)) would have stuffed the extra fields into dataScope and silently
// widened own-scope users to all-scope during a rollback/mixed-version window.
// Orphaned v1 entries are never read again and expire within their 5-minute TTL.
func SessionCacheKey(userID, orgID uuid.UUID) string {
	return "session:v2:" + userID.String() + ":org:" + orgID.String()
}

// EncodeSessionCacheValue renders the v2 session-cache value:
//
//	status:tokenVersion:dataScope:roleID:isOwner:roleName
//
// The tenant-editable role NAME goes LAST because it may itself contain colons;
// every other field is colon-free (fixed vocabulary, int, uuid, bool), so
// ParseSessionCacheValue can split with a bounded SplitN and keep the name whole.
func EncodeSessionCacheValue(status string, tokenVersion int, dataScope string, roleID uuid.UUID, isOwner bool, roleName string) string {
	return status + ":" + strconv.Itoa(tokenVersion) + ":" + dataScope + ":" +
		roleID.String() + ":" + strconv.FormatBool(isOwner) + ":" + roleName
}

// ParseSessionCacheValue decodes a v2 session-cache value. ok=false marks a
// malformed entry (wrong field count or bad types) — the middleware treats that
// as a cache miss and re-resolves from the DB, so a corrupt entry can never
// grant stale or widened access.
func ParseSessionCacheValue(val string) (status string, tokenVersion int, dataScope string, roleID uuid.UUID, isOwner bool, roleName string, ok bool) {
	parts := strings.SplitN(val, ":", 6)
	if len(parts) != 6 {
		return "", 0, "", uuid.Nil, false, "", false
	}
	tv, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, "", uuid.Nil, false, "", false
	}
	rid, err := uuid.Parse(parts[3])
	if err != nil {
		return "", 0, "", uuid.Nil, false, "", false
	}
	owner, err := strconv.ParseBool(parts[4])
	if err != nil {
		return "", 0, "", uuid.Nil, false, "", false
	}
	// Whitelist the scope (unknown → narrowest). The old shape here was
	// `if scope != own { scope = all }`, which would have handed every team-scoped
	// user the entire workspace on their first Redis cache hit — and only in
	// production, since a cache-less dev run re-reads the DB on every request and
	// never exercises this path.
	scope := domain.NormalizeDataScope(parts[2])
	return parts[0], tv, scope, rid, owner, parts[5], true
}

// SessionEvictor deletes a per-(user, org) session-cache entry so a membership
// or role change takes effect on the target's next request instead of after the
// cache TTL — without it, a removed/suspended/demoted member keeps their old
// access for up to 5 minutes (P10 P0). Implementations must be nil-safe.
type SessionEvictor interface {
	EvictOrgSession(ctx context.Context, userID, orgID uuid.UUID)
}

// RedisSessionEvictor is the production SessionEvictor over the same Redis the
// auth middleware caches sessions in. A nil evictor or client no-ops: without
// Redis the middleware re-reads the DB per request, which is already fresh.
type RedisSessionEvictor struct {
	Client *redis.Client
}

func (e *RedisSessionEvictor) EvictOrgSession(ctx context.Context, userID, orgID uuid.UUID) {
	if e == nil || e.Client == nil {
		return
	}
	_ = e.Client.Del(ctx, SessionCacheKey(userID, orgID)).Err()
}

// PermissionCacheFanout satisfies the role usecase's permissionCache dependency
// by delegating to the real OLS/FLS/capability cache and ALSO busting the layout
// cache: the layout resolver's role→layout map is keyed by role name until the
// P5 id re-key, so a role rename/delete must refresh layouts alongside
// permissions or layouts misroute for the cache TTL.
type PermissionCacheFanout struct {
	Perm interface {
		Invalidate(orgID uuid.UUID)
		EnsureSeeded(ctx context.Context, orgID uuid.UUID) error
	}
	Layouts interface{ Invalidate(orgID uuid.UUID) }
}

func (f *PermissionCacheFanout) Invalidate(orgID uuid.UUID) {
	f.Perm.Invalidate(orgID)
	if f.Layouts != nil {
		f.Layouts.Invalidate(orgID)
	}
}

func (f *PermissionCacheFanout) EnsureSeeded(ctx context.Context, orgID uuid.UUID) error {
	return f.Perm.EnsureSeeded(ctx, orgID)
}
