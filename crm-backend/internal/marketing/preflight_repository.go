package marketing

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// CountMailableContacts counts addresses that would pass HasLawfulBasis right now.
//
// The predicate mirrors HasLawfulBasis (models.go) branch for branch, and the two
// must be changed together — a preflight that disagrees with the send-time gate is
// worse than no preflight, because it reports green while every recipient is
// suppressed. Deliberately does NOT join contacts: this table holds at most one row
// per marketed address and is the smaller side.
func (r *Repository) CountMailableContacts(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Raw(`
		SELECT count(*) FROM contact_marketing_state
		WHERE org_id = ?
		  AND marketing_status NOT IN ('unsubscribed', 'cleaned')
		  AND ( marketing_status = 'subscribed'
		     OR consent_basis IN ('existing_business_relationship', 'legitimate_interest')
		     OR ( consent_basis IN ('implied_transaction', 'implied_inquiry')
		          AND (casl_expires_at IS NULL OR casl_expires_at > NOW()) ) )`, orgID).
		Scan(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count mailable contacts: %w", err)
	}
	return n, nil
}

// CountKnownMarketingContacts is the denominator: every address this org has any
// recorded marketing position for. Zero here means consent has never been recorded
// at all, which reads very differently from "recorded, and everyone opted out".
func (r *Repository) CountKnownMarketingContacts(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Raw(
		`SELECT count(*) FROM contact_marketing_state WHERE org_id = ?`, orgID).Scan(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count known marketing contacts: %w", err)
	}
	return n, nil
}

// HasConsentUniqueConstraint probes for UNIQUE (org_id, email_normalized) on
// contact_marketing_state. It is declared inline in the CREATE TABLE and — unlike
// the suppression and domain uniques — has no probe-and-refuse boot guard, so
// nothing repairs its absence. Every consent grant uses it as the ON CONFLICT
// arbiter and fails with SQLSTATE 42P10 without it.
func (r *Repository) HasConsentUniqueConstraint(ctx context.Context) (bool, error) {
	var ok bool
	err := r.db.WithContext(ctx).Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM pg_constraint con
			JOIN pg_class rel ON rel.oid = con.conrelid
			WHERE rel.relname = 'contact_marketing_state'
			  AND con.contype = 'u'
			  AND (
			    -- attname has type "name", not "text"; without the cast this comparison
			    -- fails with 'operator does not exist: name[] = text[]' (SQLSTATE 42883).
			    SELECT array_agg(att.attname::text ORDER BY att.attname::text)
			    FROM unnest(con.conkey) k
			    JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = k
			  ) = ARRAY['email_normalized', 'org_id']
		)`).Scan(&ok).Error
	if err != nil {
		return false, fmt.Errorf("probe consent unique constraint: %w", err)
	}
	return ok, nil
}
