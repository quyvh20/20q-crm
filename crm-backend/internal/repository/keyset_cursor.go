package repository

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ============================================================
// Keyset cursors — ONE place that emits both the ORDER BY and the cursor
// predicate for every keyset-paginated list.
// ============================================================
//
// R4.2. Before this file, deal_repository.go and company_repository.go paged with
// `WHERE id < <last id>` while ordering by `created_at DESC, id DESC`. The two
// clauses sat 22 lines apart and disagreed: ids are UUIDv4 and carry no time
// information, so "the rows after the one I last saw" and "the rows whose id
// sorts below it" are unrelated sets. Reproduced against real Postgres with four
// rows and limit 2, paging to exhaustion returned A, B, A, C, A — one row three
// times, one row never. With other id orderings page 2 comes back EMPTY instead,
// which blanks nextCursor and makes the frontend's "Load more" button vanish with
// half the list unreachable.
//
// The fix is not a smarter predicate. It is a single type that emits the predicate
// AND the ORDER BY from the same resolved ordering, so the two cannot drift apart
// again — that drift is the actual root cause.
//
// contact_repository.go was accidentally correct: it compared (created_at, id) and
// ordered by the same. Accidentally, because it hardwired created_at into the
// comparison while the ORDER BY already honoured f.SortBy — the day anyone wired
// sort into the object registry it would have inherited the deals bug verbatim. It
// moves onto this shared path too, so it stops depending on that accident.

// keysetCursorVersion is baked into every cursor and checked on decode. Bump it
// whenever the payload's meaning changes; the old cursors then fail soft to page 1
// instead of being reinterpreted.
const keysetCursorVersion = 1

// keysetCursorPrefix marks the wire format. It exists so a cursor minted by the
// PREVIOUS format (a bare base64 UUID) is recognisably not one of ours and gets
// refused rather than decoded into nonsense: mid-deploy both formats are in flight
// in already-open browser tabs.
const keysetCursorPrefix = "k1:"

// keysetCursor is the decoded wire payload. Sort and Dir are carried so the
// predicate can refuse a cursor minted under a DIFFERENT ordering — see
// decodeKeysetCursor.
type keysetCursor struct {
	Ver  int             `json:"v"` // keysetCursorVersion
	Sort string          `json:"s"` // logical sort key: "created_at", "value", …
	Dir  string          `json:"d"` // "ASC" | "DESC"
	Val  json.RawMessage `json:"k"` // the sort column's value on the cursor row ("null" when NULL)
	ID   uuid.UUID       `json:"i"` // the tiebreaker
}

// keysetKind says how a sort column's value round-trips through the cursor's JSON
// and back into a SQL bind parameter. The column's Go/SQL type is known at the
// call site, never in the cursor, so a corrupted payload cannot pick its own type.
type keysetKind uint8

const (
	keysetTime keysetKind = iota
	keysetNumber
	keysetText
)

// keysetColumn is one entry in a table's sort whitelist. The whitelist is the ONLY
// thing standing between a caller-supplied sort_by and an fmt.Sprintf into
// Order(): never build a column name out of request data.
type keysetColumn struct {
	col      string     // fully qualified SQL column, e.g. "deals.value"
	kind     keysetKind // how the cursor payload round-trips
	nullable bool       // column can be NULL, so the predicate needs its null arms

	// nullAs folds a NULL to a constant BEFORE it is ordered or compared. It exists
	// for the columns whose Go field cannot carry NULL — domain.Deal.Value is a
	// float64, so a NULL numeric scans back as 0 — and those are the DANGEROUS case,
	// not the harmless one: the cursor minted from such a row says 0 while the ORDER
	// BY sorted the row as NULL, so the next page is filtered against a value that
	// row never had and the walk dead-stops (DESC) or repeats rows (ASC). The null
	// arms below cannot rescue it, because they switch on a nil cursor value that a
	// non-pointer field can never produce. COALESCE makes the SQL ordering agree with
	// the value the Go model reports, which is the invariant this whole file rests
	// on. It is a literal from this whitelist, never request data.
	//
	// nullAs and nullable are mutually exclusive: after the COALESCE the expression
	// is never NULL, so there is no null block for a cursor to be inside of.
	nullAs string
}

// sortExpr is what the ORDER BY orders on and what the predicate compares: the
// column itself, or the COALESCE that folds its NULLs to nullAs.
func (c keysetColumn) sortExpr() string {
	if c.nullAs == "" {
		return c.col
	}
	return fmt.Sprintf("COALESCE(%s, %s)", c.col, c.nullAs)
}

