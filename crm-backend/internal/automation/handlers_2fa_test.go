package automation

import (
	"net/http"
	"net/http/httptest"
	"testing"

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

// twoFactorStack mirrors what cmd/server/main.go passes: auth first (it sets the
// context key), enforcement second. A fake auth stands in for the JWT parse.
func twoFactorStack(pending bool) []gin.HandlerFunc {
	auth := func(c *gin.Context) {
		c.Set("org_id", uuid.New())
		c.Set("user_id", uuid.New())
		c.Set("role", "admin")
		c.Set("two_factor_pending", pending)
		c.Next()
	}
	enforce := func(c *gin.Context) {
		p, _ := c.Get("two_factor_pending")
		if b, ok := p.(bool); ok && b {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"code": "two_factor_required"})
			return
		}
		c.Next()
	}
	return []gin.HandlerFunc{auth, enforce}
}

func routerWith2FA(t *testing.T, pending bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Recovery so that a handler reached with nil dependencies surfaces as a 500
	// rather than unwinding the test. What is under test is whether the request
	// gets PAST the gate; what the handler then does with a nil repo is not.
	r.Use(gin.Recovery())
	allowCap := func(string) gin.HandlerFunc { return func(c *gin.Context) { c.Next() } }
	h := &Handler{logger: handlerRunNowDiscardLogger()}
	h.RegisterRoutes(r, twoFactorStack(pending), allowCap)
	return r
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
