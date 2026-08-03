package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ===========================================================================
// VerifyTwoFactor — the public endpoint that turns a challenge into a session
// ===========================================================================

// TestVerifyTwoFactor_BodyTokenWinsOverCookie pins a precedence that is the exact
// INVERSE of refreshTokenFromRequest's (where the cookie wins). Both are pinned
// here together and on purpose: the two resolvers look interchangeable, so a
// future "let's make these consistent" refactor could quietly swap which
// credential authenticates a request. The asymmetry is intentional —
//
//   - /2fa/verify: the SPA posts the challenge it was just handed in the login
//     response body; the cookie only exists for the Google redirect flow, which has
//     no response body to carry it.
//   - /refresh: the httpOnly cookie is the credential of record; the body is the
//     non-browser fallback.
func TestVerifyTwoFactor_BodyTokenWinsOverCookie(t *testing.T) {
	uc := newFakeAuthUC()
	uc.verifyResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/2fa/verify", `{"challenge_token":"FROM-BODY","code":"123456"}`,
		&http.Cookie{Name: twoFactorChallengeCookie, Value: "FROM-COOKIE"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if uc.lastChallengeToken != "FROM-BODY" {
		t.Errorf("/2fa/verify used %q, want the BODY token to win", uc.lastChallengeToken)
	}

	// The mirror-image contract on /refresh, asserted in the same test so the pair
	// cannot drift apart unnoticed.
	uc2 := newFakeAuthUC()
	uc2.refreshResp = authResponseFixture()
	r2 := mountAuthRoutes(NewAuthHandler(uc2, testCfg()))
	if w := post(r2, "/api/auth/refresh", `{"refresh_token":"FROM-BODY"}`,
		&http.Cookie{Name: refreshCookieName, Value: "FROM-COOKIE"}); w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d", w.Code)
	}
	if uc2.lastRefreshToken != "FROM-COOKIE" {
		t.Errorf("/refresh used %q, want the COOKIE to win", uc2.lastRefreshToken)
	}
}

func TestVerifyTwoFactor_FallsBackToChallengeCookie(t *testing.T) {
	uc := newFakeAuthUC()
	uc.verifyResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/2fa/verify", `{"code":"123456"}`,
		&http.Cookie{Name: twoFactorChallengeCookie, Value: "FROM-COOKIE"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if uc.lastChallengeToken != "FROM-COOKIE" {
		t.Errorf("challenge token = %q, want the cookie (the Google redirect flow)", uc.lastChallengeToken)
	}
	if uc.lastCode != "123456" {
		t.Errorf("code = %q, want 123456", uc.lastCode)
	}

	// With neither, the empty token still reaches the usecase — which is where the
	// "your sign-in session expired" decision lives. Pinned so nobody "helpfully"
	// short-circuits it into a different status.
	uc2 := newFakeAuthUC()
	uc2.verifyErr = domain.NewAppError(http.StatusBadRequest, "your sign-in session expired — please sign in again")
	r2 := mountAuthRoutes(NewAuthHandler(uc2, testCfg()))
	w2 := post(r2, "/api/auth/2fa/verify", `{"code":"123456"}`)
	if uc2.count("VerifyTwoFactor") != 1 || uc2.lastChallengeToken != "" {
		t.Errorf("with no token anywhere: calls=%d token=%q", uc2.count("VerifyTwoFactor"), uc2.lastChallengeToken)
	}
	if w2.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want the usecase's 400", w2.Code)
	}
}

