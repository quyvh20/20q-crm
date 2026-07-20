package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// blobPrefix tags the serialization so a future format change is detectable
// rather than being read as corruption. Bump it only alongside a reader that
// still opens v1.
const blobPrefix = "ienv1"

// Purpose names what a ciphertext is FOR, and is bound into the authenticated
// data so a blob cannot be lifted from one column into another. Adding a value
// here is adding a namespace; reusing one across two columns defeats the point.
type Purpose string

const (
	// PurposeConnectionCredentials is the provider access-token blob on
	// integration_connections.
	PurposeConnectionCredentials Purpose = "connection_credentials"
	// PurposeConnectionWebhookSecret is the per-connection webhook secret.
	PurposeConnectionWebhookSecret Purpose = "connection_webhook_secret"
	// PurposePendingToken is the exchanged-but-not-yet-claimed provider token
	// held in integration_pending_connections between the OAuth callback and
	// the admin's account selection.
	PurposePendingToken Purpose = "pending_token"
	// PurposeOAuthCodeVerifier is the PKCE code verifier held on an
	// integration_oauth_states row between the connect redirect and the callback.
	PurposeOAuthCodeVerifier Purpose = "oauth_code_verifier"
)

// Binding is the context a ciphertext is cryptographically welded to. All three
// fields go into the GCM additional data, so a blob copied to another org,
// another row, or another column fails its authentication tag instead of
// quietly opening in the wrong place.
//
// ID is the owning row's primary key. It is required: a zero ID would make
// every row in an org share one binding, which is the same as not binding at
// all. Seal a row's secret only once you know the id you will store it under.
type Binding struct {
	OrgID   uuid.UUID
	Purpose Purpose
	ID      uuid.UUID
}

func (b Binding) validate() error {
	if b.OrgID == uuid.Nil {
		return errors.New("envelope: binding needs an org id")
	}
	if b.Purpose == "" {
		return errors.New("envelope: binding needs a purpose")
	}
	if b.ID == uuid.Nil {
		return errors.New("envelope: binding needs the owning row's id")
	}
	return nil
}

// aad renders the binding plus the key version as additional authenticated
// data. The version is included so that re-labelling a blob's version — in the
// mirror column or in the blob itself — breaks the tag rather than selecting a
// different key and producing garbage.
func (b Binding) aad(version int) []byte {
	return []byte(strings.Join([]string{
		blobPrefix,
		strconv.Itoa(version),
		b.OrgID.String(),
		string(b.Purpose),
		b.ID.String(),
	}, "|"))
}

// Codec seals and opens credentials under a keyring. A nil *Codec is a valid
// "provider connections are not configured" value: every method returns
// ErrNotConfigured, so a development boot without INTEGRATION_ENC_KEY degrades
// to a clear error at the connect route instead of a nil-pointer panic.
type Codec struct {
	ring *Keyring
}

// ErrNotConfigured reports a call against a nil Codec.
var ErrNotConfigured = errors.New("envelope: provider credential encryption is not configured (set INTEGRATION_ENC_KEY)")

// NewCodec builds a codec over an already-parsed keyring.
func NewCodec(ring *Keyring) *Codec { return &Codec{ring: ring} }

// Configured reports whether sealing is possible. Route handlers check this to
// answer 503 with an actionable message rather than failing mid-write.
func (c *Codec) Configured() bool { return c != nil && c.ring != nil }

// PrimaryVersion is the key version new seals use — the value callers mirror
// into the row's key_version column for ops queries. The blob remains the
// authority; the column is never read back to choose a key.
func (c *Codec) PrimaryVersion() int {
	if !c.Configured() {
		return 0
	}
	return c.ring.Primary()
}

