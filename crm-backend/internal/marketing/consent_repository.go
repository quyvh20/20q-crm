package marketing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// consent_repository.go writes a POSITIVE lawful basis onto contact_marketing_state.
//
// Until this existed, nothing in the codebase ever did. The table's only writers
// were SetMarketingStatus (both call sites passing StatusUnsubscribed) and the GDPR
// collapse, so HasLawfulBasis was false for every address and every marketing
// campaign suppressed 100% of its recipients with "no_lawful_basis" — after passing
// a fully green launch checklist. email_marketing_plan.md specified this mechanism
// ("an org can mark its own existing customers with consent_basis=
// existing_business_relationship at import"); it was never built.

// GrantScope names the population a grant applies to. Exactly one field is set;
// the usecase enforces that before calling in.
type GrantScope struct {
	// Audience is a segment-compiled, org-wide, callerless query yielding
	// (contact_id, email_normalized) — the exact key contact_marketing_state is
	// UNIQUE on. Nothing else in the codebase speaks that key.
	Audience *domain.AudienceQuery
	// ContactIDs is an explicit list. Capped by the usecase.
	ContactIDs []uuid.UUID
}

// GrantInput is the declaration being recorded.
type GrantInput struct {
	Basis     string
	Source    string // the org's DECLARED provenance, e.g. "2024 customer import"
	GrantedBy uuid.UUID
	// CASLExpiresAt is required for the implied bases (they expire) and must be nil
	// for the standing ones.
	CASLExpiresAt *time.Time
	Now           time.Time
}

// GrantCounts reports what a grant did, or what a preview predicts.
type GrantCounts struct {
	// Candidates is every distinct normalized address the scope resolves to.
	Candidates int64 `json:"candidates"`
	// Granted is the number of rows the write actually touched. Sourced from
	// RowsAffected, never from the candidate count — reporting a number the database
	// did not write is the failure mode this whole feature exists to end.
	Granted int64 `json:"granted"`
	// Skipped is Candidates-Granted: addresses left alone because they are already
	// unsubscribed or cleaned. A grant must never resurrect an opt-out.
	Skipped int64 `json:"skipped"`
	// Suppressed counts candidates carrying a suppression entry. They may still be
	// granted a basis, but IsSendable will refuse them, so surfacing the number
	// stops an operator reading "granted 5,000" as "5,000 mailable".
	Suppressed int64 `json:"suppressed"`
}

var errEmptyScope = errors.New("marketing: grant scope resolves to no contacts")

// candidateSource builds the subquery both the preview and the write run over, so
// their numbers cannot drift apart.
//
// DISTINCT ON (email_normalized) is mandatory, not stylistic. CompileAudienceQuery
// UNIONs whole (contact_id, email_normalized) rows, while the target is UNIQUE on
// (org_id, email_normalized) and contacts(org_id, email) is CASE-SENSITIVE unique —
// so "A@x.com" and "a@x.com" survive the UNION as two rows that collide on one
// conflict target. Postgres rejects that outright ("ON CONFLICT DO UPDATE command
// cannot affect row a second time", SQLSTATE 21000), which would make every grant
// over a realistic contact list fail.
func candidateSource(orgID uuid.UUID, scope GrantScope) (string, []any, error) {
	switch {
	case scope.Audience != nil:
		return "(SELECT DISTINCT ON (email_normalized) contact_id, email_normalized FROM (" +
			scope.Audience.SelectSQL +
			") aud ORDER BY email_normalized, contact_id)", scope.Audience.Args, nil
	case len(scope.ContactIDs) > 0:
		return `(SELECT DISTINCT ON (email_normalized) c.id AS contact_id, lower(btrim(c.email)) AS email_normalized
		         FROM contacts c
		         WHERE c.org_id = ? AND c.deleted_at IS NULL AND c.id IN ?
		           AND c.email IS NOT NULL AND btrim(c.email) <> ''
		         ORDER BY email_normalized, contact_id)`,
			[]any{orgID, scope.ContactIDs}, nil
	default:
		return "", nil, errEmptyScope
	}
}

