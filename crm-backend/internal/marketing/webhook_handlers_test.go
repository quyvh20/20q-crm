package marketing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWebhookStore struct {
	owners    map[string]uuid.UUID
	ownerErr  error
	inserted  []*MarketingEmailEvent
	insertErr error
}

func (f *fakeWebhookStore) DomainOwnerOrg(_ context.Context, domain string) (uuid.UUID, bool, error) {
	if f.ownerErr != nil {
		return uuid.Nil, false, f.ownerErr
	}
	if id, ok := f.owners[domain]; ok {
		return id, true, nil
	}
	return uuid.Nil, false, nil
}

func (f *fakeWebhookStore) InsertEvent(_ context.Context, e *MarketingEmailEvent) (bool, error) {
	if f.insertErr != nil {
		return false, f.insertErr
	}
	f.inserted = append(f.inserted, e)
	return true, nil
}

var fixedNow = time.Unix(1_700_000_000, 0)

func newWebhookServer(t *testing.T, store resendWebhookStore, limiter ipRateLimiter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewResendWebhookHandler(store, limiter, testWhsec, "production", nil)
	h.now = func() time.Time { return fixedNow }
	h.RegisterRoutes(r)
	return r
}

func signedRequest(body string) *http.Request {
	ts := strconv.FormatInt(fixedNow.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/api/marketing/webhooks/resend", strings.NewReader(body))
	req.Header.Set("svix-id", "msg_test")
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", signSvix(testWhsec, "msg_test", ts, []byte(body)))
	return req
}

func TestResendWebhook_ValidEvent_Enqueued(t *testing.T) {
	org := uuid.New()
	store := &fakeWebhookStore{owners: map[string]uuid.UUID{"send.acme.com": org}}
	r := newWebhookServer(t, store, fakeLimiter{allow: true})

	body := `{"type":"email.bounced","created_at":"2026-07-25T00:00:00Z","data":{"email_id":"e1","from":"Acme <noreply@send.acme.com>","to":["user@example.com"],"bounce":{"type":"Permanent"}}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedRequest(body))

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.inserted, 1)
	e := store.inserted[0]
	assert.Equal(t, org, e.OrgID)
	assert.Equal(t, "msg_test", e.SvixID)
	assert.Equal(t, ResendTypeBounced, e.EventType)
	assert.Equal(t, "send.acme.com", e.FromDomain)
	assert.Equal(t, "user@example.com", e.EmailNormalized)
	assert.Equal(t, ReasonHardBounce, e.Reason)
	assert.Equal(t, "permanent", e.BounceType)
	assert.Equal(t, EventStatusPending, e.Status)
}

func TestResendWebhook_BadSignature_401NoWrite(t *testing.T) {
	store := &fakeWebhookStore{owners: map[string]uuid.UUID{"send.acme.com": uuid.New()}}
	r := newWebhookServer(t, store, fakeLimiter{allow: true})

	body := `{"type":"email.complained","data":{"from":"x@send.acme.com","to":["u@e.com"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/marketing/webhooks/resend", strings.NewReader(body))
	req.Header.Set("svix-id", "m")
	req.Header.Set("svix-timestamp", strconv.FormatInt(fixedNow.Unix(), 10))
	req.Header.Set("svix-signature", "v1,deadbeef") // wrong
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, store.inserted, "a bad signature must never write (fail-closed)")
}

func TestResendWebhook_UnresolvedOrg_DroppedButAcked(t *testing.T) {
	// The global platform domain is owned by no org (the pre-M7 reality).
	store := &fakeWebhookStore{owners: map[string]uuid.UUID{}}
	r := newWebhookServer(t, store, fakeLimiter{allow: true})

	body := `{"type":"email.bounced","data":{"from":"noreply@twentyq.io","to":["u@e.com"],"bounce":{"type":"Permanent"}}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedRequest(body))

	assert.Equal(t, http.StatusOK, w.Code, "authentic-but-unownable events are ACKed 200, never 5xx")
	assert.Empty(t, store.inserted, "an unresolved event is dropped, never enqueued")
}

func TestResendWebhook_ParentDomainFallback(t *testing.T) {
	org := uuid.New()
	// The org owns the apex; the send came from an aligned subdomain.
	store := &fakeWebhookStore{owners: map[string]uuid.UUID{"acme.com": org}}
	r := newWebhookServer(t, store, fakeLimiter{allow: true})

	body := `{"type":"email.complained","data":{"from":"noreply@mail.acme.com","to":["u@e.com"]}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedRequest(body))

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.inserted, 1)
	assert.Equal(t, org, store.inserted[0].OrgID)
	assert.Equal(t, ReasonComplaint, store.inserted[0].Reason)
}

func TestResendWebhook_RateLimited_429(t *testing.T) {
	store := &fakeWebhookStore{owners: map[string]uuid.UUID{}}
	r := newWebhookServer(t, store, fakeLimiter{allow: false})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedRequest(`{"type":"email.delivered","data":{}}`))
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Empty(t, store.inserted)
}

func TestResendWebhook_TransientDBError_503(t *testing.T) {
	store := &fakeWebhookStore{owners: map[string]uuid.UUID{"send.acme.com": uuid.New()}, insertErr: assertErr("db down")}
	r := newWebhookServer(t, store, fakeLimiter{allow: true})

	body := `{"type":"email.bounced","data":{"from":"x@send.acme.com","to":["u@e.com"],"bounce":{"type":"Permanent"}}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, signedRequest(body))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "a transient enqueue failure must 503 so Resend redelivers")
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
