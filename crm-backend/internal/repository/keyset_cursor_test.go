package repository

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These are pure tests: no Docker, no DB. They pin the two properties that make
// the keyset cursor safe on its own — a value survives the round trip unchanged,
// and EVERY other input lands the caller on page 1 rather than in SQL. The
// end-to-end "paging to exhaustion returns each row exactly once" property is in
// pagination_integration_test.go, which needs a real Postgres.

var (
	testCursorID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testTimeCol  = map[string]keysetColumn{"created_at": {col: "t.created_at", kind: keysetTime}}
	testNumCol   = map[string]keysetColumn{"value": {col: "t.value", kind: keysetNumber}}
	testTextCol  = map[string]keysetColumn{"title": {col: "t.title", kind: keysetText}}
	testNullCol  = map[string]keysetColumn{"email": {col: "t.email", kind: keysetText, nullable: true}}
)

func TestKeysetCursor_RoundTrip(t *testing.T) {
	ts := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)

	cases := []struct {
		name    string
		allowed map[string]keysetColumn
		key     string
		dir     string
		val     any
		want    any
	}{
		{"time desc", testTimeCol, "created_at", "DESC", ts, ts},
		{"time asc", testTimeCol, "created_at", "ASC", ts, ts},
		{"float", testNumCol, "value", "DESC", 1234.56, 1234.56},
		// probability is an int on the model; it must come back bindable as a number.
		{"int", testNumCol, "value", "DESC", 42, float64(42)},
		{"text", testTextCol, "title", "ASC", "Acme renewal", "Acme renewal"},
		{"empty text", testTextCol, "title", "ASC", "", ""},
		{"null", testNullCol, "email", "DESC", nil, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ord := newKeysetOrdering(tc.allowed, tc.key, "t.id", tc.key, tc.dir)
			raw := ord.nextCursor(tc.val, testCursorID)
			require.NotEmpty(t, raw)

			c, ok := decodeKeysetCursor(raw, tc.key, tc.dir)
			require.True(t, ok, "decode must accept a cursor it just minted")
			assert.Equal(t, testCursorID, c.ID)
			assert.Equal(t, tc.key, c.Sort)
			assert.Equal(t, tc.dir, c.Dir)

			got, ok := c.bindValue(tc.allowed[tc.key].kind)
			require.True(t, ok)
			if wantTime, isTime := tc.want.(time.Time); isTime {
				gotTime, isTime := got.(time.Time)
				require.True(t, isTime, "want a time.Time back, got %T", got)
				assert.True(t, wantTime.Equal(gotTime), "time round trip: %v != %v", wantTime, gotTime)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// Every malformed cursor must land on page 1. A cursor that reaches SQL undecoded
// is how the predecessor turned a "Load more" click into a 500: it decoded base64
// and fed whatever fell out straight into `id < ?`, so a non-UUID body arrived at
// Postgres as SQLSTATE 22P02.
func TestKeysetCursor_MalformedGoesToPageOne(t *testing.T) {
	goodBody := func(mutate func(*keysetCursor)) string {
		c := keysetCursor{Ver: keysetCursorVersion, Sort: "created_at", Dir: "DESC", Val: json.RawMessage(`"2026-01-01T00:00:00Z"`), ID: testCursorID}
		mutate(&c)
		b, err := json.Marshal(c)
		require.NoError(t, err)
		return keysetCursorPrefix + base64.StdEncoding.EncodeToString(b)
	}

	cases := []struct {
		name   string
		cursor string
	}{
		{"empty", ""},
		{"garbage", "not-a-cursor"},
		// The pre-R4.2 wire format: a bare base64 UUID with no prefix. Both formats
		// are in flight across a deploy, in both directions.
		{"legacy bare uuid", base64.StdEncoding.EncodeToString([]byte(testCursorID.String()))},
		{"legacy bare uuid, arbitrary text", base64.StdEncoding.EncodeToString([]byte("'; DROP TABLE deals; --"))},
		{"prefix but not base64", keysetCursorPrefix + "!!!not base64!!!"},
		{"prefix, base64, not json", keysetCursorPrefix + base64.StdEncoding.EncodeToString([]byte("hello"))},
		{"prefix, json, wrong shape", keysetCursorPrefix + base64.StdEncoding.EncodeToString([]byte(`[1,2,3]`))},
		{"future version", goodBody(func(c *keysetCursor) { c.Ver = keysetCursorVersion + 1 })},
		{"zero version", goodBody(func(c *keysetCursor) { c.Ver = 0 })},
		{"nil id", goodBody(func(c *keysetCursor) { c.ID = uuid.Nil })},
		{"different sort key", goodBody(func(c *keysetCursor) { c.Sort = "value" })},
		{"different direction", goodBody(func(c *keysetCursor) { c.Dir = "ASC" })},
	}

	ord := newKeysetOrdering(testTimeCol, "created_at", "t.id", "created_at", "DESC")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := decodeKeysetCursor(tc.cursor, "created_at", "DESC")
			assert.False(t, ok, "decode must reject %q", tc.cursor)

			sql, args, ok := ord.cursorPredicate(tc.cursor)
			assert.False(t, ok, "predicate must be dropped for %q", tc.cursor)
			assert.Empty(t, sql, "a rejected cursor must contribute no SQL")
			assert.Empty(t, args)
		})
	}
}

