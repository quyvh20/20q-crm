// Package envelope seals third-party provider credentials for storage at rest.
//
// Two rules are enforced by the shape of this API rather than by discipline,
// because both have already failed elsewhere in this codebase when they were
// left to discipline:
//
//  1. There is NO way to hand this package a fallback secret. The only other
//     AES-GCM site in the backend (usecase/two_factor_crypto.go:37) derives its
//     key from TOTP_ENC_KEY and falls back to JWT_SECRET when that is blank —
//     and TOTP_ENC_KEY has no viper.BindEnv, so in production the fallback is
//     not an edge case, it is the only path that ever runs. Rotating JWT_SECRET
//     there silently orphans every stored secret. Nothing here accepts a JWT
//     secret, so that failure cannot be written.
//
//  2. A ciphertext carries its own key version and is welded to the row it
//     belongs to. Keeping the version only in a neighbouring column means an
//     edited or copied row decrypts under the WRONG key instead of failing;
//     here the version lives inside the authenticated blob (the column is a
//     mirror for ops queries, never the authority) and the org/purpose/id
//     triple is the GCM additional data, so a ciphertext relocated to another
//     row, another org, or another purpose fails its tag instead of opening.
package envelope

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// KeySize is the AES-256 key length for both the KEK and every per-record DEK.
const KeySize = 32

// Keyring holds the key-encryption keys parsed from INTEGRATION_ENC_KEY.
//
// Wire format is a comma-separated list of `version:base64key` entries, e.g.
//
//	INTEGRATION_ENC_KEY=2:bmV3a2V5…,1:b2xka2V5…
//
// New seals always use the HIGHEST version present; older versions stay so
// their existing ciphertexts keep opening. A bare key with no `version:` prefix
// is read as version 1, which is the whole configuration for a deployment that
// has never rotated.
//
// Primary is derived from the version numbers rather than from list order on
// purpose: ordering is invisible in a Railway environment-variable box, so a
// paste that reorders entries must not silently change which key new
// credentials are sealed under.
type Keyring struct {
	keys    map[int][]byte
	primary int
}

// ErrNoKeys reports an empty or absent INTEGRATION_ENC_KEY. Callers distinguish
// it from a malformed one: absent is legitimate in local development, malformed
// never is.
var ErrNoKeys = errors.New("integration encryption key is not set")

// ParseKeyring reads INTEGRATION_ENC_KEY.
//
// Every error is deliberately material-free — a key that fails to parse must not
// put fragments of itself into a log line that ships to Sentry.
func ParseKeyring(raw string) (*Keyring, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrNoKeys
	}

	kr := &Keyring{keys: make(map[int][]byte)}
	for i, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		version := 1
		encoded := entry
		// SplitN, not Split: base64 std encoding never produces ':', but being
		// explicit here keeps a future encoding change from silently reshaping
		// which bytes are treated as the key.
		if parts := strings.SplitN(entry, ":", 2); len(parts) == 2 {
			v, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				return nil, fmt.Errorf("integration encryption key: entry %d has a non-numeric version", i+1)
			}
			if v < 1 {
				return nil, fmt.Errorf("integration encryption key: entry %d has version %d, which must be >= 1", i+1, v)
			}
			version = v
			encoded = strings.TrimSpace(parts[1])
		}

		key, err := decodeKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("integration encryption key: entry %d (version %d) %w", i+1, version, err)
		}
		if _, dup := kr.keys[version]; dup {
			return nil, fmt.Errorf("integration encryption key: version %d appears twice", version)
		}
		kr.keys[version] = key
		if version > kr.primary {
			kr.primary = version
		}
	}

	if len(kr.keys) == 0 {
		return nil, ErrNoKeys
	}
	return kr, nil
}

// decodeKey accepts both base64 alphabets, with or without padding. A generated
// 32-byte key is pasted through shells, Railway's UI and .env files, and which
// alphabet produced it is not something an operator should have to know.
func decodeKey(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("is empty")
	}
	var (
		key []byte
		err error
	)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if key, err = enc.DecodeString(encoded); err == nil {
			break
		}
	}
	if err != nil {
		return nil, errors.New("is not valid base64")
	}
	if len(key) != KeySize {
		// The length is safe to print; it is a property of the mistake, not of
		// the secret.
		return nil, fmt.Errorf("decodes to %d bytes, but AES-256 needs exactly %d", len(key), KeySize)
	}
	return key, nil
}

// Primary is the version new ciphertexts are sealed under.
func (k *Keyring) Primary() int { return k.primary }

// Versions lists every configured version, ascending. The startup canary walks
// it to prove each key still opens the rows that claim it.
func (k *Keyring) Versions() []int {
	out := make([]int, 0, len(k.keys))
	for v := range k.keys {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func (k *Keyring) key(version int) ([]byte, error) {
	key, ok := k.keys[version]
	if !ok {
		// Naming the versions we DO hold is what makes a partial keyring
		// diagnosable: "sealed under 2, we have [1]" says "you dropped a key
		// during rotation", which the generic failure does not.
		return nil, fmt.Errorf("no key configured for version %d (configured: %v)", version, k.Versions())
	}
	return key, nil
}
