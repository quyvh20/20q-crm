package http

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/internal/repository"
	"crm-backend/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func AuthMiddleware(jwtSecret string, authRepo domain.AuthRepository, redisClient *redis.Client) gin.HandlerFunc {
	return authMiddleware(jwtSecret, authRepo, redisClient, false)
}

// AuthMiddlewareOptionalOrg authenticates the bearer token but does NOT require an
// active workspace membership when the token carries no org (org_id == uuid.Nil):
// a zero-membership "dead-end" caller — e.g. a brand-new Google invitee for whom no
// junk personal org was forked (U4 item 6) — can reach account-level routes (/me,
// list/create workspaces, list/accept their own pending invitations) before they
// belong to any workspace. token_version is still enforced (global invalidation via
// sign-out-everywhere / password reset), and an org-SCOPED token still gets the full
// active-membership check — nil-org tolerance is the ONLY relaxation, so no existing
// behavior changes for a normal session.
func AuthMiddlewareOptionalOrg(jwtSecret string, authRepo domain.AuthRepository, redisClient *redis.Client) gin.HandlerFunc {
	return authMiddleware(jwtSecret, authRepo, redisClient, true)
}

// AuthMiddlewareWithAPITokens is AuthMiddleware that ALSO accepts a personal
// access token (U6.5). It is mounted on the /api protected group only — never on
// the /auth/* routes, which are cookie-authenticated: CSRFProtect only bites when
// the refresh cookie is present, so a PAT sent from a browser would reach those
// routes with no CSRF protection at all.
func AuthMiddlewareWithAPITokens(jwtSecret string, authRepo domain.AuthRepository, redisClient *redis.Client, tokenRepo domain.APITokenRepository) gin.HandlerFunc {
	return authMiddleware(jwtSecret, authRepo, redisClient, false, tokenRepo)
}

