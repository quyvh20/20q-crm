package integrations

import (
	"context"
	"testing"
	"time"
)

// newTestLimiter builds a Redis-less limiter (the fail-closed in-process path) and
// stops its sweeper when the test ends.
func newTestLimiter(t *testing.T, limit int, window time.Duration) *RateLimiter {
	t.Helper()
	rl := NewRateLimiter(nil, limit, window)
	t.Cleanup(rl.Close)
	return rl
}

// TestRateLimiter_FailsClosedWithoutRedis is the guard for a NAMED security
// invariant: this endpoint's limiter must never degrade to "allow" when Redis is
// unavailable. The app's other limiter (RateLimitByIP) does exactly that, which is
// defensible for a login form and not for an endpoint where every accepted request
// writes a record, enrols workflows and can send billable email.
func TestRateLimiter_FailsClosedWithoutRedis(t *testing.T) {
	rl := newTestLimiter(t, 3, time.Minute) // nil Redis == the outage case
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if ok, _ := rl.Allow(ctx, "k:abc"); !ok {
			t.Fatalf("request %d should be allowed within the limit", i+1)
		}
	}
	ok, retry := rl.Allow(ctx, "k:abc")
	if ok {
		t.Fatal("with no Redis the limiter must still throttle — never fall through to allow")
	}
	if retry <= 0 {
		t.Error("a throttled response must carry a positive Retry-After")
	}
}

// TestRateLimiter_LimitIsExact pins the off-by-one the automation bucket has: it
// seeds tokens=99 for a "100/min" limit, so parameterizing its limit grants
// limit+1.
func TestRateLimiter_LimitIsExact(t *testing.T) {
	rl := newTestLimiter(t, 5, time.Minute)
	ctx := context.Background()
	allowed := 0
	for i := 0; i < 50; i++ {
		if ok, _ := rl.Allow(ctx, "k:exact"); ok {
			allowed++
		}
	}
	if allowed != 5 {
		t.Errorf("allowed %d requests, want exactly the limit (5)", allowed)
	}
}

func TestRateLimiter_KeysAreIndependent(t *testing.T) {
	rl := newTestLimiter(t, 1, time.Minute)
	ctx := context.Background()
	if ok, _ := rl.Allow(ctx, "k:one"); !ok {
		t.Fatal("first key should pass")
	}
	if ok, _ := rl.Allow(ctx, "k:two"); !ok {
		t.Error("a different key must have its own budget — one noisy source must not throttle another")
	}
}

func TestRateLimiter_WindowResets(t *testing.T) {
	rl := newTestLimiter(t, 1, 40*time.Millisecond)
	ctx := context.Background()
	if ok, _ := rl.Allow(ctx, "k:win"); !ok {
		t.Fatal("first request should pass")
	}
	if ok, _ := rl.Allow(ctx, "k:win"); ok {
		t.Fatal("second request in-window should be throttled")
	}
	time.Sleep(60 * time.Millisecond)
	if ok, _ := rl.Allow(ctx, "k:win"); !ok {
		t.Error("the window must reset — otherwise a throttle becomes a permanent lockout")
	}
}

// TestRateLimiter_MapCardinalityIsBounded is the memory-exhaustion guard. Sweeping
// alone bounds each entry's LIFETIME, not the map's SIZE: entries are inserted at
// line rate and only reclaimed once idle, so an attacker minting unseen keys grows
// the map at will — worst on exactly the Redis-outage path this fallback exists
// for. At the cap, unknown keys are denied rather than allocated.
func TestRateLimiter_MapCardinalityIsBounded(t *testing.T) {
	rl := newTestLimiter(t, 100, time.Minute)
	rl.maxBuckets = 64 // keep the test fast; the mechanism is what matters
	ctx := context.Background()

	denied := 0
	for i := 0; i < 500; i++ {
		// Every key is unseen — the key-rotation flood.
		if ok, _ := rl.Allow(ctx, "k:"+string(rune('a'+i%26))+string(rune('a'+i/26))); !ok {
			denied++
		}
	}
	rl.mu.Lock()
	size := len(rl.buckets)
	rl.mu.Unlock()

	if size > rl.maxBuckets {
		t.Errorf("bucket map grew to %d, above the %d cap — a stranger can exhaust memory", size, rl.maxBuckets)
	}
	if denied == 0 {
		t.Error("at the cap, unknown keys must be DENIED (fail closed), not allocated")
	}
}

// TestRateLimiter_ResidentKeyKeepsWorkingAtCap pins the cost of the cap: it must
// fall on strangers, not on an established caller. An integrator's key is already
// resident, so a flood of unseen keys must not throttle them out.
func TestRateLimiter_ResidentKeyKeepsWorkingAtCap(t *testing.T) {
	rl := newTestLimiter(t, 100, time.Minute)
	rl.maxBuckets = 8
	ctx := context.Background()

	if ok, _ := rl.Allow(ctx, "k:established"); !ok {
		t.Fatal("the established key's first request should pass")
	}
	for i := 0; i < 200; i++ { // flood with unseen keys until the cap binds
		rl.Allow(ctx, "k:flood"+string(rune(i)))
	}
	if ok, _ := rl.Allow(ctx, "k:established"); !ok {
		t.Error("an established caller must keep working while the cap refuses strangers")
	}
}

// ── Weighted charging (batch) ────────────────────────────────────────────────

// TestAllowN_ChargesTheFullCost pins the property the batch endpoint depends on: a
// batch of N must cost what N single requests cost. Charging 1 for 100 would leave
// every bound on this endpoint meaning 100x less than it says.
func TestAllowN_ChargesTheFullCost(t *testing.T) {
	rl := NewRateLimiter(nil, 10, time.Minute)
	defer rl.Close()

	if ok, _ := rl.AllowN(context.Background(), "k", 10); !ok {
		t.Fatal("a cost exactly at the limit must be admitted")
	}
	if ok, _ := rl.Allow(context.Background(), "k"); ok {
		t.Error("the window is spent — one more request must be refused")
	}
}

// TestAllowN_RollingWindowChargesCost is the site a half-patch leaves behind: the
// rollover branch hardcoded count=1, which is the steady-state path for a caller
// sending one batch per window.
func TestAllowN_RollingWindowChargesCost(t *testing.T) {
	rl := NewRateLimiter(nil, 10, 20*time.Millisecond)
	defer rl.Close()
	ctx := context.Background()

	rl.AllowN(ctx, "k", 5)
	time.Sleep(30 * time.Millisecond) // window elapses

	if ok, _ := rl.AllowN(ctx, "k", 10); !ok {
		t.Fatal("a fresh window must admit a full-cost batch")
	}
	if ok, _ := rl.Allow(ctx, "k"); ok {
		t.Error("the rolled-over window must have been charged 10, not 1")
	}
}

// TestAllowN_OverLimitCostIsRefused: a batch larger than any window could admit must
// be refused rather than admitted and then half-processed.
func TestAllowN_OverLimitCostIsRefused(t *testing.T) {
	rl := NewRateLimiter(nil, 10, time.Minute)
	defer rl.Close()

	if ok, _ := rl.AllowN(context.Background(), "fresh", 11); ok {
		t.Error("a cost above the limit must be refused on a fresh key")
	}
}

// TestLimit_ReportsTheCeiling — the batch handler refuses an oversized batch by
// naming this number, instead of charging for it and then 429ing.
func TestLimit_ReportsTheCeiling(t *testing.T) {
	rl := NewRateLimiter(nil, 42, time.Minute)
	defer rl.Close()
	if rl.Limit() != 42 {
		t.Errorf("Limit() = %d, want 42", rl.Limit())
	}
}