// A payload whose JSON does not match the column's kind is corrupt, not a hint to
// guess a type. The kind comes from the whitelist at the call site, never from the
// cursor, so a hand-edited cursor cannot pick its own comparator.
func TestKeysetCursor_KindMismatchGoesToPageOne(t *testing.T) {
	// Mint under a text ordering, then read back under a numeric one with the same
	// key/dir so only the kind differs.
	textOrd := newKeysetOrdering(map[string]keysetColumn{"k": {col: "t.k", kind: keysetText}}, "k", "t.id", "k", "DESC")
	numOrd := newKeysetOrdering(map[string]keysetColumn{"k": {col: "t.k", kind: keysetNumber}}, "k", "t.id", "k", "DESC")

	raw := textOrd.nextCursor("not a number", testCursorID)
	sql, _, ok := numOrd.cursorPredicate(raw)
	assert.False(t, ok, "a text payload must not bind against a numeric column")
	assert.Empty(t, sql)
}

// The sort/dir binding is the structural fix: a cursor minted under one ordering
// can never be applied against another, so the two clauses cannot silently mix.
func TestKeysetCursor_OrderingMismatchGoesToPageOne(t *testing.T) {
	allowed := map[string]keysetColumn{
		"created_at": {col: "t.created_at", kind: keysetTime},
		"value":      {col: "t.value", kind: keysetNumber},
	}
	minted := newKeysetOrdering(allowed, "created_at", "t.id", "created_at", "DESC").
		nextCursor(time.Now().UTC(), testCursorID)

	for _, tc := range []struct{ sortBy, sortOrder string }{
		{"value", "DESC"},      // same direction, different column
		{"created_at", "ASC"},  // same column, flipped direction
		{"value", "ASC"},       // both differ
		{"nonexistent", "ASC"}, // falls back to created_at ASC — still a mismatch
	} {
		ord := newKeysetOrdering(allowed, "created_at", "t.id", tc.sortBy, tc.sortOrder)
		_, _, ok := ord.cursorPredicate(minted)
		assert.False(t, ok, "sort_by=%q sort_order=%q must ignore a created_at DESC cursor", tc.sortBy, tc.sortOrder)
	}
}

