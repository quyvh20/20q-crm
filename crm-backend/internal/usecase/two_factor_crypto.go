package usecase

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

// two_factor_crypto.go holds the crypto around TOTP: the secret is encrypted at
// rest, and the backup codes are generated here.
//
// Why encrypt the secret at all, when it lives in the same database as the
// password hashes? Because it is not a hash — it is the SEED. A leaked
// password_hash still costs an attacker a crack; a leaked totp_secret hands them
// a working second factor forever, silently, with no way for the user to notice.
// Encrypting it means a stolen DB dump alone is not enough: the key lives in the
// process environment, not the table.
//
// The key is derived from TOTP_ENC_KEY when set, and otherwise from the JWT
// secret (which is already mandatory and already a break-everything secret). That
// keeps a self-hosted deployment from having to manage a second secret to turn 2FA
// on, while still letting an operator rotate the two independently.

const totpKeyInfo = "20q-crm/totp-secret-encryption/v1"

// totpKey derives the 32-byte AES key. Deriving (rather than using the raw env
// value) means the key is always the right length regardless of what the operator
// set, and the JWT secret is never used verbatim as an encryption key.
func totpKey(encKey, jwtSecret string) ([]byte, error) {
	material := encKey
	if strings.TrimSpace(material) == "" {
		material = jwtSecret
	}
	if strings.TrimSpace(material) == "" {
		return nil, errors.New("two-factor: no encryption key available (set TOTP_ENC_KEY or JWT_SECRET)")
	}
	sum := sha256.Sum256([]byte(totpKeyInfo + ":" + material))
	return sum[:], nil
}

// encryptTOTPSecret seals the shared secret with AES-256-GCM. The nonce is
// prepended to the ciphertext, and the whole thing is base64'd for a text column.
func encryptTOTPSecret(secret, encKey, jwtSecret string) (string, error) {
	key, err := totpKey(encKey, jwtSecret)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(secret), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptTOTPSecret opens a secret sealed by encryptTOTPSecret.
func decryptTOTPSecret(stored, encKey, jwtSecret string) (string, error) {
	key, err := totpKey(encKey, jwtSecret)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return "", fmt.Errorf("two-factor: stored secret is not valid base64: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("two-factor: stored secret is truncated")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		// Wrong key (rotated JWT secret without setting TOTP_ENC_KEY) or a tampered
		// row. Either way the secret is unusable — fail closed and let the user
		// recover with a backup code, then re-enroll.
		return "", errors.New("two-factor: could not decrypt the stored secret")
	}
	return string(plain), nil
}

// backupCodeAlphabet excludes the characters people misread when copying a code
// off a screen under stress (0/O, 1/I/l). A recovery code that fails because it
// was transcribed wrong is indistinguishable, to the user, from being locked out.
const backupCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const (
	backupCodeCount  = 10
	backupCodeLength = 10 // rendered as XXXXX-XXXXX
)

// generateBackupCodes returns fresh single-use recovery codes in plaintext. The
// caller hashes them for storage and shows these to the user exactly once.
func generateBackupCodes() ([]string, error) {
	codes := make([]string, 0, backupCodeCount)
	for i := 0; i < backupCodeCount; i++ {
		buf := make([]byte, backupCodeLength)
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			return nil, err
		}
		var b strings.Builder
		for j, v := range buf {
			if j == backupCodeLength/2 {
				b.WriteByte('-')
			}
			// Modulo bias is negligible here (256 % 31), and these are single-use
			// codes with an attempt limit, not long-lived key material.
			b.WriteByte(backupCodeAlphabet[int(v)%len(backupCodeAlphabet)])
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// normalizeBackupCode makes verification forgiving of how the code was typed
// (case, stray spaces, a missing dash) without weakening it.
func normalizeBackupCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, " ", "")
	code = strings.ReplaceAll(code, "-", "")
	return code
}
