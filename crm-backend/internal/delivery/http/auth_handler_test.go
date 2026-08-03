package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// post is a small request helper: JSON body, optional cookies.
func post(r *gin.Engine, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ===========================================================================
// Login
// ===========================================================================

// TestLogin_TwoFactorChallengeSetsNoSessionCookies is the highest-value assertion
// on this surface. A TwoFactorRequired response is a CHALLENGE, not a session: the
// password was right, the second factor is outstanding. If the early return in
// Login is deleted (or reordered below setAuthCookies), the handler hands out a
// refresh cookie to a caller who proved only the first factor — 2FA is then
// bypassed for every password login in the product, silently, with the endpoint
// still returning the same 200 and the same JSON.
func TestLogin_TwoFactorChallengeSetsNoSessionCookies(t *testing.T) {
	uc := newFakeAuthUC()
	uc.loginResp = &domain.AuthResponse{
		TwoFactorRequired: true,
		ChallengeToken:    "challenge-abc",
		// A hostile/buggy usecase could still populate these; the handler must not
		// turn them into cookies.
		AccessToken:  "should-never-be-issued",
		RefreshToken: "should-never-be-issued",
	}
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/login", `{"email":"user@example.com","password":"correct-horse"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("2FA challenge status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if ck := cookieByName(w, refreshCookieName); ck != nil {
		t.Errorf("2FA challenge issued a %s cookie (%q) — the second factor is bypassed", refreshCookieName, ck.Value)
	}
	if ck := cookieByName(w, csrfCookieName); ck != nil {
		t.Errorf("2FA challenge issued a %s cookie — half a session is still a session", csrfCookieName)
	}
	if got := setCookieHeaders(w); len(got) != 0 {
		t.Errorf("2FA challenge must set NO cookies at all, got %v", got)
	}
	// The challenge token itself is what the SPA posts to /2fa/verify, so it must
	// be present in the body.
	if !strings.Contains(w.Body.String(), "challenge-abc") {
		t.Errorf("challenge token missing from the response body: %s", w.Body.String())
	}
}

// TestLogin_SuccessSetsBothAuthCookies pins the cookie contract a real session
// depends on: the refresh token is httpOnly and scoped to /api/auth (so it is not
// readable by JS and not attached to every data request); the CSRF token is
// deliberately NOT httpOnly (the SPA must read it for the double-submit) and is
// scoped to /.
func TestLogin_SuccessSetsBothAuthCookies(t *testing.T) {
	uc := newFakeAuthUC()
	uc.loginResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/login", `{"email":"user@example.com","password":"correct-horse"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}

	refresh := cookieByName(w, refreshCookieName)
	if refresh == nil {
		t.Fatalf("no %s cookie on a successful login", refreshCookieName)
	}
	if refresh.Value != "refresh-token-value" {
		t.Errorf("refresh cookie value = %q", refresh.Value)
	}
	if !refresh.HttpOnly {
		t.Error("refresh cookie must be httpOnly — otherwise XSS lifts a 7-day credential")
	}
	if refresh.Path != "/api/auth" {
		t.Errorf("refresh cookie Path = %q, want /api/auth", refresh.Path)
	}
	if refresh.MaxAge != refreshCookieMaxAge {
		t.Errorf("refresh cookie MaxAge = %d, want %d", refresh.MaxAge, refreshCookieMaxAge)
	}

	csrf := cookieByName(w, csrfCookieName)
	if csrf == nil {
		t.Fatalf("no %s cookie on a successful login", csrfCookieName)
	}
	if csrf.HttpOnly {
		t.Error("csrf cookie must NOT be httpOnly — the SPA has to read it for the double-submit")
	}
	if csrf.Path != "/" {
		t.Errorf("csrf cookie Path = %q, want /", csrf.Path)
	}
	if csrf.Value == "" || csrf.Value == refresh.Value {
		t.Errorf("csrf cookie value = %q — must be independently random", csrf.Value)
	}

	// The usecase saw the transport metadata the auth event log records.
	if uc.lastLogin.Email != "user@example.com" {
		t.Errorf("login input not threaded: %+v", uc.lastLogin)
	}
	if uc.lastMeta.IP == "" {
		t.Error("request meta IP not threaded into the usecase")
	}
}

func TestLogin_BadJSONIs400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed json", `{"email":`},
		{"missing password", `{"email":"user@example.com"}`},
		{"not an email", `{"email":"nope","password":"correct-horse"}`},
		{"empty body", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newFakeAuthUC()
			r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))
			w := post(r, "/api/auth/login", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			if uc.count("Login") != 0 {
				t.Error("a bind failure must not reach the usecase")
			}
			if len(setCookieHeaders(w)) != 0 {
				t.Errorf("a rejected login set cookies: %v", setCookieHeaders(w))
			}
		})
	}
}

