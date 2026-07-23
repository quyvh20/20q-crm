package marketing

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/datatypes"
)

// Sentinel errors the handler maps to HTTP status codes.
var (
	ErrDomainInvalid       = errors.New("marketing: invalid domain")
	ErrDomainTakenOtherOrg = errors.New("marketing: domain is already registered by another workspace")
	ErrDomainExists        = errors.New("marketing: domain is already added to this workspace")
	ErrDomainNotFound      = errors.New("marketing: domain not found")
	ErrSendingNotConfigured = errors.New("marketing: email sending is not configured (no Resend API key)")
)

// domainRe is a permissive apex/subdomain check (labels of letters/digits/hyphens,
// at least one dot, a 2+ char TLD). Not a full RFC validator — just enough to
// reject obvious junk before calling Resend.
var domainRe = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// txtLookupFunc resolves TXT records for a name. Injected for testability.
type txtLookupFunc func(name string) ([]string, error)

// domainStore is the persistence slice the service needs (the concrete *Repository
// satisfies it). Declared consumer-side so the service is unit-testable with a fake.
type domainStore interface {
	CreateDomain(ctx context.Context, d *EmailDomain) error
	GetDomainByID(ctx context.Context, orgID, id uuid.UUID) (*EmailDomain, error)
	DomainOwnerOrg(ctx context.Context, domain string) (uuid.UUID, bool, error)
	ListDomainsByOrg(ctx context.Context, orgID uuid.UUID) ([]EmailDomain, error)
	UpdateDomain(ctx context.Context, d *EmailDomain) error
	SoftDeleteDomain(ctx context.Context, orgID, id uuid.UUID) (bool, error)
}

// domainsAPI is the Resend client slice the service needs (the concrete
// *DomainsClient satisfies it), faked in tests to avoid real network calls.
type domainsAPI interface {
	Configured() bool
	CreateDomain(ctx context.Context, name, region, customReturnPath string) (*ResendDomain, error)
	GetDomain(ctx context.Context, id string) (*ResendDomain, error)
	VerifyDomain(ctx context.Context, id string) error
	DeleteDomain(ctx context.Context, id string) error
}

// DomainService orchestrates per-org sending domains against the Resend Domains
// API and our own DMARC check. The bulk worker (M7) will consult CanBulkSend.
type DomainService struct {
	repo   domainStore
	client domainsAPI
	lookup txtLookupFunc
	now    func() time.Time
}

// NewDomainService builds the service. The TXT lookup defaults to net.LookupTXT;
// tests inject a fake store/client/lookup.
func NewDomainService(repo *Repository, client *DomainsClient) *DomainService {
	return &DomainService{repo: repo, client: client, lookup: net.LookupTXT, now: time.Now}
}

// normalizeDomain lowercases, trims, and strips a scheme/path/leading www. so
// "https://www.Example.com/" and "example.com" collapse to the same key.
func normalizeDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "www.")
	return strings.TrimSuffix(s, ".")
}

