package schemaaudit

import "testing"

// fixture_parser_test.go pins the CREATE TABLE parser itself, after a real bug
// was found by hand: task_activity_scope_db_test.go's `tasks` fixture genuinely
// declares last_reminded_at, yet TestFixtureDrift reported it missing. The
// parser split columns on every top-level comma with no idea some of those
// commas sat inside a `--` prose comment — this repo's fixture doc comments
// routinely contain one ("gorm names every column on the model, so the scan
// dies with…") — so the comment's OWN internal comma cut a real column
// declaration in half, and the trailing half's first token (an ordinary
// English word, not a column name) got recorded instead of the real one.
//
// A parser that hides real columns is worse than one that reports too few
// gaps: it can retire a genuine finding from the ratchet by making the column
// look declared when it silently is not.

func TestColumnNames_CommentWithInternalCommaDoesNotEatTheNextColumn(t *testing.T) {
	body := `id UUID PRIMARY KEY,
		priority VARCHAR(20) NOT NULL DEFAULT 'medium',
		-- gorm names every column on the model, so the scan dies with
		-- column "not_a_real_column" does not exist.
		not_a_real_column TIMESTAMPTZ,
		created_at TIMESTAMPTZ`

	cols := columnNames(stripLineComments(body))
	if !cols["not_a_real_column"] {
		t.Errorf("not_a_real_column must be detected despite the preceding comment's internal comma; got %v", cols)
	}
	if cols["so"] || cols["with"] {
		t.Errorf("a comment's internal words must never be recorded as columns; got %v", cols)
	}
	for _, want := range []string{"id", "priority", "created_at"} {
		if !cols[want] {
			t.Errorf("expected %q to still be detected; got %v", want, cols)
		}
	}
}

func TestColumnNames_PlainCommentWithNoCommaStillWorked(t *testing.T) {
	// The case that was ALREADY fine — pinned so a future change can't
	// regress it while fixing something else.
	body := `id UUID PRIMARY KEY,
		-- a simple comment with no comma
		not_a_real_column VARCHAR(20)`
	cols := columnNames(stripLineComments(body))
	if !cols["not_a_real_column"] {
		t.Errorf("expected not_a_real_column to be detected; got %v", cols)
	}
}

func TestBalancedBody_CommentWithUnmatchedParenDoesNotDesyncDepth(t *testing.T) {
	// A comment with ONE unmatched paren (as opposed to this repo's real
	// comments, which always parenthesize a migration number and so net to
	// zero) would desync a paren-counting scan that isn't comment-aware,
	// closing the CREATE TABLE early or swallowing the next statement.
	src := `CREATE TABLE synthetic_fixture_a (
		id UUID PRIMARY KEY,
		-- see the note (below for why this exists
		not_a_real_column VARCHAR(20)
	)
	CREATE TABLE synthetic_fixture_b (x UUID)`

	tables := parseCreateTables(src)
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d: %+v", len(tables), tables)
	}
	if tables[0].table != "synthetic_fixture_a" || !tables[0].columns["not_a_real_column"] {
		t.Errorf("first table parsed wrong: %+v", tables[0])
	}
	if tables[1].table != "synthetic_fixture_b" {
		t.Errorf("the comment's unmatched paren must not swallow the next CREATE TABLE; got %+v", tables[1])
	}
}

func TestParseCreateTables_RealFixtureShapeStillParsesCleanly(t *testing.T) {
	// A comment-free, ordinary fixture — the common case — must parse exactly
	// as before these fixes.
	src := "CREATE TABLE IF NOT EXISTS synthetic_fixture_c (\n" +
		"\tid UUID PRIMARY KEY,\n" +
		"\torg_id UUID NOT NULL,\n" +
		"\tsome_column TEXT\n" +
		")"
	tables := parseCreateTables(src)
	if len(tables) != 1 || tables[0].table != "synthetic_fixture_c" {
		t.Fatalf("unexpected parse: %+v", tables)
	}
	for _, want := range []string{"id", "org_id", "some_column"} {
		if !tables[0].columns[want] {
			t.Errorf("expected %q, got %v", want, tables[0].columns)
		}
	}
}
