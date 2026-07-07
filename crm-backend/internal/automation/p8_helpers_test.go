package automation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// p8_helpers_test.go holds the shared fakes + the capability-driven Run Now / Retry
// authorization test for the P8 actor model. As of P8 the legacy owner/admin/manager
// role-name fallback is gone (NewHandler requires a capability checker), so handler
// tests must inject one explicitly.

// capAllow is a domain.CapabilityChecker that grants every capability — used by
// handler tests where authorization must PASS.
type capAllow struct{}

func (capAllow) HasCapability(context.Context, uuid.UUID, string) error { return nil }
func (capAllow) CallerCapabilities(context.Context, uuid.UUID) []string { return nil }

// capDeny grants nothing — used to prove (a) the creator allowance authorizes a run
// with NO capability, and (b) a non-creator without workflows.run_any is forbidden.
type capDeny struct{}

func (capDeny) HasCapability(context.Context, uuid.UUID, string) error {
	return domain.NewAppError(http.StatusForbidden, "denied")
}
func (capDeny) CallerCapabilities(context.Context, uuid.UUID) []string { return nil }

// TestAuthorizeRunNowCtx verifies the capability-driven Run Now / Retry gate that
// replaced the deleted role-name matrix (P8): the creator allowance authorizes a
// caller's own workflow with no capability; any other caller needs workflows.run_any;
// a nil caller id never satisfies the creator allowance.
func TestAuthorizeRunNowCtx(t *testing.T) {
	creator := uuid.New()
	other := uuid.New()
	orgID := uuid.New()

	mkCtx := func() *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		c.Set("org_id", orgID)
		return c
	}

	cases := []struct {
		name      string
		checker   domain.CapabilityChecker
		userID    uuid.UUID
		createdBy uuid.UUID
		want      bool
	}{
		{"creator runs own workflow (no capability needed)", capDeny{}, creator, creator, true},
		{"workflows.run_any grants running another's workflow", capAllow{}, other, creator, true},
		{"non-creator without run_any is denied", capDeny{}, other, creator, false},
		{"nil caller id never satisfies the creator allowance", capDeny{}, uuid.Nil, uuid.Nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{capChecker: tc.checker}
			assert.Equal(t, tc.want, h.authorizeRunNowCtx(mkCtx(), tc.userID, tc.createdBy))
		})
	}
}
