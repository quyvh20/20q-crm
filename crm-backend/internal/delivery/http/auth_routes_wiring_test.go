package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// auth_routes_wiring_test.go drives the REAL RegisterRoutes.
//
// Every other test in this stream mounts a hand-written mirror of the auth group,
// which proves the HANDLERS behave — and proves nothing at all about router.go. A
// middleware silently dropped from a route in router.go would leave all of those
// tests green. This file is the one that notices, so it deliberately pays the cost
// of constructing the whole route tree.
//
// RegisterRoutes does nothing eager except AllowedOrigins, RateLimitByIP (which
// no-ops on a nil client), closure construction, and three SetFieldMasker calls —
// so typed-nil handlers are enough for every collaborator we do not exercise.

// signAuthTestToken mints a real HS256 access token. It is a SIBLING of signToken
// in middleware_optionalorg_test.go rather than a change to it: that helper has a
// live caller and no way to express a user id or the 2FA-pending claim.
func signAuthTestToken(t *testing.T, secret string, userID, orgID uuid.UUID, tv int, twoFactorPending bool) string {
	t.Helper()
	claims := usecase.JWTClaims{
		UserID:           userID,
		OrgID:            orgID,
		Role:             "admin",
		TokenVersion:     tv,
		TwoFactorPending: twoFactorPending,
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

// wiredRouter builds the production route tree. Only the collaborators the auth
// surface actually touches are real; everything else is a typed nil, which is safe
// because RegisterRoutes only takes method values off them.
// patRepo is the personal-access-token repository RegisterRoutes is handed. Pass
// nil for everything except the PAT-rejection test, which needs a repo that would
// genuinely authenticate the token.
func wiredRouter(t *testing.T, authUC *fakeAuthUC, tokenUC *fakeAPITokenUC, repo authWiringRepo, patRepo domain.APITokenRepository, cfg *config.Config) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()

	var permissionUC domain.PermissionUseCase // nil: cap()/ols() only CONSTRUCT handlers here

	RegisterRoutes(
		r,
		NewAuthHandler(authUC, cfg),
		// These three are the only handlers RegisterRoutes calls a method ON
		// (SetFieldMasker), so they must be real values rather than typed nils.
		NewContactHandler(nil),
		(*ContactMergeHandler)(nil),
		(*LeadScoringHandler)(nil),
		NewCompanyHandler(nil),
		(*TagHandler)(nil),
		NewDealHandler(nil),
		(*PipelineHandler)(nil),
		(*ActivityHandler)(nil),
		(*TaskHandler)(nil),
		(*UserHandler)(nil),
		(*AIHandler)(nil),
		(*SettingsHandler)(nil),
		(*CustomObjectHandler)(nil),
		(*ObjectRegistryHandler)(nil),
		(*RecordHandler)(nil),
		(*PermissionHandler)(nil),
		(*SearchHandler)(nil),
		(*KnowledgeHandler)(nil),
		(*CommandHandler)(nil),
		(*EventsHandler)(nil),
		(*WorkspaceHandler)(nil),
		(*ChatSessionHandler)(nil),
		(*VoiceHandler)(nil),
		(*ObjectLayoutHandler)(nil),
		(*RoleHandler)(nil),
		(*RoleAccessHandler)(nil),
		(*AuditHandler)(nil),
		(*ReportHandler)(nil),
		(*ReportShareHandler)(nil),
		(*ReportCommentHandler)(nil),
		(*DashboardHandler)(nil),
		(*UserGroupHandler)(nil),
		(*NotificationHandler)(nil),
		NewAPITokenHandler(tokenUC),
		(*SystemTemplateHandler)(nil),
		cfg,
		nil, // *gorm.DB — unused during registration
		nil, // *redis.Client — RateLimitByIP no-ops, the session cache path is skipped
		repo,
		patRepo,
		permissionUC,
	)
	return r
}

// activeMemberRepo is an AuthRepository whose caller is a live, active member —
// enough for AuthMiddleware to admit the token so the gates BEHIND it are what the
// test actually measures.
func activeMemberRepo() authWiringRepo {
	return authWiringRepo{
		user:         &domain.User{TokenVersion: 0},
		tokenVersion: 0,
		orgUser: &domain.OrgUser{
			Status: domain.StatusActive,
			RoleID: uuid.New(),
			Role:   &domain.Role{Name: "admin", DataScope: domain.DataScopeAll},
		},
		org: &domain.Organization{RequireTwoFactor: false},
	}
}

func serveWired(r *gin.Engine, method, path, bearer, body string) *httptest.ResponseRecorder {
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestAuthRoutes_RegisterWithoutPanic: gin panics at REGISTRATION time on a
// static/param route conflict, which `go build` cannot catch and which no handler
// test can reach. This is the cheapest possible guard against shipping a router
// that refuses to boot.
func TestAuthRoutes_RegisterWithoutPanic(t *testing.T) {
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("RegisterRoutes panicked: %v", rec)
		}
	}()
	wiredRouter(t, newFakeAuthUC(), &fakeAPITokenUC{}, activeMemberRepo(), nil, testCfg())
}

