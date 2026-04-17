package http

import (
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

		if redisClient != nil {
			cacheKey := "session:" + claims.UserID.String() + ":org:" + claims.OrgID.String()
			val, err := redisClient.Get(c.Request.Context(), cacheKey).Result()
			if err == nil && val != "" {
				// We just store "status:roleName"
				parts := strings.Split(val, ":")
				if len(parts) == 2 {
					status = parts[0]
					roleName = parts[1]
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
				}
				_ = redisClient.Set(c.Request.Context(), cacheKey, status+":"+roleName, 5*time.Minute).Err()
			}
		}

		if status != "active" {
			c.AbortWithStatusJSON(http.StatusForbidden, domain.Err("account suspended or inactive"))
			return
		}

		c.Set("role", roleName)
		scopedCtx := repository.WithDataScope(c.Request.Context(), roleName, claims.UserID)
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

		if roleStr == "super_admin" {
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
