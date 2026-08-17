package domain

import (
	"math"
	"strings"
	"testing"
)

// ============================================================
// Filter SQL compiler — the NEW operators (filtering overhaul F1)
// ============================================================
//
// The compiler is pure (no DB handle), so these run without a database. They
// pin the four operators this overhaul added — `on`, `between`, `last_n_days`,
// `next_n_days` — plus the compiler-wide invariants the move to domain must not
// have loosened: the operator matrix, the depth/rule caps, and the gorm
// placeholder discipline (no literal `?` in SQL text that is not a bind site).

func fltRef() FilterTableRef {
	return FilterTableRef{Table: "contacts", JSONColumn: "custom_fields"}
}

// fltCatalog covers both storage kinds for the types the new operators accept:
// a native number/date column AND a jsonb number/date extraction — the jsonb
// side is where the guarded casts (and historically the `?`-quantifier trap)
// live.
func fltCatalog() []ReportField {
	return []ReportField{
		{Key: "email", Label: "Email", Type: "text", Column: "email"},
		{Key: "value", Label: "Value", Type: "number", Column: "value"},
		{Key: "sqft", Label: "Sq Ft", Type: "number", JSONKey: "sqft"},
		{Key: "created_at", Label: "Created", Type: "date", Column: "created_at"},
		{Key: "listed_on", Label: "Listed", Type: "date", JSONKey: "listed_on"},
		{Key: "company", Label: "Company", Type: "relation", Column: "company_id"},
	}
}

func fltFields() map[string]ReportField {
	m := make(map[string]ReportField, len(fltCatalog()))
	for _, f := range fltCatalog() {
		m[f.Key] = f
	}
	return m
}

func fltLeaf(t *testing.T, key, op string, val any) (string, []any, error) {
	t.Helper()
	n := 0
	return BuildFilterLeaf(fltRef(), fltFields(), key, op, val, &n)
}

// ============================================================
// `on` — day-granular date equality
// ============================================================

// `on` must compile to the two-sided civil-day range, with the day string bound
// TWICE — one bind reused for both sides would misalign every later placeholder
// in the enclosing group.
func TestFilterLeaf_On_ColumnDate(t *testing.T) {
	sql, args, err := fltLeaf(t, "created_at", "on", "2026-08-15")
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	want := "(contacts.created_at >= (?)::date AND contacts.created_at < ((?)::date + INTERVAL '1 day'))"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 2 || args[0] != "2026-08-15" || args[1] != "2026-08-15" {
		t.Errorf("args = %v, want the day bound twice", args)
	}
}

// A jsonb date goes through the NULLIF timestamptz cast on BOTH sides of the
// range; the day is still bound twice.
func TestFilterLeaf_On_JSONBDate(t *testing.T) {
	sql, args, err := fltLeaf(t, "listed_on", "on", "2026-01-02")
	if err != nil {
		t.Fatalf("on: %v", err)
	}
	typed := "(NULLIF(contacts.custom_fields->>'listed_on', ''))::timestamptz"
	if got := strings.Count(sql, typed); got != 2 {
		t.Errorf("typed expr appears %d times, want 2 (both range sides):\n%s", got, sql)
	}
	if len(args) != 2 || args[0] != "2026-01-02" || args[1] != "2026-01-02" {
		t.Errorf("args = %v, want the day bound twice", args)
	}
}

// Leading/trailing whitespace is trimmed rather than rejected (what a pasted
// value looks like), and the TRIMMED day is what gets bound.
func TestFilterLeaf_On_TrimsWhitespace(t *testing.T) {
	_, args, err := fltLeaf(t, "created_at", "on", "  2026-08-15  ")
	if err != nil {
		t.Fatalf("on with padding: %v", err)
	}
	if len(args) != 2 || args[0] != "2026-08-15" {
		t.Errorf("args = %v, want the trimmed day", args)
	}
}

