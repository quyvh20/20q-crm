package integrations

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The OAuth callback is the one PUBLIC route on ConnectionHandler — authenticated only
// by its single-use state row — so it is bounded per source IP. These pin two things:
// the limit is actually wired, and the key ignores the caller-controlled :provider
// segment (otherwise a flood mints a fresh bucket per name and multiplies its budget).
func TestConnectionCallback_PerIPRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// serve routes one request. Recovery (writing to io.Discard so the intentional
	// panic does not clutter test output) is installed because a request that PASSES
	// the limiter reaches the nil svc and panics — recovering turns that into a 500,
	// which is all we need: the assertions only ever distinguish 429 from not-429.
	serve := func(h *ConnectionHandler, provider, ip string) int {
		r := gin.New()
		r.Use(gin.RecoveryWithWriter(io.Discard))
		r.GET("/api/integrations/providers/:provider/callback", h.Callback)
		req := httptest.NewRequest(http.MethodGet, "/api/integrations/providers/"+provider+"/callback?state=s&code=c", nil)
		req.RemoteAddr = ip + ":40000"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	rl := NewRateLimiter(nil, 1, time.Minute)
	defer rl.Close()
	h := &ConnectionHandler{ipLimiter: rl}

	// Exhaust the single per-IP slot, then the handler must 429 BEFORE any svc work,
	// so a nil svc is never reached on this path.
	_, _ = rl.AllowN(context.Background(), "provcb:ip:9.9.9.9", 1)
	require.Equal(t, http.StatusTooManyRequests, serve(h, "facebook", "9.9.9.9"),
		"an over-budget IP must be refused")

	// SAME ip, DIFFERENT provider segment → still 429. The key must not fold in the
	// caller-controlled :provider, or the per-IP bound is trivially defeated.
	require.Equal(t, http.StatusTooManyRequests, serve(h, "tiktok", "9.9.9.9"),
		"varying the provider path segment must not mint a fresh per-IP bucket")

	// A different IP still has budget: it passes the limiter (then panics on the nil
	// svc → 500 via Recovery). The point is only that it is NOT refused.
	require.NotEqual(t, http.StatusTooManyRequests, serve(h, "facebook", "8.8.8.8"),
		"a fresh IP within budget must not be refused (guard is not always-deny)")

	// A nil limiter is tolerated: the guard is skipped, not a crash in the limit path.
	require.NotEqual(t, http.StatusTooManyRequests, serve(&ConnectionHandler{}, "facebook", "7.7.7.7"),
		"a nil limiter must be tolerated")
}
