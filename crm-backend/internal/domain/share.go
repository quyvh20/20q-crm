package domain

import (
	"context"

	"github.com/google/uuid"
)

// ============================================================
// Shared sharing vocabulary (U6)
// ============================================================
//
// Two things are shareable — reports (P9) and records (U6.2) — and both grant to
// the same three target kinds at an ordered level. The vocabulary lives here, in
// one neutral place, so a grant means the same thing whichever object it hangs
// off and one resolver shape ("highest matching level wins") serves both. Before
// U6 these constants lived in report_share.go while records had their own ad-hoc
// 'read'/'edit' strings and a user-only grantee column; the split is exactly how
// record shares drifted into a decorative no-op.
//
// Levels differ per shareable ONLY in which of them are storable: a report can be
// shared for comment, a record cannot (there is nothing to comment on). Use
// IsStorableShareLevel for reports and IsStorableRecordShareLevel for records.

// Share target kinds. A grant addresses a user, a role, or a user group; group
// grants follow membership, so adding someone to a group grants them every share
// the group holds (and removing them revokes it) with no share-row edits.
const (
	ShareTargetUser  = "user"
	ShareTargetRole  = "role"
	ShareTargetGroup = "group"
)

// Access levels, lowest → highest. ShareLevelManage is never stored — it is
// derived for creators/owners/capability holders — but the resolvers return it.
const (
	ShareLevelNone    = "none"
	ShareLevelView    = "view"
	ShareLevelComment = "comment"
	ShareLevelEdit    = "edit"
	ShareLevelManage  = "manage"
)

// shareLevelRank ranks levels so "highest wins" is a max over ints.
var shareLevelRank = map[string]int{
	ShareLevelNone: 0, ShareLevelView: 1, ShareLevelComment: 2, ShareLevelEdit: 3, ShareLevelManage: 4,
}

// ShareLevelRank returns the level's rank (unknown → 0). Higher = more access.
func ShareLevelRank(level string) int { return shareLevelRank[level] }

// ShareLevelAtLeast reports whether have grants at least want.
func ShareLevelAtLeast(have, want string) bool { return shareLevelRank[have] >= shareLevelRank[want] }

// IsStorableShareLevel guards the level a REPORT share row may carry
// (manage/none are derived, never stored).
func IsStorableShareLevel(level string) bool {
	return level == ShareLevelView || level == ShareLevelComment || level == ShareLevelEdit
}

// IsStorableRecordShareLevel guards the level a RECORD share row may carry.
// 'comment' is deliberately absent: records have no comment surface, and quietly
// storing one would hand the grantee a level whose rank (2) sits above 'view'
// while granting nothing — an honesty bug of exactly the kind U6 exists to kill.
func IsStorableRecordShareLevel(level string) bool {
	return level == ShareLevelView || level == ShareLevelEdit
}

// IsShareTarget reports whether t is a known share target kind.
func IsShareTarget(t string) bool {
	return t == ShareTargetUser || t == ShareTargetRole || t == ShareTargetGroup
}

// ShareIdentity is the caller's set of share "handles" — the things a share row
// can name to reach them: their user id, their role id, and every group they
// belong to. A share matches the caller when its (target_type, target_id) hits
// any handle. A Nil RoleID must never match a role-targeted row (fail closed).
type ShareIdentity struct {
	UserID   uuid.UUID
	RoleID   uuid.UUID
	GroupIDs []uuid.UUID
}

// ShareIdentityRepository resolves a caller's share handles. Both share systems
// (records + reports) need exactly this and nothing more, so it is its own port:
// the record-share usecase does not have to depend on the report-share repo to
// ask who the caller is.
type ShareIdentityRepository interface {
	GetShareIdentity(ctx context.Context, orgID, userID uuid.UUID) (ShareIdentity, error)
}
