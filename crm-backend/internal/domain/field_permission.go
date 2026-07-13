package domain

import (
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Field-Level Security (plan §7, P5b — opt-in)
// ============================================================
//
// FLS lets an admin mark individual fields "sensitive" and choose which roles may
// see or edit them. It is enforced inside RecordService — the same chokepoint as
// OLS + audit — so a hidden field is stripped from the JSON *response*, not just
// the UI (plan §7.4 "strip before serialize"). It is opt-in and off by default: a
// field with no rows is fully accessible to every role, so FLS adds no behaviour
// and no per-request work until a field is actually restricted.
//
// Keyed by (object_slug, field_key) rather than an object_fields(id) FK — the same
// cross-stack identifier OLS/audit/links use — so custom-object fields (which live
// in custom_object_defs.fields, not object_fields, until P7) are protectable too.
// See migration 000017b for the full rationale.

// FieldLevel is one role's access to one field.
type FieldLevel string

const (
	// FieldLevelHidden strips the field from read responses and rejects writes.
	FieldLevelHidden FieldLevel = "hidden"
	// FieldLevelRead keeps the field visible but rejects writes.
	FieldLevelRead FieldLevel = "read"
	// FieldLevelEdit is full access — the default. Such rows are never stored
	// (setting a field back to edit deletes the row), so "no row" == edit and the
	// empty-table fast path stays meaningful.
	FieldLevelEdit FieldLevel = "edit"
)

// Valid reports whether l is one of the three known levels.
func (l FieldLevel) Valid() bool {
	switch l {
	case FieldLevelHidden, FieldLevelRead, FieldLevelEdit:
		return true
	}
	return false
}

// FieldPermission is one (role × field) restriction row. Absence of a row means
// the field is unrestricted (FieldLevelEdit) for that role.
type FieldPermission struct {
	OrgID      uuid.UUID `gorm:"type:uuid;primaryKey" json:"org_id"`
	RoleID     uuid.UUID `gorm:"type:uuid;primaryKey" json:"role_id"`
	ObjectSlug string    `gorm:"size:100;primaryKey" json:"object_slug"`
	FieldKey   string    `gorm:"size:100;primaryKey" json:"field_key"`
	Level      string    `gorm:"size:10;not null;default:edit" json:"level"`
	CreatedAt  time.Time `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null;default:now()" json:"updated_at"`
}

func (FieldPermission) TableName() string { return "field_permissions" }

// FieldMask is the set of field restrictions in force for one caller on one
// object. The empty mask (the common case) means nothing is restricted, so
// RecordService can skip all FLS work via Empty(). Maps are nil until a
// restriction exists, so the empty mask allocates nothing.
type FieldMask struct {
	Hidden   map[string]bool
	ReadOnly map[string]bool
}

// Empty reports whether the mask restricts nothing — the fast path that lets
// RecordService bypass strip/guard entirely when FLS is unused.
func (m FieldMask) Empty() bool { return len(m.Hidden) == 0 && len(m.ReadOnly) == 0 }

// IsHidden reports whether the field must be stripped from a read response.
func (m FieldMask) IsHidden(key string) bool { return m.Hidden[key] }

// CanWrite reports whether the caller may write the field. Hidden and read-only
// fields are both unwritable.
func (m FieldMask) CanWrite(key string) bool { return !m.Hidden[key] && !m.ReadOnly[key] }

// ============================================================
// Admin field-security grid DTOs
// ============================================================

// FieldPermissionGrid is everything the admin field-security UI needs for one
// object in a single payload: the object's fields (rows), the roles (columns),
// and the current non-default levels. Cells absent from the matrix are the
// default (edit). The frontend joins fields × roles by (role_id, field_key).
type FieldPermissionGrid struct {
	Slug   string                  `json:"slug"`
	Label  string                  `json:"label"`
	Fields []FieldPermFieldInfo    `json:"fields"`
	Roles  []PermRoleInfo          `json:"roles"`
	Matrix []FieldPermissionMatrix `json:"matrix"`
}

// FieldPermFieldInfo is one selectable field in the grid. System (native) fields
// are flagged so the UI can label them, but they are equally restrictable.
type FieldPermFieldInfo struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	IsSystem bool   `json:"is_system"`
}

// FieldPermissionMatrix is one configured (role, field) cell. Only non-default
// (read/hidden) cells appear; everything else is implicitly edit.
type FieldPermissionMatrix struct {
	RoleID   uuid.UUID `json:"role_id"`
	FieldKey string    `json:"field_key"`
	Level    string    `json:"level"`
}

// SetFieldPermissionInput sets one (role, field) level. Level 'edit' clears the
// restriction (deletes the row). ObjectSlug has no binding tag because the HTTP
// handler fills it authoritatively from the route path; the usecase still rejects
// an empty slug for non-HTTP callers.
type SetFieldPermissionInput struct {
	RoleID     uuid.UUID `json:"role_id" binding:"required"`
	ObjectSlug string    `json:"object_slug"`
	FieldKey   string    `json:"field_key" binding:"required"`
	Level      string    `json:"level" binding:"required"`
}

// SetFieldPermissionsBulkInput sets one level for MANY fields of one (role,
// object) at once (U3) — one transaction, one cache bust, one audit event.
// Level 'edit' clears the restrictions (deletes the rows). ObjectSlug is filled
// authoritatively from the route path, like SetFieldPermissionInput.
type SetFieldPermissionsBulkInput struct {
	RoleID     uuid.UUID `json:"role_id" binding:"required"`
	ObjectSlug string    `json:"object_slug"`
	FieldKeys  []string  `json:"field_keys" binding:"required"`
	Level      string    `json:"level" binding:"required"`
}