// TestAuthRoutes_ShapeIsStable pins the auth surface's method+path set. A renamed
// or dropped route shows up here as a named failure rather than as a 404 in
// production.
func TestAuthRoutes_ShapeIsStable(t *testing.T) {
	r := wiredRouter(t, newFakeAuthUC(), &fakeAPITokenUC{}, activeMemberRepo(), nil, testCfg())

	want := map[string]bool{
		"POST /api/auth/register":            false,
		"POST /api/auth/login":               false,
		"POST /api/auth/refresh":             false,
		"POST /api/auth/logout":              false,
		"POST /api/auth/forgot-password":     false,
		"POST /api/auth/reset-password":      false,
		"POST /api/auth/verify-email":        false,
		"POST /api/auth/resend-verification": false,
		"GET /api/auth/google":               false,
		"GET /api/auth/google/callback":      false,
		"GET /api/auth/me":                   false,
		"PATCH /api/auth/me":                 false,
		"POST /api/auth/change-password":     false,
		"POST /api/auth/set-password":        false,
		"POST /api/auth/unlink-google":       false,
		"POST /api/auth/2fa/verify":          false,
		"GET /api/auth/2fa":                  false,
		"POST /api/auth/2fa/setup":           false,
		"POST /api/auth/2fa/enable":          false,
		"POST /api/auth/2fa/disable":         false,
		"POST /api/auth/2fa/backup-codes":    false,
		"GET /api/auth/api-tokens":           false,
		"POST /api/auth/api-tokens":          false,
		"DELETE /api/auth/api-tokens/:id":    false,
		"GET /api/auth/sessions":             false,
		"DELETE /api/auth/sessions":          false,
		"DELETE /api/auth/sessions/:id":      false,
		"GET /api/auth/capabilities":         false,
		"POST /api/auth/switch-workspace":    false,
		"GET /api/workspaces":                false,
		"DELETE /api/workspaces/members/:user_id/two-factor": false,
	}
	for _, rt := range r.Routes() {
		key := rt.Method + " " + rt.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("auth route not registered: %s", key)
		}
	}
}

// TestAPITokenRoutes_RejectAPersonalAccessToken is the reason this file exists.
//
// The /auth group mounts the plain AuthMiddleware; the protected /api group mounts
// AuthMiddlewareWithAPITokens. Swapping the former for the latter — a plausible
// "consistency" edit — would let a single leaked PAT mint unlimited fresh tokens
// and list every token the victim holds: a self-propagating credential, on routes
// that are cookie-authenticated and therefore have no CSRF protection either.
//
// No handler-level test can see this. Only the real router can.
func TestAPITokenRoutes_RejectAPersonalAccessToken(t *testing.T) {
	cfg := testCfg()
	repo := activeMemberRepo()
	pat := domain.APITokenPrefix + "0123456789abcdef0123456789abcdef"
	// A live, unexpired, workspace-bound token owned by an active member — i.e. a
	// credential that WOULD authenticate anywhere PATs are accepted.
	liveToken := &domain.APIToken{
		ID: uuid.New(), OrgID: uuid.New(), UserID: uuid.New(),
		Name: "leaked", Prefix: "crm_pat_0123", Scopes: domain.StringList{domain.CapRecordsWrite},
	}
	patRepo := livePATRepo{token: liveToken}

	// CONTROL. Without this, the assertions below would pass for the wrong reason:
	// handed a nil token repo the middleware short-circuits to "API tokens are not
	// accepted on this endpoint", so a swapped middleware would still 401 and the
	// test would sit there green while the bypass was wide open. This proves the
	// fixture really is a working credential.
	gin.SetMode(gin.TestMode)
	probe := gin.New()
	probe.GET("/probe", AuthMiddlewareWithAPITokens(cfg.JWTSecret, repo, nil, patRepo),
		func(c *gin.Context) { c.Status(http.StatusOK) })
	pw := httptest.NewRecorder()
	preq := httptest.NewRequest(http.MethodGet, "/probe", nil)
	preq.Header.Set("Authorization", "Bearer "+pat)
	probe.ServeHTTP(pw, preq)
	if pw.Code != http.StatusOK {
		t.Fatalf("control failed: the PAT fixture is not a valid credential (status %d: %s) — "+
			"the assertions below would be vacuous", pw.Code, pw.Body.String())
	}

	r := wiredRouter(t, newFakeAuthUC(), &fakeAPITokenUC{}, repo, patRepo, cfg)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/auth/api-tokens", ""},
		{http.MethodPost, "/api/auth/api-tokens", `{"name":"self-propagating","scopes":["records.write"]}`},
		{http.MethodDelete, "/api/auth/api-tokens/" + uuid.New().String(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := serveWired(r, tc.method, tc.path, pat, tc.body)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("a PAT reached %s %s with status %d — one leaked token can now mint more: %s",
					tc.method, tc.path, w.Code, w.Body.String())
			}
		})
	}
}

