package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"
)

// auth_google_oauth_test.go covers GoogleLogin / GoogleCallback.
//
// GoogleCallback is the single densest security function on this surface: it is
// unauthenticated, it is reached by a top-level browser navigation the attacker
// can fully compose, and it mints a session at the end. Four distinct login-CSRF
// bypass paths live in its state check (no cookie / empty cookie / empty query /
// mismatch), and two credential-placement decisions live in its success path
// (challenge in a cookie not the URL; access token in the fragment not the query).

const googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth?client_id=x&state=abc"

func getReq(r http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestGoogleLogin_PersistsStateInHttpOnlyCookie: a state parameter that is
// generated but never persisted is no CSRF protection at all — the callback would
// have nothing to compare against. The cookie must be httpOnly (JS on any page
// must not be able to read or forge the value) and scoped to the auth path.
func TestGoogleLogin_PersistsStateInHttpOnlyCookie(t *testing.T) {
	uc := newFakeAuthUC()
	uc.googleURL = googleAuthURL
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := getReq(r, "/api/auth/google")

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != googleAuthURL {
		t.Errorf("Location = %q, want the provider URL", got)
	}

	ck := cookieByName(w, oauthStateCookieName)
	if ck == nil {
		t.Fatalf("no %s cookie — the callback would have nothing to validate against", oauthStateCookieName)
	}
	if ck.Value == "" {
		t.Error("state cookie is empty; the callback treats an empty cookie as a failure, so login would break")
	}
	if ck.Value != uc.lastState {
		t.Errorf("cookie state %q != the state sent to the provider %q", ck.Value, uc.lastState)
	}
	if !ck.HttpOnly {
		t.Error("state cookie must be httpOnly")
	}
	if ck.Path != "/api/auth" {
		t.Errorf("state cookie Path = %q, want /api/auth", ck.Path)
	}
	if ck.MaxAge != oauthStateCookieMaxAge {
		t.Errorf("state cookie MaxAge = %d, want %d", ck.MaxAge, oauthStateCookieMaxAge)
	}
}

func TestGoogleLogin_UnconfiguredIs503(t *testing.T) {
	uc := newFakeAuthUC() // googleURL == "" → OAuth not configured
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := getReq(r, "/api/auth/google")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
	if ck := cookieByName(w, oauthStateCookieName); ck != nil {
		t.Errorf("an unconfigured provider still planted a state cookie: %+v", ck)
	}
}

// TestGoogleCallback_StateMismatchRejected walks every branch of the state check.
// Each one is a complete login-CSRF bypass if it is dropped: an attacker sends the
// victim a crafted callback URL carrying the attacker's own ?code=, and the victim
// ends up silently signed into the ATTACKER's account — everything they then type
// lands in an account the attacker controls.
//
// The assertion that matters most is not the redirect: it is that
// authUC.GoogleLogin is never reached. A handler that redirects to an error page
// AFTER exchanging the code has already done the damage.
func TestGoogleCallback_StateMismatchRejected(t *testing.T) {
	cases := []struct {
		name   string
		cookie *http.Cookie
		query  string
	}{
		{"no state cookie at all", nil, "?state=abc&code=c"},
		{"empty state cookie", &http.Cookie{Name: oauthStateCookieName, Value: ""}, "?state=abc&code=c"},
		{"no state in the query", &http.Cookie{Name: oauthStateCookieName, Value: "abc"}, "?code=c"},
		{"empty state in the query", &http.Cookie{Name: oauthStateCookieName, Value: "abc"}, "?state=&code=c"},
		{"mismatched state", &http.Cookie{Name: oauthStateCookieName, Value: "abc"}, "?state=attacker&code=c"},
		{"neither present", nil, "?code=c"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := newFakeAuthUC()
			uc.googleResp = authResponseFixture() // would succeed if it were ever called
			cfg := testCfg()
			r := mountAuthRoutes(NewAuthHandler(uc, cfg))

			var cookies []*http.Cookie
			if tc.cookie != nil {
				cookies = append(cookies, tc.cookie)
			}
			w := getReq(r, "/api/auth/google/callback"+tc.query, cookies...)

			if w.Code != http.StatusTemporaryRedirect {
				t.Fatalf("status = %d, want 307; body: %s", w.Code, w.Body.String())
			}
			want := cfg.FrontendURL + "/login?error=invalid_oauth_state"
			if got := w.Header().Get("Location"); got != want {
				t.Errorf("Location = %q, want %q", got, want)
			}
			if uc.count("GoogleLogin") != 0 {
				t.Error("the code was exchanged despite a failed state check — login CSRF is open")
			}
		})
	}
}

