package repository

import (
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// access_predicate.go is the SINGLE definition of "which rows may this caller
// touch" (U6 S0).
//
// Before U6 that rule was copy-pasted into five places — scopes.go (read + write),
// report_sql.go, voice_note_repository.go, automation/actor.go and
// automation/executor_update_record.go — one of which literally carried a
// "KEEP IN SYNC with repository/scopes.go" comment. Every U6 item widens the rule
// (team scope; role/group share targets; custom-object owners), and five hand-kept
// copies of an authorization predicate is how a list page and a report end up
// disagreeing about who can see a record. They all call this now.
//
// The rule, in one sentence: an 'all'-scoped caller sees every row in the org; a
// row-scoped caller ('own' or 'team') sees rows they own, rows owned by a teammate
// when scoped to 'team', and rows shared to them — as a user, via their role, or
// via any group they belong to.

// RecordAccessArgs is everything the row predicate needs to know.
type RecordAccessArgs struct {
	// Table is the SQL table/alias the predicate is written against
	// ("contacts", "deals", "custom_object_records").
	Table string
	// RecordType is the discriminator stored in record_shares.record_type — the
	// object SLUG ("contact", "deal", or a custom object's slug). It is NOT
	// derivable from Table (all custom objects share one table), so callers must
	// pass it explicitly. Pre-U6 scopes.go guessed it from the table name and
	// defaulted anything unrecognized to "deal"; that silent mislabel is why this
	// is a required field rather than an inference.
	RecordType string
	OrgID      uuid.UUID
	// Scope is the caller's row scope: domain.DataScopeAll (no filter),
	// DataScopeTeam, or DataScopeOwn. Any unknown value is treated as the
	// narrowest ('own') — fail closed.
	Scope  string
	UserID uuid.UUID
	// RoleID is the caller's role. uuid.Nil (a role-less or bridge caller) simply
	// matches no role-targeted share — fail closed, never fail open.
	RoleID uuid.UUID
	// RequireEdit demands an 'edit'-level share. Read visibility alone is not write
	// access (U0.4): a 'view' share must never be silently writable.
	RequireEdit bool
	// OwnerColumn/IDColumn default to "owner_user_id" / "id".
	OwnerColumn string
	IDColumn    string
}

// RecordAccessPredicate builds the row filter for a caller. It returns ("", nil)
// for an 'all'-scoped caller (no restriction). Otherwise it returns a
// parenthesised SQL fragment plus its args; the ORG filter is NOT included —
// callers already apply org scoping and must keep doing so.
//
// A row with a NULL owner (an unassigned custom record) matches no row-scoped
// caller and is visible only to 'all' scope. That is deliberate: an unowned record
// belongs to nobody, so nobody's "own" view should claim it.
func RecordAccessPredicate(a RecordAccessArgs) (string, []any) {
	if a.Scope == domain.DataScopeAll {
		return "", nil
	}
	t := a.Table
	owner := a.OwnerColumn
	if owner == "" {
		owner = "owner_user_id"
	}
	idCol := a.IDColumn
	if idCol == "" {
		idCol = "id"
	}

	var b strings.Builder
	args := make([]any, 0, 8)

	b.WriteString("(" + t + "." + owner + " = ?")
	args = append(args, a.UserID)

	// Team scope adds every row owned by someone who shares a group with the caller.
	// Resolved as a correlated subquery rather than a list of ids threaded through
	// the request context, so a membership change takes effect on the caller's very
	// next request instead of after the 5-minute session-cache TTL — and so the
	// automation/AI callers, which never touch that cache, get it for free.
	//
	// It is a READ grant only (hence the !RequireEdit guard). "I can see my team's
	// pipeline" must not silently mean "I can edit and delete my teammates' deals" —
	// and the effective-level resolver, the record page and the AI persona all tell
	// the user it is view-only, so granting writes here would make every one of them
	// a lie. A teammate who genuinely needs to edit gets an explicit 'edit' share.
	if a.Scope == domain.DataScopeTeam && !a.RequireEdit {
		b.WriteString(" OR " + t + "." + owner + ` IN (
			SELECT m2.user_id FROM user_group_members m1
			JOIN user_group_members m2 ON m2.group_id = m1.group_id
			JOIN user_groups ug ON ug.id = m1.group_id AND ug.deleted_at IS NULL
			WHERE m1.user_id = ? AND m1.org_id = ?)`)
		args = append(args, a.UserID, a.OrgID)
	}

	b.WriteString(" OR EXISTS (SELECT 1 FROM record_shares rs WHERE rs.record_id = " + t + "." + idCol +
		" AND rs.record_type = ? AND rs.org_id = ? AND (" +
		"(rs.target_type = '" + domain.ShareTargetUser + "' AND rs.target_id = ?)" +
		" OR (rs.target_type = '" + domain.ShareTargetRole + "' AND rs.target_id = ?)" +
		" OR (rs.target_type = '" + domain.ShareTargetGroup + `' AND rs.target_id IN (
			SELECT ugm.group_id FROM user_group_members ugm
			JOIN user_groups ug2 ON ug2.id = ugm.group_id AND ug2.deleted_at IS NULL
			WHERE ugm.user_id = ? AND ugm.org_id = ?))`)
	args = append(args, a.RecordType, a.OrgID, a.UserID, a.RoleID, a.UserID, a.OrgID)

	if a.RequireEdit {
		b.WriteString(") AND rs.permission_level = '" + domain.ShareLevelEdit + "'))")
	} else {
		b.WriteString(")))")
	}

	return b.String(), args
}

// recordTypeForTable maps a built-in table to the slug stored in
// record_shares.record_type. Custom objects are NOT here — they all live in one
// table and must pass their slug explicitly.
func recordTypeForTable(table string) string {
	switch table {
	case "contacts":
		return "contact"
	case "deals":
		return "deal"
	default:
		return ""
	}
}
