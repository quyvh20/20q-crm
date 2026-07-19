package integrations

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// errUnexpectedScriptResult guards against the Redis script returning a shape we
// did not expect. Treated like any other Redis error — fall through to the local
// bucket, never to "allow".
var errUnexpectedScriptResult = errors.New("integrations: unexpected rate-limit script result")

// Rate-limit defaults for the public capture endpoint.
const (
	defaultCaptureLimit  = 120 // requests per window, per key
	defaultCaptureWindow = time.Minute
	// bucketIdleTTL is how long an unused in-process bucket survives a sweep.
	bucketIdleTTL = 10 * time.Minute
	// sweepInterval is deliberately a FRACTION of bucketIdleTTL. Using the idle TTL
	// as its own tick interval makes reclamation hostage to the attack's duration:
	// the sweeper would only ever collect entries idle for longer than the flood has
	// been running — i.e. none of them, while it matters.
	sweepInterval = bucketIdleTTL / 4
	// defaultMaxBuckets caps the in-process map. A public endpoint takes keys from
	// strangers, so the key space is not a bound — this is.
	defaultMaxBuckets = 50_000
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

	mu         sync.Mutex
	buckets    map[string]*bucket
	maxBuckets int
	stop       chan struct{}
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
		redis:      rc,
		limit:      limit,
		window:     window,
		buckets:    map[string]*bucket{},
		maxBuckets: defaultMaxBuckets,
		stop:       make(chan struct{}),
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
	return rl.AllowN(ctx, key, 1)
}

// Limit reports the per-window ceiling, so a caller can refuse a batch larger than
// any single window could ever admit instead of charging for it and then 429ing.
func (rl *RateLimiter) Limit() int { return rl.limit }

// AllowN charges `cost` against the key's window.
//
// A batch of N leads must cost what N single requests cost. Charging 1 for a
// 100-item batch would leave every bound on this endpoint meaning 100x less than
// it says — the limiter is the outer bound on records created, workflows enrolled
// and billable email sent.
func (rl *RateLimiter) AllowN(ctx context.Context, key string, cost int) (bool, time.Duration) {
	if cost <= 0 {
		cost = 1
	}
	if rl.redis != nil {
		if ok, retry, err := rl.allowRedis(ctx, key, cost); err == nil {
			return ok, retry
		}
		// Redis errored — fall through to the local bucket, NOT to allow.
	}
	return rl.allowLocalN(key, cost)
}

// incrWindow increments a fixed-window counter and sets its TTL ATOMICALLY,
// returning the new count and the window's remaining TTL.
//
// Atomic because the obvious two-step (INCR then EXPIRE) has a failure mode that
// is permanent rather than transient: if the process dies — or the request context
// is cancelled — between the two, the key survives with NO TTL. Nothing ever
// repairs it, because the TTL is only ever set on the branch where INCR returns 1,
// and INCR never returns 1 for an existing key again. The counter then climbs
// forever and that key 429s for eternity, while TTL(-1) makes the endpoint
// advertise a Retry-After that will never come true. A compensating DEL cannot save
// it either: it would run on the same dead context that broke the EXPIRE.
//
// A script runs both commands in one round trip on Redis's single thread, so the
// key cannot exist without its expiry.
var incrWindow = redis.NewScript(`
	local n = redis.call('INCRBY', KEYS[1], ARGV[2])
	if n == tonumber(ARGV[2]) then
		-- INCRBY on a MISSING key returns exactly the amount, so this still means
		-- "fresh key" — the test must move with the increment or a batch-sized first
		-- charge leaves the key with no TTL and 429s that caller forever.
		redis.call('PEXPIRE', KEYS[1], ARGV[1])
	end
	local ttl = redis.call('PTTL', KEYS[1])
	if ttl < 0 then
		-- Defensive: a pre-existing TTL-less key from an older build. Re-arm it
		-- rather than inheriting a permanent lockout.
		redis.call('PEXPIRE', KEYS[1], ARGV[1])
		ttl = tonumber(ARGV[1])
	end
	return {n, ttl}
`)

func (rl *RateLimiter) allowRedis(ctx context.Context, key string, cost int) (bool, time.Duration, error) {
	rkey := "ratelimit:capture:" + key
	res, err := incrWindow.Run(ctx, rl.redis, []string{rkey}, rl.window.Milliseconds(), cost).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(res) != 2 {
		return false, 0, errUnexpectedScriptResult
	}
	n, _ := res[0].(int64)
	ttlMS, _ := res[1].(int64)
	if n > int64(rl.limit) {
		ttl := time.Duration(ttlMS) * time.Millisecond
		if ttl <= 0 {
			ttl = rl.window
		}
		return false, ttl, nil
	}
	return true, 0, nil
}

// allowLocal is the in-process fallback: a fixed window, honestly named.
//
// It bounds the map's CARDINALITY, not just each entry's lifetime. Sweeping alone
// does not bound anything an attacker controls: entries are inserted at line rate
// and reclaimed only once idle, so steady-state size is insertion-rate × idle-TTL —
// unbounded in the attacker's favour, and reached fastest on exactly the path this
// fallback exists for (a Redis outage). At the cap we DENY unknown keys, which is
// the same fail-closed posture as the rest of this type: an established caller's
// bucket is already resident and keeps working, while a flood of unseen keys is
// refused for free instead of being allocated.
func (rl *RateLimiter) allowLocalN(key string, cost int) (bool, time.Duration) {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	b, ok := rl.buckets[key]
	if ok && now.After(b.resetsAt) {
		// Window elapsed: reuse the entry rather than deleting and re-inserting.
		// Charges COST, not 1 — this is the steady-state path for any recurring
		// caller, so hardcoding 1 here would let a caller timing one batch per window
		// pay for a single lead, on exactly the Redis-outage path this fallback exists
		// for.
		b.count, b.resetsAt, b.seenAt = cost, now.Add(rl.window), now
		return cost <= rl.limit, 0
	}
	if !ok {
		if len(rl.buckets) >= rl.maxBuckets {
			// Reclaim opportunistically before refusing — expired entries are dead
			// weight the sweeper has not reached yet.
			rl.evictExpiredLocked(now)
			if len(rl.buckets) >= rl.maxBuckets {
				return false, rl.window
			}
		}
		// A fresh window. count starts at 1 — this request — rather than seeding
		// limit-1 tokens, which is how the automation bucket ends up granting
		// limit+1 the moment its limit is parameterized.
		rl.buckets[key] = &bucket{count: cost, resetsAt: now.Add(rl.window), seenAt: now}
		return cost <= rl.limit, 0
	}
	b.seenAt = now
	// Charge THEN check, matching the Redis path (which INCRBYs before comparing) —
	// otherwise the two halves disagree about the boundary and a Redis blip silently
	// changes the effective limit.
	b.count += cost
	if b.count > rl.limit {
		return false, time.Until(b.resetsAt)
	}
	return true, 0
}

// evictExpiredLocked drops entries whose window has already elapsed. Caller holds
// the mutex.
func (rl *RateLimiter) evictExpiredLocked(now time.Time) {
	for k, b := range rl.buckets {
		if now.After(b.resetsAt) {
			delete(rl.buckets, k)
		}
	}
}

// sweep evicts idle buckets so a public endpoint cannot grow the map without
// bound.
func (rl *RateLimiter) sweep() {
	t := time.NewTicker(sweepInterval)
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