// ===========================================================================
// Refresh
// ===========================================================================

// TestRefresh_PrefersCookieOverBody pins refreshTokenFromRequest's precedence: the
// httpOnly cookie is the credential of record, and a body token is only the
// non-browser / shim fallback. If this flipped, a page that can inject a request
// body (but not read the httpOnly cookie) would choose which credential
// authenticates the refresh.
func TestRefresh_PrefersCookieOverBody(t *testing.T) {
	uc := newFakeAuthUC()
	uc.refreshResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/refresh", `{"refresh_token":"FROM-BODY"}`,
		&http.Cookie{Name: refreshCookieName, Value: "FROM-COOKIE"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if uc.lastRefreshToken != "FROM-COOKIE" {
		t.Errorf("usecase got %q, want the COOKIE to win", uc.lastRefreshToken)
	}

	// …and with no cookie the body token is used.
	uc2 := newFakeAuthUC()
	uc2.refreshResp = authResponseFixture()
	r2 := mountAuthRoutes(NewAuthHandler(uc2, testCfg()))
	if w := post(r2, "/api/auth/refresh", `{"refresh_token":"FROM-BODY"}`); w.Code != http.StatusOK {
		t.Fatalf("body-only refresh status = %d", w.Code)
	}
	if uc2.lastRefreshToken != "FROM-BODY" {
		t.Errorf("body-only refresh: usecase got %q, want FROM-BODY", uc2.lastRefreshToken)
	}
}

func TestRefresh_MissingTokenIs401WithoutCallingUsecase(t *testing.T) {
	uc := newFakeAuthUC()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/refresh", `{}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	if uc.count("RefreshToken") != 0 {
		t.Error("a credential-less refresh must be refused before the usecase is reached")
	}
}

