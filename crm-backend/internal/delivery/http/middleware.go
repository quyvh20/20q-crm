package http

import (
	"net/http"
	"strings"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ============================================================
// Auth Middleware — extracts JWT from Authorization header
// ============================================================

func AuthMiddleware(jwtSecret string) gin.HandlerFunc {
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

		// Inject into context
		c.Set("user_id", claims.UserID)
		c.Set("org_id", claims.OrgID)
		c.Set("role", claims.Role)

		c.Next()
	}
}

// ============================================================
// RBAC Middleware — checks user's role against required roles
// ============================================================

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

		for _, r := range roles {
			if r == roleStr {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("insufficient permissions"))
	}
}

// ============================================================
// Context Helpers
// ============================================================

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
