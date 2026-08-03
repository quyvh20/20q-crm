package http

import (
	"context"
	"net/http"
	"net/http/httptest"

	"crm-backend/internal/domain"
	"crm-backend/pkg/config"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// authtest_fixtures_test.go holds the shared harness for the auth-surface handler
// tests (auth_handler_test.go, auth_google_oauth_test.go, two_factor_handler_test.go,
// api_token_handler_test.go, auth_routes_wiring_test.go).
//
// Everything here is DB-free on purpose: domain.AuthUseCase and
// domain.APITokenUseCase are interfaces, so the whole auth delivery layer is
// reachable with fakes and these tests run under -short.

// ---------------------------------------------------------------------------
// fakeAuthUC
// ---------------------------------------------------------------------------

// fakeAuthUC is a recording stand-in for domain.AuthUseCase (36 methods). The
// embedded interface is nil, so any method a handler calls that is NOT overridden
// below panics with a nil-pointer dereference — that is the point: it proves each
// handler touches exactly the usecase methods we think it does, and a future
// handler that quietly starts calling something else fails loudly instead of
// passing on a permissive stub.
type fakeAuthUC struct {
	domain.AuthUseCase

	// --- recorded inputs -------------------------------------------------
	lastMeta           domain.RequestMeta
	lastRefreshToken   string // the token Refresh/Logout/SignOutEverywhere/SwitchWorkspace resolved
	lastChallengeToken string // the token VerifyTwoFactor resolved (body vs cookie)
	lastCode           string // the 2FA code submitted
	lastLogin          domain.LoginInput
	lastState          string    // the OAuth state handed to GetGoogleAuthURL
	lastGoogleCode     string    // the ?code= handed to GoogleLogin
	lastUserID         uuid.UUID // caller identity the handler pulled from the session
	lastOrgID          uuid.UUID
	lastTargetUserID   uuid.UUID // ResetMemberTwoFactor target
	lastSessionID      uuid.UUID
	lastProfile        domain.UpdateProfileInput
	lastSwitch         domain.SwitchWorkspaceInput

	// --- call counters (assert a usecase was NOT reached) ----------------
	calls map[string]int

	// --- programmable responses ------------------------------------------
	loginResp     *domain.AuthResponse
	loginErr      error
	refreshResp   *domain.AuthResponse
	refreshErr    error
	logoutErr     error
	registerResp  *domain.AuthResponse
	registerErr   error
	me            *domain.User
	meErr         error
	workspaces    []domain.WorkspaceInfo
	workspacesErr error
	googleURL     string
	googleResp    *domain.AuthResponse
	googleErr     error
	sessions      []domain.SessionInfo
	sessionsErr   error
	sessionResp   *domain.AuthResponse
	sessionErr    error
	forgotToken   *string
	forgotErr     error
	resendToken   *string
	resendErr     error
	profile       *domain.User
	profileErr    error
	genericErr    error // returned by the simple void methods (unlink, reset-password, …)
	setup         *domain.TwoFactorSetup
	setupErr      error
	backupCodes   *domain.BackupCodesResult
	backupErr     error
	status        *domain.TwoFactorStatus
	statusErr     error
	verifyResp    *domain.AuthResponse
	verifyErr     error
}

func newFakeAuthUC() *fakeAuthUC { return &fakeAuthUC{calls: map[string]int{}} }

func (f *fakeAuthUC) note(name string) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

func (f *fakeAuthUC) count(name string) int { return f.calls[name] }

func (f *fakeAuthUC) Register(_ context.Context, _ domain.RegisterInput) (*domain.AuthResponse, error) {
	f.note("Register")
	return f.registerResp, f.registerErr
}

func (f *fakeAuthUC) Login(_ context.Context, in domain.LoginInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	f.note("Login")
	f.lastLogin, f.lastMeta = in, meta
	return f.loginResp, f.loginErr
}

func (f *fakeAuthUC) RefreshToken(_ context.Context, in domain.RefreshInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	f.note("RefreshToken")
	f.lastRefreshToken, f.lastMeta = in.RefreshToken, meta
	return f.refreshResp, f.refreshErr
}

func (f *fakeAuthUC) Logout(_ context.Context, refreshToken string) error {
	f.note("Logout")
	f.lastRefreshToken = refreshToken
	return f.logoutErr
}

func (f *fakeAuthUC) GetMe(_ context.Context, userID uuid.UUID) (*domain.User, error) {
	f.note("GetMe")
	f.lastUserID = userID
	return f.me, f.meErr
}

func (f *fakeAuthUC) ListWorkspaces(_ context.Context, userID uuid.UUID) ([]domain.WorkspaceInfo, error) {
	f.note("ListWorkspaces")
	f.lastUserID = userID
	return f.workspaces, f.workspacesErr
}

func (f *fakeAuthUC) GetGoogleAuthURL(state string) string {
	f.note("GetGoogleAuthURL")
	f.lastState = state
	return f.googleURL
}

func (f *fakeAuthUC) GoogleLogin(_ context.Context, code string) (*domain.AuthResponse, error) {
	f.note("GoogleLogin")
	f.lastGoogleCode = code
	return f.googleResp, f.googleErr
}

func (f *fakeAuthUC) SwitchWorkspace(_ context.Context, userID uuid.UUID, in domain.SwitchWorkspaceInput, meta domain.RequestMeta, current string) (*domain.AuthResponse, error) {
	f.note("SwitchWorkspace")
	f.lastUserID, f.lastSwitch, f.lastMeta, f.lastRefreshToken = userID, in, meta, current
	return f.sessionResp, f.sessionErr
}

func (f *fakeAuthUC) ForgotPassword(_ context.Context, _ domain.ForgotPasswordInput, meta domain.RequestMeta) (*string, error) {
	f.note("ForgotPassword")
	f.lastMeta = meta
	return f.forgotToken, f.forgotErr
}

func (f *fakeAuthUC) ResetPassword(_ context.Context, _ domain.ResetPasswordInput, meta domain.RequestMeta) error {
	f.note("ResetPassword")
	f.lastMeta = meta
	return f.genericErr
}

func (f *fakeAuthUC) VerifyEmail(_ context.Context, _ domain.VerifyEmailInput) error {
	f.note("VerifyEmail")
	return f.genericErr
}

func (f *fakeAuthUC) ResendVerification(_ context.Context, userID uuid.UUID, meta domain.RequestMeta) (*string, error) {
	f.note("ResendVerification")
	f.lastUserID, f.lastMeta = userID, meta
	return f.resendToken, f.resendErr
}

func (f *fakeAuthUC) ListSessions(_ context.Context, userID uuid.UUID, current string) ([]domain.SessionInfo, error) {
	f.note("ListSessions")
	f.lastUserID, f.lastRefreshToken = userID, current
	return f.sessions, f.sessionsErr
}

func (f *fakeAuthUC) RevokeSession(_ context.Context, userID, orgID, sessionID uuid.UUID) error {
	f.note("RevokeSession")
	f.lastUserID, f.lastOrgID, f.lastSessionID = userID, orgID, sessionID
	return f.genericErr
}

func (f *fakeAuthUC) SignOutEverywhere(_ context.Context, userID, orgID uuid.UUID, current string) (*domain.AuthResponse, error) {
	f.note("SignOutEverywhere")
	f.lastUserID, f.lastOrgID, f.lastRefreshToken = userID, orgID, current
	return f.sessionResp, f.sessionErr
}

func (f *fakeAuthUC) UpdateProfile(_ context.Context, userID uuid.UUID, in domain.UpdateProfileInput) (*domain.User, error) {
	f.note("UpdateProfile")
	f.lastUserID, f.lastProfile = userID, in
	return f.profile, f.profileErr
}

func (f *fakeAuthUC) ChangePassword(_ context.Context, userID, orgID uuid.UUID, _ domain.ChangePasswordInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	f.note("ChangePassword")
	f.lastUserID, f.lastOrgID, f.lastMeta = userID, orgID, meta
	return f.sessionResp, f.sessionErr
}

func (f *fakeAuthUC) SetPassword(_ context.Context, userID, orgID uuid.UUID, _ domain.SetPasswordInput, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	f.note("SetPassword")
	f.lastUserID, f.lastOrgID, f.lastMeta = userID, orgID, meta
	return f.sessionResp, f.sessionErr
}

func (f *fakeAuthUC) UnlinkGoogle(_ context.Context, userID uuid.UUID, meta domain.RequestMeta) error {
	f.note("UnlinkGoogle")
	f.lastUserID, f.lastMeta = userID, meta
	return f.genericErr
}

func (f *fakeAuthUC) StartTwoFactorSetup(_ context.Context, userID uuid.UUID) (*domain.TwoFactorSetup, error) {
	f.note("StartTwoFactorSetup")
	f.lastUserID = userID
	return f.setup, f.setupErr
}

func (f *fakeAuthUC) EnableTwoFactor(_ context.Context, userID, orgID uuid.UUID, code string, meta domain.RequestMeta) (*domain.BackupCodesResult, error) {
	f.note("EnableTwoFactor")
	f.lastUserID, f.lastOrgID, f.lastCode, f.lastMeta = userID, orgID, code, meta
	return f.backupCodes, f.backupErr
}

func (f *fakeAuthUC) DisableTwoFactor(_ context.Context, userID, orgID uuid.UUID, code string, meta domain.RequestMeta) error {
	f.note("DisableTwoFactor")
	f.lastUserID, f.lastOrgID, f.lastCode, f.lastMeta = userID, orgID, code, meta
	return f.genericErr
}

func (f *fakeAuthUC) RegenerateBackupCodes(_ context.Context, userID, orgID uuid.UUID, code string, meta domain.RequestMeta) (*domain.BackupCodesResult, error) {
	f.note("RegenerateBackupCodes")
	f.lastUserID, f.lastOrgID, f.lastCode, f.lastMeta = userID, orgID, code, meta
	return f.backupCodes, f.backupErr
}

func (f *fakeAuthUC) GetTwoFactorStatus(_ context.Context, userID, orgID uuid.UUID) (*domain.TwoFactorStatus, error) {
	f.note("GetTwoFactorStatus")
	f.lastUserID, f.lastOrgID = userID, orgID
	return f.status, f.statusErr
}

func (f *fakeAuthUC) VerifyTwoFactor(_ context.Context, challengeToken, code string, meta domain.RequestMeta) (*domain.AuthResponse, error) {
	f.note("VerifyTwoFactor")
	f.lastChallengeToken, f.lastCode, f.lastMeta = challengeToken, code, meta
	return f.verifyResp, f.verifyErr
}

func (f *fakeAuthUC) ResetMemberTwoFactor(_ context.Context, orgID, actorID, targetID uuid.UUID, meta domain.RequestMeta) error {
	f.note("ResetMemberTwoFactor")
	f.lastOrgID, f.lastUserID, f.lastTargetUserID, f.lastMeta = orgID, actorID, targetID, meta
	return f.genericErr
}

// ---------------------------------------------------------------------------
// fakeAPITokenUC
// ---------------------------------------------------------------------------

// apiTokenCall records the (orgID, userID) pair a personal-access-token route
// resolved. Every one of these routes is self-scoped: the identity MUST come from
// the session, never from the request, so recording the pair is the whole test.
type apiTokenCall struct {
	orgID  uuid.UUID
	userID uuid.UUID
	id     uuid.UUID
	input  domain.CreateAPITokenInput
}

type fakeAPITokenUC struct {
	domain.APITokenUseCase

	listCalls   []apiTokenCall
	createCalls []apiTokenCall
	revokeCalls []apiTokenCall

	tokens    []domain.APIToken
	listErr   error
	created   *domain.CreatedAPIToken
	createErr error
	revokeErr error
}

func (f *fakeAPITokenUC) List(_ context.Context, orgID, userID uuid.UUID) ([]domain.APIToken, error) {
	f.listCalls = append(f.listCalls, apiTokenCall{orgID: orgID, userID: userID})
	return f.tokens, f.listErr
}

func (f *fakeAPITokenUC) Create(_ context.Context, orgID, userID uuid.UUID, in domain.CreateAPITokenInput) (*domain.CreatedAPIToken, error) {
	f.createCalls = append(f.createCalls, apiTokenCall{orgID: orgID, userID: userID, input: in})
	return f.created, f.createErr
}

func (f *fakeAPITokenUC) Revoke(_ context.Context, orgID, userID, id uuid.UUID) error {
	f.revokeCalls = append(f.revokeCalls, apiTokenCall{orgID: orgID, userID: userID, id: id})
	return f.revokeErr
}

// ---------------------------------------------------------------------------
// repository fakes for the routes-wiring test
// ---------------------------------------------------------------------------

// authWiringRepo is an AuthRepository whose caller is a live, ACTIVE member. It is
// a sibling of mwRepo (middleware_optionalorg_test.go) rather than a change to it:
// mwRepo has a live caller and does not implement GetOrganizationByID, which the
// personal-access-token authentication path needs.
//
// The embedded interface is nil, so anything else the middleware reaches panics.
type authWiringRepo struct {
	domain.AuthRepository
	user         *domain.User
	tokenVersion int
	orgUser      *domain.OrgUser
	org          *domain.Organization
}

func (r authWiringRepo) GetUserByID(context.Context, uuid.UUID) (*domain.User, error) {
	return r.user, nil
}
func (r authWiringRepo) GetUserTokenVersion(context.Context, uuid.UUID) (int, error) {
	return r.tokenVersion, nil
}
func (r authWiringRepo) GetOrgUser(context.Context, uuid.UUID, uuid.UUID) (*domain.OrgUser, error) {
	return r.orgUser, nil
}
func (r authWiringRepo) GetOrganizationByID(context.Context, uuid.UUID) (*domain.Organization, error) {
	return r.org, nil
}

// livePATRepo is an APITokenRepository that ACCEPTS the token it is built with.
//
// This is what gives TestAPITokenRoutes_RejectAPersonalAccessToken its teeth: with
// a nil repo the middleware short-circuits to "API tokens are not accepted on this
// endpoint", so the test would pass for the wrong reason even after someone swapped
// AuthMiddleware for AuthMiddlewareWithAPITokens. With this repo, the PAT is a
// genuinely valid credential and the only thing refusing it is the route's choice
// of middleware.
type livePATRepo struct {
	domain.APITokenRepository
	token *domain.APIToken
}

func (r livePATRepo) GetByHash(context.Context, string) (*domain.APIToken, error) {
	return r.token, nil
}
func (r livePATRepo) TouchLastUsed(context.Context, uuid.UUID) error { return nil }

// ---------------------------------------------------------------------------
// harness helpers
// ---------------------------------------------------------------------------

// testCfg is the plain-http local-dev cookie posture: SameSite=Lax, Secure=false,
// host-only. Individual tests override the fields they are about.
func testCfg() *config.Config {
	return &config.Config{
		JWTSecret:   "auth-handler-test-secret",
		FrontendURL: "http://localhost:5173",
	}
}

// authSessionCtx sets the context keys AuthMiddleware would have set. uuid.Nil
// means "absent", which is how the handlers' unauthorized branches get exercised
// without standing up the real middleware.
func authSessionCtx(orgID, userID uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		if userID != uuid.Nil {
			c.Set("user_id", userID)
		}
		if orgID != uuid.Nil {
			c.Set("org_id", orgID)
		}
	}
}

