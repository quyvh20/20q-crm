package marketing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// resendBaseURL is the Resend API root. A field on the client (not a const) so
// tests can point it at an httptest server.
const resendBaseURL = "https://api.resend.com"

// Resend domain status values (dashboard/domains/introduction).
const (
	DomainStatusNotStarted        = "not_started"
	DomainStatusPending           = "pending"
	DomainStatusVerified          = "verified"
	DomainStatusPartiallyVerified = "partially_verified"
	DomainStatusPartiallyFailed   = "partially_failed"
	DomainStatusFailed            = "failed"
	DomainStatusTemporaryFailure  = "temporary_failure"
)

// ResendDNSRecord is one DNS record Resend asks the customer to publish. `Record`
// is the logical kind ("SPF", "DKIM", "Tracking"); `Type` is the DNS type
// ("MX", "TXT", "CNAME"); `Status` is that record's own verification state.
type ResendDNSRecord struct {
	Record   string `json:"record"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	TTL      string `json:"ttl"`
	Status   string `json:"status"`
	Value    string `json:"value"`
	Priority *int   `json:"priority,omitempty"`
}

// ResendDomain is the subset of the Resend domain object M2 uses.
type ResendDomain struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Region  string            `json:"region"`
	Records []ResendDNSRecord `json:"records"`
}

// DomainsClient talks to the Resend Domains API. It is independent of the send
// mailer but reuses the same RESEND_API_KEY.
type DomainsClient struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// NewDomainsClient builds a client. apiKey may be empty (dev / MAIL_DISABLED); the
// service layer decides whether to call — a call with an empty key returns a clear
// "not configured" error rather than a confusing 401.
func NewDomainsClient(apiKey string) *DomainsClient {
	return &DomainsClient{
		apiKey:  apiKey,
		baseURL: resendBaseURL,
		http:    &http.Client{Timeout: 20 * time.Second},
	}
}

// errNotConfigured is returned when no Resend API key is set.
var errNotConfigured = errors.New("marketing: Resend API key not configured")

// Configured reports whether the client can make calls.
func (c *DomainsClient) Configured() bool { return c.apiKey != "" }

type createDomainRequest struct {
	Name             string `json:"name"`
	Region           string `json:"region,omitempty"`
	CustomReturnPath string `json:"custom_return_path,omitempty"`
}

// CreateDomain registers a domain with Resend and returns it with the DNS records
// the customer must publish.
func (c *DomainsClient) CreateDomain(ctx context.Context, name, region, customReturnPath string) (*ResendDomain, error) {
	body := createDomainRequest{Name: name, Region: region, CustomReturnPath: customReturnPath}
	var out ResendDomain
	if err := c.do(ctx, http.MethodPost, "/domains", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDomain reads a domain's current status + records.
func (c *DomainsClient) GetDomain(ctx context.Context, id string) (*ResendDomain, error) {
	var out ResendDomain
	if err := c.do(ctx, http.MethodGet, "/domains/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VerifyDomain triggers Resend's asynchronous DNS re-check.
func (c *DomainsClient) VerifyDomain(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/domains/"+id+"/verify", nil, nil)
}

// DeleteDomain removes the domain from the Resend team.
func (c *DomainsClient) DeleteDomain(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/domains/"+id, nil, nil)
}

// do performs one Resend API call. On a non-2xx it returns an error carrying the
// status and the response body (Resend puts a JSON {message} there).
func (c *DomainsClient) do(ctx context.Context, method, path string, in, out any) error {
	if c.apiKey == "" {
		return errNotConfigured
	}
	var reader io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return &ResendAPIError{Status: resp.StatusCode, Body: string(bytes.TrimSpace(rb))}
	}
	if out != nil && len(rb) > 0 {
		if err := json.Unmarshal(rb, out); err != nil {
			return fmt.Errorf("marketing: decode Resend response: %w", err)
		}
	}
	return nil
}

// ResendAPIError carries a non-2xx Resend response.
type ResendAPIError struct {
	Status int
	Body   string
}

func (e *ResendAPIError) Error() string {
	return fmt.Sprintf("resend API error: status %d: %s", e.Status, e.Body)
}
