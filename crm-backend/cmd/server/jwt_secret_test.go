package main

import (
	"strings"
	"testing"

	"crm-backend/pkg/config"
)

// The check compares against a COPY of the viper default. That duplication is
// deliberate — main must not depend on config exporting it — but a copy that
// drifts is a check that silently passes forever, so pin them together.
func TestDefaultJWTSecretMatchesTheConfigDefault(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.JWTSecret != defaultJWTSecret {
		t.Fatalf("checkJWTSecret compares against %q but the config default is now %q — the guard would never fire",
			defaultJWTSecret, cfg.JWTSecret)
	}
}

// The value we refuse to boot on must actually look like the thing it names: a
// placeholder, not something an operator could plausibly have chosen on purpose.
func TestDefaultJWTSecretIsRecognisablyAPlaceholder(t *testing.T) {
	if !strings.Contains(defaultJWTSecret, "change-me") {
		t.Fatalf("the default no longer reads as a placeholder (%q); revisit whether matching on it is still the right signal", defaultJWTSecret)
	}
}

// Guards the rollout itself. jwtSecretFatal ships false for exactly one deploy so
// the log answers "is prod on the default?" without risking a crash-loop; this test
// is the reminder to flip it. Delete the test in the same commit that flips it.
func TestJWTSecretFatalIsStillTheOneDeployDefault(t *testing.T) {
	if jwtSecretFatal {
		t.Log("jwtSecretFatal is enabled — an insecure JWT_SECRET now refuses the boot. " +
			"Delete this test; it exists only to mark the pending flip.")
	}
}