// An unknown sort_by must fall back to the default column rather than reaching
// Order() — this whitelist is the only thing between request data and an
// fmt.Sprintf into SQL.
func TestKeysetOrdering_RejectsUnknownSortColumn(t *testing.T) {
	allowed := map[string]keysetColumn{
		"created_at": {col: "t.created_at", kind: keysetTime},
		"value":      {col: "t.value", kind: keysetNumber},
	}
	ord := newKeysetOrdering(allowed, "created_at", "t.id", "value); DROP TABLE deals; --", "desc")
	assert.Equal(t, "created_at", ord.key)
	assert.Equal(t, "t.created_at DESC, t.id DESC", ord.orderClause())

	// Direction is likewise a two-valued choice, never pass-through.
	assert.Equal(t, "t.created_at DESC, t.id DESC",
		newKeysetOrdering(allowed, "created_at", "t.id", "", "sideways").orderClause())
	assert.Equal(t, "t.created_at ASC, t.id ASC",
		newKeysetOrdering(allowed, "created_at", "t.id", "", "asc").orderClause())
}

// The predicate and the ORDER BY are produced by the same object; these assertions
// pin that they agree on column, direction and tiebreaker.
func TestKeysetCursor_PredicateMatchesOrderClause(t *testing.T) {
	t.Run("not-null column, desc", func(t *testing.T) {
		ord := newKeysetOrdering(testNumCol, "value", "t.id", "value", "DESC")
		sql, args, ok := ord.cursorPredicate(ord.nextCursor(10.0, testCursorID))
		require.True(t, ok)
		assert.Equal(t, "((t.value, t.id) < (?, ?))", sql)
		assert.Equal(t, []any{10.0, testCursorID}, args)
		assert.Equal(t, "t.value DESC, t.id DESC", ord.orderClause())
	})

	t.Run("not-null column, asc", func(t *testing.T) {
		ord := newKeysetOrdering(testNumCol, "value", "t.id", "value", "ASC")
		sql, _, ok := ord.cursorPredicate(ord.nextCursor(10.0, testCursorID))
		require.True(t, ok)
		assert.Equal(t, "((t.value, t.id) > (?, ?))", sql)
		assert.Equal(t, "t.value ASC, t.id ASC", ord.orderClause())
	})

	// Nullable column, DESC. Postgres's default placement for DESC is NULLS FIRST,
	// and orderClause emits no explicit NULLS clause, so the predicate must assume
	// exactly that.
	t.Run("nullable desc, non-null cursor", func(t *testing.T) {
		ord := newKeysetOrdering(testNullCol, "email", "t.id", "email", "DESC")
		sql, args, ok := ord.cursorPredicate(ord.nextCursor("b@x.com", testCursorID))
		require.True(t, ok)
		// The null block is already behind us; the row-value form excludes NULLs.
		assert.Equal(t, "((t.email, t.id) < (?, ?))", sql)
		assert.Equal(t, []any{"b@x.com", testCursorID}, args)
	})

	t.Run("nullable desc, null cursor", func(t *testing.T) {
		ord := newKeysetOrdering(testNullCol, "email", "t.id", "email", "DESC")
		sql, args, ok := ord.cursorPredicate(ord.nextCursor(nil, testCursorID))
		require.True(t, ok)
		assert.Equal(t, "((t.email IS NULL AND t.id < ?) OR t.email IS NOT NULL)", sql)
		assert.Equal(t, []any{testCursorID}, args)
	})

	t.Run("nullable asc, non-null cursor", func(t *testing.T) {
		ord := newKeysetOrdering(testNullCol, "email", "t.id", "email", "ASC")
		sql, args, ok := ord.cursorPredicate(ord.nextCursor("b@x.com", testCursorID))
		require.True(t, ok)
		// NULLS LAST under ASC, so every null row still lies ahead.
		assert.Equal(t, "(((t.email, t.id) > (?, ?)) OR t.email IS NULL)", sql)
		assert.Equal(t, []any{"b@x.com", testCursorID}, args)
	})

	t.Run("nullable asc, null cursor", func(t *testing.T) {
		ord := newKeysetOrdering(testNullCol, "email", "t.id", "email", "ASC")
		sql, args, ok := ord.cursorPredicate(ord.nextCursor(nil, testCursorID))
		require.True(t, ok)
		assert.Equal(t, "(t.email IS NULL AND t.id > ?)", sql)
		assert.Equal(t, []any{testCursorID}, args)
	})
}