// handlesNull says this entry has SOME answer for a NULL in the column — the
// predicate's null arms, or a COALESCE that removes the question. A column that is
// NULLable in the schema and has neither is the exact shape of the shipped deals
// bug, which is why the schema-drift test asserts on this and not on `nullable`.
func (c keysetColumn) handlesNull() bool { return c.nullable || c.nullAs != "" }

// keysetOrdering is a request's resolved sort. It is the sole producer of both the
// ORDER BY clause and the cursor predicate.
type keysetOrdering struct {
	key   string // the LOGICAL sort key, recorded in the cursor and checked on decode
	idCol string // fully qualified id column — the tiebreaker that makes the order total
	col   keysetColumn
	dir   string // "ASC" | "DESC"
}

// newKeysetOrdering resolves sort_by/sort_order against a whitelist. An unknown or
// empty sort_by falls back to defaultKey, which MUST exist in allowed.
func newKeysetOrdering(allowed map[string]keysetColumn, defaultKey, idCol, sortBy, sortOrder string) keysetOrdering {
	key := defaultKey
	if _, ok := allowed[sortBy]; ok {
		key = sortBy
	}
	dir := "DESC"
	if strings.EqualFold(sortOrder, "ASC") {
		dir = "ASC"
	}
	return keysetOrdering{key: key, idCol: idCol, col: allowed[key], dir: dir}
}

// orderClause is the ONLY producer of the ORDER BY for a keyset-paginated list.
// The id tiebreaker is not cosmetic: without it, rows sharing a sort value have no
// defined order between them, so the same row can legitimately appear on two
// consecutive pages and another never appear at all.
//
// No explicit NULLS FIRST/LAST. The predicate below is written against Postgres's
// DEFAULT placement (DESC ⇒ NULLS FIRST, ASC ⇒ NULLS LAST) precisely so that
// adopting this file reorders nobody's existing list.
func (o keysetOrdering) orderClause() string {
	return fmt.Sprintf("%s %s, %s %s", o.col.sortExpr(), o.dir, o.idCol, o.dir)
}

// nextCursor mints the cursor for the last row of a page. val is that row's value
// for THIS ordering's sort column — nil when the column is NULL there.
func (o keysetOrdering) nextCursor(val any, id uuid.UUID) string {
	return encodeKeysetCursor(o.key, o.dir, val, id)
}

// applyCursor narrows a query to the rows strictly AFTER the cursor row under this
// exact ordering. A cursor that fails to decode — or that was minted under a
// different ordering — is dropped, and the caller simply gets page 1.
func (o keysetOrdering) applyCursor(q *gorm.DB, cursor string) *gorm.DB {
	sql, args, ok := o.cursorPredicate(cursor)
	if !ok {
		return q
	}
	return q.Where(sql, args...)
}

// cursorPredicate is applyCursor's testable core: the SQL and binds that keep only
// the rows after the cursor row. ok=false means "ignore the cursor, serve page 1".
//
// Row-value comparison, deliberately, rather than the
// `col < ? OR (col = ? AND id < ?)` expansion: it is a SINGLE total-order
// comparison, so the tie case cannot be written down wrongly, and the expansion is
// exactly where tie-handling bugs hide. (It is also the form Postgres can drive a
// composite (col, id) index straight off — but no such index exists on deals,
// companies or contacts today, in the migrations or in any boot guard, so that is a
// property this form would BE ABLE to exploit, not one it exploits now. This choice
// buys correctness, not speed.)
func (o keysetOrdering) cursorPredicate(cursor string) (string, []any, bool) {
	if cursor == "" {
		return "", nil, false
	}
	c, ok := decodeKeysetCursor(cursor, o.key, o.dir)
	if !ok {
		return "", nil, false
	}
	val, ok := c.bindValue(o.col.kind)
	if !ok {
		return "", nil, false
	}
	// A null payload for a column this whitelist says never sorts as NULL is a
	// cursor we could not have minted, so it is corrupt. Serve page 1. Without this
	// the non-nullable arm below would bind NULL into the row-value comparison,
	// which evaluates to NULL rather than true and hands back an EMPTY page — a
	// silent dead end that contradicts the "every cursor failure lands on page 1"
	// contract every caller here relies on.
	if val == nil && !o.col.nullable {
		return "", nil, false
	}

	expr := o.col.sortExpr()
	op := "<"
	if o.dir == "ASC" {
		op = ">"
	}
	tuple := fmt.Sprintf("((%s, %s) %s (?, ?))", expr, o.idCol, op)

	if !o.col.nullable {
		return tuple, []any{val, c.ID}, true
	}

	// Nullable sort column. A row-value comparison against NULL evaluates to NULL
	// rather than true, so without the arms below every NULL-sorted row would be
	// silently dropped from every page after the first — the list would just lose
	// them. nullsFirst mirrors the DEFAULT placement orderClause relies on.
	nullsFirst := o.dir == "DESC"
	switch {
	case val == nil && nullsFirst:
		// Inside the LEADING null block: the rest of that block, then every non-null row.
		return fmt.Sprintf("((%s IS NULL AND %s %s ?) OR %s IS NOT NULL)", expr, o.idCol, op, expr),
			[]any{c.ID}, true
	case val == nil:
		// Inside the TRAILING null block: only the rest of that block can follow.
		return fmt.Sprintf("(%s IS NULL AND %s %s ?)", expr, o.idCol, op),
			[]any{c.ID}, true
	case nullsFirst:
		// The null block is already behind us; only non-null rows remain, and the
		// row-value comparison excludes NULLs for free.
		return tuple, []any{val, c.ID}, true
	default:
		// NULLs sort last, so all of them still lie ahead of us.
		return fmt.Sprintf("(%s OR %s IS NULL)", tuple, expr), []any{val, c.ID}, true
	}
}