// Anything that is not exactly YYYY-MM-DD is a validation error, not a bind:
// the value is cast with ::date in SQL, so letting a bad one through would
// surface as a Postgres error instead of a 400.
func TestFilterLeaf_On_RejectsNonDate(t *testing.T) {
	for name, val := range map[string]any{
		"free text":         "yesterday",
		"single-digit day":  "2026-8-15",
		"eu format":         "15/08/2026",
		"datetime":          "2026-08-15T10:00:00Z", // day-granular means DAY only
		"number":            float64(20260815),
		"empty":             "",
		"nil":               nil,
	} {
		if _, _, err := fltLeaf(t, "created_at", "on", val); err == nil {
			t.Errorf("%s: on accepted %#v, want an error", name, val)
		}
	}
}

// ============================================================
// `between` — inclusive two-value range
// ============================================================

func TestFilterLeaf_Between_NumberColumn(t *testing.T) {
	sql, args, err := fltLeaf(t, "value", "between", []any{float64(10), float64(20)})
	if err != nil {
		t.Fatalf("between: %v", err)
	}
	want := "(contacts.value >= ? AND contacts.value <= ?)"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 2 || args[0] != float64(10) || args[1] != float64(20) {
		t.Errorf("args = %v, want [10 20]", args)
	}
}

// A jsonb number field must go through the guarded-cast expression (one dirty
// row NULLs out instead of killing the query) — and that expression's
// numeric-validity regex must use {0,1}, never `?` quantifiers, because gorm
// treats EVERY literal `?` in the SQL text as a bind placeholder.
func TestFilterLeaf_Between_JSONBNumber(t *testing.T) {
	sql, args, err := fltLeaf(t, "sqft", "between", []any{float64(1000), float64(2000)})
	if err != nil {
		t.Fatalf("between: %v", err)
	}
	typed := `(CASE WHEN contacts.custom_fields->>'sqft' ~ '^-{0,1}[0-9]+(\.[0-9]+){0,1}$' THEN (contacts.custom_fields->>'sqft')::numeric END)`
	want := "(" + typed + " >= ? AND " + typed + " <= ?)"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v, want 2", args)
	}
	// The regression guard itself: exactly as many `?` as bind args.
	if strings.Count(sql, "?") != len(args) {
		t.Errorf("placeholder count %d != args %d — a literal `?` leaked into the SQL text:\n%s", strings.Count(sql, "?"), len(args), sql)
	}
}

func TestFilterLeaf_Between_Date(t *testing.T) {
	sql, args, err := fltLeaf(t, "created_at", "between", []any{"2026-01-01", "2026-12-31"})
	if err != nil {
		t.Fatalf("between: %v", err)
	}
	want := "(contacts.created_at >= ? AND contacts.created_at <= ?)"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 2 || args[0] != "2026-01-01" || args[1] != "2026-12-31" {
		t.Errorf("args = %v", args)
	}
}

// between takes EXACTLY two values — a single scalar (which the list coercion
// treats as a one-element list), a one-element list, and a three-element list
// are all arity errors, never a silently degenerate range.
func TestFilterLeaf_Between_RejectsWrongArity(t *testing.T) {
	for name, val := range map[string]any{
		"bare scalar": float64(5),
		"one value":   []any{float64(5)},
		"three":       []any{float64(1), float64(2), float64(3)},
		"empty list":  []any{},
	} {
		if _, _, err := fltLeaf(t, "value", "between", val); err == nil {
			t.Errorf("%s: between accepted %#v, want an arity error", name, val)
		}
	}
	// Element type errors surface too — a text bound on a number field is a 400,
	// not a Postgres cast failure.
	if _, _, err := fltLeaf(t, "value", "between", []any{"low", "high"}); err == nil {
		t.Error("between accepted non-numeric bounds on a number field")
	}
}

// ============================================================
// `last_n_days` / `next_n_days` — DB-clock-relative ranges
// ============================================================

