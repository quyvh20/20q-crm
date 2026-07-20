package integrations

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Connection statuses — the provider-account lifecycle (L5).
//
// The three that HOLD the exclusive page->workspace claim
// (uix_integration_connections_claim) are exactly the ones that can still
// legitimately receive a delivery. `revoked` and `disconnected` release the
// claim so a page can be moved between workspaces without support intervention;
// a soft delete releases it too. Webhook routing resolves connections filtered
// to the claim-holding set, so a delivery for a released page is quarantined,
// never written into the workspace that used to hold it.
const (
	// ConnStatusConnected is a healthy, actively-receiving connection.
	ConnStatusConnected = "connected"
	// ConnStatusDegraded is receiving but rate-limited or partially impaired
	// (provider throttle headers). Still holds the claim — it is still the right
	// destination for the page's leads.
	ConnStatusDegraded = "degraded"
	// ConnStatusError is a connection whose fetches are failing (token invalid,
	// consecutive failures). Still holds the claim on purpose: the same
	// self-healing badge lead_sources uses — releasing it on the first failure
	// would drop deliveries for a transient blip into nowhere, and the reconnect
	// banner is what a human acts on.
	ConnStatusError = "error"
	// ConnStatusRevoked is a connection the provider (or the user, provider-side)
	// tore down. Releases the claim.
	ConnStatusRevoked = "revoked"
	// ConnStatusDisconnected is an admin's explicit disconnect. Releases the claim.
	ConnStatusDisconnected = "disconnected"
)

// claimHoldingStatuses are the statuses that hold the exclusive page->workspace
// claim, mirroring the partial-unique-index predicate in the migration. Kept in
// sync by hand: the index is the source of truth, and this is what Go queries
// filter by so an app read and the DB constraint agree.
var claimHoldingStatuses = []string{ConnStatusConnected, ConnStatusDegraded, ConnStatusError}

// IsConnectionLive reports whether a status can still receive deliveries — i.e.
// whether it holds the claim.
func IsConnectionLive(status string) bool {
	switch status {
	case ConnStatusConnected, ConnStatusDegraded, ConnStatusError:
		return true
	default:
		return false
	}
}

// IntegrationConnection is one OAuth'd provider account: a Facebook page, a
// TikTok advertiser. It is the credential store the webhook/fetch pipeline
// resolves a delivery against.
//
// Unlike LeadSource's ALTER-added columns, EVERY column here is MAPPED. The
// reason the owner_pool/consent columns had to be unmapped does not apply: those
// were added to a table that predates them, so a failed boot guard would leave a
// column missing from a SELECT that an existing capture path depends on. This
// whole table is created by one guard — it either exists in full or the
// connection routes are simply dormant. There is no half-existing shape to guard
// against, so the columns can be ordinary struct fields.
type IntegrationConnection struct {
	ID                   uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID                uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	Provider             string    `gorm:"type:varchar(32);not null" json:"provider"`
	ExternalAccountID    string    `gorm:"type:varchar(255);not null" json:"external_account_id"`
	ExternalAccountLabel string    `gorm:"type:varchar(255);not null;default:''" json:"external_account_label"`

	// EncryptedCredentials is the envelope-sealed provider token blob (see
	// internal/integrations/envelope). NEVER serialized — a `json:"-"` here is a
	// security control, not a formatting choice: the whole point of the codec is
	// that this value never leaves the server, and the management views below
	// deliberately do not carry it.
	EncryptedCredentials string `gorm:"column:encrypted_credentials;not null" json:"-"`
	// KeyVersion MIRRORS the version inside the blob, for ops queries ("which rows
	// are still on key 1"). Never read back to CHOOSE a key — the version inside
	// the authenticated blob is the authority.
	KeyVersion int `gorm:"not null;default:0" json:"-"`
	// WebhookSecretEncrypted is a per-connection webhook secret, sealed. Nil for
	// providers that verify webhooks with an app-level secret (Facebook uses the
	// app secret from config, so this stays nil for it).
	WebhookSecretEncrypted *string `gorm:"column:webhook_secret_encrypted" json:"-"`

	Status string `gorm:"type:varchar(32);not null;default:connected" json:"status"`
	// Cursor is provider-specific sync bookkeeping (the reconciliation poller's
	// last-seen marker, L5.4). Internal — not serialized.
	Cursor datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"-"`
	Config datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"config,omitempty"`

	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty"`
	LastError           string     `gorm:"type:text" json:"last_error,omitempty"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`

	// CreatedBy is a pointer so the connection can outlive the admin who made it
	// (ON DELETE SET NULL is not declared, but a NULL creator is always valid) —
	// the same lead-pipe-is-org-infrastructure rule LeadSource follows.
	CreatedBy *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-"` // soft delete: releases the claim, keeps ledger references valid
}

