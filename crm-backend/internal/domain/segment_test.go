package domain

import (
	"sort"
	"testing"
)

func TestSegmentFilter_Touched(t *testing.T) {
	ast := SegmentFilter{Op: "and", Rules: []SegmentFilter{
		{Field: "email", Operator: "eq", Value: "x"},
		{Op: "or", Rules: []SegmentFilter{
			{Field: "lead_source", Operator: "eq", Value: "web"},
			{TagID: "tag-1"},
		}},
		{Op: "not", Rules: []SegmentFilter{{TagID: "tag-2"}}},
	}}
	var fields, tags []string
	ast.Touched(&fields, &tags)
	sort.Strings(fields)
	sort.Strings(tags)

	wantFields := []string{"email", "lead_source"}
	if len(fields) != 2 || fields[0] != wantFields[0] || fields[1] != wantFields[1] {
		t.Fatalf("touched fields = %v, want %v", fields, wantFields)
	}
	wantTags := []string{"tag-1", "tag-2"}
	if len(tags) != 2 || tags[0] != wantTags[0] || tags[1] != wantTags[1] {
		t.Fatalf("touched tags = %v, want %v", tags, wantTags)
	}
}

func TestSegmentFilter_Kinds(t *testing.T) {
	if !(SegmentFilter{TagID: "t"}).IsTagLeaf() {
		t.Fatal("tag leaf")
	}
	if !(SegmentFilter{Field: "email"}).IsFieldLeaf() {
		t.Fatal("field leaf")
	}
	// A tag id takes precedence over a field on a malformed node.
	if (SegmentFilter{TagID: "t", Field: "email"}).IsFieldLeaf() {
		t.Fatal("tag id must take precedence — not a field leaf")
	}
}