// TestRefresh_OrgUnavailableIs409AndKeepsCookies pins the R2 fail-closed contract:
// the workspace is gone but the SESSION is still valid, so the cookies must
// survive — the SPA retries a plain refresh and routes to the chooser. Clearing
// them here would sign the user out of a workspace they still belong to.
func TestRefresh_OrgUnavailableIs409AndKeepsCookies(t *testing.T) {
	uc := newFakeAuthUC()
	uc.refreshErr = &domain.OrgUnavailableError{Workspaces: []domain.WorkspaceInfo{
		{OrgID: uuid.New(), OrgName: "Still Yours", Role: "admin", Status: "active"},
	}}
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/refresh", `{}`, &http.Cookie{Name: refreshCookieName, Value: "rt"})

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", w.Code, w.Body.String())
	}
	var body struct {
		Code       string                 `json:"code"`
		Workspaces []domain.WorkspaceInfo `json:"workspaces"`
		Error      string                 `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v (%s)", err, w.Body.String())
	}
	if body.Code != "ORG_UNAVAILABLE" {
		t.Errorf("code = %q, want ORG_UNAVAILABLE (the SPA switches on the code, not the message)", body.Code)
	}
	if len(body.Workspaces) != 1 || body.Workspaces[0].OrgName != "Still Yours" {
		t.Errorf("workspaces not carried through: %+v", body.Workspaces)
	}
	// The load-bearing part: NO Set-Cookie at all, so nothing expires the session.
	if got := setCookieHeaders(w); len(got) != 0 {
		t.Errorf("ORG_UNAVAILABLE must not touch cookies, got %v", got)
	}
}

// TestRefresh_OtherErrorsClearCookies: a rotated/revoked/expired refresh token must
// expire the cookies, or the SPA keeps bouncing off the server with a dead credential.
func TestRefresh_OtherErrorsClearCookies(t *testing.T) {
	uc := newFakeAuthUC()
	uc.refreshErr = domain.NewAppError(http.StatusUnauthorized, "invalid refresh token")
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/refresh", `{}`, &http.Cookie{Name: refreshCookieName, Value: "rt"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	for _, name := range []string{refreshCookieName, csrfCookieName} {
		ck := cookieByName(w, name)
		if ck == nil {
			t.Errorf("%s was not cleared", name)
			continue
		}
		if ck.MaxAge >= 0 || ck.Value != "" {
			t.Errorf("%s not expired: value=%q MaxAge=%d", name, ck.Value, ck.MaxAge)
		}
	}
}

// ===========================================================================
// Logout
// ===========================================================================

// TestLogout_AlwaysClearsCookiesAndReturns200: logout is unconditionally
// successful from the browser's point of view. Whatever the server-side revoke
// does, the local credential must go — a logout that leaves the cookie behind
// because the usecase erred is a logout that did not log anyone out.
func TestLogout_AlwaysClearsCookiesAndReturns200(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		cookies    []*http.Cookie
		logoutErr  error
		wantCalled int
		wantToken  string
	}{
		{name: "no token at all", body: `{}`, wantCalled: 0},
		{
			name: "cookie token, usecase errors", body: `{}`,
			cookies:   []*http.Cookie{{Name: refreshCookieName, Value: "rt-cookie"}},
			logoutErr: errors.New("revoke failed"), wantCalled: 1, wantToken: "rt-cookie",
		},
		{
			name: "body token, happy", body: `{"refresh_token":"rt-body"}`,
			wantCalled: 1, wantToken: "rt-body",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newFakeAuthUC()
			uc.logoutErr = tc.logoutErr
			r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

			w := post(r, "/api/auth/logout", tc.body, tc.cookies...)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			if uc.count("Logout") != tc.wantCalled {
				t.Errorf("Logout called %d times, want %d", uc.count("Logout"), tc.wantCalled)
			}
			if tc.wantToken != "" && uc.lastRefreshToken != tc.wantToken {
				t.Errorf("revoked %q, want %q", uc.lastRefreshToken, tc.wantToken)
			}
			for _, name := range []string{refreshCookieName, csrfCookieName} {
				ck := cookieByName(w, name)
				if ck == nil || ck.MaxAge >= 0 || ck.Value != "" {
					t.Errorf("%s not cleared on logout: %+v", name, ck)
				}
			}
		})
	}
}

// ===========================================================================
// ForgotPassword — account enumeration
// ===========================================================================

// TestForgotPassword_IdenticalBodyForHitAndMiss: the response must be
// byte-identical whether or not the address exists, otherwise the endpoint is a
// free account-enumeration oracle. debug_token is a non-production affordance and
// must be absent whenever the usecase returns nil.
func TestForgotPassword_IdenticalBodyForHitAndMiss(t *testing.T) {
	serve := func() (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
		uc := newFakeAuthUC() // forgotToken stays nil for both calls
		r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))
		hit := post(r, "/api/auth/forgot-password", `{"email":"real@example.com"}`)
		miss := post(r, "/api/auth/forgot-password", `{"email":"nobody@example.com"}`)
		return hit, miss
	}
	hit, miss := serve()

	if hit.Code != http.StatusOK || miss.Code != http.StatusOK {
		t.Fatalf("statuses = %d / %d, want 200 / 200", hit.Code, miss.Code)
	}
	if hit.Body.String() != miss.Body.String() {
		t.Errorf("forgot-password leaks account existence:\n hit:  %s\n miss: %s", hit.Body.String(), miss.Body.String())
	}
	if strings.Contains(hit.Body.String(), "debug_token") {
		t.Errorf("debug_token present with a nil usecase token: %s", hit.Body.String())
	}

	// And when the usecase DOES hand back a debug token (non-production), it is
	// surfaced — otherwise local testing has no way to complete the flow.
	uc := newFakeAuthUC()
	tok := "dbg-123"
	uc.forgotToken = &tok
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))
	w := post(r, "/api/auth/forgot-password", `{"email":"real@example.com"}`)
	if !strings.Contains(w.Body.String(), "dbg-123") {
		t.Errorf("debug token not surfaced when provided: %s", w.Body.String())
	}
}

// ===========================================================================
// handleAppError — the chokepoint ~190 handlers funnel through
// ===========================================================================

func errCtx(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	return c, w
}

// TestHandleAppError_NeverLeaksRawErrorText: an unexpected error's text is log
// material, never response material. The old implementation interpolated it
// straight into the body, handing the caller table names, constraint names, and
// occasionally a value from a failing driver.
func TestHandleAppError_NeverLeaksRawErrorText(t *testing.T) {
	c, w := errCtx(t)
	handleAppError(c, errors.New("pq: duplicate key on users_email_idx"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"pq:", "duplicate key", "users_email_idx"} {
		if strings.Contains(body, leak) {
			t.Errorf("raw driver error leaked to the client (%q): %s", leak, body)
		}
	}
	if !strings.Contains(body, "internal server error") {
		t.Errorf("expected the generic message, got: %s", body)
	}
}

func TestHandleAppError_AppErrorPassesThroughWithRetryAfter(t *testing.T) {
	c, w := errCtx(t)
	handleAppError(c, &domain.AppError{Code: http.StatusTooManyRequests, Message: "too many attempts", RetryAfter: 42})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "42" {
		t.Errorf("Retry-After = %q, want 42", got)
	}
	if !strings.Contains(w.Body.String(), "too many attempts") {
		t.Errorf("AppError message not rendered: %s", w.Body.String())
	}

	// No RetryAfter → no header (an empty Retry-After confuses clients).
	c2, w2 := errCtx(t)
	handleAppError(c2, domain.NewAppError(http.StatusForbidden, "nope"))
	if got := w2.Header().Get("Retry-After"); got != "" {
		t.Errorf("unexpected Retry-After %q on a non-throttling error", got)
	}
	if w2.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w2.Code)
	}
}

// TestHandleAppError_AttachesRequestID: the reference the user reads off the error
// banner has to be the same one on the log line, on BOTH branches.
func TestHandleAppError_AttachesRequestID(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"app error", domain.NewAppError(http.StatusForbidden, "denied")},
		{"unexpected error", errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, w := errCtx(t)
			c.Set("request_id", "req-abc-123")
			handleAppError(c, tc.err)

			var body domain.APIResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("bad JSON: %v (%s)", err, w.Body.String())
			}
			if body.RequestID != "req-abc-123" {
				t.Errorf("request_id = %q, want req-abc-123", body.RequestID)
			}
		})
	}

	// With no request_id in the context the field is omitted rather than empty-string'd.
	c, w := errCtx(t)
	handleAppError(c, errors.New("boom"))
	if strings.Contains(w.Body.String(), "request_id") {
		t.Errorf("request_id emitted with no correlation id set: %s", w.Body.String())
	}
}

// ===========================================================================
// Cookie policy
// ===========================================================================

// TestCookiePolicy_HTTPSGetsSameSiteNoneSecure: production is cross-site (Pages
// frontend + separate API host), so an HTTPS request must get SameSite=None with
// Secure — without which the browser drops the refresh cookie on the cross-origin
// call and login silently loops back to the sign-in page. Plain-http dev stays Lax.
func TestCookiePolicy_HTTPSGetsSameSiteNoneSecure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newCtx := func(target string, headers map[string]string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, target, nil)
		for k, v := range headers {
			c.Request.Header.Set(k, v)
		}
		return c
	}

	cases := []struct {
		name       string
		target     string
		headers    map[string]string
		sameSite   string // config value
		secureCfg  bool
		wantMode   http.SameSite
		wantSecure bool
	}{
		{name: "plain http dev", target: "http://localhost/api/auth/login",
			wantMode: http.SameSiteLaxMode, wantSecure: false},
		{name: "direct TLS", target: "https://api.example.com/api/auth/login",
			wantMode: http.SameSiteNoneMode, wantSecure: true},
		{name: "behind a terminating proxy", target: "http://api.example.com/api/auth/login",
			headers:  map[string]string{"X-Forwarded-Proto": "https"},
			wantMode: http.SameSiteNoneMode, wantSecure: true},
		{name: "explicit COOKIE_SAMESITE=strict wins over plain http", target: "http://localhost/api/auth/login",
			sameSite: "strict", secureCfg: true,
			wantMode: http.SameSiteStrictMode, wantSecure: true},
		{name: "explicit COOKIE_SAMESITE=strict wins over TLS too", target: "https://api.example.com/api/auth/login",
			sameSite: "strict", secureCfg: false,
			wantMode: http.SameSiteStrictMode, wantSecure: false},
		{name: "COOKIE_SAMESITE=none on plain http", target: "http://localhost/api/auth/login",
			sameSite: "none",
			wantMode: http.SameSiteNoneMode, wantSecure: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg()
			cfg.CookieSameSite = tc.sameSite
			cfg.CookieSecure = tc.secureCfg

			mode, secure := cookiePolicy(newCtx(tc.target, tc.headers), cfg)
			if mode != tc.wantMode || secure != tc.wantSecure {
				t.Errorf("cookiePolicy = (%v, %v), want (%v, %v)", mode, secure, tc.wantMode, tc.wantSecure)
			}

			// oauthStateCookiePolicy is the same policy with Strict downgraded to Lax:
			// SameSite=Strict blocks the state cookie on the top-level redirect back
			// from Google, which would break every Google login.
			oMode, oSecure := oauthStateCookiePolicy(newCtx(tc.target, tc.headers), cfg)
			wantOMode := tc.wantMode
			if wantOMode == http.SameSiteStrictMode {
				wantOMode = http.SameSiteLaxMode
			}
			if oMode != wantOMode || oSecure != tc.wantSecure {
				t.Errorf("oauthStateCookiePolicy = (%v, %v), want (%v, %v)", oMode, oSecure, wantOMode, tc.wantSecure)
			}
		})
	}
}

// TestCookiePolicy_AppliedToTheRealSetCookieHeader closes the loop: the policy is
// not just computed, it reaches the wire.
func TestCookiePolicy_AppliedToTheRealSetCookieHeader(t *testing.T) {
	uc := newFakeAuthUC()
	uc.loginResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"email":"user@example.com","password":"correct-horse"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	ck := cookieByName(w, refreshCookieName)
	if ck == nil {
		t.Fatalf("no refresh cookie: %v", setCookieHeaders(w))
	}
	if !ck.Secure {
		t.Error("a proxied-HTTPS login must set Secure on the refresh cookie")
	}
	if ck.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None (cross-site prod)", ck.SameSite)
	}
}

// ===========================================================================
// Session routes
// ===========================================================================

func TestBadSessionUUIDIs400(t *testing.T) {
	uc := newFakeAuthUC()
	r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.New(), uuid.New())

	req := httptest.NewRequest(http.MethodDelete, "/api/auth/sessions/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if uc.count("RevokeSession") != 0 {
		t.Error("an unparseable session id must not reach the usecase")
	}
}

// TestAuthHandlers_401WithoutUserContext pins the fail-closed guard on every
// self-scoped handler. Mounted on a bare engine (no auth middleware at all), each
// must refuse rather than fall through with uuid.Nil — a nil user id would
// otherwise address "the zero user" in whatever query runs next.
func TestAuthHandlers_401WithoutUserContext(t *testing.T) {
	uc := newFakeAuthUC()
	h := NewAuthHandler(uc, testCfg())

	cases := []struct {
		name    string
		method  string
		path    string
		handler gin.HandlerFunc
	}{
		{"Me", http.MethodGet, "/me", h.Me},
		{"UpdateMe", http.MethodPatch, "/me", h.UpdateMe},
		{"ListSessions", http.MethodGet, "/sessions", h.ListSessions},
		{"SignOutEverywhere", http.MethodDelete, "/sessions", h.SignOutEverywhere},
		{"SwitchWorkspace", http.MethodPost, "/switch-workspace", h.SwitchWorkspace},
		{"ListWorkspaces", http.MethodGet, "/workspaces", h.ListWorkspaces},
		{"ResendVerification", http.MethodPost, "/resend-verification", h.ResendVerification},
		{"ChangePassword", http.MethodPost, "/change-password", h.ChangePassword},
		{"SetPassword", http.MethodPost, "/set-password", h.SetPassword},
		{"UnlinkGoogle", http.MethodPost, "/unlink-google", h.UnlinkGoogle},
		{"RevokeSession", http.MethodDelete, "/sessions/:id", h.RevokeSession},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New() // deliberately no auth middleware
			r.Handle(tc.method, tc.path, tc.handler)

			path := strings.Replace(tc.path, ":id", uuid.New().String(), 1)
			req := httptest.NewRequest(tc.method, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s: status = %d, want 401; body: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
	// None of them should have reached the usecase — the embedded nil interface
	// would have panicked on anything unimplemented, but this also catches an
	// implemented-but-wrongly-reached method.
	if len(uc.calls) != 0 {
		t.Errorf("context-less requests reached the usecase: %v", uc.calls)
	}
}

// TestSessionMintingHandlers_CookiesOnSuccessOnly covers the four handlers that
// re-mint a session mid-flight. Two distinct failure modes are pinned at once:
//
//   - On SUCCESS the new refresh cookie must be written. ChangePassword and
//     SignOutEverywhere deliberately revoke every other session, INCLUDING the
//     credential this request arrived with — so if the response forgets to set the
//     replacement, changing your own password signs you out of the device you
//     changed it on.
//   - On FAILURE no cookie may be written at all, or a rejected switch/change
//     silently swaps the caller's live session for whatever the zero-value response
//     carried.
func TestSessionMintingHandlers_CookiesOnSuccessOnly(t *testing.T) {
	targetOrg := uuid.New()
	routes := []struct {
		name   string
		method string
		path   string
		body   string
		// threadsCurrentToken records a real asymmetry in these four handlers, rather
		// than asserting a uniformity that does not exist: SignOutEverywhere and
		// SwitchWorkspace pass the PRESENTED refresh token down (their usecases need
		// to know which single credential to spare / revoke on switch hygiene), while
		// ChangePassword and SetPassword do not — those revoke everything and mint
		// fresh, so they have nothing to identify. Pinned so a future signature change
		// that starts or stops threading it is a deliberate, visible decision.
		threadsCurrentToken bool
	}{
		{name: "change-password", method: http.MethodPost, path: "/api/auth/change-password",
			body: `{"current_password":"old-password","new_password":"new-password"}`},
		{name: "set-password", method: http.MethodPost, path: "/api/auth/set-password",
			body: `{"new_password":"new-password"}`},
		{name: "sign-out-everywhere", method: http.MethodDelete, path: "/api/auth/sessions",
			threadsCurrentToken: true},
		{name: "switch-workspace", method: http.MethodPost, path: "/api/auth/switch-workspace",
			body: `{"org_id":"` + targetOrg.String() + `"}`, threadsCurrentToken: true},
	}

	for _, rt := range routes {
		t.Run(rt.name+"/success re-mints the session cookie", func(t *testing.T) {
			uc := newFakeAuthUC()
			uc.sessionResp = authResponseFixture()
			r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.New(), uuid.New())

			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "old-credential"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
			}
			ck := cookieByName(w, refreshCookieName)
			if ck == nil || ck.Value != "refresh-token-value" {
				t.Fatalf("the replacement session cookie was not written (%+v) — the caller is signed out of this device", ck)
			}
			if cookieByName(w, csrfCookieName) == nil {
				t.Errorf("no %s cookie alongside the re-minted session", csrfCookieName)
			}
			if tokenSeen := uc.lastRefreshToken == "old-credential"; tokenSeen != rt.threadsCurrentToken {
				t.Errorf("presented refresh token reached the usecase = %v (%q), want %v",
					tokenSeen, uc.lastRefreshToken, rt.threadsCurrentToken)
			}
		})

		t.Run(rt.name+"/failure writes no cookie", func(t *testing.T) {
			uc := newFakeAuthUC()
			uc.sessionErr = domain.NewAppError(http.StatusForbidden, "refused")
			r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.New(), uuid.New())

			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "old-credential"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body: %s", w.Code, w.Body.String())
			}
			if got := setCookieHeaders(w); len(got) != 0 {
				t.Errorf("a refused %s still wrote cookies: %v", rt.name, got)
			}
		})
	}
}

// TestRegister_Returns201WithSession: register is the one auth route that answers
// 201 rather than 200, and the SPA branches on it.
func TestRegister_Returns201WithSession(t *testing.T) {
	uc := newFakeAuthUC()
	uc.registerResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/register",
		`{"org_name":"Acme","email":"user@example.com","password":"password123","first_name":"Ada"}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	ck := cookieByName(w, refreshCookieName)
	if ck == nil || !ck.HttpOnly || ck.Path != "/api/auth" {
		t.Errorf("register did not establish the session cookie correctly: %+v", ck)
	}

	// A bind failure is a 400 that never reaches the usecase and never sets cookies.
	uc2 := newFakeAuthUC()
	r2 := mountAuthRoutes(NewAuthHandler(uc2, testCfg()))
	w2 := post(r2, "/api/auth/register", `{"email":"user@example.com"}`)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w2.Code)
	}
	if uc2.count("Register") != 0 || len(setCookieHeaders(w2)) != 0 {
		t.Error("an invalid registration reached the usecase or set cookies")
	}
}

