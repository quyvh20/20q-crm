package domain

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

// The users table is created by migrations/000002 with
// `CONSTRAINT users_email_unique UNIQUE (email)` — a unique CONSTRAINT, not a
// unique index. gorm's MigrateColumn compares that fact against the field's
// own Unique flag, and when they disagree it "fixes" the column by dropping
// the constraint under its own naming convention:
//
//	ALTER TABLE "users" DROP CONSTRAINT "uni_users_email"
//
// No such constraint exists, so the statement fails with 42704 — and because
// gorm's AutoMigrate returns on the first error, every model queued behind
// `users` (which is pulled in as OrgUser's belongs-to) was skipped in silence
// on every boot. The symptom was one ignorable-looking line in the Postgres
// log; the consequence was a schema reconciliation that had quietly not run
// for months.
//
// Parsing the model is enough to catch a revert, and it needs no database —
// the `uniqueIndex` spelling is the one that reintroduces the bug.
func TestUserEmail_DeclaresAUniqueConstraintNotAnIndex(t *testing.T) {
	s, err := schema.Parse(&User{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("parse User schema: %v", err)
	}
	f := s.LookUpField("email")
	if f == nil {
		t.Fatal("User has no email field")
	}
	if !f.Unique {
		t.Error("User.Email must be tagged `unique` so gorm matches the UNIQUE CONSTRAINT " +
			"migrations/000002 creates; `uniqueIndex` makes gorm try to DROP it every boot " +
			"and abort the rest of AutoMigrate")
	}
	if len(f.UniqueIndex) != 0 {
		t.Errorf("User.Email declares uniqueIndex %q — the database has a constraint, not an index", f.UniqueIndex)
	}
}