func authMiddleware(jwtSecret string, authRepo domain.AuthRepository, redisClient *redis.Client, allowNoOrg bool, tokenRepos ...domain.APITokenRepository) gin.HandlerFunc {
	var tokenRepo domain.APITokenRepository
	if len(tokenRepos) > 0 {
		tokenRepo = tokenRepos[0]
	}
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("missing authorization header"))
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("invalid authorization format"))
			return
		}

		tokenString := parts[1]

		// Personal access token (U6.5). Fork BEFORE the JWT parse — a PAT is not a JWT
		// and would simply fail to parse. On this path the token authenticates as its
		// owner and resolves the IDENTICAL Caller a JWT would, so OLS, FLS, row scope
		// and the audit actor all apply with no downstream changes; the token's scopes
		// then narrow it further in RequireCapability.
		if strings.HasPrefix(tokenString, domain.APITokenPrefix) {
			if tokenRepo == nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("API tokens are not accepted on this endpoint"))
				return
			}
			authenticateAPIToken(c, tokenString, tokenRepo, authRepo)
			return
		}

		token, err := jwt.ParseWithClaims(tokenString, &usecase.JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(jwtSecret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("invalid or expired token"))
			return
		}

		claims, ok := token.Claims.(*usecase.JWTClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("invalid token claims"))
			return
		}

		c.Set("user_id", claims.UserID)
		c.Set("org_id", claims.OrgID)

		// Zero-membership caller (org-optional routes only): a nil-org token has no
		// membership to resolve a role/scope from, so skip the GetOrgUser check that
		// would otherwise 403. Authenticate the account directly instead: the user must
		// still EXIST — GetUserByID is soft-delete-scoped, so a deleted account fails
		// closed here (GetUserTokenVersion's Pluck would return 0 for a missing row and
		// let a tv=0 token through) — and carry a current token_version (the
		// sign-out-everywhere / password-reset global-invalidation gate). Only then
		// admit a least-privilege, org-less caller (these routes key off user_id). An
		// org-SCOPED token falls through to the full membership check below, unchanged.
		if allowNoOrg && claims.OrgID == uuid.Nil {
			u, err := authRepo.GetUserByID(c.Request.Context(), claims.UserID)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusInternalServerError, domain.Err("internal server error"))
				return
			}
			if u == nil || claims.TokenVersion != u.TokenVersion {
				c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("session expired, please sign in again"))
				return
			}
			// uuid.Nil role: a zero-membership caller holds no role, so it matches no
			// role-targeted record share (fail closed).
			scopedCtx := repository.WithCallerScope(c.Request.Context(), domain.DataScopeOwn, claims.UserID, uuid.Nil)
			scopedCtx = domain.WithCallerIdentity(scopedCtx, domain.Caller{
				UserID:    claims.UserID,
				DataScope: domain.DataScopeOwn,
			})
			scopedCtx = domain.WithRequestMeta(scopedCtx, domain.RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()})
			c.Request = c.Request.WithContext(scopedCtx)
			c.Next()
			return
		}

		status := "active"
		roleName := claims.Role
		// Default to trusting the claim's values; the cache/DB path below overrides
		// them with the authoritative version/scope/identity when available.
		tokenVersion := claims.TokenVersion
		dataScope := claims.DataScope
		if dataScope == "" {
			dataScope = domain.DataScopeAll // pre-P3 token → default to org-wide scope
		}
		// Role identity (P5 re-key): the rid claim carries it since P3; the cache/DB
		// re-resolution below is authoritative. IsOwner comes ONLY from cache/DB —
		// never from a name comparison on the claim.
		roleID := claims.RoleID
		isOwner := false

		// resolveFromDB reads the authoritative membership + token version. Used on
		// the no-Redis path, on a cache miss, and on a malformed cache entry (which
		// is treated as a miss — fail-safe, never trust a corrupt value). Returns
		// false after aborting when the membership is gone.
		resolveFromDB := func() bool {
			ou, err := authRepo.GetOrgUser(c.Request.Context(), claims.UserID, claims.OrgID)
			if err != nil || ou == nil || ou.DeletedAt != nil {
				c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("access denied"))
				return false
			}
			status = ou.Status
			roleID = ou.RoleID
			if ou.Role != nil {
				roleName = ou.Role.Name
				isOwner = domain.IsOwnerRole(ou.Role)
				// Whitelist, don't coerce-to-all: an unrecognized stored value must
				// narrow to 'own', never widen to the whole workspace.
				dataScope = domain.NormalizeDataScope(ou.Role.DataScope)
			} else {
				// Role-less membership (unresolved/deleted role): least-privilege,
				// fail-closed — the NARROWEST 'own' scope, never a stale-token 'all'
				// (mirrors generateAccessToken's P9 nil-role fix). roleID stays as the
				// stored value, so OLS default-denies when it matches no grants.
				dataScope = domain.DataScopeOwn
			}
			if tv, e := authRepo.GetUserTokenVersion(c.Request.Context(), claims.UserID); e == nil {
				tokenVersion = tv
			}
			return true
		}

		if redisClient != nil {
			cacheKey := usecase.SessionCacheKey(claims.UserID, claims.OrgID)
			val, err := redisClient.Get(c.Request.Context(), cacheKey).Result()
			parsed := false
			if err == nil && val != "" {
				// v2 value: status:tv:ds:rid:isOwner:roleName (P5). v2 keys are only
				// ever written in this full form; a malformed value falls through to
				// the DB below instead of being partially trusted.
				if s, tv, ds, rid, owner, name, ok := usecase.ParseSessionCacheValue(val); ok {
					status, tokenVersion, dataScope, roleID, isOwner, roleName = s, tv, ds, rid, owner, name
					parsed = true
				}
			}
			if !parsed {
				// Cache miss (or malformed entry) — hit the DB and rewrite the entry.
				if !resolveFromDB() {
					return
				}
				_ = redisClient.Set(c.Request.Context(), cacheKey,
					usecase.EncodeSessionCacheValue(status, tokenVersion, dataScope, roleID, isOwner, roleName),
					5*time.Minute).Err()
			}
		} else {
			// No Redis (e.g. a dev / small self-host deployment without a cache):
			// there is no session cache to consult, but the authoritative account
			// status and token_version must still be enforced from the DB —
			// otherwise suspension and sign-out-everywhere / password-reset
			// invalidation silently degrade to the ≤2h access-token TTL (the very
			// gap the P4 sessions UI promises to close). One extra read per request
			// on the cache-less path only; production runs Redis and never gets here.
			if !resolveFromDB() {
				return
			}
		}

		// Instant global invalidation (P2): a token minted before a password reset /
		// sign-out-everywhere / theft-triggered bump carries a stale version and is
		// rejected here.
		if claims.TokenVersion != tokenVersion {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("session expired, please sign in again"))
			return
		}

		if status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("account suspended or inactive"))
			return
		}

		c.Set("role", roleName)
		c.Set("data_scope", dataScope)
		// The workspace 2FA policy (U6.4), evaluated at mint time and carried on the
		// token. RequireTwoFactorSatisfied reads it; routes that don't mount that
		// middleware (enrollment, /me, logout) stay reachable by design.
		c.Set("two_factor_pending", claims.TwoFactorPending)
		// The ROLE rides along because a record can be shared to a role (U6.2): the
		// repository predicate matches shares against the caller's user id, role id,
		// and group memberships.
		scopedCtx := repository.WithCallerScope(c.Request.Context(), dataScope, claims.UserID, roleID)
		// Carry the caller identity so RecordService can enforce Object-Level
		// Security and stamp the audit actor without threading role/user through
		// every method (plan P5a). Authorization keys off RoleID/IsOwner (P5); the
		// name rides along for display/audit and the bridge fallback. Set for every
		// protected route; a request that reaches RecordService without it is a
		// trusted in-process call.
		scopedCtx = domain.WithCallerIdentity(scopedCtx, domain.Caller{
			Role:      roleName,
			UserID:    claims.UserID,
			RoleID:    roleID,
			IsOwner:   isOwner,
			DataScope: dataScope,
		})
		// Carry transport detail so admin mutations (member/role/permission) can
		// stamp an auth_events row with the actor's IP/UA without threading
		// RequestMeta through every usecase method (P4).
		scopedCtx = domain.WithRequestMeta(scopedCtx, domain.RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()})
		c.Request = c.Request.WithContext(scopedCtx)

		c.Next()
	}
}

