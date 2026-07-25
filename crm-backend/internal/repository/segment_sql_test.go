package repository

import (
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

func segCatalog() []domain.ReportField {
	return []domain.ReportField{
		{Key: "email", Label: "Email", Type: "text", Column: "email"},
		{Key: "first_name", Label: "First", Type: "text", Column: "first_name"},
		{Key: "lead_source", Label: "Lead Source", Type: "text", JSONKey: "lead_source"},
		{Key: "score", Label: "Score", Type: "number", JSONKey: "score"},
		{Key: "owner_user_id", Label: "Owner", Type: "relation", Column: "owner_user_id"},
	}
}

func fieldMapT(c []domain.ReportField) map[string]domain.ReportField {
	m := make(map[string]domain.ReportField, len(c))
	for _, f := range c {
		m[f.Key] = f
	}
	return m
}

func compileGroup(t *testing.T, ast domain.SegmentFilter) (string, []any) {
	t.Helper()
	leafCount := 0
	sql, args, err := buildSegmentGroup(contactsSegmentRef(), fieldMapT(segCatalog()), ast, 1, &leafCount)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return sql, args
}

func TestSegment_FieldLeaf_ColumnEq(t *testing.T) {
	sql, args := compileGroup(t, domain.SegmentFilter{Field: "email", Operator: "eq", Value: "a@b.com"})
	if !strings.Contains(sql, "contacts.email = ?") {
		t.Fatalf("want column eq, got %q", sql)
	}
	if len(args) != 1 || args[0] != "a@b.com" {
		t.Fatalf("value must be a single bind arg, got %v", args)
	}
	if strings.Contains(sql, "a@b.com") {
		t.Fatalf("value must NOT be interpolated into SQL: %q", sql)
	}
}

func TestSegment_JSONBLeaf(t *testing.T) {
	sql, args := compileGroup(t, domain.SegmentFilter{Field: "lead_source", Operator: "eq", Value: "web"})
	if !strings.Contains(sql, "contacts.custom_fields->>'lead_source' = ?") {
		t.Fatalf("want jsonb extraction, got %q", sql)
	}
	if len(args) != 1 || args[0] != "web" {
		t.Fatalf("args=%v", args)
	}
}

func TestSegment_NumberRangeGuardedCast(t *testing.T) {
	sql, args := compileGroup(t, domain.SegmentFilter{Field: "score", Operator: "gt", Value: 10})
	if !strings.Contains(sql, "::numeric") || !strings.Contains(sql, "> ?") {
		t.Fatalf("number jsonb range needs a guarded numeric cast + bound value, got %q", sql)
	}
	if len(args) != 1 {
		t.Fatalf("args=%v", args)
	}
}

func TestSegment_TagLeaf(t *testing.T) {
	tag := uuid.New()
	sql, args := compileGroup(t, domain.SegmentFilter{TagID: tag.String()})
	if !strings.Contains(sql, "EXISTS (SELECT 1 FROM contact_tags ct WHERE ct.contact_id = contacts.id AND ct.tag_id = ?)") {
		t.Fatalf("tag leaf must be a correlated EXISTS, got %q", sql)
	}
	if strings.HasPrefix(sql, "NOT ") {
		t.Fatalf("non-negated tag must not be NOT: %q", sql)
	}
	if len(args) != 1 || args[0] != tag {
		t.Fatalf("tag id must be a single bound uuid arg, got %v", args)
	}
}

func TestSegment_TagLeafNegated(t *testing.T) {
	sql, _ := compileGroup(t, domain.SegmentFilter{TagID: uuid.New().String(), TagNot: true})
	if !strings.HasPrefix(sql, "NOT EXISTS") {
		t.Fatalf("negated tag must be NOT EXISTS, got %q", sql)
	}
}

func TestSegment_AndOrGroups(t *testing.T) {
	and := domain.SegmentFilter{Op: "and", Rules: []domain.SegmentFilter{
		{Field: "email", Operator: "contains", Value: "acme"},
		{Field: "first_name", Operator: "is_not_empty"},
	}}
	sql, _ := compileGroup(t, and)
	if !strings.Contains(sql, " AND ") || !strings.HasPrefix(sql, "(") {
		t.Fatalf("AND group: %q", sql)
	}
	or := domain.SegmentFilter{Op: "or", Rules: []domain.SegmentFilter{
		{Field: "email", Operator: "eq", Value: "a@b.com"},
		{Field: "email", Operator: "eq", Value: "c@d.com"},
	}}
	sql, _ = compileGroup(t, or)
	if !strings.Contains(sql, " OR ") {
		t.Fatalf("OR group: %q", sql)
	}
}

func TestSegment_NotGroup(t *testing.T) {
	not := domain.SegmentFilter{Op: "not", Rules: []domain.SegmentFilter{
		{TagID: uuid.New().String()},
	}}
	sql, _ := compileGroup(t, not)
	if !strings.HasPrefix(sql, "(NOT ") {
		t.Fatalf("NOT group must wrap in (NOT ...), got %q", sql)
	}
}

func TestSegment_ContainsEscapesLike(t *testing.T) {
	sql, args := compileGroup(t, domain.SegmentFilter{Field: "email", Operator: "contains", Value: "50%_x"})
	if !strings.Contains(sql, "ILIKE ?") {
		t.Fatalf("contains → ILIKE, got %q", sql)
	}
	pat, _ := args[0].(string)
	if !strings.Contains(pat, `\%`) || !strings.Contains(pat, `\_`) {
		t.Fatalf("LIKE wildcards must be escaped in the bound pattern, got %q", pat)
	}
}

func TestSegment_UnknownFieldRejected(t *testing.T) {
	leafCount := 0
	_, _, err := buildSegmentGroup(contactsSegmentRef(), fieldMapT(segCatalog()),
		domain.SegmentFilter{Field: "salary", Operator: "gt", Value: 1}, 1, &leafCount)
	if err == nil {
		t.Fatal("a field not in the catalog must be rejected (injection/whitelist)")
	}
}

func TestSegment_BadOperatorForType(t *testing.T) {
	leafCount := 0
	_, _, err := buildSegmentGroup(contactsSegmentRef(), fieldMapT(segCatalog()),
		domain.SegmentFilter{Field: "score", Operator: "contains", Value: "x"}, 1, &leafCount)
	if err == nil {
		t.Fatal("contains on a number field must be rejected by the operator whitelist")
	}
}

func TestSegment_BadTagID(t *testing.T) {
	leafCount := 0
	_, _, err := buildSegmentGroup(contactsSegmentRef(), fieldMapT(segCatalog()),
		domain.SegmentFilter{TagID: "not-a-uuid"}, 1, &leafCount)
	if err == nil {
		t.Fatal("a non-uuid tag id must be rejected")
	}
}

func TestSegment_DepthCap(t *testing.T) {
	// Nest deeper than maxSegmentFilterDepth.
	node := domain.SegmentFilter{Field: "email", Operator: "eq", Value: "x"}
	for i := 0; i < maxSegmentFilterDepth+2; i++ {
		node = domain.SegmentFilter{Op: "and", Rules: []domain.SegmentFilter{node}}
	}
	leafCount := 0
	_, _, err := buildSegmentGroup(contactsSegmentRef(), fieldMapT(segCatalog()), node, 1, &leafCount)
	if err == nil {
		t.Fatal("over-deep nesting must be rejected")
	}
}

func TestSegment_WhereAlwaysScopesOrgAndSoftDelete(t *testing.T) {
	org := uuid.New()
	where, args, err := buildSegmentWhere(fieldMapT(segCatalog()),
		domain.SegmentFilter{Field: "email", Operator: "eq", Value: "a@b.com"}, org, reportScope{Scope: domain.DataScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "contacts.org_id = ?") || !strings.Contains(where, "contacts.deleted_at IS NULL") {
		t.Fatalf("mandatory org + soft-delete predicates missing: %q", where)
	}
	if len(args) < 1 || args[0] != org {
		t.Fatalf("org id must be the first bound arg, got %v", args)
	}
	// All-scope adds no row predicate.
	if strings.Contains(where, "owner_user_id") {
		t.Fatalf("all-scope must not add a row predicate: %q", where)
	}
}

func TestSegment_WhereOwnScopeAddsRowPredicate(t *testing.T) {
	org, user := uuid.New(), uuid.New()
	where, _, err := buildSegmentWhere(fieldMapT(segCatalog()),
		domain.SegmentFilter{}, org, reportScope{Scope: domain.DataScopeOwn, UserID: user})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(where, "contacts.owner_user_id = ?") || !strings.Contains(where, "record_shares") {
		t.Fatalf("own-scope must AND the RecordAccessPredicate (owner + shares): %q", where)
	}
}

func TestSegment_ValidateFilter(t *testing.T) {
	if err := ValidateSegmentFilter(segCatalog(), domain.SegmentFilter{Field: "email", Operator: "eq", Value: "x"}); err != nil {
		t.Fatalf("valid filter must pass: %v", err)
	}
	if err := ValidateSegmentFilter(segCatalog(), domain.SegmentFilter{Field: "nope", Operator: "eq", Value: "x"}); err == nil {
		t.Fatal("unknown field must fail validation")
	}
}
