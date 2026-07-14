package domain

import (
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Two-factor authentication (U6.4)
// ============================================================
//
// TOTP (RFC 6238) with single-use backup codes, plus an org policy that can
// require it of every member.
//
// The login flow gains a step. On a correct password (or a successful Google
// sign-in) a 2FA-enrolled user does NOT get a session: they get a short-lived,
// single-use CHALLENGE, and only exchanging it for a valid code mints the tokens.
// The challenge is a hashed random token — deliberately NOT a JWT, because a JWT
// would sail straight through the auth middleware and BE the session it is meant
// to gate.

// TwoFactorChallenge is the half-authenticated state between "password correct"
// and "code correct". Modeled on PasswordResetToken: opaque token, stored hashed,
// short TTL, single use — plus an attempt counter, because a 6-digit code is
// brute-forceable and the per-IP rate limiter fails open when Redis is absent.
type TwoFactorChallenge struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"-"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"-"`
	TokenHash string     `gorm:"size:255;not null;uniqueIndex" json:"-"`
	Attempts  int        `gorm:"not null;default:0" json:"-"`
	ExpiresAt time.Time  `gorm:"not null" json:"-"`
	UsedAt    *time.Time `json:"-"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"-"`
}

func (TwoFactorChallenge) TableName() string { return "two_factor_challenges" }

// TwoFactorBackupCode is one bcrypt-hashed, single-use recovery code. They are
// shown exactly once, at enrollment.
type TwoFactorBackupCode struct {
	ID        uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"-"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null" json:"-"`
	CodeHash  string     `gorm:"size:255;not null" json:"-"`
	UsedAt    *time.Time `json:"-"`
	CreatedAt time.Time  `gorm:"not null;default:now()" json:"-"`
}

func (TwoFactorBackupCode) TableName() string { return "two_factor_backup_codes" }

// MaxTwoFactorAttempts kills a challenge after this many wrong codes. DB-backed
// (not Redis) so the limit holds even on a deployment with no cache.
const MaxTwoFactorAttempts = 5

// TwoFactorChallengeTTL is how long the half-authenticated state survives.
const TwoFactorChallengeTTL = 5 * time.Minute

// TwoFactorSetup is the enrollment payload: the shared secret (for manual entry),
// the otpauth:// URI, and a server-rendered QR PNG as a data URI — rendered on the
// server so the browser needs no QR library.
type TwoFactorSetup struct {
	Secret     string `json:"secret"`
	OtpAuthURL string `json:"otpauth_url"`
	QRDataURI  string `json:"qr_data_uri"`
}

// TwoFactorEnableInput confirms enrollment by proving the authenticator works.
type TwoFactorEnableInput struct {
	Code string `json:"code" binding:"required"`
}

// TwoFactorDisableInput turns 2FA off. A code (TOTP or backup) is required —
// possession of a live session is not enough to drop a second factor.
type TwoFactorDisableInput struct {
	Code string `json:"code" binding:"required"`
}

// TwoFactorVerifyInput exchanges a login challenge for a session. Code is a TOTP
// code or a backup code. ChallengeToken may be empty when the challenge is carried
// by its httpOnly cookie (the Google redirect flow).
type TwoFactorVerifyInput struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code" binding:"required"`
}

// TwoFactorStatus is what the Security settings page renders.
type TwoFactorStatus struct {
	Enabled            bool       `json:"enabled"`
	EnabledAt          *time.Time `json:"enabled_at,omitempty"`
	BackupCodesLeft    int        `json:"backup_codes_left"`
	RequiredByWorkspace bool      `json:"required_by_workspace"`
}

// BackupCodesResult carries freshly generated codes. They are returned in
// plaintext EXACTLY ONCE — only their bcrypt hashes are stored.
type BackupCodesResult struct {
	Codes []string `json:"codes"`
}