// abortWithAppError renders an *AppError (falling back to 403) and aborts. Used
// by the capability/OLS middlewares so a denial carries the right status + a
// message that names the missing grant.
func abortWithAppError(c *gin.Context, err error) {
	if appErr, ok := err.(*domain.AppError); ok {
		c.AbortWithStatusJSON(appErr.Code, domain.Err(appErr.Message))
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("insufficient permissions"))
}

// RequireCapability gates a route on a system capability (P3, D5), replacing the
// old hardcoded role-name lists for admin/workspace powers. It reads the
// caller (set by AuthMiddleware) from the request context; the owner role
// bypasses, and a role without the capability row is default-denied with a 403
// that names the missing capability — so custom roles work through the SAME gate
// as system roles.
func RequireCapability(checker domain.CapabilityChecker, capability string) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID, ok := GetOrgID(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("unauthorized"))
			return
		}
		// The API-token scope intersection lives INSIDE HasCapability (and Authorize),
		// not here — see usecase/permission_usecase.go. Most record read routes carry
		// no route-level gate at all, and several capability checks happen outside the
		// middleware entirely (the AI command centre, reports, workspaces), so a check
		// bolted onto the middleware would have left all of them ungated.
		if err := checker.HasCapability(c.Request.Context(), orgID, capability); err != nil {
			abortWithAppError(c, err)
			return
		}
		c.Next()
	}
}

// RequireObjectAccess gates a data route on Object-Level Security for the object
// named by the :slug path param and the given action. Backed by the same OLS
// cache RecordService enforces, so a custom role's grid governs data everywhere —
// not just inside RecordService. owner bypasses; default-deny otherwise.
func RequireObjectAccess(authz domain.RecordAuthorizer, action domain.RecordAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		requireObjectAccess(c, authz, c.Param("slug"), action)
	}
}

// RequireObjectAccessOn is RequireObjectAccess for a fixed slug — used on the
// legacy per-object routes (/contacts, /companies, /deals) whose object is
// implied by the route group rather than a path param.
func RequireObjectAccessOn(authz domain.RecordAuthorizer, slug string, action domain.RecordAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		requireObjectAccess(c, authz, slug, action)
	}
}

func requireObjectAccess(c *gin.Context, authz domain.RecordAuthorizer, slug string, action domain.RecordAction) {
	orgID, ok := GetOrgID(c)
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("unauthorized"))
		return
	}
	if slug == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, domain.Err("missing object slug"))
		return
	}
	// Authorize also applies the API-token scope intersection (records.read /
	// records.write), so a token reaching a record route it was never scoped for is
	// denied here AND on every read route that has no route-level gate at all.
	if err := authz.Authorize(c.Request.Context(), orgID, slug, action); err != nil {
		abortWithAppError(c, err)
		return
	}
	c.Next()
}

