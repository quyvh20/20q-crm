package marketing

import (
	"encoding/json"
	"strings"
	"testing"
)

// roundTripDoc mirrors what the handlers do on save: parse the wire JSON into the
// struct and re-marshal it, so a field the struct does not carry is dropped.
func roundTripDoc(t *testing.T, doc BlockDocument) BlockDocument {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out BlockDocument
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func libBlock() Block {
	return Block{Type: BlockText, Text: "<p>Updated footer copy</p>", Bg: "#f1f5f9"}
}

func TestSyncedBlocks_ReplacesRootAndNestedInstances(t *testing.T) {
	blocks := []Block{
		{ID: "a", Type: BlockText, Text: "<p>untouched</p>"},
		{ID: "hdr", Ref: "ref-1", Type: BlockText, Text: "<p>old copy</p>", Bg: "#ffffff"},
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{
			{{ID: "n1", Ref: "ref-1", Type: BlockText, Text: "<p>old copy</p>"}},
			{{ID: "n2", Type: BlockText, Text: "<p>also untouched</p>"}},
		}},
		{ID: "other", Ref: "ref-2", Type: BlockText, Text: "<p>a different synced block</p>"},
	}
	next, n := replaceRefInstances(blocks, "ref-1", libBlock())
	if n != 2 {
		t.Fatalf("expected 2 instances replaced (root + nested), got %d", n)
	}
	// Content swapped...
	if next[1].Text != "<p>Updated footer copy</p>" || next[1].Bg != "#f1f5f9" {
		t.Fatalf("root instance not updated: %+v", next[1])
	}
	if next[2].Columns[0][0].Text != "<p>Updated footer copy</p>" {
		t.Fatalf("nested instance not updated: %+v", next[2].Columns[0][0])
	}
	// ...but identity preserved, or the document's React keys and its link break.
	if next[1].ID != "hdr" || next[1].Ref != "ref-1" {
		t.Fatalf("instance identity must survive: %+v", next[1])
	}
	if next[2].Columns[0][0].ID != "n1" || next[2].Columns[0][0].Ref != "ref-1" {
		t.Fatalf("nested identity must survive: %+v", next[2].Columns[0][0])
	}
	// Unrelated blocks and other refs are untouched.
	if next[0].Text != "<p>untouched</p>" || next[3].Text != "<p>a different synced block</p>" {
		t.Fatalf("unrelated blocks must not change: %+v", next)
	}
	if next[2].Columns[1][0].Text != "<p>also untouched</p>" {
		t.Fatalf("sibling column content must not change")
	}
}

// A columns block is illegal inside a column (compileBlock drops nested columns),
// so propagating one into a nested instance must refuse rather than corrupt the
// document into something the compiler silently prunes.
func TestSyncedBlocks_RefusesColumnsIntoAColumn(t *testing.T) {
	blocks := []Block{
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{
			{{ID: "n1", Ref: "ref-1", Type: BlockText, Text: "<p>keep me</p>"}},
			{},
		}},
		{ID: "root", Ref: "ref-1", Type: BlockText, Text: "<p>replace me</p>"},
	}
	lib := Block{Type: BlockColumns, Columns: [][]Block{
		{{ID: "x", Type: BlockText, Text: "<p>left</p>"}},
		{{ID: "y", Type: BlockText, Text: "<p>right</p>"}},
	}}
	next, n := replaceRefInstances(blocks, "ref-1", lib)
	if next[0].Columns[0][0].Text != "<p>keep me</p>" {
		t.Fatalf("a columns block must not be pushed into a column: %+v", next[0].Columns[0][0])
	}
	if next[1].Type != BlockColumns {
		t.Fatalf("the ROOT instance should still be replaced, got %+v", next[1])
	}
	if n != 1 {
		t.Fatalf("only the legal instance counts as replaced, got %d", n)
	}
}

// Two instances of the same synced columns block in one document must not end up
// sharing nested ids — duplicate React keys break the canvas.
func TestSyncedBlocks_NestedIdsStayUniquePerInstance(t *testing.T) {
	blocks := []Block{
		{ID: "one", Ref: "ref-1", Type: BlockColumns},
		{ID: "two", Ref: "ref-1", Type: BlockColumns},
	}
	lib := Block{Type: BlockColumns, Columns: [][]Block{
		{{ID: "shared", Type: BlockText, Text: "<p>a</p>"}},
		{{ID: "shared2", Type: BlockText, Text: "<p>b</p>"}},
	}}
	next, n := replaceRefInstances(blocks, "ref-1", lib)
	if n != 2 {
		t.Fatalf("expected both instances replaced, got %d", n)
	}
	seen := map[string]bool{}
	for _, b := range next {
		for _, col := range b.Columns {
			for _, sub := range col {
				if seen[sub.ID] {
					t.Fatalf("duplicate nested id across instances: %q", sub.ID)
				}
				seen[sub.ID] = true
			}
		}
	}
	// The two instances must also not share the same backing slice.
	next[0].Columns[0][0].Text = "<p>mutated</p>"
	if next[1].Columns[0][0].Text == "<p>mutated</p>" {
		t.Fatalf("instances must not share nested storage")
	}
}

