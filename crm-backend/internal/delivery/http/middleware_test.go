package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
)

// writeGateRoles mirrors the create/edit role set used across the write routes
// in router.go (contacts/deals/companies/tags/custom objects/uniform records).
var writeGateRoles = []string{domain.RoleAdmin, domain.RoleManager, domain.RoleSales}

// TestRequireRoleWriteGate is a regression guard for the role-name drift where
// router.go gated writes on the literal "sales" while the seeded role (and
// domain.RoleSales) is "sales_rep". A sales_rep caller MUST pass the create/edit
// gate so it can write its own contacts/deals — the data-scope layer
// (repository/scopes.go) explicitly grants domain.RoleSales an 'own' scope, so a
// 403 here would contradict it.
func TestRequireRoleWriteGate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Lock the contract: the gate constant must equal the seeded role name.
	if domain.RoleSales != "sales_rep" {
		t.Fatalf("domain.RoleSales = %q, want %q (seeded role name)", domain.RoleSales, "sales_rep")
	}

	tests := []struct {
		name     string
		role     string
		wantCode int
	}{
		{"sales_rep passes write gate", domain.RoleSales, http.StatusOK},
		{"admin passes write gate", domain.RoleAdmin, http.StatusOK},
		{"manager passes write gate", domain.RoleManager, http.StatusOK},
		{"owner bypasses gate", domain.RoleOwner, http.StatusOK},
		{"viewer blocked from write gate", domain.RoleViewer, http.StatusForbidden},
		// The pre-fix literal must no longer be accepted: only "sales_rep" is.
		{"legacy \"sales\" literal rejected", "sales", http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.POST("/records",
				func(c *gin.Context) { c.Set("role", tt.role) },
				RequireRole(writeGateRoles...),
				func(c *gin.Context) { c.Status(http.StatusOK) },
			)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/records", nil)
			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("role %q: got status %d, want %d", tt.role, w.Code, tt.wantCode)
			}
		})
	}
}

// TestRequireRoleMissingContext verifies the gate fails closed when no role was
// established on the request context.
func TestRequireRoleMissingContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.POST("/records",
		RequireRole(writeGateRoles...),
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/records", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("missing role: got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestCSRFProtect_OriginValidation locks the cross-site CSRF behavior: when the
// ambient refresh cookie is present, a request is admitted only if its Origin is
// allow-listed (the defense that works cross-site, where the SPA can't read the
// API-domain csrf_token cookie), with a same-site double-submit fallback when no
// Origin header is present. A request without the cookie (body-token shim) is not
// a CSRF vector and passes.
func TestCSRFProtect_OriginValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const allowedOrigin = "https://app.example.com"

	newReq := func(withCookie bool, origin, csrfCookie, csrfHeader string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
		if withCookie {
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "rt"})
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if csrfCookie != "" {
			req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfCookie})
		}
		if csrfHeader != "" {
			req.Header.Set("X-CSRF-Token", csrfHeader)
		}
		return req
	}

	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"no cookie (body-token shim) passes", newReq(false, "https://evil.example", "", ""), http.StatusOK},
		{"cookie + allowed origin passes", newReq(true, allowedOrigin, "", ""), http.StatusOK},
		{"cookie + disallowed origin rejected", newReq(true, "https://evil.example", "", ""), http.StatusForbidden},
		{"cookie + no origin + matching double-submit passes", newReq(true, "", "tok123", "tok123"), http.StatusOK},
		{"cookie + no origin + mismatched double-submit rejected", newReq(true, "", "tok123", "nope"), http.StatusForbidden},
		{"cookie + no origin + no token rejected", newReq(true, "", "", ""), http.StatusForbidden},
	}

	r := gin.New()
	r.POST("/api/auth/refresh", CSRFProtect([]string{allowedOrigin}), func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, tc.req)
			if w.Code != tc.want {
				t.Errorf("%s: got %d, want %d", tc.name, w.Code, tc.want)
			}
		})
	}
}