// authenticateAPIToken authenticates a personal access token and installs the
// SAME Caller the token's owner would get from a JWT — same role, same row scope,
// same audit actor — plus the token's scopes. Reusing the session's identity
// resolution verbatim is the whole trick: OLS, FLS, own/team scope and audit all
// keep working with no PAT-specific branches downstream.
func authenticateAPIToken(c *gin.Context, secret string, tokenRepo domain.APITokenRepository, authRepo domain.AuthRepository) {
	ctx := c.Request.Context()
	tok, err := tokenRepo.GetByHash(ctx, usecase.HashAPIToken(secret))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, domain.Err("internal server error"))
		return
	}
	// Revocation and expiry are checked on EVERY request. A PAT is not a JWT, so the
	// token_version gate below does not cover it — this read IS the revocation check,
	// which is exactly why it is not cached.
	if tok == nil || !tok.IsLive(time.Now()) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("invalid or revoked API token"))
		return
	}
	// A token must be bound to a workspace. Letting a nil-org token through would
	// drop it into the zero-membership branch, which admits a role-less caller.
	if tok.OrgID == uuid.Nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("invalid API token"))
		return
	}

	ou, err := authRepo.GetOrgUser(ctx, tok.UserID, tok.OrgID)
	if err != nil || ou == nil || ou.DeletedAt != nil || ou.Status != domain.StatusActive {
		// The owner left, was suspended, or lost the membership: the token dies with
		// their access, without anyone having to remember to revoke it.
		c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("access denied"))
		return
	}

	roleName := domain.RoleViewer
	isOwner := false
	dataScope := domain.DataScopeOwn // fail closed if the role can't be resolved
	if ou.Role != nil {
		roleName = ou.Role.Name
		isOwner = domain.IsOwnerRole(ou.Role)
		dataScope = domain.NormalizeDataScope(ou.Role.DataScope)
	}

	// The workspace 2FA policy binds a token exactly as it binds a session. Asserting
	// `false` here would have made a personal access token the way AROUND the policy:
	// a user the workspace is demanding 2FA from could mint one and walk straight
	// past RequireTwoFactorSatisfied without ever enrolling.
	//
	// GetOrgUser does not Preload("User"), so ou.User is nil here — reading
	// ou.User.TotpEnabledAt off it would make twoFactorPending unconditionally
	// false and silently reopen exactly that bypass. Resolve the enrollment state
	// explicitly, and fail CLOSED (treat as pending) if the policy is on and the
	// user can't be loaded to prove enrollment.
	twoFactorPending := false
	if org, err := authRepo.GetOrganizationByID(ctx, tok.OrgID); err == nil && org != nil && org.RequireTwoFactor {
		u, err := authRepo.GetUserByID(ctx, tok.UserID)
		if err != nil || u == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("access denied"))
			return
		}
		twoFactorPending = u.TotpEnabledAt == nil
	}

	c.Set("user_id", tok.UserID)
	c.Set("org_id", tok.OrgID)
	c.Set("role", roleName)
	c.Set("data_scope", dataScope)
	c.Set("two_factor_pending", twoFactorPending)

	scopedCtx := repository.WithCallerScope(ctx, dataScope, tok.UserID, ou.RoleID)
	scopedCtx = domain.WithCallerIdentity(scopedCtx, domain.Caller{
		Role:        roleName,
		UserID:      tok.UserID,
		RoleID:      ou.RoleID,
		IsOwner:     isOwner,
		DataScope:   dataScope,
		IsAPIToken:  true,
		TokenScopes: tok.Scopes,
	})
	scopedCtx = domain.WithRequestMeta(scopedCtx, domain.RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()})
	c.Request = c.Request.WithContext(scopedCtx)

	// Fire-and-forget on a DETACHED context: the request's context is cancelled the
	// moment the response is written, which would abort this write most of the time.
	id := tok.ID
	go func() {
		bg, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tokenRepo.TouchLastUsed(bg, id)
	}()

	c.Next()
}

// RequireVerifiedEmail blocks a sensitive action until the caller has confirmed
// their email (plan D2 soft gate). Runs after AuthMiddleware (reads user_id from
// context) and costs one user lookup, so mount it only on the few routes that
// warrant it. Existing users are grandfathered verified by migration 000026.
func RequireVerifiedEmail(authRepo domain.AuthRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := GetUserID(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("unauthorized"))
			return
		}
		user, err := authRepo.GetUserByID(c.Request.Context(), userID)
		if err != nil || user == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("access denied"))
			return
		}
		if user.EmailVerifiedAt == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err(domain.ErrEmailNotVerified.Message))
			return
		}
		c.Next()
	}
}

