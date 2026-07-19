// Package integrations owns inbound lead capture: the per-source credentials a
// third party authenticates with, the ledger of every delivery, and the pipeline
// that turns a raw payload into a CRM record.
//
// Layering mirrors the automation package: this package declares the narrow ports
// it needs and receives them from cmd/server/main.go. Nothing imports integrations
// except main.
package integrations

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Source kinds. Validated in the app, NOT by a CHECK constraint: Postgres has no
// ADD CONSTRAINT IF NOT EXISTS, so a CHECK can only be declared inline in CREATE
// TABLE — where it silently does not apply to an already-existing table, and
// where adding a kind later (L3 google_ads, L5 facebook_form) would need a
// DROP+ADD guard pair on every boot. App-level validation keeps the option open.
const (
	KindAPI = "api" // generic capture API (Make/Zapier/custom) — the only L1 kind
)

// validKinds is the kind allowlist. Later phases append their own.
var validKinds = map[string]bool{KindAPI: true}

// IsValidKind reports whether a source kind is known.
func IsValidKind(k string) bool { return validKinds[k] }

// Update policies — what an inbound lead may do to a contact that already exists.
const (
	// UpdatePolicyFillBlankOnly writes a mapped field only where the existing
	// record's value is blank. The default, because the alternative silently
	// destroys human work: a rep fixes a name's casing, the lead resubmits the
	// form, and the fix is gone. Nobody sees it happen.
	UpdatePolicyFillBlankOnly = "fill_blank_only"
	// UpdatePolicyOverwrite lets the newest submission win every mapped field.
	UpdatePolicyOverwrite = "overwrite"
	// UpdatePolicyCreateOnly never touches an existing record — a re-submission is
	// recorded in the ledger and otherwise ignored.
	UpdatePolicyCreateOnly = "create_only"
)

var validUpdatePolicies = map[string]bool{
	UpdatePolicyFillBlankOnly: true,
	UpdatePolicyOverwrite:     true,
	UpdatePolicyCreateOnly:    true,
}

// supportedTargets are the objects a lead source may write to.
//
// Restricted to system objects backed by an adapter. The custom-object write path
// stamps CreatedBy = &userID and OwnerUserID = &userID, so the callerless ingest
// actor (uuid.Nil) would write the all-zero UUID into a column with a FK to
// users(id) — an insert that cannot succeed. Widening this needs that path to
// accept a NULL creator first.
var supportedTargets = map[string]bool{"contact": true}

// IsSupportedTarget reports whether leads may be written to this object.
func IsSupportedTarget(slug string) bool { return supportedTargets[slug] }

// IsValidUpdatePolicy reports whether an update policy is known.
func IsValidUpdatePolicy(p string) bool { return validUpdatePolicies[p] }

// Source lifecycle.
const (
	SourceStatusActive   = "active"
	SourceStatusDisabled = "disabled" // switched off by an admin
	SourceStatusError    = "error"    // tripped by consecutive failures (L6 alerts on this)
)

// Event statuses — the ledger's state machine.
//
// Sync channels (the L1 capture API) insert directly as EventStatusProcessing and
// are NEVER claimable. Only provider-webhook channels (L5) insert EventStatusPending
// for the async worker to claim. Keeping the two apart is what stops a worker from
// re-processing an in-flight synchronous request and creating a duplicate contact.
const (
	EventStatusPending     = "pending"     // async only: awaiting a worker claim
	EventStatusProcessing  = "processing"  // claimed / in-flight
	EventStatusProcessed   = "processed"   // a record was written
	EventStatusDuplicate   = "duplicate"   // provider redelivery; prior result returned
	EventStatusFailed      = "failed"      // gave up
	EventStatusQuarantined = "quarantined" // rejected before any write (e.g. spam, bad payload)
	EventStatusTest        = "test"        // traversed the pipeline as a test lead
)

