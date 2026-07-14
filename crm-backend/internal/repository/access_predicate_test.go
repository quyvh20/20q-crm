package repository

import (
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// access_predicate_test.go pins the shape of the ONE row predicate every read and
// write path now shares. The SQL text is asserted only where a mistake would be a
// security bug (a missing clause = a leak; an extra one = a silent lockout); the
// live behavior is exercised against a real Postgres in the automation package's
// row-scope DB test.

var (
	predUser = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	predRole = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	predOrg  = uuid.MustParse("33333333-3333-3333-3333-333333333333")
)

func baseArgs(scope string) RecordAccessArgs {
	return RecordAccessArgs{
		Table:      "contacts",
		RecordType: "contact",
		OrgID:      predOrg,
		Scope:      scope,
		UserID:     predUser,
		RoleID:     predRole,
	}
}

// An 'all'-scoped caller must get NO predicate — anything else would silently
// restrict admins and managers.
func TestRecordAccessPredicate_AllScopeIsUnrestricted(t *testing.T) {
	sql, args := RecordAccessPredicate(baseArgs(domain.DataScopeAll))
	if sql != "" || args != nil {
		t.Fatalf("all scope must produce no predicate, got %q / %v", sql, args)
	}
}

// 'own' reaches owned rows and shared rows — and NOT teammates' rows.
func TestRecordAccessPredicate_OwnScope(t *testing.T) {
	sql, args := RecordAccessPredicate(baseArgs(domain.DataScopeOwn))

	for _, want := range []string{
		"contacts.owner_user_id = ?",
		"FROM record_shares rs",
		"rs.target_type = 'user'",
		"rs.target_type = 'role'",
		"rs.target_type = 'group'",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("own-scope predicate missing %q:\n%s", want, sql)
		}
	}
	// The teammate clause must NOT appear: own scope stops at the caller.
	if strings.Contains(sql, "user_group_members m1") {
		t.Errorf("own scope must not reach teammates:\n%s", sql)
	}
	// A read must not demand an edit-level share.
	if strings.Contains(sql, "permission_level") {
		t.Errorf("a read predicate must accept any share level:\n%s", sql)
	}

	wantArgs := []any{predUser, "contact", predOrg, predUser, predRole, predUser, predOrg}
	assertArgs(t, args, wantArgs)
}

// 'team' = own + rows owned by anyone sharing a group with the caller.
func TestRecordAccessPredicate_TeamScopeAddsTeammates(t *testing.T) {
	sql, args := RecordAccessPredicate(baseArgs(domain.DataScopeTeam))

	if !strings.Contains(sql, "contacts.owner_user_id = ?") {
		t.Errorf("team scope must still include the caller's own rows:\n%s", sql)
	}
	if !strings.Contains(sql, "user_group_members m1") || !strings.Contains(sql, "JOIN user_group_members m2") {
		t.Errorf("team scope must include the teammate self-join:\n%s", sql)
	}
	// A deleted group must not keep granting team visibility.
	if !strings.Contains(sql, "ug.deleted_at IS NULL") {
		t.Errorf("team scope must exclude soft-deleted groups:\n%s", sql)
	}

	// The teammate clause binds (user, org) before the share args.
	wantArgs := []any{predUser, predUser, predOrg, "contact", predOrg, predUser, predRole, predUser, predOrg}
	assertArgs(t, args, wantArgs)
}

// A write demands an 'edit' share: read visibility is not write access (U0.4).
func TestRecordAccessPredicate_WriteRequiresEditShare(t *testing.T) {
	a := baseArgs(domain.DataScopeOwn)
	a.RequireEdit = true
	sql, _ := RecordAccessPredicate(a)

	if !strings.Contains(sql, "rs.permission_level = 'edit'") {
		t.Errorf("a write predicate must require an edit-level share:\n%s", sql)
	}
	// The owner clause must survive: owning a record is always enough to write it.
	if !strings.Contains(sql, "contacts.owner_user_id = ?") {
		t.Errorf("a write predicate must still admit the record's owner:\n%s", sql)
	}
}

// A role-less caller (uuid.Nil) must not match role-targeted shares. The clause is
// still emitted — it simply compares against the nil uuid, which no role holds — so
// the guarantee is that the caller's role id is bound, never omitted.
func TestRecordAccessPredicate_NilRoleMatchesNoRoleShare(t *testing.T) {
	a := baseArgs(domain.DataScopeOwn)
	a.RoleID = uuid.Nil
	_, args := RecordAccessPredicate(a)

	for _, v := range args {
		if id, ok := v.(uuid.UUID); ok && id == uuid.Nil {
			return // the nil role id is bound — it can match no real role row
		}
	}
	t.Fatalf("a nil role id must still be bound as an arg (fail closed), got %v", args)
}

// An unknown scope value must be treated as the narrowest, never as 'all'.
func TestRecordAccessPredicate_UnknownScopeFailsClosed(t *testing.T) {
	sql, _ := RecordAccessPredicate(baseArgs("wat"))
	if sql == "" {
		t.Fatal("an unknown scope must NOT produce an unrestricted query")
	}
	if strings.Contains(sql, "user_group_members m1") {
		t.Errorf("an unknown scope must not grant team visibility:\n%s", sql)
	}
}

// Custom objects all live in one table, so the record_shares discriminator must
// come from the caller-supplied slug, not be inferred from the table name.
func TestRecordAccessPredicate_CustomObjectUsesSlug(t *testing.T) {
	sql, args := RecordAccessPredicate(RecordAccessArgs{
		Table:      "custom_object_records",
		RecordType: "ticket",
		OrgID:      predOrg,
		Scope:      domain.DataScopeOwn,
		UserID:     predUser,
		RoleID:     predRole,
	})
	if !strings.Contains(sql, "custom_object_records.owner_user_id = ?") {
		t.Errorf("custom records must be filtered on their owner column:\n%s", sql)
	}
	found := false
	for _, v := range args {
		if s, ok := v.(string); ok && s == "ticket" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the object slug must be bound as the record_shares discriminator, got %v", args)
	}
}

func assertArgs(t *testing.T, got, want []any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