// TestVerifyTwoFactor_SuccessClearsChallengeAndSetsSession: the challenge is
// spent, the session begins. Leaving the challenge cookie behind leaves a
// half-authenticated credential lying in the browser after it is no longer needed.
func TestVerifyTwoFactor_SuccessClearsChallengeAndSetsSession(t *testing.T) {
	uc := newFakeAuthUC()
	uc.verifyResp = authResponseFixture()
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/2fa/verify", `{"code":"123456"}`,
		&http.Cookie{Name: twoFactorChallengeCookie, Value: "ch"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	ch := cookieByName(w, twoFactorChallengeCookie)
	if ch == nil || ch.MaxAge >= 0 || ch.Value != "" {
		t.Errorf("challenge cookie not cleared: %+v", ch)
	}
	refresh := cookieByName(w, refreshCookieName)
	if refresh == nil || refresh.Value != "refresh-token-value" || !refresh.HttpOnly {
		t.Errorf("session refresh cookie wrong after verify: %+v", refresh)
	}
	if cookieByName(w, csrfCookieName) == nil {
		t.Errorf("no %s cookie after a successful verify", csrfCookieName)
	}
}

// TestVerifyTwoFactor_FailureSetsNoSessionCookies: a wrong code must leave the
// caller exactly as unauthenticated as they were.
func TestVerifyTwoFactor_FailureSetsNoSessionCookies(t *testing.T) {
	uc := newFakeAuthUC()
	uc.verifyErr = domain.NewAppError(http.StatusUnauthorized, "that code isn't right")
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	w := post(r, "/api/auth/2fa/verify", `{"code":"000000"}`,
		&http.Cookie{Name: twoFactorChallengeCookie, Value: "ch"})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	for _, name := range []string{refreshCookieName, csrfCookieName} {
		if ck := cookieByName(w, name); ck != nil {
			t.Errorf("a failed 2FA verify issued %s (%q)", name, ck.Value)
		}
	}
}

func TestVerifyTwoFactor_MissingCodeIs400(t *testing.T) {
	for _, body := range []string{`{}`, `{"challenge_token":"ch"}`, `{"code":""}`, ``} {
		uc := newFakeAuthUC()
		r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))
		w := post(r, "/api/auth/2fa/verify", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, w.Code)
		}
		if uc.count("VerifyTwoFactor") != 0 {
			t.Errorf("body %q reached the usecase with no code", body)
		}
	}
}

// TestVerifyTwoFactor_ErrorShapeIsGeneric: the refusal must not tell an attacker
// WHICH factor was wrong (a "that's not a valid backup code" would confirm the
// account has backup codes left, and split the search space), and must never echo
// the submitted code back — an echoed credential lands in the SPA's error banner,
// in any client-side error reporter, and in shared screenshots.
func TestVerifyTwoFactor_ErrorShapeIsGeneric(t *testing.T) {
	uc := newFakeAuthUC()
	uc.verifyErr = domain.NewAppError(http.StatusUnauthorized, "that code isn't right")
	r := mountAuthRoutes(NewAuthHandler(uc, testCfg()))

	const submitted = "914725"
	w := post(r, "/api/auth/2fa/verify", `{"challenge_token":"ch-secret","code":"`+submitted+`"}`)

	body := strings.ToLower(w.Body.String())
	if strings.Contains(body, submitted) {
		t.Errorf("the submitted code was echoed back: %s", w.Body.String())
	}
	if strings.Contains(body, "ch-secret") {
		t.Errorf("the challenge token was echoed back: %s", w.Body.String())
	}
	for _, tell := range []string{"totp", "authenticator", "backup"} {
		if strings.Contains(body, tell) {
			t.Errorf("the refusal distinguishes the factor type (%q): %s", tell, w.Body.String())
		}
	}
}

// ===========================================================================
// Enrollment routes
// ===========================================================================