// encodeKeysetCursor renders the opaque cursor handed back to the client.
func encodeKeysetCursor(sortKey, dir string, val any, id uuid.UUID) string {
	raw, err := json.Marshal(val)
	if err != nil {
		return "" // an unencodable sort value ends the walk rather than breaking it
	}
	b, err := json.Marshal(keysetCursor{
		Ver:  keysetCursorVersion,
		Sort: sortKey,
		Dir:  dir,
		Val:  raw,
		ID:   id,
	})
	if err != nil {
		return ""
	}
	return keysetCursorPrefix + base64.StdEncoding.EncodeToString(b)
}

// decodeKeysetCursor parses a cursor and checks it was minted for the SAME
// ordering this request asks for. ok=false on EVERY failure — missing prefix, bad
// base64, bad JSON, wrong version, nil id, or a sort/direction mismatch — and
// every caller treats that as "page 1".
//
// Failing soft rather than erroring matches decodeNotificationCursor: org and row
// scope are applied independently of the cursor, so a bogus cursor can never widen
// what a caller sees, only where their page starts. Failing HARD would be actively
// worse — the predecessor decoded base64 and passed whatever came out straight
// into `id < ?`, so a non-UUID body reached Postgres as SQLSTATE 22P02 and turned
// an ordinary "Load more" into a 500. That is not hypothetical: across a deploy,
// tabs holding a cursor of the other format are in flight in both directions.
//
// The sort/dir binding is what makes the contacts class of this bug structurally
// impossible from here on. A cursor minted while sorting by created_at can never
// be applied against a value comparator, because the mismatch routes the caller to
// page 1 instead of quietly mixing two orderings.
func decodeKeysetCursor(raw, sortKey, dir string) (keysetCursor, bool) {
	body, ok := strings.CutPrefix(raw, keysetCursorPrefix)
	if !ok {
		return keysetCursor{}, false
	}
	b, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return keysetCursor{}, false
	}
	var c keysetCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return keysetCursor{}, false
	}
	if c.Ver != keysetCursorVersion || c.Sort != sortKey || c.Dir != dir || c.ID == uuid.Nil {
		return keysetCursor{}, false
	}
	return c, true
}

// bindValue turns the cursor payload back into a SQL bind parameter. A nil first
// return means the sort column was NULL on the cursor row. ok=false means the
// payload does not match the column's kind, i.e. the cursor is corrupt.
func (c keysetCursor) bindValue(kind keysetKind) (any, bool) {
	if strings.TrimSpace(string(c.Val)) == "null" {
		return nil, true
	}
	switch kind {
	case keysetTime:
		var t time.Time
		if err := json.Unmarshal(c.Val, &t); err != nil {
			return nil, false
		}
		return t, true
	case keysetNumber:
		var f float64
		if err := json.Unmarshal(c.Val, &f); err != nil {
			return nil, false
		}
		return f, true
	case keysetText:
		var s string
		if err := json.Unmarshal(c.Val, &s); err != nil {
			return nil, false
		}
		return s, true
	}
	return nil, false
}
