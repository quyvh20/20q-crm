package repository

import (
	"encoding/json"
	"fmt"
	"strings"

	"crm-backend/internal/domain"
)

// lead_score_sql.go compiles a set of scoring rules into ONE arithmetic
// expression: a sum of CASE arms, clamped to [0,100].
//
//	LEAST(100, GREATEST(0,
//	    (CASE WHEN <rule1 predicate> THEN 10 ELSE 0 END)
//	  + (CASE WHEN <rule2 predicate> THEN -40 ELSE 0 END)))
//
// One expression, evaluated by Postgres inside a single UPDATE, is the whole
// engine. There is deliberately no Go-side evaluator to pair with it — see the
// note on domain.LeadScoringRule for why a second implementation of "what does
// this rule mean" is the bug this design exists to make impossible.
//
// Field rules reuse the M5 segment compiler verbatim (buildSegmentGroup, which
// itself delegates field leaves to buildReportFilterLeaf). That is why this
// file lives in package repository: those functions are unexported, and
// reaching them is the entire point — a scoring rule and a segment rule must
// mean the same thing or an org's "score ≥ 50" segment will disagree with the
// score it filters on.

// leadScoreClampedExpr wraps a sum in the 0..100 clamp. Clamping the SUM rather
// than each rule is what keeps the model order-independent: rules are added in
// any order and reach the same total.
func leadScoreClampedExpr(sum string) string {
	return fmt.Sprintf("LEAST(%d, GREATEST(%d, %s))", domain.LeadScoreMax, domain.LeadScoreMin, sum)
}

// buildLeadScoreExpr compiles enabled rules into the clamped score expression
// plus its bind args, in the order the args appear in the SQL.
//
// A rule that compiles to an EMPTY predicate is SKIPPED, not treated as true.
// An empty segment AST legitimately compiles to "" and means "everyone" — which
// is correct for an audience and catastrophic for a score, where it would make
// a half-built rule award its points to every contact in the org. The usecase
// also rejects empty conditions at save time; this is the second line.
//
// Returns ("0", nil, nil) when no rule contributes, which scores everyone 0 —
// the correct reading of "this org has no enabled rules".
func buildLeadScoreExpr(catalog []domain.ReportField, rules []domain.LeadScoringRule) (string, []any, []string, error) {
	fields := make(map[string]domain.ReportField, len(catalog))
	for _, f := range catalog {
		fields[f.Key] = f
	}

	terms := make([]string, 0, len(rules))
	args := make([]any, 0, len(rules)*2)
	var skipped []string

	for i := range rules {
		r := &rules[i]
		if !r.Enabled {
			continue
		}
		var (
			pred     string
			predArgs []any
			err      error
		)
		switch r.Kind {
		case domain.LeadRuleKindField:
			pred, predArgs, err = buildLeadFieldPredicate(fields, r)
		case domain.LeadRuleKindEngagement:
			pred, predArgs, err = buildLeadEngagementPredicate(r)
		default:
			err = fmt.Errorf("unknown kind %q", r.Kind)
		}
		if err != nil {
			// SKIP, do not abort. A rule stops compiling through ordinary
			// unrelated actions — an admin deletes the custom field it tests, or
			// retypes it from text to number, invalidating a `contains` operator.
			// Aborting the whole expression would return before a single UPDATE
			// ran, freezing every contact's score in the workspace, on every
			// pass, forever, with nothing on screen to explain it. One broken
			// rule must cost only its own points. The caller logs these.
			skipped = append(skipped, fmt.Sprintf("%q (%s): %v", r.Name, r.ID, err))
			continue
		}
		if strings.TrimSpace(pred) == "" {
			continue // see the doc comment: an empty predicate is not "true"
		}
		// Points are an int from our own column, never caller text — safe to
		// inline, and inlining keeps the bind-arg order identical to the order
		// the predicates were appended, which is what makes this auditable.
		terms = append(terms, fmt.Sprintf("(CASE WHEN %s THEN %d ELSE 0 END)", pred, r.Points))
		args = append(args, predArgs...)
	}

	if len(terms) == 0 {
		return "0", nil, skipped, nil
	}
	return leadScoreClampedExpr(strings.Join(terms, " + ")), args, skipped, nil
}