// Ingest outcomes recorded on a processed event.
const (
	OutcomeCreated = "created"
	OutcomeUpdated = "updated"
)

// WriteSourcePrefix namespaces a source's kind in the automation trigger payload
// (domain.WithWriteSource), e.g. "integration:api". Workflows condition on it to
// tell a captured lead from a UI write.
const WriteSourcePrefix = "integration:"

// LeadSource is one inbound channel instance: a credential, where its leads land,
// and how they are matched and merged.
type LeadSource struct {
	ID    uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	Kind  string    `gorm:"type:varchar(32);not null" json:"kind"`
	Name  string    `gorm:"type:varchar(160);not null" json:"name"`

	// TokenHash is SHA-256 of the plaintext key (never the key itself), probed on
	// every capture request — the same trade the PATs make: a fast hash because it
	// is per-request, and storing only the digest so a DB leak yields no working
	// credential. TokenPrefix is a display hint ("crm_lead_a1b2…"), recognizable
	// but useless.
	TokenHash   string `gorm:"type:varchar(64);uniqueIndex" json:"-"`
	TokenPrefix string `gorm:"type:varchar(24)" json:"token_prefix"`

	// TargetSlug is the object leads become. Restricted to system objects backed by
	// an adapter (contact today): the custom-object write path stamps
	// CreatedBy = &userID, so the callerless ingest actor (uuid.Nil) would write
	// the all-zero UUID into a column with a FK to users(id) and crash.
	TargetSlug string `gorm:"type:varchar(64);not null;default:contact" json:"target_slug"`

	// MatchFields is the dedupe key order (Zoho's model). L1 supports "email" only.
	// Always set explicitly on create: GORM sends the column on INSERT, so a nil
	// here would persist [] and silently defeat the column DEFAULT (the U5
	// digest_only trap).
	MatchFields datatypes.JSON `gorm:"type:jsonb;not null;default:'[\"email\"]'" json:"match_fields"`

	// FieldMap is reserved for L2's mapping engine. An empty map means — and will
	// keep meaning — identity over the schema-valid keys, so L1-era sources behave
	// identically the day L2 ships.
	FieldMap datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"field_map"`

	UpdatePolicy string `gorm:"type:varchar(24);not null;default:fill_blank_only" json:"update_policy"`

	// DefaultOwnerID is stamped on CREATE only. Never on update: the contact
	// adapter treats a present-but-null owner_user_id as UNASSIGN, so emitting it
	// on every re-submission would strip the rep off the record — silently, with a
	// 200. An unowned contact is invisible to own-scoped roles, which is the exact
	// legacy-webhook bug this platform exists to fix.
	DefaultOwnerID *uuid.UUID `gorm:"type:uuid" json:"default_owner_id,omitempty"`

	// owner_pool and owner_cursor are DELIBERATELY ABSENT from this struct. Do not
	// add them "for the UI" — the management API returns the pool through a separate
	// view type, and both columns are read and written only by targeted SQL in
	// repository.go. Two independent traps make that a rule rather than a preference:
	//
	//  1. UpdateSource is db.Save(s), which writes every mapped column from a struct
	//     read at the start of the request. A mapped owner_cursor means an admin
	//     renaming a source writes back the cursor as it stood at page load —
	//     rewinding the rotation by however many leads landed in between, silently,
	//     with no failing test.
	//  2. The boot-guard loop LOGS a failed ALTER and boots anyway. A mapped
	//     owner_pool whose guard failed would be named in FindSourceByTokenHash's
	//     SELECT, and every capture request in every org would 500 on a missing
	//     column — a routing column taking down lead capture platform-wide. Unmapped,
	//     the same failure breaks one advisory statement and routing degrades to
	//     default_owner_id.

	Config datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"config"`

	Status              string     `gorm:"type:varchar(16);not null;default:active" json:"status"`
	ConsecutiveFailures int        `gorm:"not null;default:0" json:"consecutive_failures"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`

	// DailyCap bounds how many records one source may create per day, enforced in
	// the DB rather than the rate limiter. It is the backstop that survives both
	// limiters being wrong: every capture can send billable email and enrol
	// workflows, so an unbounded window is a cost bomb, not just noise. 0 = unset.
	DailyCap int `gorm:"not null;default:0" json:"daily_cap"`

	// CreatedBy is a POINTER so it can be NULL. A plain uuid.UUID makes GORM send
	// the zero UUID on every insert, which violates the users(id) FK — the column
	// is nullable precisely so a source can outlive the admin who made it (ON
	// DELETE SET NULL). A lead pipe is org infrastructure; it must not die with a
	// person, which is the credential-dies-with-membership failure this platform
	// exists to avoid.
	CreatedBy *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"` // soft delete: the ledger outlives the source

	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// TableName pins the table (GORM would pluralize to "lead_sources" anyway; pinned
// so a rename of the Go type can't silently repoint at a new table).
func (LeadSource) TableName() string { return "lead_sources" }

// IsLive reports whether a source may accept traffic.
func (s *LeadSource) IsLive() bool {
	return s.Status == SourceStatusActive && s.DeletedAt.Time.IsZero()
}

// WriteSource is the trigger.source value leads from this source carry.
func (s *LeadSource) WriteSource() string { return WriteSourcePrefix + s.Kind }

// IntegrationEvent is one inbound delivery: what arrived, what happened to it, and
// what it became. It is both the customer-facing ledger ("what happened to the
// lead John submitted Tuesday") and the async work queue for provider webhooks.
//
// RawPayload is stored verbatim on purpose: mapping drifts, providers rename form
// questions, and a lead whose field we failed to map must still be recoverable.
// The ledger is also the source-attribution record — RecordService's audit has no
// metadata hook, so its row lands as "System" and this table says which source.
type IntegrationEvent struct {
	ID    uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`

	SourceID *uuid.UUID `gorm:"type:uuid;index" json:"source_id,omitempty"`
	// ConnectionID is L5's provider connection. It exists now because the dedupe
	// index below must, and a partial index cannot be added to rows that predate it.
	ConnectionID *uuid.UUID `gorm:"type:uuid;index" json:"connection_id,omitempty"`

	// ProviderEventID is the delivery's stable id across retries (an
	// Idempotency-Key here, a Google lead_id in L3, a Meta leadgen_id in L5). It
	// carries the two partial unique indexes that make redelivery a no-op.
	ProviderEventID *string `gorm:"type:text" json:"provider_event_id,omitempty"`

	Status    string     `gorm:"type:varchar(16);not null" json:"status"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
	Attempts  int        `gorm:"not null;default:0" json:"attempts"`

	RawPayload datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"raw_payload"`
	Context    datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"context"`
	// QuarantinedFields records keys the payload carried that the allowlist
	// refused. Recorded, never written — dropping them silently is how a customer
	// discovers six weeks later that half their form was never captured.
	QuarantinedFields datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"quarantined_fields"`

	ResultSlug     string     `gorm:"type:varchar(64)" json:"result_slug,omitempty"`
	ResultRecordID *uuid.UUID `gorm:"type:uuid" json:"result_record_id,omitempty"`
	Outcome        string     `gorm:"type:varchar(16)" json:"outcome,omitempty"`

	Error string `gorm:"type:text" json:"error,omitempty"`
	// Note explains a judgement the pipeline made on a delivery that SUCCEEDED —
	// today, refusing to merge into a phone shared by several contacts. Separate
	// from Error on purpose: a careful decision rendered in the UI's red error box
	// reads as a failure, which is the opposite of what it is.
	Note string `gorm:"type:text" json:"note,omitempty"`

	CreatedAt   time.Time  `gorm:"index" json:"created_at"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
}

// TableName pins the table.
func (IntegrationEvent) TableName() string { return "integration_events" }