// TestAPITokenRoutes_BlockA2FAConfinedSession: RequireTwoFactorSatisfied is
// mounted on these /auth routes even though the rest of the group is outside it.
// Without it, a user the workspace policy is demanding 2FA from could mint a token
// from their confined session and walk straight past the policy — the token would
// carry full role access while they never enrolled.
func TestAPITokenRoutes_BlockA2FAConfinedSession(t *testing.T) {
	cfg := testCfg()
	r := wiredRouter(t, newFakeAuthUC(), &fakeAPITokenUC{}, activeMemberRepo(), nil, cfg)
	confined := signAuthTestToken(t, cfg.JWTSecret, uuid.New(), uuid.New(), 0, true)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/auth/api-tokens", ""},
		{http.MethodPost, "/api/auth/api-tokens", `{"name":"escape hatch","scopes":["records.write"]}`},
		{http.MethodDelete, "/api/auth/api-tokens/" + uuid.New().String(), ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := serveWired(r, tc.method, tc.path, confined, tc.body)
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 — a 2FA-confined session minted/read tokens: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "two_factor_required") {
				t.Errorf("the refusal must carry the two_factor_required code: %s", w.Body.String())
			}
		})
	}

	// Control: the SAME routes, the SAME session, only the pending flag cleared.
	// Without this the test above would also pass if the routes were simply broken.
	compliant := signAuthTestToken(t, cfg.JWTSecret, uuid.New(), uuid.New(), 0, false)
	if w := serveWired(r, http.MethodGet, "/api/auth/api-tokens", compliant, ""); w.Code != http.StatusOK {
		t.Fatalf("a compliant session must reach the token list, got %d: %s", w.Code, w.Body.String())
	}
}

