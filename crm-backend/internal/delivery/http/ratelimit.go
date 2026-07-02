package http

import (
	"net/http"
	"strconv"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// Per-IP request limits on the credential endpoints (P2). This is the coarse,
// volumetric layer that protects endpoints with no email in the body (e.g.
// /refresh) and blunts distributed floods; the finer per-email failure backoff
// lives in the Login usecase. The threshold is deliberately loose so a shared
// office/NAT IP doesn't lock legitimate users out — authenticated data routes
// are never rate-limited, only the auth group.
const (
	authIPRateLimit  = 30
	authIPRateWindow = time.Minute
)

// RateLimitByIP is a fixed-window Redis limiter keyed by client IP. It fails
// OPEN — a Redis outage must never lock everyone out of login — and no-ops when
// Redis is unavailable (dev). On breach it returns 429 with a Retry-After.
func RateLimitByIP(redisClient *redis.Client, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if redisClient == nil {
			c.Next()
			return
		}
		ctx := c.Request.Context()
		key := "ratelimit:auth:ip:" + c.ClientIP()

		cnt, err := redisClient.Incr(ctx, key).Result()
		if err != nil {
			c.Next() // fail open
			return
		}
		if cnt == 1 {
			redisClient.Expire(ctx, key, window)
		}
		if cnt > int64(limit) {
			ttl, _ := redisClient.TTL(ctx, key).Result()
			retry := int(ttl.Seconds())
			if retry < 1 {
				retry = int(window.Seconds())
			}
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, domain.Err(domain.ErrTooManyRequests.Message))
			return
		}
		c.Next()
	}
}