// TestGoogleCallback_StateCookieIsSingleUseEvenOnFailure: the state cookie is
// burned on every callback, success or failure. A state that survives a failed
// attempt can be replayed, which turns a one-shot forgery into a retryable one.
func TestGoogleCallback_StateCookieIsSingleUseEvenOnFailure(t *testing.T) {
	uc := newFakeAuthUC()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := getReq(r, "/api/auth/google/callback?state=attacker&code=c",
		&http.Cookie{Name: oauthStateCookieName, Value: "abc"})

	ck := cookieByName(w, oauthStateCookieName)
	if ck == nil {
		t.Fatalf("state cookie not touched on the failure path: %v", setCookieHeaders(w))
	}
	if ck.MaxAge >= 0 || ck.Value != "" {
		t.Errorf("state cookie not expired: value=%q MaxAge=%d", ck.Value, ck.MaxAge)
	}
}

// TestGoogleCallback_ProviderErrorPassesThrough: user-denied-consent is a normal
// outcome, not a failure to hide — the SPA renders it.
func TestGoogleCallback_ProviderErrorPassesThrough(t *testing.T) {
	uc := newFakeAuthUC()
	cfg := testCfg()
	r := mountAuthRoutes(NewAuthHandler(uc, cfg))

	w := getReq(r, "/api/auth/google/callback?error=access_denied")

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", w.Code)
	}
	if got, want := w.Header().Get("Location"), cfg.FrontendURL+"/login?error=access_denied"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if uc.count("GoogleLogin") != 0 {
		t.Error("a denied consent must not reach the provider exchange")
	}
}

func TestGoogleCallback_MissingCodeRejected(t *testing.T) {
	uc := newFakeAuthUC()
	cfg := testCfg()
	r := mountAuthRoutes(NewAuthHandler(uc, cfg))

	w := getReq(r, "/api/auth/google/callback?state=abc",
		&http.Cookie{Name: oauthStateCookieName, Value: "abc"})

	if got, want := w.Header().Get("Location"), cfg.FrontendURL+"/login?error=missing_code"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if uc.count("GoogleLogin") != 0 {
		t.Error("an empty code must not reach the provider exchange")
	}
}

// TestGoogleCallback_ExchangeFailureRedirectsWithoutASession pins that a provider
// failure never leaks a partial session.
func TestGoogleCallback_ExchangeFailureRedirectsWithoutASession(t *testing.T) {
	uc := newFakeAuthUC()
	uc.googleErr = errors.New("token exchange refused")
	cfg := testCfg()
	r := mountAuthRoutes(NewAuthHandler(uc, cfg))

	w := getReq(r, "/api/auth/google/callback?state=abc&code=the-code",
		&http.Cookie{Name: oauthStateCookieName, Value: "abc"})

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, cfg.FrontendURL+"/login?error=google_login_failed") {
		t.Errorf("Location = %q, want a google_login_failed redirect", loc)
	}
	if uc.lastGoogleCode != "the-code" {
		t.Errorf("code threaded as %q, want the-code", uc.lastGoogleCode)
	}
	for _, name := range []string{refreshCookieName, csrfCookieName, twoFactorChallengeCookie} {
		if ck := cookieByName(w, name); ck != nil {
			t.Errorf("failed exchange set %s: %+v", name, ck)
		}
	}
}

