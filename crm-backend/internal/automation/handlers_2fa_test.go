package automation

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// Automation was the one module registered with a bare auth middleware instead of
// the full authenticated stack, so RequireTwoFactorSatisfied never ran on it. In a
// workspace that mandates 2FA, a member who had not enrolled could still reach
// POST /api/webhooks/reveal-secret and read the org's inbound-webhook signing
// secret — the credential that authenticates every inbound automation call.
//
// These drive the real RegisterRoutes wiring rather than the middleware in
// isolation, because the defect was never in the middleware: it was in what the
// module mounted.

// twoFactorStack mirrors the shape cmd/server/main.go passes: auth first (it sets
// the context key), enforcement second.
//
// The enforcement handler here is a SENTINEL, not a copy of the real gate's logic.
// What these tests can prove is that RegisterRoutes actually APPLIES the slice to
// both route groups and leaves the public inbound route outside it — which is
// where the bug was. Whether RequireTwoFactorSatisfied itself behaves correctly is
// proved against the real middleware in
// internal/delivery/http/two_factor_gate_test.go; it cannot be imported here
// because delivery -> usecase -> automation is an import cycle.
func twoFactorStack(pending bool) []gin.HandlerFunc {
	auth := func(c *gin.Context) {
		c.Set("org_id", uuid.New())
		c.Set("user_id", uuid.New())
		c.Set("role", "admin")
		c.Set("two_factor_pending", pending)
		c.Next()
	}
	sentinel := func(c *gin.Context) {
		// Reads the same context key the real gate reads, and does nothing else.
		if p, _ := c.Get("two_factor_pending"); p == true {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": "two_factor_required"})
			return
		}
		c.Next()
	}
	return []gin.HandlerFunc{auth, sentinel}
}

// capRecorder records which capability each route asked for, so the tests can
// assert the READ routes are gated rather than trusting a permissive stub. An
// always-allow stub would pass even with every requireCap call deleted.
type capRecorder struct {
	mu       sync.Mutex
	requested []string
}

func (r *capRecorder) fn(code string) gin.HandlerFunc {
	r.mu.Lock()
	r.requested = append(r.requested, code)
	r.mu.Unlock()
	return func(c *gin.Context) { c.Next() }
}

func (r *capRecorder) count(code string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.requested {
		if c == code {
			n++
		}
	}
	return n
}

func routerWith2FA(t *testing.T, pending bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Recovery so that a handler reached with nil dependencies surfaces as a 500
	// rather than unwinding the test. What is under test is whether the request
	// gets PAST the gate; what the handler then does with a nil repo is not.
	r.Use(gin.Recovery())
	rec := &capRecorder{}
	h := &Handler{logger: handlerRunNowDiscardLogger()}
	h.RegisterRoutes(r, twoFactorStack(pending), rec.fn)
	return r
}

// routeCaps runs RegisterRoutes purely to capture the capability each route asks
// for.
func routeCaps(t *testing.T) *capRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := &capRecorder{}
	h := &Handler{logger: handlerRunNowDiscardLogger()}
	h.RegisterRoutes(gin.New(), twoFactorStack(false), rec.fn)
	return rec
}

func TestWebhookSecretRoutesEnforceTwoFactor(t *testing.T) {
	// Every route that exposes or rotates the org's signing credential.
	for _, path := range []string{"/api/webhooks/reveal-secret", "/api/webhooks/regenerate-secret"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			routerWith2FA(t, true).ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))

			assert.Equal(t, http.StatusForbidden, w.Code,
				"a member the workspace is demanding 2FA from must not reach %s; body: %s", path, w.Body.String())
			assert.Contains(t, w.Body.String(), "two_factor_required")
		})
	}
}

func TestWorkflowRoutesEnforceTwoFactor(t *testing.T) {
	w := httptest.NewRecorder()
	routerWith2FA(t, true).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/workflows", nil))

	assert.Equal(t, http.StatusForbidden, w.Code,
		"the workflows surface must sit behind the 2FA gate too; body: %s", w.Body.String())
}

// The gate must not fire for a member who HAS satisfied the policy — a 403 here
// would lock every compliant user out of automation entirely.
func TestSatisfiedTwoFactorReachesTheHandler(t *testing.T) {
	w := httptest.NewRecorder()
	routerWith2FA(t, false).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/webhooks/reveal-secret", nil))

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"a 2FA-satisfied admin must pass the gate; body: %s", w.Body.String())
}

// The inbound route authenticates with an HMAC signature, not a session, and is
// mounted on the bare router for exactly that reason. Sweeping it behind the
// authenticated stack would 401 every third-party sender.
func TestInboundWebhookStaysPublic(t *testing.T) {
	w := httptest.NewRecorder()
	routerWith2FA(t, true).ServeHTTP(w,
		httptest.NewRequest(http.MethodPost, "/api/webhooks/inbound/some-org-token", nil))

	assert.NotEqual(t, http.StatusForbidden, w.Code,
		"the public inbound webhook must not be behind the session/2FA stack; body: %s", w.Body.String())
}

// The workflows READ surface must be capability-gated. Asserted by counting the
// requireCap calls RegisterRoutes makes, because a permissive stub would pass with
// every gate deleted — which is exactly how the previous version of these tests
// gave false assurance.
//
// Reads carry secrets: a workflow definition holds send_webhook headers, and a run
// log holds each step's fetched output.
func TestWorkflowReadRoutesRequestTheManageCapability(t *testing.T) {
	rec := routeCaps(t)

	// GET "", /schema, /schema/objects, /schema/objects/:slug/fields, /:id,
	// /:id/runs, /runs/:runId — seven reads — plus the writes and the templates.
	// The precise total is brittle, so assert the floor that matters: the reads
	// were previously ungated, so removing them drops the count well below this.
	if got := rec.count(domain.CapWorkflowsManage); got < 15 {
		t.Fatalf("only %d routes requested workflows.manage; the read surface looks ungated again", got)
	}
	if got := rec.count("workflows.view"); got != 0 {
		t.Fatalf("a workflows.view capability appeared (%d routes) — no role holds it, so those routes are unreachable", got)
	}
}