// TableName pins the table so a rename of the Go type cannot silently repoint it.
func (IntegrationConnection) TableName() string { return "integration_connections" }

// ConnectionView is the safe projection returned by the management API. It is a
// separate type rather than a json-tagged model so the sealed credential columns
// cannot leak by someone later removing a `json:"-"`: a value that has no token
// field cannot serialize one.
type ConnectionView struct {
	ID                   uuid.UUID  `json:"id"`
	Provider             string     `json:"provider"`
	ExternalAccountID    string     `json:"external_account_id"`
	ExternalAccountLabel string     `json:"external_account_label"`
	Status               string     `json:"status"`
	// Subscribed reports whether provider-side delivery is active (a Facebook page
	// subscribed to leadgen). A connection can be `connected` yet unsubscribed —
	// which looks healthy but receives nothing — so the card surfaces this
	// separately with its own warning.
	Subscribed          bool       `json:"subscribed"`
	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// connectionConfig is the shape stored in IntegrationConnection.Config.
type connectionConfig struct {
	Subscribed bool `json:"subscribed"`
}

// ViewOfConnection projects a connection for the API.
func ViewOfConnection(c *IntegrationConnection) ConnectionView {
	subscribed := false
	if len(c.Config) > 0 {
		var cfg connectionConfig
		_ = json.Unmarshal(c.Config, &cfg) // absent/garbage ⇒ not subscribed, the safe default
		subscribed = cfg.Subscribed
	}
	return ConnectionView{
		ID:                   c.ID,
		Provider:             c.Provider,
		ExternalAccountID:    c.ExternalAccountID,
		ExternalAccountLabel: c.ExternalAccountLabel,
		Status:               c.Status,
		Subscribed:           subscribed,
		LastSyncedAt:         c.LastSyncedAt,
		LastError:            c.LastError,
		ConsecutiveFailures:  c.ConsecutiveFailures,
		CreatedAt:            c.CreatedAt,
		UpdatedAt:            c.UpdatedAt,
	}
}

// IntegrationOAuthState is a server-side, single-use OAuth state token.
//
// The state PARAMETER carried through the provider round-trip is an opaque
// random string; org and user are resolved from THIS row, never decoded out of
// the parameter — the capture-vulnerability class the U4 review killed. The row
// stores only the SHA-256 of the state (StateHash), so a DB leak yields nothing
// replayable.
type IntegrationOAuthState struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	StateHash string    `gorm:"type:varchar(64);not null;uniqueIndex"`
	OrgID     uuid.UUID `gorm:"type:uuid;not null"`
	UserID    uuid.UUID `gorm:"type:uuid;not null"`
	Provider  string    `gorm:"type:varchar(32);not null"`
	ReturnTo  string    `gorm:"type:text;not null;default:''"`
	// CodeVerifier is the PKCE verifier, envelope-sealed under this row's id. Only
	// set for providers that use PKCE. It is a short-lived secret that upgrades a
	// stolen authorization code into a token, so it is sealed like any credential.
	CodeVerifier *string `gorm:"type:text"`
	KeyVersion   int     `gorm:"not null;default:0"`
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	CreatedAt    time.Time
}

// TableName pins the table.
func (IntegrationOAuthState) TableName() string { return "integration_oauth_states" }

// IntegrationPendingConnection is custody for an exchanged token between the
// OAuth callback and the admin choosing which account to connect.
//
// The provider token never rides through the browser: the callback seals it
// here, the frontend receives only the candidate account list plus a single-use
// selection token, and the select-account POST proves the caller is the SAME org
// AND the same user before anything is promoted to a connection.
type IntegrationPendingConnection struct {
	ID       uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()"`
	OrgID    uuid.UUID `gorm:"type:uuid;not null"`
	UserID   uuid.UUID `gorm:"type:uuid;not null"`
	Provider string    `gorm:"type:varchar(32);not null"`
	// EncryptedToken is the sealed candidate-accounts blob (each account with its
	// per-account credentials — e.g. a Facebook page token). Sealed bound to THIS
	// row's id, so it cannot be lifted into a connection row without a re-seal.
	EncryptedToken string `gorm:"column:encrypted_token;not null"`
	KeyVersion     int    `gorm:"not null;default:0"`
	// CandidateAccounts is the NON-SECRET picker list ({id, label, meta}). It never
	// contains a token — that lives only in EncryptedToken.
	CandidateAccounts  datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'"`
	SelectionTokenHash string         `gorm:"type:varchar(64);not null;uniqueIndex"`
	ExpiresAt          time.Time
	ConsumedAt         *time.Time
	CreatedAt          time.Time
}

// TableName pins the table.
func (IntegrationPendingConnection) TableName() string {
	return "integration_pending_connections"
}
