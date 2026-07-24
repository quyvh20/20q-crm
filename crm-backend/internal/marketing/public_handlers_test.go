package marketing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeUnsubStore struct {
	added     []*Suppression
	statusSet []string // "email=status"
	topics    []MarketingTopic
	addErr    error
}

func (f *fakeUnsubStore) AddSuppression(_ context.Context, s *Suppression) (bool, error) {
	if f.addErr != nil {
		return false, f.addErr
	}
	f.added = append(f.added, s)
	return true, nil
}

func (f *fakeUnsubStore) SetMarketingStatus(_ context.Context, _ uuid.UUID, email, status string) error {
	f.statusSet = append(f.statusSet, email+"="+status)
	return nil
}

func (f *fakeUnsubStore) ListTopicsByOrg(_ context.Context, _ uuid.UUID) ([]MarketingTopic, error) {
	return f.topics, nil
}

type fakeLimiter struct{ allow bool }

func (f fakeLimiter) AllowN(_ context.Context, _ string, _ int) (bool, time.Duration) {
	return f.allow, 0
}

func newPublicTestServer(t *testing.T, store unsubStore, tokens *TokenService, limiter ipRateLimiter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	orgName := func(_ context.Context, _ uuid.UUID) (string, error) { return "Acme Inc", nil }
	NewPublicUnsubHandler(store, tokens, limiter, orgName, nil).RegisterRoutes(r)
	return r
}

func TestPublicUnsub_OneClickGlobal(t *testing.T) {
	ts := testTokenService(t)
	org := uuid.New()
	tok, _ := ts.Mint(org, "jane@acme.com")
	store := &fakeUnsubStore{}
	r := newPublicTestServer(t, store, ts, fakeLimiter{allow: true})

	// RFC-8058 one-click: form body, not JSON.
	req := httptest.NewRequest(http.MethodPost, "/api/marketing/u/"+tok, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.added, 1)
	assert.Equal(t, org, store.added[0].OrgID)
	assert.Equal(t, "jane@acme.com", store.added[0].EmailNormalized)
	assert.Equal(t, ReasonUnsubscribe, store.added[0].Reason)
	assert.Equal(t, ScopeMarketing, store.added[0].Scope)
	assert.Nil(t, store.added[0].TopicID, "one-click is a GLOBAL opt-out (topic_id nil)")
	require.Len(t, store.statusSet, 1)
	assert.Equal(t, "jane@acme.com="+StatusUnsubscribed, store.statusSet[0])
}

func TestPublicUnsub_TopicOptDown(t *testing.T) {
	ts := testTokenService(t)
	org := uuid.New()
	topic := uuid.New()
	tok, _ := ts.Mint(org, "jane@acme.com")
	store := &fakeUnsubStore{}
	r := newPublicTestServer(t, store, ts, fakeLimiter{allow: true})

	body, _ := json.Marshal(map[string]string{"topic_id": topic.String()})
	req := httptest.NewRequest(http.MethodPost, "/api/marketing/u/"+tok, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, store.added, 1)
	require.NotNil(t, store.added[0].TopicID)
	assert.Equal(t, topic, *store.added[0].TopicID)
	assert.Empty(t, store.statusSet, "a per-topic opt-down must NOT flip the overall lifecycle status")
}

func TestPublicUnsub_PreferenceInfoNoStateLeak(t *testing.T) {
	ts := testTokenService(t)
	tok, _ := ts.Mint(uuid.New(), "jane@acme.com")
	store := &fakeUnsubStore{topics: []MarketingTopic{{ID: uuid.New(), Name: "Product news"}}}
	r := newPublicTestServer(t, store, ts, fakeLimiter{allow: true})

	req := httptest.NewRequest(http.MethodGet, "/api/marketing/u/"+tok, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp.Data["ok"])
	assert.Equal(t, "Acme Inc", resp.Data["org_name"])
	// The email must never be echoed, and no per-recipient subscription state must appear.
	assert.NotContains(t, w.Body.String(), "jane@acme.com")
	assert.NotContains(t, w.Body.String(), "subscribed")
	assert.NotContains(t, w.Body.String(), "marketing_status")
	topics, _ := resp.Data["topics"].([]any)
	assert.Len(t, topics, 1)
}

func TestPublicUnsub_BadTokenRejected(t *testing.T) {
	ts := testTokenService(t)
	store := &fakeUnsubStore{}
	r := newPublicTestServer(t, store, ts, fakeLimiter{allow: true})

	req := httptest.NewRequest(http.MethodPost, "/api/marketing/u/not-a-real-token", strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, store.added, "a bad token must never write a suppression (fail-closed)")
}

func TestPublicUnsub_NotConfiguredRejected(t *testing.T) {
	store := &fakeUnsubStore{}
	// A token service with no key can neither mint nor verify → every link is invalid.
	r := newPublicTestServer(t, store, NewTokenService(nil), fakeLimiter{allow: true})

	req := httptest.NewRequest(http.MethodPost, "/api/marketing/u/senv1.1.AAAA", strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, store.added)
}

func TestPublicUnsub_RateLimited(t *testing.T) {
	ts := testTokenService(t)
	tok, _ := ts.Mint(uuid.New(), "jane@acme.com")
	store := &fakeUnsubStore{}
	r := newPublicTestServer(t, store, ts, fakeLimiter{allow: false}) // deny

	req := httptest.NewRequest(http.MethodPost, "/api/marketing/u/"+tok, strings.NewReader(""))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Empty(t, store.added, "a rate-limited request must not reach the write path")
}
