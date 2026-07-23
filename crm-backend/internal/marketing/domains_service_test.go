package marketing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeDomainStore struct {
	byID          map[uuid.UUID]*EmailDomain
	ownerByDomain map[string]uuid.UUID
	// createErr simulates an insert failure (e.g. a unique violation on the global
	// domain index); createErrOwner, if set, is recorded as the domain's owner just
	// before the error, simulating the racing winner's row now existing.
	createErr      error
	createErrOwner uuid.UUID
}

func newFakeStore() *fakeDomainStore {
	return &fakeDomainStore{byID: map[uuid.UUID]*EmailDomain{}, ownerByDomain: map[string]uuid.UUID{}}
}
func (f *fakeDomainStore) CreateDomain(_ context.Context, d *EmailDomain) error {
	if f.createErr != nil {
		if f.createErrOwner != uuid.Nil {
			f.ownerByDomain[d.Domain] = f.createErrOwner
		}
		return f.createErr
	}
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	f.byID[d.ID] = d
	f.ownerByDomain[d.Domain] = d.OrgID
	return nil
}
func (f *fakeDomainStore) GetDomainByID(_ context.Context, orgID, id uuid.UUID) (*EmailDomain, error) {
	if d, ok := f.byID[id]; ok && d.OrgID == orgID {
		return d, nil
	}
	return nil, nil
}
func (f *fakeDomainStore) DomainOwnerOrg(_ context.Context, domain string) (uuid.UUID, bool, error) {
	o, ok := f.ownerByDomain[domain]
	return o, ok, nil
}
func (f *fakeDomainStore) ListDomainsByOrg(_ context.Context, orgID uuid.UUID) ([]EmailDomain, error) {
	var out []EmailDomain
	for _, d := range f.byID {
		if d.OrgID == orgID {
			out = append(out, *d)
		}
	}
	return out, nil
}
func (f *fakeDomainStore) UpdateDomain(_ context.Context, d *EmailDomain) error {
	f.byID[d.ID] = d
	return nil
}
func (f *fakeDomainStore) SoftDeleteDomain(_ context.Context, orgID, id uuid.UUID) (bool, error) {
	if d, ok := f.byID[id]; ok && d.OrgID == orgID {
		delete(f.byID, id)
		delete(f.ownerByDomain, d.Domain)
		return true, nil
	}
	return false, nil
}

type fakeDomainsAPI struct {
	configured  bool
	createResp  *ResendDomain
	getResp     *ResendDomain
	verifyErr   error
	deleteErr   error
	deleteCalls []string
}

func (f *fakeDomainsAPI) Configured() bool { return f.configured }
func (f *fakeDomainsAPI) CreateDomain(_ context.Context, name, _, _ string) (*ResendDomain, error) {
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &ResendDomain{ID: "d_" + name, Name: name, Status: DomainStatusNotStarted, Region: "us-east-1"}, nil
}
func (f *fakeDomainsAPI) GetDomain(_ context.Context, id string) (*ResendDomain, error) {
	return f.getResp, nil
}
func (f *fakeDomainsAPI) VerifyDomain(_ context.Context, id string) error { return f.verifyErr }
func (f *fakeDomainsAPI) DeleteDomain(_ context.Context, id string) error {
	f.deleteCalls = append(f.deleteCalls, id)
	return f.deleteErr
}

func newSvc(store domainStore, api domainsAPI, txt txtLookupFunc) *DomainService {
	return &DomainService{repo: store, client: api, lookup: txt, now: func() time.Time { return testNow }}
}

func noTXT(string) ([]string, error) { return nil, nil }
func dmarcTXT(policy string) txtLookupFunc {
	return func(string) ([]string, error) { return []string{"v=DMARC1; p=" + policy}, nil }
}
func errTXT(string) ([]string, error) { return nil, errors.New("SERVFAIL") }
func mapLookup(m map[string][]string) txtLookupFunc {
	return func(name string) ([]string, error) { return m[name], nil }
}

