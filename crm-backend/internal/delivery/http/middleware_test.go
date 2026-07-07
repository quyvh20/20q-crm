package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

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
