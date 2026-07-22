package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// signFB builds the X-Hub-Signature-256 header value for a body under a secret.
func signFB(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestFacebook_VerifyWebhook(t *testing.T) {
	p := NewFacebookProvider("app", "the-app-secret", "", nil)
	body := []byte(`{"object":"page","entry":[]}`)

	t.Run("good signature passes", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-Hub-Signature-256", signFB("the-app-secret", body))
		if err := p.VerifyWebhook(r, body); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})

	t.Run("wrong secret fails", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-Hub-Signature-256", signFB("not-the-secret", body))
		if err := p.VerifyWebhook(r, body); err == nil {
			t.Fatal("a signature under the wrong secret must fail")
		}
	})

	t.Run("tampered body fails", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Header.Set("X-Hub-Signature-256", signFB("the-app-secret", body))
		// Same signature, different body — the whole point of signing the RAW bytes.
		if err := p.VerifyWebhook(r, []byte(`{"object":"page","entry":[{}]}`)); err == nil {
			t.Fatal("a body that does not match the signature must fail")
		}
	})

	t.Run("missing header fails", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if err := p.VerifyWebhook(r, body); err == nil {
			t.Fatal("a missing signature must fail")
		}
	})
}

func TestFacebook_ParseWebhook(t *testing.T) {
	p := NewFacebookProvider("app", "s", "", nil)
	body := []byte(`{
		"object":"page",
		"entry":[
			{"id":"page1","time":1,"changes":[
				{"field":"leadgen","value":{"leadgen_id":"L1","page_id":"page1","form_id":"F1"}},
				{"field":"leadgen","value":{"leadgen_id":"L2","form_id":"F2"}},
				{"field":"other","value":{"leadgen_id":"IGNORED"}},
				{"field":"leadgen","value":{"page_id":"page1"}}
			]},
			{"id":"page2","time":2,"changes":[
				{"field":"leadgen","value":{"leadgen_id":"L3","form_id":"F3"}}
			]}
		]
	}`)
	events, err := p.ParseWebhook(body)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	// L1, L2, L3 — the non-leadgen change and the leadgen change with no id are dropped.
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d: %+v", len(events), events)
	}
	if events[0].ProviderEventID != "L1" || events[0].ExternalAccountID != "page1" || events[0].FormID != "F1" {
		t.Errorf("event[0] = %+v", events[0])
	}
	// L2 had no page_id in the value → falls back to the entry id.
	if events[1].ProviderEventID != "L2" || events[1].ExternalAccountID != "page1" {
		t.Errorf("event[1] must fall back to entry id for page: %+v", events[1])
	}
	if events[2].ProviderEventID != "L3" || events[2].ExternalAccountID != "page2" {
		t.Errorf("event[2] = %+v", events[2])
	}
}

func TestFacebook_ParseWebhook_NumericIds(t *testing.T) {
	// Graph sometimes sends ids as JSON numbers; they must coerce to strings, not "".
	p := NewFacebookProvider("app", "s", "", nil)
	body := []byte(`{"object":"page","entry":[{"id":"page1","changes":[
		{"field":"leadgen","value":{"leadgen_id":123456789012345,"form_id":987654321}}
	]}]}`)
	events, err := p.ParseWebhook(body)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if len(events) != 1 || events[0].ProviderEventID != "123456789012345" || events[0].FormID != "987654321" {
		t.Fatalf("numeric ids must coerce to strings, got %+v", events)
	}
}

func TestFacebook_FetchLead(t *testing.T) {
	g := newFakeGraph(t, "app-secret")
	p := g.provider("app")
	lead, err := p.FetchLead(context.Background(), &IntegrationConnection{ExternalAccountID: "page1"},
		Credentials{AccessToken: "tok-page1"}, InboundEvent{ProviderEventID: "L1", FormID: "F1"})
	if err != nil {
		t.Fatalf("FetchLead: %v", err)
	}
	if lead.ProviderEventID != "L1" {
		t.Errorf("lead should carry the leadgen id, got %q", lead.ProviderEventID)
	}
	if lead.Fields["email"] != "lead@example.com" {
		t.Errorf("field_data not flattened: %+v", lead.Fields)
	}
	if lead.Context["form_id"] == nil {
		t.Errorf("form_id should ride the context: %+v", lead.Context)
	}
}

func TestWebhook_VerifyHandshake(t *testing.T) {
	h := NewWebhookHandler(nil, nil, ProviderKeyFacebook, "the-verify-token", nil, nil)
	router := gin.New()
	router.GET("/api/integrations/facebook/webhook", h.Verify)

	t.Run("correct token echoes the challenge", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/integrations/facebook/webhook?hub.mode=subscribe&hub.verify_token=the-verify-token&hub.challenge=CHALLENGE123", nil)
		router.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "CHALLENGE123" {
			t.Fatalf("want 200 + challenge, got %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("wrong token is 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/integrations/facebook/webhook?hub.mode=subscribe&hub.verify_token=WRONG&hub.challenge=x", nil)
		router.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("want 403, got %d", w.Code)
		}
	})

	t.Run("wrong mode is 403", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/integrations/facebook/webhook?hub.mode=unsubscribe&hub.verify_token=the-verify-token&hub.challenge=x", nil)
		router.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("want 403, got %d", w.Code)
		}
	})
}

// leadgenBody builds a leadgen webhook body for one page/form/leadgen id.
func leadgenBody(pageID, formID, leadgenID string) []byte {
	return []byte(`{"object":"page","entry":[{"id":"` + pageID + `","changes":[` +
		`{"field":"leadgen","value":{"leadgen_id":"` + leadgenID + `","page_id":"` + pageID + `","form_id":"` + formID + `"}}]}]}`)
}
