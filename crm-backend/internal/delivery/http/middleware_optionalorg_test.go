package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// mwRepo implements just the AuthRepository methods the auth middleware calls:
// GetUserByID (nil-org branch existence + token_version), GetUserTokenVersion +
// GetOrgUser (org-scoped branch). The embedded interface is nil, so any other call
// panics — proving the middleware touches nothing else on these paths.
type mwRepo struct {
	domain.AuthRepository
	user         *domain.User    // GetUserByID → nil models a deleted/absent account
	tokenVersion int             // GetUserTokenVersion (org-scoped path)
	orgUser      *domain.OrgUser // GetOrgUser (org-scoped path)
}

func (r mwRepo) GetUserByID(context.Context, uuid.UUID) (*domain.User, error) { return r.user, nil }
func (r mwRepo) GetUserTokenVersion(context.Context, uuid.UUID) (int, error) {
	return r.tokenVersion, nil
}
func (r mwRepo) GetOrgUser(context.Context, uuid.UUID, uuid.UUID) (*domain.OrgUser, error) {
	return r.orgUser, nil
}

func signToken(t *testing.T, secret string, orgID uuid.UUID, tv int) string {
	t.Helper()
	claims := usecase.JWTClaims{
		UserID:       uuid.New(),
		OrgID:        orgID,
		TokenVersion: tv,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return s
}

// AuthMiddlewareOptionalOrg admits a nil-org token (the zero-membership dead-end)
// once the account exists and token_version matches, so account-level routes work
// before the user belongs to any workspace (U4 item 6) — while still rejecting a
// stale token, a deleted account, and still applying the full membership check to an
// org-scoped token.
func TestAuthMiddlewareOptionalOrg_NilOrgTolerance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "test-secret"

	activeOU := &domain.OrgUser{Status: domain.StatusActive, RoleID: uuid.New(),
		Role: &domain.Role{Name: "viewer", DataScope: domain.DataScopeOwn}}

	cases := []struct {
		name       string
		optionalMW bool
		orgID      uuid.UUID
		tokenTV    int
		repo       mwRepo
		want       int
	}{
		{
			name: "optional MW: nil-org token, live account, matching token_version passes",
			optionalMW: true, orgID: uuid.Nil, tokenTV: 3,
			repo: mwRepo{user: &domain.User{TokenVersion: 3}},
			want: http.StatusOK,
		},
		{
			name: "optional MW: nil-org token with STALE token_version is rejected",
			optionalMW: true, orgID: uuid.Nil, tokenTV: 2,
			repo: mwRepo{user: &domain.User{TokenVersion: 3}}, // signed-out-everywhere bumped it
			want: http.StatusUnauthorized,
		},
		{
			name: "optional MW: nil-org token for a DELETED account fails closed",
			optionalMW: true, orgID: uuid.Nil, tokenTV: 0,
			repo: mwRepo{user: nil}, // GetUserByID is soft-delete-scoped → nil
			want: http.StatusUnauthorized,
		},
		{
			name: "optional MW: org-scoped token still gets the full active-membership check",
			optionalMW: true, orgID: uuid.New(), tokenTV: 0,
			repo: mwRepo{tokenVersion: 0, orgUser: activeOU},
			want: http.StatusOK,
		},
		{
			name: "required MW: a nil-org token is still rejected (no membership)",
			optionalMW: false, orgID: uuid.Nil, tokenTV: 0,
			repo: mwRepo{orgUser: nil}, // GetOrgUser(nil) -> nil -> 403
			want: http.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			var mw gin.HandlerFunc
			if tc.optionalMW {
				mw = AuthMiddlewareOptionalOrg(secret, tc.repo, nil)
			} else {
				mw = AuthMiddleware(secret, tc.repo, nil)
			}
			r.GET("/x", mw, func(c *gin.Context) { c.Status(http.StatusOK) })

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header.Set("Authorization", "Bearer "+signToken(t, secret, tc.orgID, tc.tokenTV))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}
