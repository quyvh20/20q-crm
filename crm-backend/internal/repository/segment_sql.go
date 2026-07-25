package repository

import (
	"fmt"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ============================================================
// Segment SQL compiler (M5)
// ============================================================
//
// A dynamic segment is a boolean AND/OR/NOT predicate over contacts. This compiler
// is a SIBLING of the P9 report builder (report_sql.go): it REUSES that builder's
// injection discipline verbatim — buildReportFilterLeaf resolves every field leaf
// via the caller-supplied catalog (SQL address from the catalog entry, operator
// through reportOperatorsByType, every value a bind arg, reportIdentRe on every
// spliced identifier), and rowPredicateFor/RecordAccessPredicate applies the SAME
// row-access rule as the list pages. Only two things are segment-specific: NOT
// groups, and a tag leaf compiling to a correlated EXISTS on contact_tags.
//
// The base WHERE is always `contacts.org_id = ? AND contacts.deleted_at IS NULL`
// plus the caller's row predicate; a callerless (M7) run passes DataScopeAll so the
// row predicate is empty and only org scoping remains.

const (
	maxSegmentFilterDepth = 5
	maxSegmentFilterRules = 100
)

// contactsSegmentRef is the fixed report table ref for contact segments.
func contactsSegmentRef() reportTableRef {
	return reportTableRef{Table: "contacts", JSONColumn: "custom_fields", Slug: "contact"}
}

// buildSegmentWhere assembles the mandatory predicates (org + soft-delete + the
// row-access predicate from the caller's scope) plus the compiled AST — mirroring
// buildReportWhere. sc is DataScopeAll for a callerless run (org-only).
func buildSegmentWhere(fields map[string]domain.ReportField, ast domain.SegmentFilter, orgID uuid.UUID, sc reportScope) (string, []any, error) {
	ref := contactsSegmentRef()
	parts := []string{ref.Table + ".org_id = ?", ref.Table + ".deleted_at IS NULL"}
	args := []any{orgID}

	if rowSQL, rowArgs := rowPredicateFor(ref, orgID, sc); rowSQL != "" {
		parts = append(parts, rowSQL)
		args = append(args, rowArgs...)
	}

	leafCount := 0
	astSQL, astArgs, err := buildSegmentGroup(ref, fields, ast, 1, &leafCount)
	if err != nil {
		return "", nil, err
	}
	if astSQL != "" {
		parts = append(parts, astSQL)
		args = append(args, astArgs...)
	}
	return strings.Join(parts, " AND "), args, nil
}

// buildSegmentGroup compiles one AST node. Precedence: tag leaf → field leaf →
// group. Field leaves REUSE buildReportFilterLeaf (identical injection discipline).
func buildSegmentGroup(ref reportTableRef, fields map[string]domain.ReportField, node domain.SegmentFilter, depth int, leafCount *int) (string, []any, error) {
	if depth > maxSegmentFilterDepth {
		return "", nil, fmt.Errorf("segment: filters nested deeper than %d levels", maxSegmentFilterDepth)
	}
	if node.IsTagLeaf() {
		return buildSegmentTagLeaf(ref, node.TagID, node.TagNot, leafCount)
	}
	if node.IsFieldLeaf() {
		return buildReportFilterLeaf(ref, fields, node.Field, node.Operator, node.Value, leafCount)
	}
	if len(node.Rules) == 0 {
		return "", nil, nil
	}
	op := strings.ToUpper(strings.TrimSpace(node.Op))
	if op == "NOT" {
		// NOT negates the AND of its rules.
		inner, iargs, err := buildSegmentGroup(ref, fields, domain.SegmentFilter{Op: "AND", Rules: node.Rules}, depth+1, leafCount)
		if err != nil {
			return "", nil, err
		}
		if inner == "" {
			return "", nil, nil
		}
		return "(NOT " + inner + ")", iargs, nil
	}
	joiner := " AND "
	if op == "OR" {
		joiner = " OR "
	}
	var sqlParts []string
	var args []any
	for _, rule := range node.Rules {
		part, pargs, err := buildSegmentGroup(ref, fields, rule, depth+1, leafCount)
		if err != nil {
			return "", nil, err
		}
		if part == "" {
			continue
		}
		sqlParts = append(sqlParts, part)
		args = append(args, pargs...)
	}
	if len(sqlParts) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(sqlParts, joiner) + ")", args, nil
}

// buildSegmentTagLeaf compiles a tag membership test to a fully-parameterized,
// correlated EXISTS on contact_tags (the tag source of truth — not object_links).
// The reverse index contact_tags(tag_id, contact_id) makes it sargable. The tag id
// is bound, never interpolated; org scoping rides the outer contacts predicate
// (contact_tags has no org_id and cascade-deletes with the contact).
func buildSegmentTagLeaf(ref reportTableRef, tagID string, not bool, leafCount *int) (string, []any, error) {
	*leafCount++
	if *leafCount > maxSegmentFilterRules {
		return "", nil, fmt.Errorf("segment: more than %d filter rules", maxSegmentFilterRules)
	}
	id, err := uuid.Parse(strings.TrimSpace(tagID))
	if err != nil {
		return "", nil, fmt.Errorf("segment: invalid tag id %q", tagID)
	}
	ex := "EXISTS (SELECT 1 FROM contact_tags ct WHERE ct.contact_id = " + ref.Table + ".id AND ct.tag_id = ?)"
	if not {
		ex = "NOT " + ex
	}
	return ex, []any{id}, nil
}

// ValidateSegmentFilter checks an AST compiles against the catalog (unknown field,
// invalid operator for the field type, bad tag id, too deep / too many rules)
// WITHOUT running it — save-time validation, defense-in-depth alongside the usecase
// FLS gate. It does NOT check FLS (the usecase does that with the field mask).
func ValidateSegmentFilter(catalog []domain.ReportField, ast domain.SegmentFilter) error {
	fields := make(map[string]domain.ReportField, len(catalog))
	for _, f := range catalog {
		fields[f.Key] = f
	}
	leafCount := 0
	_, _, err := buildSegmentGroup(contactsSegmentRef(), fields, ast, 1, &leafCount)
	return err
}