func TestFilterLeaf_LastNDays(t *testing.T) {
	sql, args, err := fltLeaf(t, "created_at", "last_n_days", float64(7))
	if err != nil {
		t.Fatalf("last_n_days: %v", err)
	}
	want := "(contacts.created_at >= NOW() - (? * INTERVAL '1 day') AND contacts.created_at <= NOW())"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	// The day count binds as an INT (it multiplies an interval), not the float
	// the JSON decoder produced.
	if len(args) != 1 || args[0] != 7 {
		t.Errorf("args = %v (%T), want [7] as int", args, args[0])
	}
}

func TestFilterLeaf_NextNDays(t *testing.T) {
	sql, args, err := fltLeaf(t, "created_at", "next_n_days", "30") // string form is fine
	if err != nil {
		t.Fatalf("next_n_days: %v", err)
	}
	want := "(contacts.created_at >= NOW() AND contacts.created_at <= NOW() + (? * INTERVAL '1 day'))"
	if sql != want {
		t.Errorf("sql mismatch:\n got: %s\nwant: %s", sql, want)
	}
	if len(args) != 1 || args[0] != 30 {
		t.Errorf("args = %v, want [30]", args)
	}
}

// 1..3650 inclusive is the whole domain: both boundaries compile, and 0,
// negatives, over-cap and non-numeric values are all validation errors.
func TestFilterLeaf_RelativeDays_Bounds(t *testing.T) {
	for _, op := range []string{"last_n_days", "next_n_days"} {
		for _, ok := range []any{float64(1), float64(3650)} {
			if _, _, err := fltLeaf(t, "created_at", op, ok); err != nil {
				t.Errorf("%s(%v): unexpected error %v", op, ok, err)
			}
		}
		for name, bad := range map[string]any{
			"zero":        float64(0),
			"negative":    float64(-1),
			"over cap":    float64(3651),
			"non-numeric": "abc",
			"nil":         nil,
			// ParseFloat accepts these spellings, and every comparison with NaN
			// is false — so a bound written as (n < 1 || n > 3650) waves NaN
			// through to int(n), whose result is implementation-defined
			// (MinInt64 on amd64 → an out-of-range interval → a 500). The guard
			// must be the negated-AND form.
			"NaN":       "NaN",
			"Inf":       "Inf",
			"-Inf":      "-Inf",
			"NaN float": math.NaN(),
		} {
			if _, _, err := fltLeaf(t, "created_at", op, bad); err == nil {
				t.Errorf("%s accepted %s (%#v), want an error", op, name, bad)
			}
		}
	}
}

// ============================================================
// Value validation added after review — save-then-500 killers
// ============================================================

// Date `between` bounds must validate at compile like `on` does: the saved-view
// dry-run approving a bound Postgres will reject at run time turns "a view that
// saves is a view that replays" into "saves fine, 500s on every open".
func TestFilterLeaf_Between_DateBoundsValidated(t *testing.T) {
	for name, ok := range map[string]any{
		"civil dates": []any{"2026-01-01", "2026-12-31"},
		"rfc3339":     []any{"2026-01-01T00:00:00Z", "2026-12-31T23:59:59Z"},
	} {
		if _, _, err := fltLeaf(t, "created_at", "between", ok); err != nil {
			t.Errorf("between rejected %s: %v", name, err)
		}
	}
	for name, bad := range map[string]any{
		"free text":   []any{"soon", "2026-12-31"},
		"eu format":   []any{"2026-01-01", "31/12/2026"},
		"number-ish":  []any{"2026-01-01", float64(42)},
	} {
		if _, _, err := fltLeaf(t, "created_at", "between", bad); err == nil {
			t.Errorf("between accepted %s (%#v) — Postgres would 22007 it at run time", name, bad)
		}
	}
}