func verifiedRecords() []ResendDNSRecord {
	return []ResendDNSRecord{
		{Record: "SPF", Type: "MX", Status: "verified"},
		{Record: "SPF", Type: "TXT", Status: "verified"},
		{Record: "DKIM", Type: "TXT", Status: "verified"},
	}
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestAddDomain_FreshCreate(t *testing.T) {
	store := newFakeStore()
	api := &fakeDomainsAPI{configured: true, createResp: &ResendDomain{
		ID: "d1", Name: "example.com", Status: DomainStatusNotStarted, Region: "us-east-1",
		Records: []ResendDNSRecord{{Record: "SPF", Status: "not_started"}, {Record: "DKIM", Status: "not_started"}},
	}}
	svc := newSvc(store, api, noTXT)

	org, user := uuid.New(), uuid.New()
	d, err := svc.AddDomain(context.Background(), org, user, "https://www.Example.com/", "track")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if d.Domain != "example.com" || d.ResendDomainID != "d1" || d.ReturnPath != "send.example.com" {
		t.Fatalf("unexpected: %+v", d)
	}
	if d.SPFVerified || d.DKIMVerified || d.VerifiedAt != nil || d.DMARCPolicy != nil {
		t.Fatalf("fresh domain should be unverified: %+v", d)
	}
	if d.TrackingSubdomain == nil || *d.TrackingSubdomain != "track" {
		t.Fatalf("tracking subdomain not set: %+v", d.TrackingSubdomain)
	}
	if d.CreatedBy == nil || *d.CreatedBy != user {
		t.Fatalf("created_by not set")
	}
}

func TestAddDomain_Errors(t *testing.T) {
	org := uuid.New()
	t.Run("invalid domain", func(t *testing.T) {
		svc := newSvc(newFakeStore(), &fakeDomainsAPI{configured: true}, noTXT)
		if _, err := svc.AddDomain(context.Background(), org, uuid.Nil, "notadomain", ""); !errors.Is(err, ErrDomainInvalid) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("not configured", func(t *testing.T) {
		svc := newSvc(newFakeStore(), &fakeDomainsAPI{configured: false}, noTXT)
		if _, err := svc.AddDomain(context.Background(), org, uuid.Nil, "example.com", ""); !errors.Is(err, ErrSendingNotConfigured) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("cross-org collision", func(t *testing.T) {
		store := newFakeStore()
		store.ownerByDomain["example.com"] = uuid.New() // some other org
		svc := newSvc(store, &fakeDomainsAPI{configured: true}, noTXT)
		if _, err := svc.AddDomain(context.Background(), org, uuid.Nil, "example.com", ""); !errors.Is(err, ErrDomainTakenOtherOrg) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("same-org already added", func(t *testing.T) {
		store := newFakeStore()
		store.ownerByDomain["example.com"] = org
		svc := newSvc(store, &fakeDomainsAPI{configured: true}, noTXT)
		if _, err := svc.AddDomain(context.Background(), org, uuid.Nil, "example.com", ""); !errors.Is(err, ErrDomainExists) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestRefreshDomain_BecomesVerified(t *testing.T) {
	store := newFakeStore()
	org := uuid.New()
	d := &EmailDomain{OrgID: org, Domain: "example.com", ResendDomainID: "d1", Status: DomainStatusPending}
	_ = store.CreateDomain(context.Background(), d)
	api := &fakeDomainsAPI{configured: true, getResp: &ResendDomain{
		ID: "d1", Status: DomainStatusVerified, Region: "us-east-1", Records: verifiedRecords(),
	}}
	svc := newSvc(store, api, dmarcTXT("none"))

	got, err := svc.RefreshDomain(context.Background(), org, d.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if !got.SPFVerified || !got.DKIMVerified || !got.HasDMARC() || !got.CanBulkSend() {
		t.Fatalf("should be fully verified: %+v", got)
	}
	if got.VerifiedAt == nil || !got.VerifiedAt.Equal(testNow) {
		t.Fatalf("verified_at not stamped: %v", got.VerifiedAt)
	}
	if got.DMARCPolicy == nil || *got.DMARCPolicy != "none" {
		t.Fatalf("dmarc policy = %v", got.DMARCPolicy)
	}
}

func TestCanBulkSend_TruthTable(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()

	t.Run("no domain", func(t *testing.T) {
		svc := newSvc(newFakeStore(), &fakeDomainsAPI{configured: true}, noTXT)
		ok, reason, _ := svc.CanBulkSend(ctx, org)
		if ok || reason != "no_domain" {
			t.Fatalf("got (%v,%q)", ok, reason)
		}
	})
	t.Run("verified but no DMARC", func(t *testing.T) {
		store := newFakeStore()
		_ = store.CreateDomain(ctx, &EmailDomain{OrgID: org, Domain: "example.com", SPFVerified: true, DKIMVerified: true})
		svc := newSvc(store, &fakeDomainsAPI{configured: true}, noTXT)
		ok, reason, _ := svc.CanBulkSend(ctx, org)
		if ok || reason != "dmarc_missing" {
			t.Fatalf("got (%v,%q)", ok, reason)
		}
	})
	t.Run("fully verified", func(t *testing.T) {
		store := newFakeStore()
		p := "none"
		_ = store.CreateDomain(ctx, &EmailDomain{OrgID: org, Domain: "example.com", SPFVerified: true, DKIMVerified: true, DMARCPolicy: &p})
		svc := newSvc(store, &fakeDomainsAPI{configured: true}, noTXT)
		ok, reason, _ := svc.CanBulkSend(ctx, org)
		if !ok || reason != "" {
			t.Fatalf("got (%v,%q)", ok, reason)
		}
	})
}

func TestRemoveDomain(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()
	t.Run("deletes from resend then soft-deletes", func(t *testing.T) {
		store := newFakeStore()
		d := &EmailDomain{OrgID: org, Domain: "example.com", ResendDomainID: "d1"}
		_ = store.CreateDomain(ctx, d)
		api := &fakeDomainsAPI{configured: true}
		svc := newSvc(store, api, noTXT)
		if err := svc.RemoveDomain(ctx, org, d.ID); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if len(api.deleteCalls) != 1 || api.deleteCalls[0] != "d1" {
			t.Fatalf("resend delete not called: %v", api.deleteCalls)
		}
		if _, ok := store.byID[d.ID]; ok {
			t.Fatalf("row not soft-deleted")
		}
	})
	t.Run("tolerates a 404 from resend", func(t *testing.T) {
		store := newFakeStore()
		d := &EmailDomain{OrgID: org, Domain: "gone.com", ResendDomainID: "d2"}
		_ = store.CreateDomain(ctx, d)
		api := &fakeDomainsAPI{configured: true, deleteErr: &ResendAPIError{Status: 404, Body: "{}"}}
		svc := newSvc(store, api, noTXT)
		if err := svc.RemoveDomain(ctx, org, d.ID); err != nil {
			t.Fatalf("404 should be tolerated, got %v", err)
		}
		if _, ok := store.byID[d.ID]; ok {
			t.Fatalf("row not soft-deleted after 404")
		}
	})
	t.Run("aborts on a non-404 resend error", func(t *testing.T) {
		store := newFakeStore()
		d := &EmailDomain{OrgID: org, Domain: "keep.com", ResendDomainID: "d3"}
		_ = store.CreateDomain(ctx, d)
		api := &fakeDomainsAPI{configured: true, deleteErr: &ResendAPIError{Status: 500, Body: "{}"}}
		svc := newSvc(store, api, noTXT)
		if err := svc.RemoveDomain(ctx, org, d.ID); err == nil {
			t.Fatalf("expected error on 500")
		}
		if _, ok := store.byID[d.ID]; !ok {
			t.Fatalf("row should NOT be soft-deleted when resend delete fails")
		}
	})
}

func TestCheckDMARC(t *testing.T) {
	svc := func(l txtLookupFunc) *DomainService { return newSvc(newFakeStore(), &fakeDomainsAPI{configured: true}, l) }

	t.Run("apex with own record", func(t *testing.T) {
		pol, resolved := svc(dmarcTXT("reject")).checkDMARC("acme.com")
		if !resolved || pol == nil || *pol != "reject" {
			t.Fatalf("got (%v, resolved=%v)", pol, resolved)
		}
	})
	t.Run("apex with no record is authoritative-absent", func(t *testing.T) {
		pol, resolved := svc(noTXT).checkDMARC("acme.com")
		if !resolved || pol != nil {
			t.Fatalf("got (%v, resolved=%v), want (nil, true)", pol, resolved)
		}
	})
	t.Run("transient lookup error is unresolved (keep stored)", func(t *testing.T) {
		pol, resolved := svc(errTXT).checkDMARC("acme.com")
		if resolved || pol != nil {
			t.Fatalf("got (%v, resolved=%v), want (nil, false)", pol, resolved)
		}
	})
	t.Run("subdomain inherits org policy (p)", func(t *testing.T) {
		l := mapLookup(map[string][]string{"_dmarc.acme.com": {"v=DMARC1; p=reject"}})
		pol, resolved := svc(l).checkDMARC("mail.acme.com")
		if !resolved || pol == nil || *pol != "reject" {
			t.Fatalf("got (%v, resolved=%v), want (reject, true)", pol, resolved)
		}
	})
	t.Run("subdomain honors sp over p", func(t *testing.T) {
		l := mapLookup(map[string][]string{"_dmarc.acme.com": {"v=DMARC1; p=none; sp=quarantine"}})
		pol, resolved := svc(l).checkDMARC("mail.acme.com")
		if !resolved || pol == nil || *pol != "quarantine" {
			t.Fatalf("got (%v, resolved=%v), want (quarantine, true)", pol, resolved)
		}
	})
}

func TestRefreshDomain_TransientDMARCKeepsStoredPolicy(t *testing.T) {
	store := newFakeStore()
	org := uuid.New()
	reject := "reject"
	// A fully-verified domain with a stored DMARC policy.
	d := &EmailDomain{OrgID: org, Domain: "acme.com", ResendDomainID: "d1", SPFVerified: true, DKIMVerified: true, DMARCPolicy: &reject}
	_ = store.CreateDomain(context.Background(), d)
	api := &fakeDomainsAPI{configured: true, getResp: &ResendDomain{ID: "d1", Status: DomainStatusVerified, Records: verifiedRecords()}}
	// DNS blip during the refresh.
	svc := newSvc(store, api, errTXT)

	got, err := svc.RefreshDomain(context.Background(), org, d.ID)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got.DMARCPolicy == nil || *got.DMARCPolicy != "reject" {
		t.Fatalf("transient DNS error must NOT downgrade DMARC; got %v", got.DMARCPolicy)
	}
	if !got.CanBulkSend() {
		t.Fatalf("domain should remain bulk-sendable across a transient DNS blip")
	}
}

func TestAddDomain_UniqueViolationMapsToConflict(t *testing.T) {
	org := uuid.New()
	other := uuid.New()

	t.Run("lost the race to another org → cleans up Resend + 409 other-org", func(t *testing.T) {
		store := newFakeStore()
		store.createErr = &pgconn.PgError{Code: "23505"}
		store.createErrOwner = other
		api := &fakeDomainsAPI{configured: true, createResp: &ResendDomain{ID: "dX", Status: DomainStatusNotStarted}}
		svc := newSvc(store, api, noTXT)
		_, err := svc.AddDomain(context.Background(), org, uuid.Nil, "acme.com", "")
		if !errors.Is(err, ErrDomainTakenOtherOrg) {
			t.Fatalf("got %v, want ErrDomainTakenOtherOrg", err)
		}
		if len(api.deleteCalls) != 1 || api.deleteCalls[0] != "dX" {
			t.Fatalf("orphaned Resend domain not rolled back: %v", api.deleteCalls)
		}
	})
	t.Run("same-org concurrent add → 409 already-exists", func(t *testing.T) {
		store := newFakeStore()
		store.createErr = &pgconn.PgError{Code: "23505"}
		store.createErrOwner = org
		api := &fakeDomainsAPI{configured: true, createResp: &ResendDomain{ID: "dY", Status: DomainStatusNotStarted}}
		svc := newSvc(store, api, noTXT)
		_, err := svc.AddDomain(context.Background(), org, uuid.Nil, "acme.com", "")
		if !errors.Is(err, ErrDomainExists) {
			t.Fatalf("got %v, want ErrDomainExists", err)
		}
	})
}

func TestFromDomainAllowed(t *testing.T) {
	org := uuid.New()
	ctx := context.Background()
	store := newFakeStore()
	p := "none"
	_ = store.CreateDomain(ctx, &EmailDomain{OrgID: org, Domain: "example.com", SPFVerified: true, DKIMVerified: true, DMARCPolicy: &p})
	svc := newSvc(store, &fakeDomainsAPI{configured: true}, noTXT)

	for in, want := range map[string]bool{
		"example.com":      true,
		"mail.example.com": true,
		"other.com":        false,
		"notexample.com":   false,
	} {
		ok, err := svc.FromDomainAllowed(ctx, org, in)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if ok != want {
			t.Errorf("FromDomainAllowed(%q)=%v want %v", in, ok, want)
		}
	}
}