// TestTwoFactorEnrollmentRoutes_401WithoutUser: every enrollment handler is
// self-scoped off the session user. Without one they must refuse, not proceed with
// uuid.Nil.
func TestTwoFactorEnrollmentRoutes_401WithoutUser(t *testing.T) {
	uc := newFakeAuthUC()
	h := NewAuthHandler(uc, testCfg())

	cases := []struct {
		name    string
		method  string
		path    string
		handler gin.HandlerFunc
	}{
		{"StartTwoFactorSetup", http.MethodPost, "/2fa/setup", h.StartTwoFactorSetup},
		{"EnableTwoFactor", http.MethodPost, "/2fa/enable", h.EnableTwoFactor},
		{"DisableTwoFactor", http.MethodPost, "/2fa/disable", h.DisableTwoFactor},
		{"RegenerateBackupCodes", http.MethodPost, "/2fa/backup-codes", h.RegenerateBackupCodes},
		{"GetTwoFactorStatus", http.MethodGet, "/2fa", h.GetTwoFactorStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New() // no auth middleware
			r.Handle(tc.method, tc.path, tc.handler)

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"code":"123456"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s: status = %d, want 401; body: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
	if len(uc.calls) != 0 {
		t.Errorf("context-less enrollment requests reached the usecase: %v", uc.calls)
	}
}

// TestTwoFactorMutations_RequireACode: possession of a live session is NOT enough
// to drop or rotate a second factor — a stolen access token must not be able to
// turn 2FA off. The code requirement is the control, and it lives in the binding
// tag, which is one deleted word away from disappearing.
func TestTwoFactorMutations_RequireACode(t *testing.T) {
	routes := []struct {
		name string
		path string
		call string
	}{
		{"enable", "/api/auth/2fa/enable", "EnableTwoFactor"},
		{"disable", "/api/auth/2fa/disable", "DisableTwoFactor"},
		{"backup-codes", "/api/auth/2fa/backup-codes", "RegenerateBackupCodes"},
	}
	bodies := []string{`{}`, `{"code":""}`, ``}

	for _, rt := range routes {
		for _, body := range bodies {
			t.Run(rt.name+" body="+body, func(t *testing.T) {
				uc := newFakeAuthUC()
				r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.New(), uuid.New())

				w := post(r, rt.path, body)
				if w.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
				}
				if uc.count(rt.call) != 0 {
					t.Errorf("%s reached the usecase with no code", rt.call)
				}
			})
		}
	}

	// With a code, the call goes through and the org + code are threaded.
	uc := newFakeAuthUC()
	uc.backupCodes = &domain.BackupCodesResult{Codes: []string{"aaaa-bbbb"}}
	orgID, userID := uuid.New(), uuid.New()
	r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), orgID, userID)
	if w := post(r, "/api/auth/2fa/enable", `{"code":"123456"}`); w.Code != http.StatusOK {
		t.Fatalf("enable status = %d: %s", w.Code, w.Body.String())
	}
	if uc.lastCode != "123456" || uc.lastOrgID != orgID || uc.lastUserID != userID {
		t.Errorf("enable threaded code=%q org=%v user=%v", uc.lastCode, uc.lastOrgID, uc.lastUserID)
	}
	if uc.lastMeta.IP == "" {
		t.Error("request meta not threaded — the auth event would have no IP")
	}
}

// ===========================================================================
// ResetMemberTwoFactor — the admin break-glass
// ===========================================================================

func deleteReq(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, path, nil))
	return w
}

func TestResetMemberTwoFactor_BadUserIDIs400(t *testing.T) {
	uc := newFakeAuthUC()
	r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.New(), uuid.New())

	w := deleteReq(r, "/api/workspaces/members/not-a-uuid/two-factor")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if uc.count("ResetMemberTwoFactor") != 0 {
		t.Error("an unparseable target id must not reach the usecase")
	}
}

// TestResetMemberTwoFactor_401WithoutOrg: this handler keys off the ORG (unlike
// the self-scoped ones), so the org guard is the one that must fail closed —
// actorID is deliberately best-effort.
func TestResetMemberTwoFactor_401WithoutOrg(t *testing.T) {
	uc := newFakeAuthUC()
	r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), uuid.Nil, uuid.New())

	w := deleteReq(r, "/api/workspaces/members/"+uuid.New().String()+"/two-factor")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", w.Code, w.Body.String())
	}
	if uc.count("ResetMemberTwoFactor") != 0 {
		t.Error("an org-less caller reached the break-glass usecase")
	}
}

// TestResetMemberTwoFactor_PassesOrgActorAndTarget: the audit trail for a
// break-glass depends on all three being distinct and correctly ordered. Swapping
// actor and target here would attribute the reset to the wrong person AND reset
// the wrong account.
func TestResetMemberTwoFactor_PassesOrgActorAndTarget(t *testing.T) {
	uc := newFakeAuthUC()
	orgID, actorID, targetID := uuid.New(), uuid.New(), uuid.New()
	r := mountAuthedRoutes(NewAuthHandler(uc, testCfg()), orgID, actorID)

	w := deleteReq(r, "/api/workspaces/members/"+targetID.String()+"/two-factor")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if uc.lastOrgID != orgID {
		t.Errorf("org = %v, want %v", uc.lastOrgID, orgID)
	}
	if uc.lastUserID != actorID {
		t.Errorf("actor = %v, want the SESSION user %v", uc.lastUserID, actorID)
	}
	if uc.lastTargetUserID != targetID {
		t.Errorf("target = %v, want the PATH user %v", uc.lastTargetUserID, targetID)
	}
	if uc.lastMeta.IP == "" {
		t.Error("request meta not threaded — an admin break-glass with no IP is a poor audit record")
	}
}
