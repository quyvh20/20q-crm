package usecase

import (
	"strings"
	"unicode"

	"crm-backend/internal/domain"
)

const minPasswordLength = 8

// commonPasswords is a tiny blocklist of the most-guessed passwords. Matched
// case-insensitively against the WHOLE password (not a substring) so a strong
// password that merely contains one of these words still passes. This is a
// pragmatic, dependency-free floor — a HIBP k-anonymity check can layer on later
// (plan P1) without changing callers.
var commonPasswords = map[string]bool{
	"password": true, "password1": true, "password123": true,
	"12345678": true, "123456789": true, "1234567890": true,
	"qwerty123": true, "qwertyuiop": true, "iloveyou": true,
	"11111111": true, "00000000": true, "letmein1": true,
	"admin123": true, "welcome1": true, "changeme": true,
}

// validatePassword enforces a self-contained password policy for any path that
// SETS a password (reset today; register can adopt it too). Returns a 400
// AppError naming the first failed rule, or nil when acceptable.
func validatePassword(pw string) error {
	if len(pw) < minPasswordLength {
		return domain.NewAppError(400, "password must be at least 8 characters")
	}
	if len(pw) > 200 {
		return domain.NewAppError(400, "password must be at most 200 characters")
	}

	var hasLetter, hasNonLetter bool
	for _, r := range pw {
		if unicode.IsLetter(r) {
			hasLetter = true
		} else {
			hasNonLetter = true
		}
	}
	if !hasLetter || !hasNonLetter {
		return domain.NewAppError(400, "password must contain letters and at least one number or symbol")
	}

	if commonPasswords[strings.ToLower(pw)] {
		return domain.NewAppError(400, "this password is too common — please choose a stronger one")
	}

	return nil
}