// RequireTwoFactorSatisfied is the enforcement half of the workspace 2FA policy
// (U6.4). Login-time checks alone are NOT enough: RefreshToken re-mints a session
// from a cookie with no credential check, so a session established before the
// policy was turned on would otherwise renew itself forever.
//
// It reads the `2fa` claim, which the token minter stamps when the workspace
// requires 2FA and the user has not enrolled. Such a session is confined to the
// enrollment endpoints — it is a real session (it has to be: with no session there
// is no authenticated way to REACH enrollment), but it can't touch anything else.
//
// The 403 carries a distinct code so the SPA can route to the enrollment screen
// instead of rendering a generic "access denied".
func RequireTwoFactorSatisfied() gin.HandlerFunc {
	return func(c *gin.Context) {
		pending, _ := c.Get("two_factor_pending")
		if b, ok := pending.(bool); ok && b {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"data":  nil,
				"error": "this workspace requires two-factor authentication — set it up to continue",
				"code":  "two_factor_required",
			})
			return
		}
		c.Next()
	}
}

// AllowedOrigins is the single source of truth for the browser origins the API
// trusts — used by both CORS and CSRF. Keep in sync with the deployment: the
// configured frontend URL plus local dev and the Cloudflare Pages host.
func AllowedOrigins(frontendURL string) []string {
	return []string{frontendURL, "http://localhost:5173", "https://20q-crm.pages.dev"}
}

func normalizeOrigin(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

// CSRFProtect guards the cookie-authenticated auth routes (/refresh, /logout).
// It only enforces when the request actually relies on the ambient refresh
// cookie: a request that carries the refresh token in its body instead (the
// one-release localStorage shim) is not a CSRF vector, so it passes through.
//
// Primary defense is Origin validation: browsers set the Origin header on
// cross-origin state-changing requests and JS cannot forge it, so a forged
// request from a malicious site carries a non-allowlisted Origin and is rejected.
// This is the CSRF defense that actually works cross-site (Cloudflare Pages
// frontend + separate API host), where the SPA can't read the API-domain
// csrf_token cookie via document.cookie. When no Origin header is present (rare;
// some same-origin requests), it falls back to the same-site double-submit token.
func CSRFProtect(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if n := normalizeOrigin(o); n != "" {
			allowed[n] = true
		}
	}
	return func(c *gin.Context) {
		refreshCookie, _ := c.Cookie(refreshCookieName)
		if refreshCookie == "" {
			c.Next() // body-token shim — not a CSRF vector
			return
		}

		// Origin check (works cross-site).
		if origin := c.GetHeader("Origin"); origin != "" {
			if allowed[normalizeOrigin(origin)] {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err(domain.ErrMissingCSRFToken.Message))
			return
		}

		// No Origin header → fall back to the same-site double-submit token.
		csrfCookie, _ := c.Cookie(csrfCookieName)
		header := c.GetHeader("X-CSRF-Token")
		if csrfCookie != "" && header != "" &&
			subtle.ConstantTimeCompare([]byte(csrfCookie), []byte(header)) == 1 {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, domain.Err(domain.ErrMissingCSRFToken.Message))
	}
}

func GetUserID(c *gin.Context) (uuid.UUID, bool) {
	id, exists := c.Get("user_id")
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := id.(uuid.UUID)
	return uid, ok
}

func GetOrgID(c *gin.Context) (uuid.UUID, bool) {
	id, exists := c.Get("org_id")
	if !exists {
		return uuid.Nil, false
	}
	uid, ok := id.(uuid.UUID)
	return uid, ok
}

func GetRole(c *gin.Context) (string, bool) {
	role, exists := c.Get("role")
	if !exists {
		return "", false
	}
	roleStr, ok := role.(string)
	return roleStr, ok
}

// GetDataScope returns the caller's resolved row scope ('own'|'all'), set by
// AuthMiddleware. Defaults to 'all' when absent.
func GetDataScope(c *gin.Context) string {
	if v, ok := c.Get("data_scope"); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return domain.DataScopeAll
}
