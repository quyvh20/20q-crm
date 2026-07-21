package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// knownUnbound lists mapstructure keys that do NOT reach the Config struct from
// the environment, deliberately left that way here.
//
// Both are live production bugs, and both are load-bearing enough that fixing
// them silently inside an unrelated change would be worse than the bug:
//
//   - TOTP_ENC_KEY: cfg.TOTPEncKey is therefore always "" in production, so
//     usecase/two_factor_crypto.go falls back to deriving the key from
//     JWT_SECRET. Binding it now would flip the key material for any deployment
//     that HAS set the variable, making every stored TOTP secret undecryptable
//     and locking those users out of 2FA until they burn a backup code. The fix
//     needs a re-encryption pass, not a one-line binding.
//   - PADDLE_WEBHOOK_SECRET: the original cautionary tale, recorded in
//     config.go's own comment.
//
// Nothing may be added to this list to make a NEW key pass. That is the whole
// value of the test: it cannot stop the two failures that already happened, but
// it makes a third one impossible to introduce by omission — which is precisely
// how both of these arrived.
var knownUnbound = map[string]string{
	"TOTP_ENC_KEY":          "fixing it needs a re-encryption pass; see the comment above",
	"PADDLE_WEBHOOK_SECRET": "the original BindEnv omission, recorded in config.go",
}

// TestEveryConfigFieldResolvesFromTheEnvironment is the regression test for the
// single most repeated defect in this file's history.
//
// viper.AutomaticEnv() does NOT feed Unmarshal — only keys reached by an
// explicit BindEnv or SetDefault land in the struct. Production runs with no
// .env file, so a forgotten binding is invisible everywhere except production,
// where the field silently reads its zero value. It has now happened twice.
//
// The check is behavioural rather than a grep for BindEnv calls, because
// SetDefault alone is also sufficient to make a key resolve (PORT relies on
// exactly that) — a source-level test would report false failures for those and
// teach people to add redundant bindings.
func TestEveryConfigFieldResolvesFromTheEnvironment(t *testing.T) {
	typ := reflect.TypeOf(Config{})

	// Keys that LoadConfig validates for FORMAT need a sentinel that satisfies the
	// format while staying unique, or this test fails on the validation rather than
	// on what it is actually asserting. Still a distinct value per key, so the
	// copy-paste detection below is unaffected.
	formatted := map[string]string{
		"FRONTEND_URL": "http://sentinel-frontend_url", // see validateFrontendURL
	}

	// A sentinel per key, so a field reading another field's value (a
	// copy-paste in the struct tags) fails too rather than passing.
	sentinels := make(map[string]string, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		key := typ.Field(i).Tag.Get("mapstructure")
		if key == "" {
			continue
		}
		if v, ok := formatted[key]; ok {
			sentinels[key] = v
			continue
		}
		if typ.Field(i).Type.Kind() == reflect.Bool {
			sentinels[key] = "true"
			continue
		}
		sentinels[key] = "sentinel-" + strings.ToLower(key)
	}

	for key, value := range sentinels {
		t.Setenv(key, value)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	val := reflect.ValueOf(*cfg)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := field.Tag.Get("mapstructure")
		if key == "" {
			continue
		}

		var resolved bool
		switch field.Type.Kind() {
		case reflect.Bool:
			resolved = val.Field(i).Bool()
		case reflect.String:
			resolved = val.Field(i).String() == sentinels[key]
		default:
			t.Fatalf("%s has unhandled kind %s — teach this test about it rather than skipping it", field.Name, field.Type.Kind())
		}

		if reason, known := knownUnbound[key]; known {
			if resolved {
				// Someone fixed it. Delete the allowlist entry so the key is
				// protected like every other one from now on.
				t.Errorf("%s (%s) now resolves from the environment — remove it from knownUnbound", key, field.Name)
			} else {
				t.Logf("%s is knowingly unbound: %s", key, reason)
			}
			continue
		}

		if !resolved {
			t.Errorf(
				"%s does not reach Config.%s from the environment.\n"+
					"Add viper.BindEnv(%q) to LoadConfig. Without it the field reads its zero value in production, "+
					"where there is no .env file — which is how TOTP_ENC_KEY and PADDLE_WEBHOOK_SECRET both broke.",
				key, field.Name, key,
			)
		}
	}
}

// TestIntegrationEncKeyHasNoDefault pins the deliberate absence of a default.
//
// A default here would be worse than useless: it would make the key resolve to
// a known value, so the boot guard could not tell "configured" from "nobody set
// this", and every provider credential on an unconfigured deployment would be
// sealed under key material that is public in this repository.
func TestIntegrationEncKeyHasNoDefault(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.IntegrationEncKey != "" {
		t.Fatalf("INTEGRATION_ENC_KEY must have no default, got %q", cfg.IntegrationEncKey)
	}
}

// TestPublicAPIBaseURLDefaultsToAnAbsoluteOrigin guards the value providers see.
//
// This config's first real consumer is an OAuth redirect URI that must
// byte-match a provider console. A relative or trailing-slashed value produces
// a callback URL that is rejected at the provider and looks fine in every test.
func TestPublicAPIBaseURLDefaultsToAnAbsoluteOrigin(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !strings.HasPrefix(cfg.PublicAPIBaseURL, "https://") {
		t.Fatalf("PUBLIC_API_BASE_URL must default to an absolute https origin, got %q", cfg.PublicAPIBaseURL)
	}
	if strings.HasSuffix(cfg.PublicAPIBaseURL, "/") {
		t.Fatalf("PUBLIC_API_BASE_URL must not end in a slash (callers join with %q), got %q", "/api/…", cfg.PublicAPIBaseURL)
	}
}