// The compiler honours a "show only if" ONLY on a root section: columnInner
// emits no condition markers, so a condition copied into a column is silently
// ignored and the block ships to EVERY recipient. Propagation must therefore
// strip the root-only fields when it lands in a column.
func TestSyncedBlocks_StripsRootOnlyFieldsWhenNested(t *testing.T) {
	padY := 40
	lib := Block{
		Type: BlockText, Text: "<p>Pro-only offer</p>",
		Cond:      &BlockCondition{Field: "contact.custom_fields.plan_tier", Op: "eq", Value: "pro"},
		Bg:        "#0f172a",
		BgURL:     "https://example.com/bg.png",
		FullWidth: true,
		PadY:      &padY,
	}
	blocks := []Block{
		{ID: "root", Ref: "ref-1", Type: BlockText},
		{ID: "cols", Type: BlockColumns, Columns: [][]Block{
			{{ID: "n1", Ref: "ref-1", Type: BlockText}},
			{},
		}},
	}
	next, n := replaceRefInstances(blocks, "ref-1", lib)
	if n != 2 {
		t.Fatalf("expected both instances replaced, got %d", n)
	}
	// The ROOT instance keeps everything — that is where the compiler honours it.
	if next[0].Cond == nil || next[0].Bg != "#0f172a" || !next[0].FullWidth {
		t.Fatalf("root instance must keep root-only fields: %+v", next[0])
	}
	// The NESTED instance must not carry fields the compiler would ignore.
	nested := next[1].Columns[0][0]
	if nested.Cond != nil {
		t.Fatalf("a condition must never ride into a column — it would be ignored and the block would send to everyone: %+v", nested)
	}
	if nested.Bg != "" || nested.BgURL != "" || nested.FullWidth || nested.PadX != nil || nested.PadY != nil {
		t.Fatalf("root-only styling must be stripped when nested: %+v", nested)
	}
	// The CONTENT still propagates.
	if nested.Text != "<p>Pro-only offer</p>" {
		t.Fatalf("nested instance should still receive the content: %+v", nested)
	}
}

// Generated nested ids must not look like ids a document could already contain.
func TestSyncedBlocks_GeneratedNestedIdsAreDistinctive(t *testing.T) {
	blocks := []Block{{ID: "blk_x", Ref: "ref-1", Type: BlockColumns}}
	lib := Block{Type: BlockColumns, Columns: [][]Block{{{ID: "a", Type: BlockText}}, {{ID: "b", Type: BlockText}}}}
	next, _ := replaceRefInstances(blocks, "ref-1", lib)
	for _, col := range next[0].Columns {
		for _, sub := range col {
			if !strings.Contains(sub.ID, "~sync") {
				t.Fatalf("generated ids must be marked so they cannot collide with authored ids, got %q", sub.ID)
			}
		}
	}
}

func TestSyncedBlocks_CountsInstances(t *testing.T) {
	blocks := []Block{
		{ID: "a", Ref: "ref-1", Type: BlockText},
		{ID: "b", Type: BlockColumns, Columns: [][]Block{
			{{ID: "n1", Ref: "ref-1", Type: BlockText}, {ID: "n2", Type: BlockText}},
			{{ID: "n3", Ref: "ref-1", Type: BlockText}},
		}},
		{ID: "c", Ref: "other", Type: BlockText},
	}
	if got := countRefInstances(blocks, "ref-1"); got != 3 {
		t.Fatalf("expected 3 instances, got %d", got)
	}
	if got := countRefInstances(blocks, "nope"); got != 0 {
		t.Fatalf("expected 0 for an unused ref, got %d", got)
	}
}

// Ref must survive the parse/re-marshal round trip the handlers perform, or a
// synced instance would quietly become an ordinary copy on the next save.
func TestSyncedBlocks_RefSurvivesTheWireRoundTrip(t *testing.T) {
	doc := BlockDocument{Blocks: []Block{
		{ID: "a", Ref: "ref-1", Type: BlockText, Text: "<p>hi</p>"},
	}}
	round := roundTripDoc(t, doc)
	if round.Blocks[0].Ref != "ref-1" {
		t.Fatalf("ref lost in the round trip: %+v", round.Blocks[0])
	}
	// And an ordinary block still emits no ref at all.
	plain := roundTripDoc(t, BlockDocument{Blocks: []Block{{ID: "b", Type: BlockText}}})
	if plain.Blocks[0].Ref != "" {
		t.Fatalf("a non-synced block must carry no ref")
	}
	if strings.Contains(mustJSON(t, plain), `"ref"`) {
		t.Fatalf("ref must be omitted when empty (byte-stability for existing docs)")
	}
}
