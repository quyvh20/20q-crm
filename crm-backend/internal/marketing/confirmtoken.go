package marketing

import (
	"encoding/json"
	"errors"
	"time"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
)

// confirmPurpose namespaces the AEAD binding so a double-opt-in confirm token
// can never be opened as (or forged from) an unsubscribe token or anything
// else — see envelope.Purpose's doc comment: a distinct purpose string is
// what makes a blob unliftable from one column into another, even though
// both token kinds are minted under the SAME keyring (MARKETING_UNSUB_KEY;
// see buildMarketingUnsubRing in main.go — the name predates this second use,
// purpose-separation alone is what keeps them from colliding).
const confirmPurpose = envelope.Purpose("marketing_double_opt_in")

const confirmTokenVersion = 1

// ConfirmToken is the payload carried, ENCRYPTED, inside a double-opt-in
// confirmation URL. Mirrors UnsubToken's shape/reasoning (see unsubtoken.go):
// Email is the ledger key, ContactID is audit-only.
type ConfirmToken struct {
	V         int        `json:"v"`
	OrgID     uuid.UUID  `json:"o"`
	Email     string     `json:"e"` // already normalized
	ContactID *uuid.UUID `json:"c,omitempty"`
	IAT       int64      `json:"i,omitempty"`
}

// ConfirmTokenService mints/verifies stateless double-opt-in confirm tokens.
// Same fail-closed shape as TokenService: a nil ring yields Configured()==false.
type ConfirmTokenService struct {
	codec *envelope.Codec
	now   func() time.Time
}

func NewConfirmTokenService(ring *envelope.Keyring) *ConfirmTokenService {
	var codec *envelope.Codec
	if ring != nil {
		codec = envelope.NewCodec(ring)
	}
	return &ConfirmTokenService{codec: codec, now: time.Now}
}

func (s *ConfirmTokenService) Configured() bool { return s != nil && s.codec.Configured() }

func (s *ConfirmTokenService) Mint(orgID uuid.UUID, emailNorm string, contactID *uuid.UUID) (string, error) {
	if !s.Configured() {
		return "", ErrTokensNotConfigured
	}
	if orgID == uuid.Nil || emailNorm == "" {
		return "", errors.New("marketing: confirm token needs an org id and an email")
	}
	t := ConfirmToken{V: confirmTokenVersion, OrgID: orgID, Email: emailNorm, ContactID: contactID, IAT: s.now().Unix()}
	payload, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	return s.codec.SealStateless(confirmPurpose, payload)
}

func (s *ConfirmTokenService) Verify(token string) (ConfirmToken, error) {
	if !s.Configured() {
		return ConfirmToken{}, ErrTokensNotConfigured
	}
	payload, err := s.codec.OpenStateless(confirmPurpose, token)
	if err != nil {
		return ConfirmToken{}, err
	}
	var t ConfirmToken
	if err := json.Unmarshal(payload, &t); err != nil {
		return ConfirmToken{}, errors.New("marketing: confirm token payload is malformed")
	}
	if t.OrgID == uuid.Nil || t.Email == "" {
		return ConfirmToken{}, errors.New("marketing: confirm token is missing its identity")
	}
	return t, nil
}
