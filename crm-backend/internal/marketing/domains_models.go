package marketing

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// EmailDomain is one org's sending domain, mirroring Resend's domain object plus
// the DMARC state we check ourselves (Resend manages only SPF + DKIM). Reputation
// is domain-first, so each org verifies its own domain rather than pooling every
// tenant under one platform From. Soft-deletable (removing a domain).
type EmailDomain struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID          uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	Domain         string    `gorm:"type:varchar(255);not null" json:"domain"`
	ResendDomainID string    `gorm:"type:varchar(64);not null;default:''" json:"resend_domain_id"`
	Region         string    `gorm:"type:varchar(32);not null;default:''" json:"region"`
	// SendSubdomain is the Return-Path subdomain (Resend default "send"); ReturnPath
	// is the full aligned bounce address host, e.g. send.customer.com. Per B1 the
	// aligned Return-Path is automatic once the domain verifies — there is no
	// per-send Return-Path field — so this is informational + drives the from-domain.
	SendSubdomain     string     `gorm:"type:varchar(63);not null;default:'send'" json:"send_subdomain"`
	TrackingSubdomain *string    `gorm:"type:varchar(63)" json:"tracking_subdomain,omitempty"`
	ReturnPath        string     `gorm:"type:varchar(320);not null;default:''" json:"return_path"`
	Status            string     `gorm:"type:varchar(24);not null;default:'not_started'" json:"status"`
	SPFVerified       bool       `gorm:"not null;default:false" json:"spf_verified"`
	DKIMVerified      bool       `gorm:"not null;default:false" json:"dkim_verified"`
	// DMARCPolicy is the parsed p= value of the domain's _dmarc record (none /
	// quarantine / reject), or nil when no DMARC record is published. Pointer so the
	// GDPR/zero-value rules hold and "unknown" is distinct from "none".
	DMARCPolicy   *string        `gorm:"type:varchar(16)" json:"dmarc_policy,omitempty"`
	DNSRecords    datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"dns_records"`
	VerifiedAt    *time.Time     `json:"verified_at,omitempty"`
	LastCheckedAt *time.Time     `json:"last_checked_at,omitempty"`
	WarmupDailyCap *int          `gorm:"column:warmup_daily_cap" json:"warmup_daily_cap,omitempty"`
	CreatedBy     *uuid.UUID     `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt     time.Time      `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt     time.Time      `gorm:"not null;default:now()" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName pins the table name.
func (EmailDomain) TableName() string { return "org_email_domains" }

// IsSendVerified reports whether Resend has verified the records needed to SEND —
// SPF and DKIM both pass. Independent of the receive capability, so a
// partially_verified (send-only) domain still qualifies.
func (d *EmailDomain) IsSendVerified() bool {
	return d != nil && d.SPFVerified && d.DKIMVerified
}

// HasDMARC reports whether a DMARC record (>= p=none) is published.
func (d *EmailDomain) HasDMARC() bool {
	return d != nil && d.DMARCPolicy != nil && *d.DMARCPolicy != ""
}

// CanBulkSend is the per-domain gate: SPF + DKIM verified AND a DMARC policy
// published. Gmail/Yahoo require all three for bulk, and SPF authenticates the
// (auto-aligned) Return-Path, not the visible From.
func (d *EmailDomain) CanBulkSend() bool {
	return d.IsSendVerified() && d.HasDMARC()
}

// NotSendableReason returns a stable reason a domain cannot bulk-send, or "".
func (d *EmailDomain) NotSendableReason() string {
	switch {
	case d == nil:
		return "no_domain"
	case !d.SPFVerified:
		return "spf_unverified"
	case !d.DKIMVerified:
		return "dkim_unverified"
	case !d.HasDMARC():
		return "dmarc_missing"
	default:
		return ""
	}
}

// deriveRecordVerification computes SPF/DKIM verification from Resend's per-record
// statuses: a kind is verified iff at least one record of that kind exists and
// every record of that kind has status "verified". (SPF is usually an MX + a TXT,
// both of which must pass.)
func deriveRecordVerification(records []ResendDNSRecord) (spf, dkim bool) {
	spfSeen, dkimSeen := false, false
	spf, dkim = true, true
	for _, r := range records {
		switch strings.ToUpper(r.Record) {
		case "SPF":
			spfSeen = true
			if !strings.EqualFold(r.Status, DomainStatusVerified) {
				spf = false
			}
		case "DKIM":
			dkimSeen = true
			if !strings.EqualFold(r.Status, DomainStatusVerified) {
				dkim = false
			}
		}
	}
	return spf && spfSeen, dkim && dkimSeen
}

// validDMARCPolicy reports whether p is one of the three RFC 7489 policy values.
func validDMARCPolicy(p string) bool {
	return p == "none" || p == "quarantine" || p == "reject"
}

// parseDMARC scans TXT records for the domain's DMARC record and returns its p=
// (domain policy) and sp= (subdomain policy) values, lowercased.
//
// present is true ONLY when EXACTLY ONE well-formed v=DMARC1 record carrying a
// valid p tag exists. Per RFC 7489 §6.6.3, when a _dmarc host publishes more than
// one v=DMARC1 record the receiver (Gmail/Yahoo) treats DMARC as absent — so we
// must too, or we would clear a domain the very receivers we gate for reject.
func parseDMARC(txts []string) (p, sp string, present bool) {
	found := 0
	for _, txt := range txts {
		t := strings.TrimSpace(txt)
		low := strings.ToLower(strings.ReplaceAll(t, " ", ""))
		if !strings.HasPrefix(low, "v=dmarc1") {
			continue
		}
		found++
		if found > 1 {
			// Multiple DMARC records → treated as no valid policy.
			return "", "", false
		}
		for _, tag := range strings.Split(t, ";") {
			kv := strings.SplitN(strings.TrimSpace(tag), "=", 2)
			if len(kv) != 2 {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(kv[0]))
			val := strings.ToLower(strings.TrimSpace(kv[1]))
			switch key {
			case "p":
				if validDMARCPolicy(val) {
					p = val
				}
			case "sp":
				if validDMARCPolicy(val) {
					sp = val
				}
			}
		}
	}
	return p, sp, found == 1 && p != ""
}

// parentDomain returns domain minus its first label, but only when the result is
// still an organizational-looking domain (>= 2 labels) — so mail.acme.com → acme.com
// but acme.com → "" (we don't chase a bare TLD). Not public-suffix-aware; a wrong
// guess only ever fails to find a record, which keeps the gate fail-closed.
func parentDomain(domain string) string {
	labels := strings.Split(domain, ".")
	if len(labels) < 3 {
		return ""
	}
	return strings.Join(labels[1:], ".")
}
