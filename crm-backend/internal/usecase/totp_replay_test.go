package usecase

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// A TOTP code is valid for its whole 30-second step plus one step either side of
// skew — roughly a 90-second window in which the same six digits authenticate.
// Anyone who observes a code inside that window can replay it. The replay guard
// records which STEP was spent, which is why matchTOTPStep has to report the step
// rather than a bare yes/no.

func codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period: totpPeriod, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	return code
}

func testSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "20q", AccountName: "t@example.com"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return key.Secret()
}

func TestMatchTOTPStep_ReportsTheCurrentStep(t *testing.T) {
	secret := testSecret(t)
	now := time.Now()

	step, ok := matchTOTPStep(codeAt(t, secret, now), secret)
	if !ok {
		t.Fatal("a freshly generated code must match")
	}
	if want := now.Unix() / totpPeriod; step != want {
		t.Fatalf("step = %d, want %d", step, want)
	}
}

// Skew must keep working — a phone one step out of sync is the ordinary case, and
// rejecting it would lock users out to fix a replay.
func TestMatchTOTPStep_AcceptsOneStepOfSkew(t *testing.T) {
	secret := testSecret(t)
	now := time.Now()
	current := now.Unix() / totpPeriod

	for _, delta := range []int64{-1, +1} {
		at := time.Unix((current+delta)*totpPeriod, 0)
		step, ok := matchTOTPStep(codeAt(t, secret, at), secret)
		if !ok {
			t.Fatalf("a code %d step away must still be accepted", delta)
		}
		// And it must be attributed to ITS OWN step, not the current one — otherwise
		// the guard would record the wrong step and let the real one be replayed.
		if step != current+delta {
			t.Fatalf("code from step %d attributed to step %d", current+delta, step)
		}
	}
}

// Outside the window, nothing matches.
func TestMatchTOTPStep_RejectsCodesOutsideTheWindow(t *testing.T) {
	secret := testSecret(t)
	now := time.Now()

	for _, delta := range []int64{-5, -2, +2, +5} {
		at := time.Unix((now.Unix()/totpPeriod+delta)*totpPeriod, 0)
		if step, ok := matchTOTPStep(codeAt(t, secret, at), secret); ok {
			t.Fatalf("a code %d steps away was accepted as step %d", delta, step)
		}
	}
}

func TestMatchTOTPStep_RejectsGarbage(t *testing.T) {
	secret := testSecret(t)
	for _, code := range []string{"", "000000", "abcdef", "12345", "1234567"} {
		if _, ok := matchTOTPStep(code, secret); ok {
			t.Fatalf("%q was accepted", code)
		}
	}
}

// matchTOTPStep must agree with validateTOTP, which is what the rest of the code
// has always used. If they diverge, the guard either rejects codes the system
// considers valid or records a step for one it does not.
func TestMatchTOTPStep_AgreesWithValidateTOTP(t *testing.T) {
	secret := testSecret(t)
	now := time.Now()
	current := now.Unix() / totpPeriod

	for _, delta := range []int64{-2, -1, 0, +1, +2} {
		at := time.Unix((current+delta)*totpPeriod, 0)
		code := codeAt(t, secret, at)

		_, matched := matchTOTPStep(code, secret)
		validated := validateTOTP(code, secret)
		if matched != validated {
			t.Fatalf("delta %d: matchTOTPStep=%v but validateTOTP=%v", delta, matched, validated)
		}
	}
}

// A replayed code resolves to the SAME step, which is the property the
// compare-and-swap relies on. This asserts only that determinism; whether the CAS
// then refuses it is tested against a real database in
// repository/two_factor_replay_test.go, and the usecase's use of it in
// TestConsumeSecondFactor_* below.
func TestReplayResolvesToTheSameStep(t *testing.T) {
	secret := testSecret(t)
	code := codeAt(t, secret, time.Now())

	first, ok := matchTOTPStep(code, secret)
	if !ok {
		t.Fatal("first presentation must match")
	}
	second, ok := matchTOTPStep(code, secret)
	if !ok {
		t.Fatal("second presentation still matches arithmetically — the CAS is what stops it")
	}
	if first != second {
		t.Fatalf("the same code resolved to two steps (%d, %d); the guard would let it through", first, second)
	}
}