// Seal encrypts plaintext under a freshly generated per-record DEK, wraps that
// DEK under the primary KEK, and returns a self-describing blob.
//
// The two-layer shape is what makes key rotation possible without touching
// plaintext: a rotation re-wraps DEKs, and never needs to decrypt a credential
// into memory to do it.
func (c *Codec) Seal(b Binding, plaintext []byte) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	if err := b.validate(); err != nil {
		return "", err
	}

	version := c.ring.Primary()
	kek, err := c.ring.key(version)
	if err != nil {
		return "", err
	}

	// io.ReadFull with a checked error, deliberately: automation's
	// GenerateToken (automation/handlers.go:1709) ignores rand.Read's error, and
	// a silent short read here would seal a credential under partially-zero key
	// material.
	dek := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return "", fmt.Errorf("envelope: could not generate a data key: %w", err)
	}

	aad := b.aad(version)

	wrapped, err := sealWith(kek, dek, aad)
	if err != nil {
		return "", fmt.Errorf("envelope: could not wrap the data key: %w", err)
	}
	sealed, err := sealWith(dek, plaintext, aad)
	if err != nil {
		return "", fmt.Errorf("envelope: could not seal the credential: %w", err)
	}

	return strings.Join([]string{
		blobPrefix,
		strconv.Itoa(version),
		base64.RawURLEncoding.EncodeToString(wrapped),
		base64.RawURLEncoding.EncodeToString(sealed),
	}, "."), nil
}

// SealString is the common case: a token or JSON blob held as a string.
func (c *Codec) SealString(b Binding, plaintext string) (string, error) {
	return c.Seal(b, []byte(plaintext))
}

// Open reverses Seal. Its error shapes are deliberately asymmetric: structural
// problems (not our format, unknown version, bad base64) say exactly what is
// wrong, because they are operator mistakes that need diagnosing. An
// authentication failure says only that the credential could not be opened,
// because wrong-key and tampered-row are not distinguishable and guessing
// between them in a log line invites the wrong remedy.
func (c *Codec) Open(b Binding, blob string) ([]byte, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if err := b.validate(); err != nil {
		return nil, err
	}

	parts := strings.Split(blob, ".")
	if len(parts) != 4 || parts[0] != blobPrefix {
		return nil, errors.New("envelope: stored credential is not in the expected format")
	}
	version, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, errors.New("envelope: stored credential has a non-numeric key version")
	}
	kek, err := c.ring.key(version)
	if err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	wrapped, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("envelope: stored credential has a malformed key envelope")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return nil, errors.New("envelope: stored credential has a malformed payload")
	}

	aad := b.aad(version)

	dek, err := openWith(kek, wrapped, aad)
	if err != nil {
		return nil, errors.New("envelope: could not open the stored credential")
	}
	plaintext, err := openWith(dek, sealed, aad)
	if err != nil {
		return nil, errors.New("envelope: could not open the stored credential")
	}
	return plaintext, nil
}

// OpenString is Open for callers holding a string secret.
func (c *Codec) OpenString(b Binding, blob string) (string, error) {
	out, err := c.Open(b, blob)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Rewrap moves a blob onto the primary key without exposing plaintext to the
// caller. Binding is unchanged, so a rewrap cannot be used to relocate a
// credential. Returns the new blob and the version it now claims.
func (c *Codec) Rewrap(b Binding, blob string) (string, int, error) {
	plaintext, err := c.Open(b, blob)
	if err != nil {
		return "", 0, err
	}
	out, err := c.Seal(b, plaintext)
	if err != nil {
		return "", 0, err
	}
	return out, c.ring.Primary(), nil
}

// VersionOf reports the key version a blob claims, without opening it — the
// query a rotation sweep runs to find rows still on an old key. It reads the
// blob, never the mirror column.
func VersionOf(blob string) (int, error) {
	parts := strings.Split(blob, ".")
	if len(parts) != 4 || parts[0] != blobPrefix {
		return 0, errors.New("envelope: stored credential is not in the expected format")
	}
	return strconv.Atoi(parts[1])
}

// sealWith is AES-256-GCM with the nonce prepended, matching the layout the
// rest of the codebase already uses — the difference here is that the AAD is
// never nil.
func sealWith(key, plaintext, aad []byte) ([]byte, error) {
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, aad), nil
}

func openWith(key, blob, aad []byte) ([]byte, error) {
	gcm, err := gcmFor(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("ciphertext is truncated")
	}
	nonce, body := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, aad)
}

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