// Relation values must be UUIDs at compile: junk against a native uuid column
// is a Postgres 22P02 → 500; against jsonb it silently matches nothing — both
// are the failure shapes this engine exists to turn into 400s.
func TestFilterLeaf_RelationValueMustBeUUID(t *testing.T) {
	id := "b3a4c0de-0000-4000-8000-000000000001"
	if _, _, err := fltLeaf(t, "company", "eq", id); err != nil {
		t.Fatalf("valid uuid rejected: %v", err)
	}
	if _, _, err := fltLeaf(t, "company", "in", []any{id, id}); err != nil {
		t.Fatalf("valid uuid list rejected: %v", err)
	}
	for name, bad := range map[string]any{
		"junk":      "acme",
		"empty":     "",
		"number":    float64(7),
		"junk list": []any{id, "acme"},
	} {
		op := "eq"
		if _, isList := bad.([]any); isList {
			op = "in"
		}
		if _, _, err := fltLeaf(t, "company", op, bad); err == nil {
			t.Errorf("relation %s accepted %s (%#v), want a 400-shaped error", op, name, bad)
		}
	}
}

// ============================================================
// Placeholder discipline — the gorm literal-? regression guard
// ============================================================

// Every fragment a new operator emits must contain exactly as many `?` as it
// has bind args. gorm's db.Raw/.Where treat every literal `?` in the TEXT —
// even inside a quoted string — as a positional placeholder, so one stray `?`
// (say, a regex quantifier in the jsonb numeric guard) steals a bind and
// misaligns the rest of the query. Compiled over the JSONB fields on purpose:
// the guarded casts are where that trap has actually bitten.
func TestFilterNewOperators_PlaceholderDiscipline(t *testing.T) {
	cases := []struct {
		name, key, op string
		val           any
	}{
		{"on jsonb date", "listed_on", "on", "2026-08-15"},
		{"between jsonb number", "sqft", "between", []any{float64(1), float64(2)}},
		{"last_n_days jsonb date", "listed_on", "last_n_days", float64(14)},
		{"next_n_days jsonb date", "listed_on", "next_n_days", float64(14)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := fltLeaf(t, tc.key, tc.op, tc.val)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got, want := strings.Count(sql, "?"), len(args); got != want {
				t.Errorf("%d placeholders for %d args:\n%s", got, want, sql)
			}
		})
	}

	// And once through the whole tree, all four operators ANDed together — the
	// misalignment only bites when fragments concatenate.
	g := &ReportFilterGroup{Op: "AND", Rules: []ReportFilterRule{
		{Field: "listed_on", Operator: "on", Value: "2026-08-15"},
		{Field: "sqft", Operator: "between", Value: []any{float64(1), float64(2)}},
		{Field: "listed_on", Operator: "last_n_days", Value: float64(7)},
		{Field: "listed_on", Operator: "next_n_days", Value: float64(7)},
	}}
	compiled, err := CompileRecordFilter(fltRef(), fltCatalog(), g)
	if err != nil {
		t.Fatalf("compile tree: %v", err)
	}
	if got, want := strings.Count(compiled.SQL, "?"), len(compiled.Args); got != want {
		t.Errorf("tree: %d placeholders for %d args:\n%s", got, want, compiled.SQL)
	}
}

// ============================================================
// Operator matrix
// ============================================================

func TestFilterOperatorAllowed_Matrix(t *testing.T) {
	cases := []struct {
		typ, op string
		want    bool
	}{
		// The new operators land exactly where the matrix says and nowhere else.
		{"date", "on", true},
		{"number", "on", false},
		{"text", "on", false},
		{"number", "between", true},
		{"date", "between", true},
		{"relation", "between", false},
		{"text", "between", false},
		{"boolean", "between", false},
		{"date", "last_n_days", true},
		{"date", "next_n_days", true},
		{"number", "last_n_days", false},
		{"relation", "next_n_days", false},
		// Unknown tokens on either axis are a hard no, not a pass-through.
		{"date", "regex", false},
		{"nonsense", "eq", false},
	}
	for _, tc := range cases {
		if got := FilterOperatorAllowed(tc.typ, tc.op); got != tc.want {
			t.Errorf("FilterOperatorAllowed(%q, %q) = %v, want %v", tc.typ, tc.op, got, tc.want)
		}
	}

	// The set form is DERIVED from the ordered menu; if the derivation ever
	// drifts, the UI would offer an operator the compiler rejects (or hide one
	// it accepts).
	for typ, ops := range FilterOperatorsOrdered {
		for _, op := range ops {
			if !FilterOperatorAllowed(typ, op) {
				t.Errorf("ordered matrix lists %s/%s but the set form denies it", typ, op)
			}
		}
	}
}