// TestEnrollmentRoutes_RemainReachableWhile2FAPending pins the deliberate
// asymmetry: the routes a confined user needs in order to COMPLY must stay open,
// or turning on a workspace 2FA policy is a one-way door that locks out everyone
// who had not already enrolled.
func TestEnrollmentRoutes_RemainReachableWhile2FAPending(t *testing.T) {
	cfg := testCfg()
	authUC := newFakeAuthUC()
	authUC.setup = &domain.TwoFactorSetup{Secret: "S", OtpAuthURL: "otpauth://x", QRDataURI: "data:image/png;base64,x"}
	authUC.backupCodes = &domain.BackupCodesResult{Codes: []string{"aaaa-bbbb"}}
	authUC.status = &domain.TwoFactorStatus{Enabled: false}
	authUC.me = &domain.User{ID: uuid.New(), Email: "confined@example.com"}
	authUC.workspaces = []domain.WorkspaceInfo{}

	r := wiredRouter(t, authUC, &fakeAPITokenUC{}, activeMemberRepo(), nil, cfg)
	confined := signAuthTestToken(t, cfg.JWTSecret, uuid.New(), uuid.New(), 0, true)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"start enrollment", http.MethodPost, "/api/auth/2fa/setup", "", http.StatusOK},
		{"finish enrollment", http.MethodPost, "/api/auth/2fa/enable", `{"code":"123456"}`, http.StatusOK},
		{"read 2fa status", http.MethodGet, "/api/auth/2fa", "", http.StatusOK},
		{"load own account", http.MethodGet, "/api/auth/me", "", http.StatusOK},
		{"sign out", http.MethodPost, "/api/auth/logout", `{}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveWired(r, tc.method, tc.path, confined, tc.body)
			if strings.Contains(w.Body.String(), "two_factor_required") {
				t.Fatalf("%s %s is gated by RequireTwoFactorSatisfied — a confined user cannot comply: %s",
					tc.method, tc.path, w.Body.String())
			}
			if w.Code != tc.want {
				t.Errorf("%s %s: status = %d, want %d; body: %s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// TestPublicAuthRoutes_NeedNoAuthorizationHeader: the endpoints that exist to
// create a session must be reachable without one. A stray AuthMiddleware on any of
// them makes signing in, resetting a password, or completing a 2FA challenge
// impossible — a total lockout that only an end-to-end call reveals.
func TestPublicAuthRoutes_NeedNoAuthorizationHeader(t *testing.T) {
	authUC := newFakeAuthUC()
	authUC.googleURL = googleAuthURL
	r := wiredRouter(t, authUC, &fakeAPITokenUC{}, activeMemberRepo(), nil, testCfg())

	cases := []struct {
		method string
		path   string
		body   string
		want   int
	}{
		// Empty/invalid bodies on purpose: a 400 proves the HANDLER was reached,
		// which is the whole point — we are testing reachability, not validation.
		{http.MethodPost, "/api/auth/register", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/auth/login", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/auth/forgot-password", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/auth/reset-password", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/auth/verify-email", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/auth/2fa/verify", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/api/auth/refresh", `{}`, http.StatusUnauthorized}, // "missing refresh token", not "missing authorization header"
		{http.MethodPost, "/api/auth/logout", `{}`, http.StatusOK},
		{http.MethodGet, "/api/auth/google", "", http.StatusTemporaryRedirect},
		{http.MethodGet, "/api/auth/google/callback", "", http.StatusTemporaryRedirect},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := serveWired(r, tc.method, tc.path, "", tc.body)
			if strings.Contains(w.Body.String(), "missing authorization header") {
				t.Fatalf("%s %s now requires a bearer token — nobody can sign in: %s", tc.method, tc.path, w.Body.String())
			}
			if w.Code != tc.want {
				t.Errorf("%s %s: status = %d, want %d; body: %s", tc.method, tc.path, w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// TestSessionRoutes_RequireABearerToken is the other side of the coin: the
// session-management routes must NOT be public.
func TestSessionRoutes_RequireABearerToken(t *testing.T) {
	r := wiredRouter(t, newFakeAuthUC(), &fakeAPITokenUC{}, activeMemberRepo(), nil, testCfg())

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/auth/sessions"},
		{http.MethodDelete, "/api/auth/sessions"},
		{http.MethodDelete, "/api/auth/sessions/" + uuid.New().String()},
		{http.MethodGet, "/api/auth/api-tokens"},
		{http.MethodPost, "/api/auth/api-tokens"},
		{http.MethodPatch, "/api/auth/me"},
		{http.MethodPost, "/api/auth/change-password"},
		{http.MethodPost, "/api/auth/set-password"},
		{http.MethodPost, "/api/auth/unlink-google"},
		{http.MethodPost, "/api/auth/switch-workspace"},
		{http.MethodPost, "/api/auth/2fa/setup"},
		{http.MethodPost, "/api/auth/2fa/enable"},
		{http.MethodPost, "/api/auth/2fa/disable"},
		{http.MethodPost, "/api/auth/2fa/backup-codes"},
		{http.MethodGet, "/api/auth/2fa"},
		{http.MethodGet, "/api/auth/me"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := serveWired(r, tc.method, tc.path, "", `{}`)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 for an anonymous caller; body: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestRateLimitByIP_NoOpsWithoutRedis pins the documented fail-open contract: a
// Redis outage (or a self-host with no cache) must never lock everyone out of
// login. It is also the honest limit of this stream's coverage — see the note
// below.
//
// NOTE: RateLimitByIP takes a concrete *redis.Client and no-ops on nil, so it
// cannot be faked through an interface, and miniredis is not a dependency of this
// module. That means route-level limiter PRESENCE (i.e. "did someone drop
// authRateLimit from /login?") is not verified anywhere. Adding
// github.com/alicebob/miniredis/v2 would close that gap.
func TestRateLimitByIP_NoOpsWithoutRedis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/x", RateLimitByIP(nil, 1, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 — the limiter must fail OPEN with no Redis", i+1, w.Code)
		}
	}
}
