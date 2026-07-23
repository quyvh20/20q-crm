package marketing

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CreateDomain inserts a sending-domain row.
func (r *Repository) CreateDomain(ctx context.Context, d *EmailDomain) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// GetDomainByID returns one domain within an org, or (nil, nil) when absent.
func (r *Repository) GetDomainByID(ctx context.Context, orgID, id uuid.UUID) (*EmailDomain, error) {
	var d EmailDomain
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// GetDomainByName returns an org's domain by its (lowercased) name, or (nil, nil).
func (r *Repository) GetDomainByName(ctx context.Context, orgID uuid.UUID, domain string) (*EmailDomain, error) {
	var d EmailDomain
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND domain = ?", orgID, strings.ToLower(strings.TrimSpace(domain))).
		First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}

// DomainOwnerOrg returns the org that already owns a domain string, if any. It is
// intentionally UNSCOPED: Resend domains are team-global, so the same domain must
// not be claimable by two orgs — the org to scope by is the answer, not an input.
// Excludes soft-deleted rows (a removed domain is claimable again).
func (r *Repository) DomainOwnerOrg(ctx context.Context, domain string) (uuid.UUID, bool, error) {
	var d EmailDomain
	err := r.db.WithContext(ctx).
		Where("domain = ?", strings.ToLower(strings.TrimSpace(domain))).
		First(&d).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return d.OrgID, true, nil
}

// ListDomainsByOrg returns an org's domains, newest first.
func (r *Repository) ListDomainsByOrg(ctx context.Context, orgID uuid.UUID) ([]EmailDomain, error) {
	var rows []EmailDomain
	if err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// UpdateDomain persists a mutated domain row (status/verification/records refresh).
// Save writes every column from the struct; domains are only ever written by admin
// actions and the refresh path (never concurrently machine-written), so a clobber
// is not a hazard here.
func (r *Repository) UpdateDomain(ctx context.Context, d *EmailDomain) error {
	return r.db.WithContext(ctx).Save(d).Error
}

// SoftDeleteDomain soft-deletes one domain within an org. Returns removed=false
// when no such row exists in the org.
func (r *Repository) SoftDeleteDomain(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).Delete(&EmailDomain{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
