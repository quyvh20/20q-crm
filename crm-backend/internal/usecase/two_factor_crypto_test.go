package usecase

import "testing"

// The whole point of these tests is one non-obvious property that an operator
// has to get right in a dashboard field, with no feedback if they get it wrong:
// totpKey HASHES its input, so TOTP_ENC_KEY is a pre-image, not a key. That makes
// "set TOTP_ENC_KEY to the value currently in use" mean JWT_SECRET verbatim — and
// makes the intuitive alternative (set it to the derived key) silently wrong.

const (
	testJWTSecret = "jwt-secret-for-tests-0123456789abcdef"
	testSeed      = "JBSWY3DPEHPK3PXP"
)

// TestTOTPEncKeyEqualToJWTSecretPreservesTheKey is the safety proof for binding
// TOTP_ENC_KEY at all. A deployment that has always run with it unset has its
// secrets sealed under the JWT_SECRET-derived key; setting the variable to
// JWT_SECRET verbatim must keep those exact secrets readable, or the cutover
// needs a re-encryption pass instead of a one-line binding.
func TestTOTPEncKeyEqualToJWTSecretPreservesTheKey(t *testing.T) {
	// Sealed the way production does it today: TOTP_ENC_KEY unset.
	sealed, err := encryptTOTPSecret(testSeed, "", testJWTSecret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Opened the way it will work after the binding lands, with the variable set
	// to JWT_SECRET verbatim. JWT_SECRET is also rotated here to something else, to
	// prove the decoupling is real and not just the fallback firing again.
	got, err := decryptTOTPSecret(sealed, testJWTSecret, "a-completely-rotated-jwt-secret")
	if err != nil {
		t.Fatalf("TOTP_ENC_KEY=JWT_SECRET must keep existing secrets readable: %v", err)
	}
	if got != testSeed {
		t.Fatalf("seed changed through the cutover: got %q want %q", got, testSeed)
	}
}

// TestTOTPEncKeySetToTheDerivedKeyBricksDecryption pins the trap. The natural
// reading of "set TOTP_ENC_KEY to the currently-derived key value" produces a
// DOUBLE hash and a different AES key. Nothing surfaces this at deploy time,
// which is why cmd/server/main.go probes stored secrets at boot.
func TestTOTPEncKeySetToTheDerivedKeyBricksDecryption(t *testing.T) {
	sealed, err := encryptTOTPSecret(testSeed, "", testJWTSecret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	derived, err := totpKey("", testJWTSecret)
	if err != nil {
		t.Fatalf("totpKey: %v", err)
	}

	if _, err := decryptTOTPSecret(sealed, string(derived), testJWTSecret); err == nil {
		t.Fatal("expected the derived key to FAIL as a pre-image — if this ever passes, the derivation changed and the operator guidance in config.go is wrong")
	}
}

// TestTOTPKeyDoesNotTrimMaterial covers the copy-paste failure: TrimSpace is used
// only to test emptiness, never assigned back, so stray whitespace in an env var
// silently changes the derived key.
func TestTOTPKeyDoesNotTrimMaterial(t *testing.T) {
	clean, err := totpKey(testJWTSecret, "")
	if err != nil {
		t.Fatalf("totpKey: %v", err)
	}
	padded, err := totpKey(testJWTSecret+"\n", "")
	if err != nil {
		t.Fatalf("totpKey: %v", err)
	}
	if string(clean) == string(padded) {
		t.Fatal("a trailing newline no longer changes the key — if the derivation started trimming, say so in the operator guidance instead of leaving this test failing")
	}
}

func TestProbeTOTPKeyMaterial(t *testing.T) {
	good, err := encryptTOTPSecret(testSeed, "", testJWTSecret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	t.Run("no enrolled users is not an error", func(t *testing.T) {
		if err := ProbeTOTPKeyMaterial(nil, "", testJWTSecret); err != nil {
			t.Fatalf("empty sample must pass: %v", err)
		}
	})

	t.Run("correct key material passes", func(t *testing.T) {
		if err := ProbeTOTPKeyMaterial([]string{good}, testJWTSecret, "rotated"); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})

	t.Run("wrong key material fails", func(t *testing.T) {
		if err := ProbeTOTPKeyMaterial([]string{good}, "not-the-right-material", testJWTSecret); err == nil {
			t.Fatal("want failure on mismatched key material")
		}
	})

	// One corrupt row must not be read as a key mismatch and take the boot down.
	t.Run("one good row among corrupt rows passes", func(t *testing.T) {
		sample := []string{"", "not-base64-at-all!!", "c2hvcnQ=", good}
		if err := ProbeTOTPKeyMaterial(sample, testJWTSecret, "rotated"); err != nil {
			t.Fatalf("a single successful open must prove the key: %v", err)
		}
	})
}
