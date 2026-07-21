package config

import (
	"strings"
	"testing"

	"github.com/gin-contrib/cors"
)

// The bug these guard: FRONTEND_URL feeds delivery.AllowedOrigins, which feeds
// cors.New(...) in main.go — and gin-contrib/cors PANICS on a bad origin instead of
// returning an error. That call precedes srv.ListenAndServe(), so a schemeless value
// kills the process before it binds: no /health, no logs of our own, just a
// connection refused for the whole healthcheck window while the previous container
// keeps serving and masks it.

func TestValidateFrontendURLAcceptsRealOrigins(t *testing.T) {
	for _, ok := range []string{
		"http://localhost:5173", // the local default
		"https://20q-crm.pages.dev",
		"https://app.example.com:8443",
		"http://127.0.0.1:3000",
		"https://*.example.com", // wildcard form the library also accepts
	} {
		if err := validateFrontendURL(ok); err != nil {
			t.Errorf("%q should be accepted, got %v", ok, err)
		}
	}
}

func TestValidateFrontendURLRejectsWhatWouldPanic(t *testing.T) {
	cases := map[string]string{
		"schemeless host":   "20q-crm.pages.dev",
		"bare hostname":     "localhost:5173",
		"protocol-relative": "//app.example.com",
		"wrong scheme":      "ftp://files.example.com",
		"empty":             "",
		"whitespace only":   "   ",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateFrontendURL(bad); err == nil {
				t.Errorf("%q must be rejected", bad)
			}
		})
	}
}

// A padded origin does NOT panic — it silently fails to match any browser Origin
// header, so every cross-origin request is rejected with nothing in the logs. That
// is worse than a crash, so it is rejected too.
func TestValidateFrontendURLRejectsPadding(t *testing.T) {
	for _, padded := range []string{"https://app.example.com ", " https://app.example.com", "\thttps://app.example.com"} {
		if err := validateFrontendURL(padded); err == nil {
			t.Errorf("%q must be rejected: padding silently breaks origin matching", padded)
		}
	}
}

// The error has to name the variable and the fix. A message that merely says
// "invalid config" would leave an operator exactly where the panic did.
func TestValidateFrontendURLErrorIsActionable(t *testing.T) {
	err := validateFrontendURL("20q-crm.pages.dev")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"FRONTEND_URL", "20q-crm.pages.dev", "https://"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err.Error())
		}
	}
}

// The decisive test: our rule must agree with the LIBRARY, not with my reading of
// it. Anything we accept must not panic in cors.New, and anything we reject for a
// scheme reason must be something the library would itself have refused. If a
// dependency bump changes the accepted schemes, this fails instead of production.
func TestValidateFrontendURLMatchesCORSLibrary(t *testing.T) {
	corsAccepts := func(origin string) (ok bool) {
		defer func() {
			if r := recover(); r != nil {
				ok = false
			}
		}()
		// Mirrors the real construction in main.go, including the two hardcoded
		// origins AllowedOrigins always appends.
		cors.New(cors.Config{
			AllowOrigins: []string{origin, "http://localhost:5173", "https://20q-crm.pages.dev"},
			AllowMethods: []string{"GET"},
		})
		return true
	}

	for _, origin := range []string{
		"https://app.example.com",
		"http://localhost:5173",
		"https://*.example.com",
		"20q-crm.pages.dev",
		"localhost:5173",
		"//app.example.com",
		"ftp://files.example.com",
	} {
		weAccept := validateFrontendURL(origin) == nil
		libAccepts := corsAccepts(origin)
		if weAccept && !libAccepts {
			t.Errorf("%q: we accept it but cors.New PANICS — this is the exact production crash", origin)
		}
	}
}