// GrantLawfulBasis stamps the declared basis onto every candidate address.
//
// Deliberately does NOT touch marketing_status. Two reasons. HasLawfulBasis already
// returns true for status 'pending' carrying a standing basis, so no status change
// is needed to make the address mailable. And RedactMarketingStateForEmail — which
// runs on EVERY contact delete, not only a GDPR request — nulls provenance while
// PRESERVING marketing_status, so a grant that wrote 'subscribed' would outlive
// erasure as a permanently mailable address with no recorded reason. Basis-only is
// correctly revoked by that collapse.
//
// The WHERE on the conflict arm is what stops a grant resurrecting an opt-out.
func (r *Repository) GrantLawfulBasis(ctx context.Context, orgID uuid.UUID, scope GrantScope, in GrantInput) (int64, error) {
	src, srcArgs, err := candidateSource(orgID, scope)
	if err != nil {
		return 0, err
	}

	// Placeholder order follows the textual order of the statement: the INSERT's
	// literal columns come before the subquery's args (mirroring SnapshotRoster).
	sql := `INSERT INTO contact_marketing_state
	          (org_id, email_normalized, contact_id, marketing_status,
	           consent_basis, consent_source, consent_at, casl_expires_at,
	           consent_granted_by, created_at, updated_at)
	        SELECT ?, cand.email_normalized, cand.contact_id, 'pending',
	               ?, ?, ?, ?, ?, NOW(), NOW()
	        FROM ` + src + ` cand
	        ON CONFLICT (org_id, email_normalized) DO UPDATE
	          SET consent_basis      = EXCLUDED.consent_basis,
	              consent_source     = EXCLUDED.consent_source,
	              consent_at         = EXCLUDED.consent_at,
	              casl_expires_at    = EXCLUDED.casl_expires_at,
	              consent_granted_by = EXCLUDED.consent_granted_by,
	              contact_id         = COALESCE(contact_marketing_state.contact_id, EXCLUDED.contact_id),
	              updated_at         = NOW()
	          WHERE contact_marketing_state.marketing_status NOT IN ('unsubscribed', 'cleaned')`

	args := append([]any{orgID, in.Basis, in.Source, in.Now, in.CASLExpiresAt, in.GrantedBy}, srcArgs...)
	res := r.db.WithContext(ctx).Exec(sql, args...)
	if res.Error != nil {
		return 0, fmt.Errorf("grant lawful basis: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// PreviewLawfulBasisGrant reports what a grant would do, over the SAME candidate
// source the write uses, so the confirmation an operator approves matches what
// lands. Granted is left zero — only the write can report that honestly.
func (r *Repository) PreviewLawfulBasisGrant(ctx context.Context, orgID uuid.UUID, scope GrantScope) (GrantCounts, error) {
	var out GrantCounts
	src, srcArgs, err := candidateSource(orgID, scope)
	if err != nil {
		return out, err
	}

	if err := r.db.WithContext(ctx).
		Raw(`SELECT count(*) FROM `+src+` cand`, srcArgs...).
		Scan(&out.Candidates).Error; err != nil {
		return out, fmt.Errorf("preview candidates: %w", err)
	}

	// Opted-out addresses the grant will deliberately leave alone.
	skipArgs := append([]any{}, srcArgs...)
	skipArgs = append(skipArgs, orgID)
	if err := r.db.WithContext(ctx).
		Raw(`SELECT count(*) FROM `+src+` cand
		     JOIN contact_marketing_state s
		       ON s.email_normalized = cand.email_normalized AND s.org_id = ?
		     WHERE s.marketing_status IN ('unsubscribed', 'cleaned')`, skipArgs...).
		Scan(&out.Skipped).Error; err != nil {
		return out, fmt.Errorf("preview skipped: %w", err)
	}

	// Suppressed addresses can be granted a basis but still will not be sent to.
	supArgs := append([]any{}, srcArgs...)
	supArgs = append(supArgs, orgID)
	if err := r.db.WithContext(ctx).
		Raw(`SELECT count(*) FROM `+src+` cand
		     WHERE EXISTS (SELECT 1 FROM marketing_suppressions m
		                   WHERE m.org_id = ? AND m.email_normalized = cand.email_normalized)`, supArgs...).
		Scan(&out.Suppressed).Error; err != nil {
		return out, fmt.Errorf("preview suppressed: %w", err)
	}

	return out, nil
}