// TestGoogleCallback_TwoFactorSetsChallengeCookieNotSession: Google proved the
// identity, not the second factor. The challenge rides in an httpOnly cookie — a
// URL fragment would be better than a query string, but a challenge that never
// enters the URL at all cannot be shoulder-surfed off the address bar or restored
// from history. And critically: no session cookies yet.
func TestGoogleCallback_TwoFactorSetsChallengeCookieNotSession(t *testing.T) {
	uc := newFakeAuthUC()
	uc.googleResp = &domain.AuthResponse{
		TwoFactorRequired: true,
		ChallengeToken:    "challenge-secret-value",
		AccessToken:       "must-not-be-issued",
		RefreshToken:      "must-not-be-issued",
	}
	cfg := testCfg()
	r := mountAuthRoutes(NewAuthHandler(uc, cfg))

	w := getReq(r, "/api/auth/google/callback?state=abc&code=the-code",
		&http.Cookie{Name: oauthStateCookieName, Value: "abc"})

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if got, want := loc, cfg.FrontendURL+"/login/2fa"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if strings.Contains(loc, "challenge-secret-value") {
		t.Errorf("the challenge token leaked into the redirect URL: %q", loc)
	}

	ch := cookieByName(w, twoFactorChallengeCookie)
	if ch == nil {
		t.Fatalf("no %s cookie — /2fa/verify would have nothing to exchange", twoFactorChallengeCookie)
	}
	if ch.Value != "challenge-secret-value" {
		t.Errorf("challenge cookie value = %q", ch.Value)
	}
	if !ch.HttpOnly {
		t.Error("challenge cookie must be httpOnly")
	}
	if ch.Path != "/api/auth" {
		t.Errorf("challenge cookie Path = %q, want /api/auth", ch.Path)
	}

	for _, name := range []string{refreshCookieName, csrfCookieName} {
		if ck := cookieByName(w, name); ck != nil {
			t.Errorf("a 2FA challenge issued %s (%q) — the second factor is bypassed", name, ck.Value)
		}
	}
}

// TestGoogleCallback_AccessTokenRidesInFragmentNotQuery: the fragment is never
// sent to a server, never lands in an access log, and never rides in the Referer
// header. A one-character slip from '#' to '?' would put a live bearer token in
// CDN logs, in the frontend host's logs, in browser history, and in the Referer of
// every asset the callback page loads — with no error and no test failing.
func TestGoogleCallback_AccessTokenRidesInFragmentNotQuery(t *testing.T) {
	uc := newFakeAuthUC()
	resp := authResponseFixture()
	resp.AccessToken = "jwt-access-token-abc"
	resp.RefreshToken = "refresh-token-secret"
	resp.NeedsChooser = true
	uc.googleResp = resp
	cfg := testCfg()
	r := mountAuthRoutes(NewAuthHandler(uc, cfg))

	w := getReq(r, "/api/auth/google/callback?state=abc&code=the-code",
		&http.Cookie{Name: oauthStateCookieName, Value: "abc"})

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")

	if !strings.Contains(loc, "#access_token=") {
		t.Fatalf("access token is not in the URL fragment: %q", loc)
	}
	beforeFragment, _, _ := strings.Cut(loc, "#")
	if strings.Contains(beforeFragment, "access_token") {
		t.Errorf("access_token appears BEFORE the fragment — it will be logged and sent as Referer: %q", loc)
	}
	if strings.Contains(beforeFragment, "jwt-access-token-abc") {
		t.Errorf("the access token value appears in the server-visible part of the URL: %q", loc)
	}
	// The refresh token is a 7-day credential and must never be in the URL at all.
	if strings.Contains(loc, "refresh-token-secret") {
		t.Errorf("the refresh token leaked into the redirect URL: %q", loc)
	}
	// needs_chooser is routing metadata, not a credential — it belongs in the query.
	if !strings.Contains(beforeFragment, "needs_chooser=true") {
		t.Errorf("needs_chooser missing from the query: %q", loc)
	}

	// The refresh token goes where it belongs: an httpOnly cookie.
	ck := cookieByName(w, refreshCookieName)
	if ck == nil || ck.Value != "refresh-token-secret" || !ck.HttpOnly {
		t.Errorf("refresh cookie wrong on the OAuth success path: %+v", ck)
	}
	if cookieByName(w, csrfCookieName) == nil {
		t.Errorf("no %s cookie on the OAuth success path", csrfCookieName)
	}
	// The state cookie is burned on success too.
	if st := cookieByName(w, oauthStateCookieName); st == nil || st.MaxAge >= 0 {
		t.Errorf("state cookie not burned on the success path: %+v", st)
	}
}

// TestGoogleCallback_FrontendURLDefaultsWhenUnset guards the redirect target
// falling back to local dev rather than emitting a scheme-less "/login?..." that a
// browser would resolve against the API host.
func TestGoogleCallback_FrontendURLDefaultsWhenUnset(t *testing.T) {
	uc := newFakeAuthUC()
	cfg := testCfg()
	cfg.FrontendURL = ""
	r := mountAuthRoutes(NewAuthHandler(uc, cfg))

	w := getReq(r, "/api/auth/google/callback?error=access_denied")

	if got, want := w.Header().Get("Location"), "http://localhost:5173/login?error=access_denied"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
}
