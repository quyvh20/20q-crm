package usecase

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// isContactEmailConflict is what lets contact create report a duplicate email as
// 409 instead of a blanket 500, and — more importantly — what lets lead ingestion
// tell "this email already exists, go update it" apart from "the database is
// down". Both branches are silent failures when wrong: a mis-detected conflict
// updates a contact the lead has nothing to do with, and a missed one fails a
// real lead. Hence the constraint-name precision below.
func TestIsContactEmailConflict(t *testing.T) {
	pgUnique := func(constraint string) error {
		return &pgconn.PgError{Code: "23505", ConstraintName: constraint}
	}

	t.Run("the contact email index → conflict", func(t *testing.T) {
		if !isContactEmailConflict(pgUnique(contactEmailUniqueIndex)) {
			t.Error("the real duplicate-email violation must be detected")
		}
	})

	t.Run("wrapped → still conflict", func(t *testing.T) {
		// errors.As unwraps, so a repo that adds context with %w keeps working.
		if !isContactEmailConflict(fmt.Errorf("create contact: %w", pgUnique(contactEmailUniqueIndex))) {
			t.Error("a wrapped violation must still be detected")
		}
	})

	t.Run("a DIFFERENT unique index on contacts → NOT an email conflict", func(t *testing.T) {
		// The reason this checks ConstraintName and not just SQLSTATE. If contacts
		// ever grows another unique index, a constraint-blind check would report its
		// violation as an email duplicate — sending ingestion to update an unrelated
		// row, with a 200.
		if isContactEmailConflict(pgUnique("idx_contacts_org_external_ref")) {
			t.Error("only the email index may be reported as an email conflict")
		}
	})

	t.Run("a different SQLSTATE → not a conflict", func(t *testing.T) {
		// 23503 = foreign_key_violation.
		if isContactEmailConflict(&pgconn.PgError{Code: "23503", ConstraintName: contactEmailUniqueIndex}) {
			t.Error("only 23505 is a unique violation")
		}
	})

	t.Run("a plain error (e.g. a dead connection) → not a conflict", func(t *testing.T) {
		// The case that matters most: this must NOT look like a duplicate, or
		// ingestion would "recover" from an outage by silently doing nothing.
		if isContactEmailConflict(errors.New("connection refused")) {
			t.Error("a non-pg error must never be read as a duplicate")
		}
	})

	t.Run("nil → not a conflict", func(t *testing.T) {
		if isContactEmailConflict(nil) {
			t.Error("nil must not be a conflict")
		}
	})
}