// ============================================================
// CompileRecordFilter — entry-point contract and caps
// ============================================================

func TestCompileRecordFilter_NilGroupIsNilNil(t *testing.T) {
	compiled, err := CompileRecordFilter(fltRef(), fltCatalog(), nil)
	if err != nil || compiled != nil {
		t.Fatalf("nil group = (%v, %v), want (nil, nil)", compiled, err)
	}
}

// A group with no rules (and no leaf fields) compiles to nothing — (nil, nil),
// not an empty-string fragment a repository would AND on as "()".
func TestCompileRecordFilter_EmptyRulesIsNilNil(t *testing.T) {
	compiled, err := CompileRecordFilter(fltRef(), fltCatalog(), &ReportFilterGroup{Op: "AND"})
	if err != nil || compiled != nil {
		t.Fatalf("empty group = (%v, %v), want (nil, nil)", compiled, err)
	}
}

// The field key must resolve in the caller-supplied catalog — injection rule 1.
func TestCompileRecordFilter_UnknownFieldErrors(t *testing.T) {
	_, err := CompileRecordFilter(fltRef(), fltCatalog(), &ReportFilterGroup{
		Field: "no_such_field", Operator: "eq", Value: "x",
	})
	if err == nil {
		t.Fatal("unknown field compiled, want an error")
	}
	if !strings.Contains(err.Error(), "unknown filter field") {
		t.Errorf("error = %q, want it to name the unknown field", err)
	}
}

// fltNested wraps a leaf in n nested groups under a root group. The root sits
// at depth 0, so the innermost group lands at depth n.
func fltNested(n int) *ReportFilterGroup {
	rule := ReportFilterRule{Field: "email", Operator: "eq", Value: "x"}
	for i := 0; i < n; i++ {
		rule = ReportFilterRule{Op: "AND", Rules: []ReportFilterRule{rule}}
	}
	return &ReportFilterGroup{Op: "AND", Rules: []ReportFilterRule{rule}}
}

func TestCompileRecordFilter_DepthCap(t *testing.T) {
	// MaxFilterDepth deep still compiles…
	if _, err := CompileRecordFilter(fltRef(), fltCatalog(), fltNested(MaxFilterDepth)); err != nil {
		t.Fatalf("depth %d should compile: %v", MaxFilterDepth, err)
	}
	// …one level deeper is refused (a hostile AST can't recurse the compiler).
	_, err := CompileRecordFilter(fltRef(), fltCatalog(), fltNested(MaxFilterDepth+1))
	if err == nil {
		t.Fatalf("depth %d compiled, want the cap error", MaxFilterDepth+1)
	}
	if !strings.Contains(err.Error(), "nested deeper") {
		t.Errorf("error = %q, want the depth-cap message", err)
	}
}

func TestCompileRecordFilter_RuleCountCap(t *testing.T) {
	leaf := ReportFilterRule{Field: "email", Operator: "eq", Value: "x"}
	atCap := &ReportFilterGroup{Op: "AND"}
	for i := 0; i < MaxFilterRules; i++ {
		atCap.Rules = append(atCap.Rules, leaf)
	}
	if _, err := CompileRecordFilter(fltRef(), fltCatalog(), atCap); err != nil {
		t.Fatalf("%d rules should compile: %v", MaxFilterRules, err)
	}
	overCap := &ReportFilterGroup{Op: "AND", Rules: append(append([]ReportFilterRule{}, atCap.Rules...), leaf)}
	if _, err := CompileRecordFilter(fltRef(), fltCatalog(), overCap); err == nil {
		t.Fatalf("%d rules compiled, want the rule-cap error", MaxFilterRules+1)
	}
}
