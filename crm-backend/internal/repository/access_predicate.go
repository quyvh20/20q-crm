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

// TaskAccessPredicate is the row-visibility rule for TASKS, as a standalone SQL
// fragment: the parenthesised condition only, with no org filter (every caller
// already applies one) and no table alias (it is written against the physical
// `tasks` table, which is the only place tasks live).
//
// It exists as a function, rather than inline in taskScope, because R9.5 gave
// tasks a SECOND consumer — the report runner. A report that applied a different
// rule than /api/tasks would either show a rep their teammates' tasks or
// under-count their own, and nobody would notice until it mattered. Exactly the
// drift this file's header describes; see RecordAccessPredicate above.
//
// Returns ("", nil) for an 'all'-scoped caller — no restriction. Any other scope
// value, including an unrecognized one, produces the narrow predicate: fail closed.
func TaskAccessPredicate(orgID uuid.UUID, scope string, userID, roleID uuid.UUID) (string, []any) {
	if scope == domain.DataScopeAll {
		return "", nil
	}

	cSQL, cArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "c", RecordType: "contact", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})
	dSQL, dArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "d", RecordType: "deal", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})

	args := []any{userID, userID}
	args = append(args, cArgs...)
	args = append(args, dArgs...)

	return `(
		tasks.assigned_to = ?
		OR tasks.created_by = ?
		OR (tasks.contact_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = tasks.contact_id
			  AND c.deleted_at IS NULL
			  AND ` + cSQL + `
		))
		OR (tasks.deal_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM deals d
			WHERE d.id = tasks.deal_id
			  AND d.deleted_at IS NULL
			  AND ` + dSQL + `
		))
	)`, args
}

// ActivityAccessPredicate is the same thing for ACTIVITIES. Same two consumers
// (activityScope and the report runner), same reason for existing.
//
// Note the asymmetry with tasks: activities have no created_by, only user_id —
// the person who logged it. Three writers leave it NULL (the deal-stage-change
// activity written by the UI path and its automation twin, plus any historical
// stage_change row), so those rows are reachable by a row-scoped caller only via
// their linked deal. That is pre-existing behaviour and is preserved verbatim
// here; do not "improve" the rule while sharing it.
func ActivityAccessPredicate(orgID uuid.UUID, scope string, userID, roleID uuid.UUID) (string, []any) {
	if scope == domain.DataScopeAll {
		return "", nil
	}

	cSQL, cArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "c", RecordType: "contact", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})
	dSQL, dArgs := RecordAccessPredicate(RecordAccessArgs{
		Table: "d", RecordType: "deal", OrgID: orgID, Scope: scope, UserID: userID, RoleID: roleID,
	})

	args := []any{userID}
	args = append(args, cArgs...)
	args = append(args, dArgs...)

	return `(
		activities.user_id = ?
		OR (activities.contact_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM contacts c
			WHERE c.id = activities.contact_id
			  AND c.deleted_at IS NULL
			  AND ` + cSQL + `
		))
		OR (activities.deal_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM deals d
			WHERE d.id = activities.deal_id
			  AND d.deleted_at IS NULL
			  AND ` + dSQL + `
		))
	)`, args
}

// recordTypeForTable maps a built-in table to the slug stored in
// record_shares.record_type. Custom objects are NOT here — they all live in one
// table and must pass their slug explicitly.
//
// tasks/activities are deliberately absent: they are not shareable records (no
// record_shares rows ever name them), so there is no discriminator to return.
// Their visibility rule is the two predicates above, not RecordAccessPredicate.
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