// TestMe_AuthMethodsReflectStoredCredentials: PasswordHash and GoogleID are
// json:"-", so the SPA's Connected-accounts panel can only work off these computed
// flags. Getting them wrong offers "remove your password" to an account that would
// then have no way back in.
func TestMe_AuthMethodsReflectStoredCredentials(t *testing.T) {
	s := func(v string) *string { return &v }

	cases := []struct {
		name         string
		passwordHash *string
		googleID     *string
		wantPassword bool
		wantGoogle   bool
	}{
		{"password only", s("$2a$hash"), nil, true, false},
		{"google only", nil, s("g-123"), false, true},
		{"both linked", s("$2a$hash"), s("g-123"), true, true},
		{"neither (invite-pending account)", nil, nil, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newFakeAuthUC()
			uc.me = &domain.User{ID: uuid.New(), Email: "user@example.com",
				PasswordHash: tc.passwordHash, GoogleID: tc.googleID}
			uc.workspaces = []domain.WorkspaceInfo{}
			r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.New(), uuid.New())

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/me", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}

			var body struct {
				Data struct {
					AuthMethods struct {
						Password bool `json:"password"`
						Google   bool `json:"google"`
					} `json:"auth_methods"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("bad JSON: %v (%s)", err, w.Body.String())
			}
			if body.Data.AuthMethods.Password != tc.wantPassword || body.Data.AuthMethods.Google != tc.wantGoogle {
				t.Errorf("auth_methods = {password:%v google:%v}, want {password:%v google:%v}",
					body.Data.AuthMethods.Password, body.Data.AuthMethods.Google, tc.wantPassword, tc.wantGoogle)
			}
			// The raw credentials themselves must never appear in the payload.
			if strings.Contains(w.Body.String(), "$2a$hash") || strings.Contains(w.Body.String(), "g-123") {
				t.Errorf("raw credential material serialized to the client: %s", w.Body.String())
			}
		})
	}
}