// mountAuthRoutes mirrors router.go's PUBLIC /api/auth routes — the ones with no
// auth middleware in front of them. The rate limiter and CSRF middleware are
// omitted deliberately: they are covered by their own tests, and including them
// here would test the middleware rather than the handler.
func mountAuthRoutes(h *AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/auth")
	g.POST("/register", h.Register)
	g.POST("/login", h.Login)
	g.POST("/refresh", h.Refresh)
	g.POST("/logout", h.Logout)
	g.POST("/forgot-password", h.ForgotPassword)
	g.POST("/reset-password", h.ResetPassword)
	g.POST("/verify-email", h.VerifyEmail)
	g.GET("/google", h.GoogleLogin)
	g.GET("/google/callback", h.GoogleCallback)
	g.POST("/2fa/verify", h.VerifyTwoFactor)
	return r
}

// mountAuthedRoutes mirrors router.go's session-bearing auth routes with the
// session pre-set. Passing uuid.Nil for either id models a request that reached
// the handler with that key missing.
func mountAuthedRoutes(h *AuthHandler, orgID, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authSessionCtx(orgID, userID))
	g := r.Group("/api/auth")
	g.GET("/me", h.Me)
	g.PATCH("/me", h.UpdateMe)
	g.POST("/resend-verification", h.ResendVerification)
	g.POST("/change-password", h.ChangePassword)
	g.POST("/set-password", h.SetPassword)
	g.POST("/unlink-google", h.UnlinkGoogle)
	g.GET("/sessions", h.ListSessions)
	g.DELETE("/sessions/:id", h.RevokeSession)
	g.DELETE("/sessions", h.SignOutEverywhere)
	g.POST("/switch-workspace", h.SwitchWorkspace)
	g.GET("/2fa", h.GetTwoFactorStatus)
	g.POST("/2fa/setup", h.StartTwoFactorSetup)
	g.POST("/2fa/enable", h.EnableTwoFactor)
	g.POST("/2fa/disable", h.DisableTwoFactor)
	g.POST("/2fa/backup-codes", h.RegenerateBackupCodes)
	// Account-level + admin routes that live outside the /auth group in router.go.
	r.GET("/api/workspaces", h.ListWorkspaces)
	r.DELETE("/api/workspaces/members/:user_id/two-factor", h.ResetMemberTwoFactor)
	return r
}