// buildLeadFieldPredicate compiles a field rule through the M5 segment compiler.
//
// leafCount is reset PER RULE on purpose. Sharing one counter across every rule
// would charge the whole rule set against a single 50-leaf cap, so an org with
// twenty modest rules would start failing to compile with an error naming a
// limit it never knowingly approached.
func buildLeadFieldPredicate(fields map[string]domain.ReportField, r *domain.LeadScoringRule) (string, []any, error) {
	var ast domain.SegmentFilter
	if err := json.Unmarshal(nonEmptyRuleCondition(r.Condition), &ast); err != nil {
		return "", nil, fmt.Errorf("lead scoring: rule %s has an unreadable condition: %w", r.ID, err)
	}
	leafCount := 0
	sql, args, err := buildSegmentGroup(contactsSegmentRef(), fields, ast, 1, &leafCount)
	if err != nil {
		return "", nil, fmt.Errorf("lead scoring: rule %s: %w", r.ID, err)
	}
	return sql, args, nil
}

// buildLeadEngagementPredicate compiles an engagement rule to a correlated
// EXISTS over the marketing event ledger.
//
// The join is on the NORMALIZED EMAIL, not on a contact id, because
// marketing_email_events has no contact_id column. The only other bridge is
// marketing_campaign_recipients, whose rows are HARD-deleted 30 days after a
// campaign finishes — a score built on that join would quietly sag over time
// with no error anywhere. lower(btrim(...)) matches both the webhook's own
// normalization and the expression the segment compiler already uses, so the
// two agree by construction.
//
// occurred_at, not created_at: created_at is when we ingested the webhook,
// which after a backlog can be hours off. occurred_at is nullable (it is
// best-effort parsed from the provider payload), so the window predicate is
// written to exclude NULLs explicitly rather than let a NULL comparison
// silently drop the row.
//
// No literal question mark appears in any SQL text below. gorm treats every '?'
// as a positional bind — including one inside a quoted string literal — so a
// stray one steals a bind arg and silently misaligns every argument after it.
func buildLeadEngagementPredicate(r *domain.LeadScoringRule) (string, []any, error) {
	var c domain.LeadEngagementCondition
	if err := json.Unmarshal(nonEmptyRuleCondition(r.Condition), &c); err != nil {
		return "", nil, fmt.Errorf("lead scoring: rule %s has an unreadable condition: %w", r.ID, err)
	}
	if !domain.ValidLeadEvents[c.Event] {
		return "", nil, fmt.Errorf("lead scoring: rule %s names unsupported event %q", r.ID, c.Event)
	}
	if c.WindowDays <= 0 {
		return "", nil, fmt.Errorf("lead scoring: rule %s needs a positive window_days", r.ID)
	}
	minCount := c.MinCount
	if minCount < 1 {
		minCount = 1
	}

	// A contact with no email can never match an email-keyed event, and
	// lower(btrim(NULL)) is NULL, so the guard is explicit rather than implied.
	// btrim(...) <> '' as well as IS NOT NULL: an empty-string email is common
	// (imports, form submissions) and lower(btrim('')) is '', which would match
	// any event row whose own email_normalized landed blank — scoring unrelated
	// contacts off each other.
	const sql = `(contacts.email IS NOT NULL AND btrim(contacts.email) <> '' AND (
		SELECT COUNT(*) FROM marketing_email_events e
		WHERE e.org_id = contacts.org_id
		  AND e.email_normalized = lower(btrim(contacts.email))
		  AND e.event_type = ?
		  AND e.occurred_at IS NOT NULL
		  AND e.occurred_at >= NOW() - make_interval(days => ?)
	) >= ?)`
	return sql, []any{c.Event, c.WindowDays, minCount}, nil
}

// nonEmptyRuleCondition guards json.Unmarshal against a zero-length column,
// which a row written before the NOT NULL default landed could still present.
func nonEmptyRuleCondition(j domain.JSON) []byte {
	if len(j) == 0 {
		return []byte("{}")
	}
	return []byte(j)
}
