package marketing

import (
	"encoding/json"
	"errors"
	"time"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
)

// unsubPurpose namespaces the AEAD binding so an unsubscribe token can never be
// opened as (or forged from) any other sealed value.
const unsubPurpose = envelope.Purpose("marketing_unsubscribe")

// unsubTokenVersion is the payload schema version. Bump it only alongside a reader
// that still accepts the older shape; because every non-identity field is optional
// (omitempty), adding a field (e.g. campaign_id at M7) does not require a bump.
const unsubTokenVersion = 1

// ErrTokensNotConfigured reports that MARKETING_UNSUB_KEY is unset, so unsubscribe
// tokens can be neither minted nor verified. It is the fail-closed signal the send
// gate and the public endpoint act on — never a reason to send without a link.
var ErrTokensNotConfigured = errors.New("marketing: unsubscribe token signing is not configured (set MARKETING_UNSUB_KEY)")

// UnsubToken is the payload carried, ENCRYPTED, inside an unsubscribe URL. Nothing
// in it is PII in cleartext: the whole struct is AES-256-GCM sealed, so the URL
// reveals only opaque bytes and only we can recover the identity. The ledger keys
// on the normalized email (never contact_id — contacts get deleted, several share
// one address), so Email is the authority; ContactID is audit-only.
//
// Field tags are terse to keep the URL short. CampaignID/TopicID are absent until a
// campaign (M7) or a topic (M3 opt-down) supplies them; a token minted without them
// still opens after those ship.
type UnsubToken struct {
	V          int        `json:"v"`
	OrgID      uuid.UUID  `json:"o"`
	Email      string     `json:"e"`            // ALREADY normalized (emailutil.Normalize)
	ContactID  *uuid.UUID `json:"c,omitempty"`  // audit only
	CampaignID *uuid.UUID `json:"cm,omitempty"` // absent until M7
	TopicID    *uuid.UUID `json:"t,omitempty"`  // per-topic campaigns (optional)
	IAT        int64      `json:"i,omitempty"`  // issued-at (unix); observability, NOT a TTL
}

// TokenService mints and verifies stateless one-click unsubscribe tokens under the
// marketing keyring. A nil ring (MARKETING_UNSUB_KEY unset) yields a service whose
// Mint/Verify return ErrTokensNotConfigured — a clear, fail-closed degrade rather
// than forgeable links. Verification is authenticated (GCM tag) and constant-time
// by construction; there is deliberately no embedded expiry (CAN-SPAM needs links
// to keep working for 30+ days, effectively indefinitely — a rotated-away key
// version is the only thing that retires a link, and old versions are retained).
type TokenService struct {
	codec *envelope.Codec
	now   func() time.Time
}

// NewTokenService builds the service over an already-parsed keyring. Pass nil when
// MARKETING_UNSUB_KEY is unset; the service then reports Configured()==false.
func NewTokenService(ring *envelope.Keyring) *TokenService {
	var codec *envelope.Codec
	if ring != nil {
		codec = envelope.NewCodec(ring)
	}
	return &TokenService{codec: codec, now: time.Now}
}

// Configured reports whether tokens can be minted/verified.
func (s *TokenService) Configured() bool { return s != nil && s.codec.Configured() }

// MintOpt sets an optional token field at mint time.
type MintOpt func(*UnsubToken)

// WithContactID records the source contact (audit only).
func WithContactID(id uuid.UUID) MintOpt {
	return func(t *UnsubToken) {
		if id != uuid.Nil {
			t.ContactID = &id
		}
	}
}

// WithCampaignID records the driving campaign (M7).
func WithCampaignID(id uuid.UUID) MintOpt {
	return func(t *UnsubToken) {
		if id != uuid.Nil {
			t.CampaignID = &id
		}
	}
}

// WithTopicID scopes the token to a marketing topic (per-topic campaigns).
func WithTopicID(id uuid.UUID) MintOpt {
	return func(t *UnsubToken) {
		if id != uuid.Nil {
			t.TopicID = &id
		}
	}
}

// Mint seals an opaque token for (org, normalized email). emailNorm MUST already be
// normalized by the caller (emailutil.Normalize) so the address recovered on
// verify matches the ledger key exactly.
func (s *TokenService) Mint(orgID uuid.UUID, emailNorm string, opts ...MintOpt) (string, error) {
	if !s.Configured() {
		return "", ErrTokensNotConfigured
	}
	if orgID == uuid.Nil || emailNorm == "" {
		return "", errors.New("marketing: unsubscribe token needs an org id and an email")
	}
	t := UnsubToken{V: unsubTokenVersion, OrgID: orgID, Email: emailNorm, IAT: s.now().Unix()}
	for _, o := range opts {
		o(&t)
	}
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return s.codec.SealStateless(unsubPurpose, payload)
}

// Verify opens a token and returns its payload, or an error (fail-closed) on any
// structural, key, tag, or schema problem. A caller MUST NOT act on a token that
// does not verify.
func (s *TokenService) Verify(token string) (UnsubToken, error) {
	if !s.Configured() {
		return UnsubToken{}, ErrTokensNotConfigured
	}
	payload, err := s.codec.OpenStateless(unsubPurpose, token)
	if err != nil {
		return UnsubToken{}, err
	}
	var t UnsubToken
	if err := json.Unmarshal(payload, &t); err != nil {
		return UnsubToken{}, errors.New("marketing: unsubscribe token payload is malformed")
	}
	if t.OrgID == uuid.Nil || t.Email == "" {
		return UnsubToken{}, errors.New("marketing: unsubscribe token is missing its identity")
	}
	return t, nil
}