// AddDomain registers a new sending domain for the org with Resend and stores it.
func (s *DomainService) AddDomain(ctx context.Context, orgID, createdBy uuid.UUID, rawDomain, trackingSubdomain string) (*EmailDomain, error) {
	domain := normalizeDomain(rawDomain)
	if domain == "" || !domainRe.MatchString(domain) {
		return nil, ErrDomainInvalid
	}
	if !s.client.Configured() {
		return nil, ErrSendingNotConfigured
	}

	// Ownership: Resend domains are team-global, so the same string must not be
	// claimed by two orgs. Block cross-org; report same-org as already-added.
	owner, exists, err := s.repo.DomainOwnerOrg(ctx, domain)
	if err != nil {
		return nil, err
	}
	if exists {
		if owner == orgID {
			return nil, ErrDomainExists
		}
		return nil, ErrDomainTakenOtherOrg
	}

	rd, err := s.client.CreateDomain(ctx, domain, "", "send")
	if err != nil {
		return nil, err
	}

	spf, dkim := deriveRecordVerification(rd.Records)
	recs, _ := json.Marshal(rd.Records)
	var tracking *string
	if t := normalizeSubdomain(trackingSubdomain); t != "" {
		tracking = &t
	}
	d := &EmailDomain{
		OrgID:             orgID,
		Domain:            domain,
		ResendDomainID:    rd.ID,
		Region:            rd.Region,
		SendSubdomain:     "send",
		TrackingSubdomain: tracking,
		ReturnPath:        "send." + domain,
		Status:            rd.Status,
		SPFVerified:       spf,
		DKIMVerified:      dkim,
		DNSRecords:        datatypes.JSON(recs),
	}
	if pol, resolved := s.checkDMARC(domain); resolved {
		d.DMARCPolicy = pol
	}
	now := s.now()
	d.LastCheckedAt = &now
	if createdBy != uuid.Nil {
		d.CreatedBy = &createdBy
	}
	if d.IsSendVerified() {
		d.VerifiedAt = &now
	}
	if err := s.repo.CreateDomain(ctx, d); err != nil {
		// The Resend domain was already created; our row didn't land, so roll it back
		// (best-effort) rather than orphan it in the Resend team.
		_ = s.client.DeleteDomain(ctx, rd.ID)
		// A unique-violation means another org (or a concurrent same-org request) won
		// the global-domain race between our pre-check and this insert. Resolve to the
		// right sentinel so the handler returns 409, not 500.
		if isUniqueViolation(err) {
			if o, exists, e := s.repo.DomainOwnerOrg(ctx, domain); e == nil && exists && o != orgID {
				return nil, ErrDomainTakenOtherOrg
			}
			return nil, ErrDomainExists
		}
		return nil, err
	}
	return d, nil
}

// isUniqueViolation reports whether err is a Postgres 23505 unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// RefreshDomain re-reads the domain from Resend + re-checks DMARC and persists the
// new state. This is the "live re-check" the onboarding wizard button calls.
func (s *DomainService) RefreshDomain(ctx context.Context, orgID, id uuid.UUID) (*EmailDomain, error) {
	d, err := s.repo.GetDomainByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}
	if s.client.Configured() && d.ResendDomainID != "" {
		rd, err := s.client.GetDomain(ctx, d.ResendDomainID)
		if err != nil {
			return nil, err
		}
		d.Status = rd.Status
		d.Region = rd.Region
		d.SPFVerified, d.DKIMVerified = deriveRecordVerification(rd.Records)
		if recs, err := json.Marshal(rd.Records); err == nil {
			d.DNSRecords = datatypes.JSON(recs)
		}
	}
	// Only overwrite the stored DMARC policy when the lookup was authoritative — a
	// transient resolver failure must not downgrade a genuinely-published policy.
	if pol, resolved := s.checkDMARC(d.Domain); resolved {
		d.DMARCPolicy = pol
	}
	now := s.now()
	d.LastCheckedAt = &now
	if d.IsSendVerified() && d.VerifiedAt == nil {
		d.VerifiedAt = &now
	}
	if err := s.repo.UpdateDomain(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

// TriggerVerify asks Resend to (asynchronously) re-check DNS, then refreshes local
// state (which will usually still read pending until Resend finishes).
func (s *DomainService) TriggerVerify(ctx context.Context, orgID, id uuid.UUID) (*EmailDomain, error) {
	d, err := s.repo.GetDomainByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}
	if !s.client.Configured() {
		return nil, ErrSendingNotConfigured
	}
	if d.ResendDomainID != "" {
		if err := s.client.VerifyDomain(ctx, d.ResendDomainID); err != nil {
			return nil, err
		}
	}
	return s.RefreshDomain(ctx, orgID, id)
}

// RemoveDomain deletes the domain from Resend, then soft-deletes the row. A Resend
// delete failure aborts (so our table can't claim a domain still live in Resend);
// a 404 from Resend is treated as already-gone.
func (s *DomainService) RemoveDomain(ctx context.Context, orgID, id uuid.UUID) error {
	d, err := s.repo.GetDomainByID(ctx, orgID, id)
	if err != nil {
		return err
	}
	if d == nil {
		return ErrDomainNotFound
	}
	if s.client.Configured() && d.ResendDomainID != "" {
		if err := s.client.DeleteDomain(ctx, d.ResendDomainID); err != nil {
			var apiErr *ResendAPIError
			if !(errors.As(err, &apiErr) && apiErr.Status == 404) {
				return err
			}
		}
	}
	_, err = s.repo.SoftDeleteDomain(ctx, orgID, id)
	return err
}

