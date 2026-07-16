package integrations

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Rate-limit defaults for the public capture endpoint.
const (
	defaultCaptureLimit  = 120 // requests per window, per key
	defaultCaptureWindow = time.Minute
	// bucketIdleTTL is how long an unused in-process bucket survives a sweep.
	// Without a sweep the map grows without bound on a public endpoint — the
	// automation limiter's live defect, which is memory exhaustion by strangers.
	bucketIdleTTL = 10 * time.Minute
)

// RateLimiter throttles the public capture endpoint.
//
// It FAILS CLOSED, which is the whole point and the opposite of the app's other
// limiter (RateLimitByIP no-ops when Redis is nil and calls c.Next() when Redis
// errors). That trade is defensible for a login form; it is not for this endpoint,
// where every accepted request writes a record, enrols workflows and can send
// billable email. A Redis blip must not become an unmetered write channel — so a
// Redis failure degrades to a strict in-process bucket rather than to "allow".
//
// Per-process, not distributed: on multiple replicas the effective limit is
// N×limit. Acceptable on single-replica Railway and documented rather than
// pretended away.
type RateLimiter struct {
	redis  *redis.Client
	limit  int
	window time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	stop    chan struct{}
}

type bucket struct {
	count    int
	resetsAt time.Time
	seenAt   time.Time
}

// NewRateLimiter builds a limiter. A nil redis client is legal — the in-process
// bucket then does all the work.
func NewRateLimiter(rc *redis.Client, limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = defaultCaptureLimit
	}
	if window <= 0 {
		window = defaultCaptureWindow
	}
	rl := &RateLimiter{
		redis:   rc,
		limit:   limit,
		window:  window,
		buckets: map[string]*bucket{},
		stop:    make(chan struct{}),
	}
	go rl.sweep()
	return rl
}

// Close stops the sweeper.
func (rl *RateLimiter) Close() { close(rl.stop) }

// Allow reports whether `key` may proceed, and how long to wait if not.
//
// Redis is authoritative when reachable; any Redis problem falls through to the
// in-process bucket. It never returns "allowed" because the infrastructure is
// unavailable.
func (rl *RateLimiter) Allow(ctx context.Context, key string) (bool, time.Duration) {
	if rl.redis != nil {
		if ok, retry, err := rl.allowRedis(ctx, key); err == nil {
			return ok, retry
		}
		// Redis errored — fall through to the local bucket, NOT to allow.
	}
	return rl.allowLocal(key)
}

func (rl *RateLimiter) allowRedis(ctx context.Context, key string) (bool, time.Duration, error) {
	rkey := "ratelimit:capture:" + key
	n, err := rl.redis.Incr(ctx, rkey).Result()
	if err != nil {
		return false, 0, err
	}
	if n == 1 {
		// First hit of the window: set the TTL. If this fails the key would never
		// expire and the caller would be throttled forever, so drop the key and let
		// the local bucket decide this request.
		if err := rl.redis.Expire(ctx, rkey, rl.window).Err(); err != nil {
			rl.redis.Del(ctx, rkey)
			return false, 0, err
		}
	}
	if n > int64(rl.limit) {
		ttl, err := rl.redis.TTL(ctx, rkey).Result()
		if err != nil || ttl <= 0 {
			ttl = rl.window
		}
		return false, ttl, nil
	}
	return true, 0, nil
}

// allowLocal is the in-process fallback: a fixed window, honestly named.
func (rl *RateLimiter) allowLocal(key string) (bool, time.Duration) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if !ok || now.After(b.resetsAt) {
		// A fresh window. count starts at 1 — this request — rather than seeding
		// limit-1 tokens, which is how the automation bucket ends up granting
		// limit+1 the moment its limit is parameterized.
		rl.buckets[key] = &bucket{count: 1, resetsAt: now.Add(rl.window), seenAt: now}
		return true, 0
	}
	b.seenAt = now
	if b.count >= rl.limit {
		return false, time.Until(b.resetsAt)
	}
	b.count++
	return true, 0
}

// sweep evicts idle buckets so a public endpoint cannot grow the map without
// bound.
func (rl *RateLimiter) sweep() {
	t := time.NewTicker(bucketIdleTTL)
	defer t.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case now := <-t.C:
			rl.mu.Lock()
			for k, b := range rl.buckets {
				if now.Sub(b.seenAt) > bucketIdleTTL {
					delete(rl.buckets, k)
				}
			}
			rl.mu.Unlock()
		}
	}
}
