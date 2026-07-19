package integrations

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// denyingAuthorizer refuses every object action — the shape of a role granted
// integrations.manage and nothing else.
type denyingAuthorizer struct{ calls int }

func (a *denyingAuthorizer) Authorize(_ context.Context, _ uuid.UUID, slug string, _ domain.RecordAction) error {
	a.calls++
	return domain.NewAppError(http.StatusForbidden, "no permission on "+slug)
}
func (a *denyingAuthorizer) Audit(_ context.Context, _ domain.AuditEntry) {}
func (a *denyingAuthorizer) FieldMask(_ context.Context, _ uuid.UUID, _ string) domain.FieldMask {
	return domain.FieldMask{}
}

type stubMembers struct{}

func (stubMembers) GetOrgUser(_ context.Context, userID, orgID uuid.UUID) (*domain.OrgUser, error) {
	return &domain.OrgUser{UserID: userID, OrgID: orgID, Status: "active"}, nil
}

func (stubMembers) ActiveMemberIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// TestSendTestLead_RequiresTheCallersOwnWritePermission is the security control this
// endpoint turns on, and the package's first negative authz test.
//
// Ingest writes callerless, so no OLS check ever sees the write itself. Source save
// re-authorizes the configuring admin — but a test click is neither a create nor a
// target change, so without an explicit check here `integrations.manage` alone would
// become a standing contact-write primitive for a role with no contact access at all.
//
// The assertion that the authorizer was CONSULTED is half the test: a handler that
// 403s for some unrelated reason would pass a status-only check while the control was
// missing.
func TestSendTestLead_RequiresTheCallersOwnWritePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authz := &denyingAuthorizer{}
	h := NewHandler(nil, nil, authz, stubMembers{}, contactSchema(),
		NewRateLimiter(nil, 0, 0), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Stand in for loadSource: the route's own lookup needs a DB, and what is under
	// test is the authorization decision, not the fetch.
	src := testSource(t, "", "")
	router := gin.New()
	router.POST("/test-lead", func(c *gin.Context) {
		c.Set("org_id", src.OrgID)
		c.Set("user_id", uuid.New())
		if !h.authorizeTarget(c, src.OrgID, src.TargetSlug) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"data": "would have written a contact"})
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/test-lead", nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("a caller who cannot write contacts must be refused: got %d, want 403", w.Code)
	}
	if authz.calls == 0 {
		t.Fatal("the authorizer was never consulted — the 403 came from somewhere else, so the control is absent")
	}
}

// TestNewHandler_PanicsWithoutAnAuthorizer pins the constructor's refusal to degrade.
// The OLS re-check is the only thing standing between integrations.manage and an
// org-wide write primitive, so it must not be silently optional.
func TestNewHandler_PanicsWithoutAnAuthorizer(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewHandler must panic on a nil authorizer rather than run without the OLS check")
		}
	}()
	NewHandler(nil, nil, nil, stubMembers{}, contactSchema(), nil, nil)
}