// ListDomains returns the org's sending domains.
func (s *DomainService) ListDomains(ctx context.Context, orgID uuid.UUID) ([]EmailDomain, error) {
	return s.repo.ListDomainsByOrg(ctx, orgID)
}

// GetDomain returns one domain (no live refresh).
func (s *DomainService) GetDomain(ctx context.Context, orgID, id uuid.UUID) (*EmailDomain, error) {
	d, err := s.repo.GetDomainByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, ErrDomainNotFound
	}
	return d, nil
}

// CanBulkSend reports whether the org has at least one domain cleared for bulk
// marketing (SPF+DKIM verified + DMARC published), with a stable reason when not.
func (s *DomainService) CanBulkSend(ctx context.Context, orgID uuid.UUID) (bool, string, error) {
	domains, err := s.repo.ListDomainsByOrg(ctx, orgID)
	if err != nil {
		return false, "error", err
	}
	if len(domains) == 0 {
		return false, "no_domain", nil
	}
	// Report the reason of the closest-to-ready domain (fewest missing checks).
	best := ""
	bestScore := -1
	for i := range domains {
		d := &domains[i]
		if d.CanBulkSend() {
			return true, "", nil
		}
		score := 0
		if d.SPFVerified {
			score++
		}
		if d.DKIMVerified {
			score++
		}
		if d.HasDMARC() {
			score++
		}
		if score > bestScore {
			bestScore = score
			best = d.NotSendableReason()
		}
	}
	return false, best, nil
}

// FromDomainAllowed reports whether an intended From domain is covered by a
// bulk-sendable verified domain (exact match or a subdomain of it). For M7.
func (s *DomainService) FromDomainAllowed(ctx context.Context, orgID uuid.UUID, fromDomain string) (bool, error) {
	fromDomain = normalizeDomain(fromDomain)
	if fromDomain == "" {
		return false, nil
	}
	domains, err := s.repo.ListDomainsByOrg(ctx, orgID)
	if err != nil {
		return false, err
	}
	for i := range domains {
		d := &domains[i]
		if !d.CanBulkSend() {
			continue
		}
		if fromDomain == d.Domain || strings.HasSuffix(fromDomain, "."+d.Domain) {
			return true, nil
		}
	}
	return false, nil
}

// checkDMARC resolves the effective DMARC policy for a sending domain.
//
// It returns (policy, resolved). resolved=false means the lookup could not be
// completed (a transient DNS error) — the caller must NOT treat that as "no DMARC"
// and must keep any previously-stored policy, or a resolver blip silently revokes a
// domain's bulk-send eligibility. resolved=true is authoritative: a non-nil policy
// is published, a nil policy means none is published.
//
// A subdomain (mail.acme.com) with no _dmarc of its own inherits the organizational
// domain's policy — sp= when present, else p= — matching receiver behavior.
func (s *DomainService) checkDMARC(domain string) (policy *string, resolved bool) {
	if s.lookup == nil {
		return nil, false
	}
	txts, err := s.lookup("_dmarc." + domain)
	if err != nil {
		return nil, false // transient / unknown — keep stored state
	}
	if p, _, ok := parseDMARC(txts); ok {
		return &p, true
	}
	// No own record: for a subdomain, DMARC is inherited from the parent domain.
	if parent := parentDomain(domain); parent != "" {
		ptxts, perr := s.lookup("_dmarc." + parent)
		if perr != nil {
			return nil, false // couldn't determine — keep stored state
		}
		if p, sp, ok := parseDMARC(ptxts); ok {
			eff := p
			if sp != "" {
				eff = sp // subdomain policy overrides the domain policy for subdomains
			}
			return &eff, true
		}
	}
	return nil, true // authoritatively no DMARC published
}

// normalizeSubdomain keeps a single DNS label (letters/digits/hyphens), lowercased.
func normalizeSubdomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	// Only the first label if a user typed a dotted value.
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	return s
}
