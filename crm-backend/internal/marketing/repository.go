package marketing

import (
	"context"
	"errors"
	"strings"

	"crm-backend/internal/emailutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the marketing persistence layer. Every query is org-scoped by an
// explicit WHERE org_id = ? — there is no RLS/global-scope hook to lean on, and
// the bulk send lane (M7) runs callerless, so scoping is always manual.
type Repository struct {
	db *gorm.DB
}

// NewRepository builds the repository over the shared handle.
func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

// AddSuppression inserts a suppression, keyed on a normalized email. It is
// insert-or-ignore: a row that duplicates the (org, email, reason, topic) dedupe
// index is a no-op (returns inserted=false). The caller sets OrgID/Reason; scope
// defaults from the reason when unset. Tolerant of the dedupe index being absent
// (ON CONFLICT DO NOTHING simply inserts when no arbiter constraint exists).
func (r *Repository) AddSuppression(ctx context.Context, s *Suppression) (inserted bool, err error) {
	s.EmailNormalized = emailutil.Normalize(s.EmailNormalized)
	if s.EmailNormalized == "" {
		return false, errors.New("marketing: suppression email is empty")
	}
	if !IsValidReason(s.Reason) {
		return false, errors.New("marketing: invalid suppression reason")
	}
	if s.Scope == "" {
		s.Scope = DefaultScopeForReason(s.Reason)
	}
	if !IsValidScope(s.Scope) {
		return false, errors.New("marketing: invalid suppression scope")
	}
	res := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(s)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListSuppressions returns an org's suppressions, newest first, optionally
// filtered by an email substring (q) and/or an exact reason. Returns the page rows
// plus the total matching count for the admin list.
func (r *Repository) ListSuppressions(ctx context.Context, orgID uuid.UUID, q, reason string, limit, offset int) ([]Suppression, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	base := r.db.WithContext(ctx).Model(&Suppression{}).Where("org_id = ?", orgID)
	if q = strings.TrimSpace(q); q != "" {
		base = base.Where("email_normalized LIKE ?", "%"+emailutil.Normalize(q)+"%")
	}
	if reason != "" {
		base = base.Where("reason = ?", reason)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []Suppression
	if err := base.Order("created_at DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// RemoveSuppression hard-deletes one suppression within an org (a deliberate,
// audited admin action — distinct from contact deletion, which never touches this
// table). Returns removed=false when no such row exists in the org.
func (r *Repository) RemoveSuppression(ctx context.Context, orgID, id uuid.UUID) (removed bool, err error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&Suppression{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SuppressionsForEmail returns every suppression for a normalized email in an org.
// The IsSendable chokepoint reads this live at send time.
func (r *Repository) SuppressionsForEmail(ctx context.Context, orgID uuid.UUID, emailNorm string) ([]Suppression, error) {
	emailNorm = emailutil.Normalize(emailNorm)
	if emailNorm == "" {
		return nil, nil
	}
	var rows []Suppression
	if err := r.db.WithContext(ctx).
		Where("org_id = ? AND email_normalized = ?", orgID, emailNorm).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// MarketingStateForEmail returns the consent/lifecycle row for a normalized email,
// or (nil, nil) when absent. Absence is NOT consent (Guardrail 5).
func (r *Repository) MarketingStateForEmail(ctx context.Context, orgID uuid.UUID, emailNorm string) (*ContactMarketingState, error) {
	emailNorm = emailutil.Normalize(emailNorm)
	if emailNorm == "" {
		return nil, nil
	}
	var st ContactMarketingState
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND email_normalized = ?", orgID, emailNorm).
		First(&st).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &st, nil
}

// RedactMarketingStateForEmail performs the GDPR-erasure collapse: it nulls every
// provenance column while PRESERVING email_normalized + marketing_status, so an
// opt-out keeps being honored after the contact and its consent detail are erased.
// The sibling suppression row is intentionally left untouched (retained in full —
// the minimum needed to keep honoring the opt-out). Best-effort by the caller.
func (r *Repository) RedactMarketingStateForEmail(ctx context.Context, orgID uuid.UUID, email string) error {
	emailNorm := emailutil.Normalize(email)
	if emailNorm == "" {
		return nil
	}
	return r.db.WithContext(ctx).Exec(`
		UPDATE contact_marketing_state
		SET consent_basis = NULL,
		    consent_source = NULL,
		    consent_at = NULL,
		    consent_ip = NULL,
		    region = NULL,
		    casl_expires_at = NULL,
		    double_opt_in_at = NULL,
		    contact_id = NULL,
		    updated_at = NOW()
		WHERE org_id = ? AND email_normalized = ?`, orgID, emailNorm).Error
}
