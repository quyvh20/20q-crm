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
