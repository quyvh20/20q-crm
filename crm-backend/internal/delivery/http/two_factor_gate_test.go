package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// RequireTwoFactorSatisfied is the gate that confines a session belonging to a
// member who has not enrolled, in a workspace that mandates 2FA.
//
// It lives here rather than beside the automation routes because
// delivery -> usecase -> automation is an import cycle, so the automation tests
// can only prove that RegisterRoutes MOUNTS the slice. This proves the slice does
// something.

func gateRouter(setKey func(*gin.Context)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/guarded", setKey, RequireTwoFactorSatisfied(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})
	return r
}

func getGuarded(r *gin.Engine) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/guarded", nil))
	return w
}

func TestRequireTwoFactorSatisfied_BlocksAPendingCaller(t *testing.T) {
	w := getGuarded(gateRouter(func(c *gin.Context) { c.Set("two_factor_pending", true) }))

	if w.Code != http.StatusForbidden {
		t.Fatalf("a 2FA-pending caller must be refused, got %d: %s", w.Code, w.Body.String())
	}
	// The code the SPA switches on to send the user to enrollment.
	if body := w.Body.String(); !strings.Contains(body, "two_factor_required") {
		t.Fatalf("the refusal must carry the two_factor_required code: %s", body)
	}
}

func TestRequireTwoFactorSatisfied_AllowsASatisfiedCaller(t *testing.T) {
	w := getGuarded(gateRouter(func(c *gin.Context) { c.Set("two_factor_pending", false) }))

	if w.Code != http.StatusOK {
		t.Fatalf("a compliant caller must pass, got %d: %s", w.Code, w.Body.String())
	}
}

// The failure mode that makes this middleware silently useless: mounted WITHOUT an
// auth middleware ahead of it, the key is absent and everything passes. That is
// why the ordering inside the slice main.go builds is load-bearing, and why this
// case is pinned rather than left implicit.
func TestRequireTwoFactorSatisfied_PassesWhenTheKeyIsAbsent(t *testing.T) {
	w := getGuarded(gateRouter(func(c *gin.Context) { /* no auth ran */ }))

	if w.Code != http.StatusOK {
		t.Fatalf("expected the documented open behaviour with no key set, got %d", w.Code)
	}
	t.Log("confirmed: this gate is inert without an auth middleware ahead of it — mount order matters")
}
