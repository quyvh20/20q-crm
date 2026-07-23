package marketing

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDomainsClient_CreateAndGet(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod, gotPath = r.Method, r.URL.Path
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/domains":
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"d1","name":"example.com","status":"not_started","region":"us-east-1",
				"records":[
					{"record":"SPF","name":"send","type":"MX","ttl":"Auto","status":"not_started","value":"feedback-smtp.us-east-1.amazonses.com","priority":10},
					{"record":"SPF","name":"send","type":"TXT","ttl":"Auto","status":"not_started","value":"v=spf1 include:amazonses.com ~all"},
					{"record":"DKIM","name":"resend._domainkey","type":"TXT","ttl":"Auto","status":"not_started","value":"p=MIGf..."}
				]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/domains/d1":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"object":"domain","id":"d1","name":"example.com","status":"verified","region":"us-east-1",
				"records":[
					{"record":"SPF","type":"MX","status":"verified","value":"x","priority":10},
					{"record":"SPF","type":"TXT","status":"verified","value":"y"},
					{"record":"DKIM","type":"TXT","status":"verified","value":"z"}
				]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	c := NewDomainsClient("re_test")
	c.baseURL = srv.URL

	created, err := c.CreateDomain(context.Background(), "example.com", "", "send")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID != "d1" || created.Status != DomainStatusNotStarted || len(created.Records) != 3 {
		t.Fatalf("unexpected create: %+v", created)
	}
	if created.Records[0].Priority == nil || *created.Records[0].Priority != 10 {
		t.Fatalf("MX priority not parsed: %+v", created.Records[0])
	}
	if gotAuth != "Bearer re_test" {
		t.Fatalf("auth header = %q", gotAuth)
	}

	got, err := c.GetDomain(context.Background(), "d1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != DomainStatusVerified {
		t.Fatalf("get status = %q", got.Status)
	}
	spf, dkim := deriveRecordVerification(got.Records)
	if !spf || !dkim {
		t.Fatalf("derive from verified get: spf=%v dkim=%v", spf, dkim)
	}
	_ = gotMethod
	_ = gotPath
}

func TestDomainsClient_VerifyAndDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"object":"domain","id":"d1"}`))
	}))
	defer srv.Close()
	c := NewDomainsClient("re_test")
	c.baseURL = srv.URL
	if err := c.VerifyDomain(context.Background(), "d1"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := c.DeleteDomain(context.Background(), "d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestDomainsClient_ErrorMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(`{"message":"The domain is already registered."}`))
	}))
	defer srv.Close()
	c := NewDomainsClient("re_test")
	c.baseURL = srv.URL
	_, err := c.CreateDomain(context.Background(), "example.com", "", "send")
	var apiErr *ResendAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected ResendAPIError, got %v", err)
	}
	if apiErr.Status != 422 {
		t.Fatalf("status = %d", apiErr.Status)
	}
	var body struct{ Message string }
	_ = json.Unmarshal([]byte(apiErr.Body), &body)
	if body.Message == "" {
		t.Fatalf("message not preserved in body: %q", apiErr.Body)
	}
}

func TestDomainsClient_NotConfigured(t *testing.T) {
	c := NewDomainsClient("")
	if c.Configured() {
		t.Fatal("empty key should be not configured")
	}
	_, err := c.CreateDomain(context.Background(), "example.com", "", "send")
	if !errors.Is(err, errNotConfigured) {
		t.Fatalf("expected errNotConfigured, got %v", err)
	}
}
