package http

import (
	"crypto/subtle"
	"net/http"
	"strconv"
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

		status := "active"
		roleName := claims.Role
		// Default to trusting the claim's values; the cache/DB path below overrides
		// them with the authoritative version/scope when Redis is available.
		tokenVersion := claims.TokenVersion
		dataScope := claims.DataScope
		if dataScope == "" {
			dataScope = domain.DataScopeAll // pre-P3 token → default to org-wide scope
		}

		if redisClient != nil {
			cacheKey := "session:" + claims.UserID.String() + ":org:" + claims.OrgID.String()
			val, err := redisClient.Get(c.Request.Context(), cacheKey).Result()
			if err == nil && val != "" {
				// Value is "status:roleName:tokenVersion:dataScope" (P3). Shorter
				// legacy entries (pre-P2/P3) are still honored for the fields they
				// carry; missing trailing fields leave the claim's value in place
				// until the entry expires and is rewritten in the full form.
				parts := strings.SplitN(val, ":", 4)
				if len(parts) >= 2 {
					status = parts[0]
					roleName = parts[1]
					if len(parts) >= 3 {
						if v, e := strconv.Atoi(parts[2]); e == nil {
							tokenVersion = v
						}
					}
					if len(parts) >= 4 && parts[3] != "" {
						dataScope = parts[3]
					}
				}
			} else {
				// Cache miss, hit DB
				ou, err := authRepo.GetOrgUser(c.Request.Context(), claims.UserID, claims.OrgID)
				if err != nil || ou == nil || ou.DeletedAt != nil {
					c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("access denied"))
					return
				}
				status = ou.Status
				if ou.Role != nil {
					roleName = ou.Role.Name
					dataScope = domain.DataScopeAll
					if ou.Role.DataScope == domain.DataScopeOwn {
						dataScope = domain.DataScopeOwn
					}
				}
				if tv, e := authRepo.GetUserTokenVersion(c.Request.Context(), claims.UserID); e == nil {
					tokenVersion = tv
				}
				_ = redisClient.Set(c.Request.Context(), cacheKey, status+":"+roleName+":"+strconv.Itoa(tokenVersion)+":"+dataScope, 5*time.Minute).Err()
			}

			// Instant global invalidation (P2): a token minted before a
			// password reset / sign-out-everywhere / theft-triggered bump carries
			// a stale version and is rejected here.
			if claims.TokenVersion != tokenVersion {
				c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("session expired, please sign in again"))
				return
			}
		} else {
			// No Redis (e.g. a dev / small self-host deployment without a cache):
			// there is no session cache to consult, but the authoritative account
			// status and token_version must still be enforced from the DB —
			// otherwise suspension and sign-out-everywhere / password-reset
			// invalidation silently degrade to the ≤2h access-token TTL (the very
			// gap the P4 sessions UI promises to close). One extra read per request
			// on the cache-less path only; production runs Redis and never gets here.
			ou, err := authRepo.GetOrgUser(c.Request.Context(), claims.UserID, claims.OrgID)
			if err != nil || ou == nil || ou.DeletedAt != nil {
				c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("access denied"))
				return
			}
			status = ou.Status
			if ou.Role != nil {
				roleName = ou.Role.Name
				dataScope = domain.DataScopeAll
				if ou.Role.DataScope == domain.DataScopeOwn {
					dataScope = domain.DataScopeOwn
				}
			}
			if tv, e := authRepo.GetUserTokenVersion(c.Request.Context(), claims.UserID); e == nil {
				tokenVersion = tv
			}
			if claims.TokenVersion != tokenVersion {
				c.AbortWithStatusJSON(http.StatusUnauthorized, domain.Err("session expired, please sign in again"))
				return
			}
		}

		if status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("account suspended or inactive"))
			return
		}

		c.Set("role", roleName)
		c.Set("data_scope", dataScope)
		scopedCtx := repository.WithDataScope(c.Request.Context(), dataScope, claims.UserID)
		// Carry the caller identity so RecordService can enforce Object-Level
		// Security and stamp the audit actor without threading role/user through
		// every method (plan P5a). Set for every protected route; a request that
		// reaches RecordService without it is a trusted in-process call.
		scopedCtx = domain.WithCaller(scopedCtx, roleName, claims.UserID)
		// Carry transport detail so admin mutations (member/role/permission) can
		// stamp an auth_events row with the actor's IP/UA without threading
		// RequestMeta through every usecase method (P4).
		scopedCtx = domain.WithRequestMeta(scopedCtx, domain.RequestMeta{IP: c.ClientIP(), UserAgent: c.Request.UserAgent()})
		c.Request = c.Request.WithContext(scopedCtx)

		c.Next()
	}
}

func RequireRole(roles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, exists := c.Get("role")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("role not found in context"))
			return
		}

		roleStr, ok := userRole.(string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("invalid role type"))
			return
		}

		if roleStr == domain.RoleOwner {
			c.Next()
			return
		}

		for _, r := range roles {
			if r == roleStr {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("insufficient permissions"))
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
// hardcoded RequireRole name lists for admin/workspace powers. It reads the
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
	if err := authz.Authorize(c.Request.Context(), orgID, slug, action); err != nil {
		abortWithAppError(c, err)
		return
	}
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
