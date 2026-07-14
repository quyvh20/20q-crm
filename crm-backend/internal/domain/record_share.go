package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Record shares (U6.2 — parity with report sharing)
// ============================================================
//
// A record can be shared with a user, a role, or a group at a level (view|edit).
// A share is the escape hatch that lets a row-scoped role ('own'/'team') reach a
// specific record outside its scope; it is NOT a way to restrict an 'all'-scoped
// role, which sees everything by definition (OLS is the knob for that).
//
// Before U6 this table had a single grantee_user_id, no org_id, no uniqueness,
// and a permission_level that could never be changed after the first grant. The
// vocabulary now matches report_shares: target_type/target_id + a level from the
// shared ladder in share.go. grantee_user_id survives one release as a mirrored,
// nullable legacy column (kept in step for 'user' targets so a rollback still
// reads correct data) and is dropped afterwards.

type RecordShare struct {
	ID         uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID      uuid.UUID `gorm:"type:uuid;not null" json:"org_id"`
	RecordType string    `gorm:"size:50;not null" json:"record_type"`
	RecordID   uuid.UUID `gorm:"type:uuid;not null" json:"record_id"`
	// TargetType/TargetID name who the grant reaches: a user, a role, or a group.
	TargetType string `gorm:"size:10;not null;default:'user'" json:"target_type"`
	TargetID   uuid.UUID `gorm:"type:uuid;not null" json:"target_id"`
	// GranteeUserID is the pre-U6 column, now legacy: written in step with TargetID
	// for 'user' targets, NULL for role/group grants. Nothing reads it — the access
	// predicate matches on target_type/target_id. Dropped a release after U6 ships.
	GranteeUserID *uuid.UUID `gorm:"type:uuid" json:"-"`
	// PermissionLevel is a storable record level: 'view' or 'edit' (share.go). The
	// column keeps its pre-U6 name to avoid a rename across every existing row and
	// query; only the vocabulary changed ('read' → 'view').
	PermissionLevel string     `gorm:"size:50;not null;default:'view'" json:"level"`
	CreatedBy       *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt       time.Time  `gorm:"not null;default:now()" json:"created_at"`
}

func (RecordShare) TableName() string { return "record_shares" }

// RecordShareView is one share rendered for the record's share dialog, with the
// target's display name resolved (user full name / role name / group name).
type RecordShareView struct {
	ID         uuid.UUID `json:"id"`
	TargetType string    `json:"target_type"`
	TargetID   uuid.UUID `json:"target_id"`
	TargetName string    `json:"target_name"`
	Level      string    `json:"level"`
	CreatedAt  time.Time `json:"created_at"`
}

// ShareRecordInput grants a record to a target at a level.
type ShareRecordInput struct {
	TargetType string    `json:"target_type" binding:"required"`
	TargetID   uuid.UUID `json:"target_id" binding:"required"`
	Level      string    `json:"level" binding:"required"`
}

// SharedRecordView is one row of the "Shared with me" list: a record someone
// else owns that has been shared to the caller (directly, via their role, or via
// a group).
type SharedRecordView struct {
	ObjectSlug  string    `json:"object_slug"`
	ObjectLabel string    `json:"object_label"`
	RecordID    uuid.UUID `json:"record_id"`
	Display     string    `json:"display"`
	Level       string    `json:"level"`
	OwnerName   string    `json:"owner_name"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ============================================================
// Ports
// ============================================================

// RecordShareRepository persists per-record grants (record_shares).
type RecordShareRepository interface {
	// Upsert creates the grant or updates the level of an existing one for the
	// same (record_type, record_id, target_type, target_id). Idempotent by the
	// table's unique index — no check-then-insert race, and re-sharing at a new
	// level actually changes the level (pre-U6 it silently kept the old one).
	Upsert(ctx context.Context, s *RecordShare) error
	// DeleteByID removes a share by id, scoped to (org, record_type, record_id) so
	// a caller can only revoke a share on the record they addressed.
	DeleteByID(ctx context.Context, orgID, id uuid.UUID, recordType string, recordID uuid.UUID) (int64, error)
	// ListByRecord returns a record's shares with target display names resolved.
	ListByRecord(ctx context.Context, orgID uuid.UUID, recordType string, recordID uuid.UUID) ([]RecordShareView, error)
	// BestLevelFor returns the highest level any share row grants the identity on
	// the record ('none' when no row matches).
	BestLevelFor(ctx context.Context, orgID uuid.UUID, recordType string, recordID uuid.UUID, ident ShareIdentity) (string, error)
	// SharedRecordTypes lists the object slugs the identity holds any share on, so
	// the usecase can resolve an OLS whitelist and push it into the page query.
	SharedRecordTypes(ctx context.Context, orgID uuid.UUID, ident ShareIdentity) ([]string, error)
	// ListSharedWithMe lists records shared TO the identity that it does not own,
	// newest-updated first, restricted to allowedSlugs (the objects the caller's
	// role may read).
	ListSharedWithMe(ctx context.Context, orgID uuid.UUID, ident ShareIdentity, allowedSlugs []string, limit, offset int) ([]SharedRecordView, int64, error)
	// DeleteByTarget revokes every share held by a target — used when a role or
	// group is deleted, so a grant never outlives the thing it names.
	DeleteByTarget(ctx context.Context, orgID uuid.UUID, targetType string, targetID uuid.UUID) (int64, error)
}

// ShareUseCase creates/revokes/lists record shares. Visibility of the record
// under the caller's own data scope is the gate: a caller can only share a record
// they can already see, so a row-scoped role shares only what it reaches while an
// 'all'-scoped role can share any record in the workspace.
type ShareUseCase interface {
	Share(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID uuid.UUID, in ShareRecordInput) (*RecordShare, error)
	Unshare(ctx context.Context, orgID, actorID uuid.UUID, slug string, recordID, shareID uuid.UUID) error
	List(ctx context.Context, orgID uuid.UUID, slug string, recordID uuid.UUID) ([]RecordShareView, error)
	// EffectiveLevel is what the caller may do with the record: 'manage' (owner or
	// an all-scoped writer — may re-share), 'edit', 'view', or 'none'.
	EffectiveLevel(ctx context.Context, orgID, userID uuid.UUID, slug string, recordID uuid.UUID) (string, error)
	ListSharedWithMe(ctx context.Context, orgID, userID uuid.UUID, slug string, limit, offset int) ([]SharedRecordView, int64, error)
}
