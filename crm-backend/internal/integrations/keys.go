package integrations

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// LeadKeyPrefix marks a lead-capture credential. Distinct from the PAT prefix on
// purpose: these are different credential CLASSES and must never meet.
//
// A PAT authenticates AS ITS OWNER — it resolves the same Caller, role and row
// scope a JWT would. A lead key has no user, no role and no membership: it is an
// ORG-level source credential. Forking on it inside authMiddleware would force one
// of two bad outcomes — invent a fake identity to audit against, or admit a
// role-less caller, which is exactly the branch authenticateAPIToken aborts on.
// Separate tables, separate lookup, separate prefix.
const LeadKeyPrefix = "crm_lead_"

// prefixDisplayLen is how much of the key is kept as a recognizable hint.
const prefixDisplayLen = 8

// GenerateLeadKey mints a capture credential, returning the plaintext (shown
// exactly once, at creation) and the values persisted in its place.
//
// 32 bytes from crypto/rand, base64url, unpadded — the api_token minting shape.
// Note it checks rand.Read's error, unlike automation's GenerateToken, which
// ignores it AND halves the requested entropy (length/2 bytes → hex).
func GenerateLeadKey() (plaintext, hash, prefix string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	plaintext = LeadKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashLeadKey(plaintext), leadKeyPrefixOf(plaintext), nil
}

// HashLeadKey is the stored form: SHA-256, hex.
//
// SHA-256 rather than bcrypt deliberately, matching the PAT decision: this is
// probed on every capture request, and the input is 32 bytes of CSPRNG output —
// there is no dictionary to attack, so a slow KDF would buy nothing and cost a
// hash on the hot path. What matters is that the plaintext is never stored, so a
// database leak yields no working credential.
func HashLeadKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// IsLeadKey reports whether a bearer credential is a lead-capture key.
func IsLeadKey(s string) bool { return strings.HasPrefix(s, LeadKeyPrefix) }

// GoogleKeyPrefix marks a google_ads webhook key. Distinct from LeadKeyPrefix on
// purpose: IsLeadKey gates the bearer capture path, and a google_key pasted into a
// Bearer header must fail there rather than half-work. The two credentials also
// authenticate different things — the bearer key IS the request's authority, while
// the google_key only corroborates a source already named by the URL's public
// token.
const GoogleKeyPrefix = "crm_gads_"

// GenerateGoogleKey mints the key an advertiser pastes beside the webhook URL.
// Same shape as GenerateLeadKey: plaintext shown exactly once, SHA-256 stored.
// Google documents no length or charset limit on the key field (their FAQ blesses
// "123"), so the full-entropy form fits.
func GenerateGoogleKey() (plaintext, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	plaintext = GoogleKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, HashLeadKey(plaintext), nil
}

// GeneratePublicToken mints the URL-path identifier of a google_ads source.
//
// 16 bytes, not 32: this is an ADDRESS, not a credential — the google_key is the
// secret. It is random anyway because a guessable path invites drive-by probing,
// and it must be generated (never user-chosen) so it cannot collide meaningfully
// or embed anything an org would mind appearing in Google's config UI.
func GeneratePublicToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// leadKeyPrefixOf builds the display hint: enough to recognize a key in a list,
// useless to anyone who steals the list.
func leadKeyPrefixOf(plaintext string) string {
	body := strings.TrimPrefix(plaintext, LeadKeyPrefix)
	if len(body) > prefixDisplayLen {
		body = body[:prefixDisplayLen]
	}
	return LeadKeyPrefix + body
}