// mountAPITokenRoutes mirrors router.go's /api/auth/api-tokens trio.
func mountAPITokenRoutes(h *APITokenHandler, orgID, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(authSessionCtx(orgID, userID))
	g := r.Group("/api/auth")
	g.GET("/api-tokens", h.List)
	g.POST("/api-tokens", h.Create)
	g.DELETE("/api-tokens/:id", h.Revoke)
	return r
}

// cookieByName parses the recorder's Set-Cookie headers and returns the named
// cookie, or nil when the response set no such cookie. Going through
// http.Response.Cookies() rather than string-matching the header means the tests
// assert on the parsed attributes (HttpOnly, Path, MaxAge, SameSite) the browser
// will actually see.
func cookieByName(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range (&http.Response{Header: w.Header()}).Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// setCookieHeaders returns the raw Set-Cookie lines — used where the assertion is
// "this response set NO cookies at all".
func setCookieHeaders(w *httptest.ResponseRecorder) []string {
	return w.Header().Values("Set-Cookie")
}

// authResponseFixture is a fully-populated successful login/refresh response.
func authResponseFixture() *domain.AuthResponse {
	return &domain.AuthResponse{
		AccessToken:  "access-token-value",
		RefreshToken: "refresh-token-value",
		User:         domain.User{ID: uuid.New(), Email: "user@example.com"},
		Workspaces:   []domain.WorkspaceInfo{{OrgID: uuid.New(), OrgName: "Acme", Role: "admin", Status: "active"}},
		ActiveOrgID:  uuid.New(),
	}
}
