package domain

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// StringList is a []string stored as a JSONB column. GORM can't round-trip a bare
// []string into jsonb, and the alternative — a comma-joined text column — makes a
// scope containing a comma a silent injection into the permission set.
type StringList []string

func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(l))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (l *StringList) Scan(value interface{}) error {
	if value == nil {
		*l = StringList{}
		return nil
	}
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("cannot scan type %T into StringList", value)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*l = out
	return nil
}

// Has reports whether the list contains s.
func (l StringList) Has(s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

// ============================================================
// Personal API tokens (U6.5)
// ============================================================
//
// A personal access token authenticates as its OWNER. It is not a service account
// and grants no new authority: the request resolves to exactly the same Caller a
// JWT would (role, capabilities, OLS/FLS, row scope, audit actor), then the token's
// own scopes INTERSECT that. So a token can only ever do a subset of what its owner
// can do — never more, even if its owner is the workspace owner.
//
// That last part is the trap worth stating plainly: the owner role bypasses every
// capability check. If the scope intersection were applied after the owner bypass,
// a leaked owner token would be god-mode. It is applied BEFORE.

// APITokenPrefix identifies a personal access token on the wire. The middleware
// forks on it before attempting to parse a JWT.
const APITokenPrefix = "crm_pat_"

// ScopeRecordsRead is a TOKEN-ONLY scope with no role capability behind it.
//
// Reading a record is gated by Object-Level Security, not by a capability — there
// is no `records.read` in the role catalog to intersect with. Without a scope of
// its own, a token scoped narrowly to (say) analytics would still be able to read
// every contact its owner can see, because nothing on that route consults the
// token's scopes at all. So record access is opt-in for tokens: reads need
// ScopeRecordsRead, writes need CapRecordsWrite. Neither grants anything the OWNER
// couldn't already do — OLS, FLS and row scope still apply underneath.
const ScopeRecordsRead = "records.read"

// IsAPITokenScope reports whether s is a scope a token may carry: any role
// capability, plus the token-only record-read scope.
func IsAPITokenScope(s string) bool {
	return s == ScopeRecordsRead || IsCapability(s)
}

// APITokenScopes is the full pickable list for the token-creation UI.
func APITokenScopes() []string {
	out := make([]string, 0, len(AllCapabilities)+1)
	out = append(out, ScopeRecordsRead)
	return append(out, AllCapabilities...)
}

// MaxAPITokensPerUser bounds how many live tokens one person can hold.
const MaxAPITokensPerUser = 20

// DefaultAPITokenDays is the default expiry for a new token. Tokens expire by
// default because the ones that leak are the ones nobody remembers creating.
const DefaultAPITokenDays = 90

type APIToken struct {
	ID     uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID  uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	UserID uuid.UUID `gorm:"type:uuid;not null" json:"user_id"`
	Name   string    `gorm:"size:120;not null" json:"name"`
	// TokenHash is SHA-256 of the secret, not bcrypt: this is looked up on EVERY
	// request, so it must be an indexed equality probe. The secret is 32 random
	// bytes, so it has nothing to brute-force — bcrypt's work factor buys nothing
	// here and would cost a KDF per API call.
	TokenHash string `gorm:"size:64;not null;uniqueIndex" json:"-"`
	// Prefix is the display hint ("crm_pat_a1b2…") — enough to recognize a token in
	// a list, useless as a credential.
	Prefix string `gorm:"size:24;not null" json:"prefix"`
	// Scopes are capability codes from the SAME catalog roles use (AllCapabilities).
	// A parallel scope vocabulary would drift out of step with the permission model
	// the moment either side gained an entry.
	Scopes     StringList `gorm:"type:jsonb;not null;default:'[]'" json:"scopes"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (APIToken) TableName() string { return "api_tokens" }

// IsLive reports whether the token may still authenticate.
func (t *APIToken) IsLive(now time.Time) bool {
	if t == nil || t.RevokedAt != nil {
		return false
	}
	if t.ExpiresAt != nil && now.After(*t.ExpiresAt) {
		return false
	}
	return true
}

// CreateAPITokenInput mints a token. Scopes must be non-empty: a token that grants
// nothing is a footgun, and one that implicitly grants everything is worse.
type CreateAPITokenInput struct {
	Name          string   `json:"name" binding:"required,min=1,max=120"`
	Scopes        []string `json:"scopes" binding:"required"`
	ExpiresInDays *int     `json:"expires_in_days"`
}

// CreatedAPIToken carries the plaintext secret — returned EXACTLY ONCE, at
// creation. Nothing can recover it afterwards; only its hash is stored.
type CreatedAPIToken struct {
	Token APIToken `json:"token"`
	// Secret is the full credential (crm_pat_…). Shown once, never again.
	Secret string `json:"secret"`
}

// ============================================================
// Ports
// ============================================================

type APITokenRepository interface {
	Create(ctx context.Context, t *APIToken) error
	ListByUser(ctx context.Context, orgID, userID uuid.UUID) ([]APIToken, error)
	CountLiveByUser(ctx context.Context, orgID, userID uuid.UUID) (int64, error)
	// GetByHash is the authentication probe, run on every PAT request.
	GetByHash(ctx context.Context, tokenHash string) (*APIToken, error)
	Revoke(ctx context.Context, orgID, userID, id uuid.UUID) (int64, error)
	// RevokeAllForUser kills every token a user holds in an org — offboarding, and
	// password reset (a compromised account's tokens must die with its password).
	RevokeAllForUser(ctx context.Context, orgID, userID uuid.UUID) (int64, error)
	// TouchLastUsed records activity, throttled so a busy token doesn't write on
	// every request.
	TouchLastUsed(ctx context.Context, id uuid.UUID) error
}

type APITokenUseCase interface {
	List(ctx context.Context, orgID, userID uuid.UUID) ([]APIToken, error)
	Create(ctx context.Context, orgID, userID uuid.UUID, in CreateAPITokenInput) (*CreatedAPIToken, error)
	Revoke(ctx context.Context, orgID, userID, id uuid.UUID) error
}
