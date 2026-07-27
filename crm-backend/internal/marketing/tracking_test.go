package marketing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestDomainsClient_EnableTracking pins the Resend PATCH request shape.
func TestDomainsClient_EnableTracking(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"object":"domain","id":"d1","name":"example.com","status":"pending","region":"us-east-1",
			"records":[
				{"record":"DKIM","type":"TXT","status":"pending","value":"z"},
				{"record":"Tracking","name":"track","type":"CNAME","status":"not_started","value":"track.resend.com"}
			]}`))
	}))
	defer srv.Close()

	c := NewDomainsClient("re_test")
	c.baseURL = srv.URL

	out, err := c.EnableTracking(context.Background(), "d1")
	if err != nil {
		t.Fatalf("enable tracking: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/domains/d1" {
		t.Fatalf("request = %s %s, want PATCH /domains/d1", gotMethod, gotPath)
	}
	if gotBody["open_tracking"] != true || gotBody["click_tracking"] != true {
		t.Fatalf("body = %v, want open_tracking + click_tracking = true", gotBody)
	}
	if len(out.Records) != 2 {
		t.Fatalf("expected the tracking-enabled records back, got %+v", out.Records)
	}
}

// TestAddDomain_EnablesTracking proves AddDomain enables tracking on the created domain
// and stores the enriched records (incl. the Tracking CNAME the customer must publish).
func TestAddDomain_EnablesTracking(t *testing.T) {
	store := newFakeStore()
	api := &fakeDomainsAPI{
		configured: true,
		createResp: &ResendDomain{ID: "d1", Name: "example.com", Status: DomainStatusNotStarted, Region: "us-east-1",
			Records: []ResendDNSRecord{{Record: "SPF", Status: "not_started"}, {Record: "DKIM", Status: "not_started"}}},
		trackingResp: &ResendDomain{ID: "d1", Name: "example.com", Status: DomainStatusPending, Region: "us-east-1",
			Records: []ResendDNSRecord{
				{Record: "SPF", Status: "not_started"},
				{Record: "DKIM", Status: "not_started"},
				{Record: "Tracking", Type: "CNAME", Status: "not_started", Value: "track.resend.com"},
			}},
	}
	svc := newSvc(store, api, noTXT)

	d, err := svc.AddDomain(context.Background(), uuid.New(), uuid.New(), "example.com", "track")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(api.trackingCalls) != 1 || api.trackingCalls[0] != "d1" {
		t.Fatalf("EnableTracking not called for the created domain: %v", api.trackingCalls)
	}
	var recs []ResendDNSRecord
	if err := json.Unmarshal([]byte(d.DNSRecords), &recs); err != nil {
		t.Fatalf("stored records: %v", err)
	}
	found := false
	for _, r := range recs {
		if r.Record == "Tracking" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the Tracking CNAME was not stored on the domain: %+v", recs)
	}
}

// TestAddDomain_TrackingFailureDoesNotBlock proves a PATCH failure never blocks
// onboarding — the domain still sends without tracking (analytics just stay empty).
func TestAddDomain_TrackingFailureDoesNotBlock(t *testing.T) {
	store := newFakeStore()
	api := &fakeDomainsAPI{
		configured: true,
		createResp: &ResendDomain{ID: "d1", Name: "example.com", Status: DomainStatusNotStarted,
			Records: []ResendDNSRecord{{Record: "SPF", Status: "not_started"}, {Record: "DKIM", Status: "not_started"}}},
		trackingErr: errors.New("resend 500"),
	}
	svc := newSvc(store, api, noTXT)

	d, err := svc.AddDomain(context.Background(), uuid.New(), uuid.New(), "example.com", "track")
	if err != nil {
		t.Fatalf("a tracking-enable failure must not block onboarding: %v", err)
	}
	if d.ResendDomainID != "d1" {
		t.Fatalf("domain was not created: %+v", d)
	}
}