// A null payload against a column the whitelist says never sorts as NULL is a
// cursor we could not have minted. It must land on page 1 like every other
// unusable cursor. Binding it instead produces `((t.value, t.id) < (NULL, ?))`,
// which SQL evaluates to NULL rather than true, so the caller gets an EMPTY page:
// "Load more" returns nothing and blanks the cursor, and the rest of the list
// becomes unreachable. An empty page is a worse failure than page 1 because it
// looks like the end of the list.
func TestKeysetCursor_NullValueOnNonNullableColumnGoesToPageOne(t *testing.T) {
	ord := newKeysetOrdering(testNumCol, "value", "t.id", "value", "DESC")

	raw := ord.nextCursor(nil, testCursorID)
	require.NotEmpty(t, raw)
	_, ok := decodeKeysetCursor(raw, "value", "DESC")
	require.True(t, ok, "the payload itself is well-formed; only its null value is impossible")

	sql, args, ok := ord.cursorPredicate(raw)
	assert.False(t, ok, "a null value for a non-nullable column must serve page 1")
	assert.Empty(t, sql)
	assert.Empty(t, args)

	// The same payload against a column that really is nullable is fine.
	nullOrd := newKeysetOrdering(testNullCol, "email", "t.id", "email", "DESC")
	_, _, ok = nullOrd.cursorPredicate(nullOrd.nextCursor(nil, testCursorID))
	assert.True(t, ok, "a nullable column still takes its null arm")
}

// nullAs is for columns that are NULLable in SQL but land in a Go field that cannot
// represent NULL (domain.Deal.Value is a float64, so a NULL numeric scans as 0).
// The ORDER BY and the predicate must BOTH fold the NULL, or the cursor carries 0
// while the ordering sorted the row as NULL and the walk compares against a value
// that row never had.
func TestKeysetColumn_NullAsFoldsTheNullIntoBothClauses(t *testing.T) {
	coalesced := map[string]keysetColumn{"value": {col: "t.value", kind: keysetNumber, nullAs: "0"}}
	ord := newKeysetOrdering(coalesced, "value", "t.id", "value", "DESC")

	assert.Equal(t, "COALESCE(t.value, 0) DESC, t.id DESC", ord.orderClause())

	sql, args, ok := ord.cursorPredicate(ord.nextCursor(0.0, testCursorID))
	require.True(t, ok)
	assert.Equal(t, "((COALESCE(t.value, 0), t.id) < (?, ?))", sql,
		"the predicate must compare the SAME expression the ORDER BY sorted on")
	assert.Equal(t, []any{0.0, testCursorID}, args)

	// A folded column is answered for NULLs, and must not ALSO take the null arms:
	// after the COALESCE there is no null block for a cursor to be inside of.
	assert.True(t, coalesced["value"].handlesNull())
	assert.False(t, coalesced["value"].nullable)

	// A plain column is unchanged — no COALESCE appears anywhere.
	plain := newKeysetOrdering(testNumCol, "value", "t.id", "value", "DESC")
	assert.Equal(t, "t.value DESC, t.id DESC", plain.orderClause())
}

// Whatever else changes, a cursor must stay opaque-but-inspectable in exactly one
// direction: it never carries a raw SQL fragment, only a version, a sort key, a
// direction, a JSON value and an id.
func TestKeysetCursor_WireFormatIsPrefixedBase64JSON(t *testing.T) {
	ord := newKeysetOrdering(testTextCol, "title", "t.id", "title", "ASC")
	raw := ord.nextCursor("x", testCursorID)

	body, ok := strings.CutPrefix(raw, keysetCursorPrefix)
	require.True(t, ok, "cursor must carry the %q version prefix", keysetCursorPrefix)

	b, err := base64.StdEncoding.DecodeString(body)
	require.NoError(t, err)

	var c keysetCursor
	require.NoError(t, json.Unmarshal(b, &c))
	assert.Equal(t, keysetCursorVersion, c.Ver)
	assert.Equal(t, "title", c.Sort)
	assert.Equal(t, "ASC", c.Dir)
	assert.Equal(t, testCursorID, c.ID)
}
