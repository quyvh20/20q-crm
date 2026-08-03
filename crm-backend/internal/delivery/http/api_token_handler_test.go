package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// api_token_handler_test.go covers the personal-access-token routes (U6.5).
//
// These three handlers have no capability gate by design: every route is
// self-scoped, so the ONLY thing standing between a caller and someone else's
// tokens is that the identity comes from the session and never from the request.
// That is what these tests pin.

func apiTokenFixture(userID uuid.UUID) domain.APIToken {
	return domain.APIToken{
		ID: uuid.New(), OrgID: uuid.New(), UserID: userID,
		Name: "CI deploy", Prefix: "crm_pat_a1b2", Scopes: domain.StringList{domain.CapRecordsWrite},
	}
}

// TestAPITokenList_ScopedToSessionOrgAndUser — a user_id in the query string must
// be ignored outright. If it were ever honored, listing another member's tokens
// would be a single query parameter away on a route with no capability gate.
func TestAPITokenList_ScopedToSessionOrgAndUser(t *testing.T) {
	uc := &fakeAPITokenUC{}
	sessionOrg, sessionUser := uuid.New(), uuid.New()
	victim := uuid.New()
	uc.tokens = []domain.APIToken{apiTokenFixture(sessionUser)}
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), sessionOrg, sessionUser)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/api/auth/api-tokens?user_id="+victim.String()+"&org_id="+uuid.New().String(), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if len(uc.listCalls) != 1 {
		t.Fatalf("List called %d times", len(uc.listCalls))
	}
	got := uc.listCalls[0]
	if got.orgID != sessionOrg || got.userID != sessionUser {
		t.Errorf("List(org=%v, user=%v), want the SESSION pair (%v, %v)", got.orgID, got.userID, sessionOrg, sessionUser)
	}
}

func TestAPITokenCreate_ScopedToSessionOrgAndUser(t *testing.T) {
	uc := &fakeAPITokenUC{}
	sessionOrg, sessionUser := uuid.New(), uuid.New()
	victim := uuid.New()
	uc.created = &domain.CreatedAPIToken{Token: apiTokenFixture(sessionUser), Secret: "crm_pat_secret"}
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), sessionOrg, sessionUser)

	// A body that tries to name a different owner, plus a query parameter doing the
	// same. Neither may have any effect.
	body := `{"name":"CI deploy","scopes":["records.write"],"user_id":"` + victim.String() +
		`","org_id":"` + uuid.New().String() + `"}`
	w := post(r, "/api/auth/api-tokens?user_id="+victim.String(), body)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	if len(uc.createCalls) != 1 {
		t.Fatalf("Create called %d times", len(uc.createCalls))
	}
	got := uc.createCalls[0]
	if got.orgID != sessionOrg || got.userID != sessionUser {
		t.Errorf("Create(org=%v, user=%v), want the SESSION pair (%v, %v)", got.orgID, got.userID, sessionOrg, sessionUser)
	}
	if got.input.Name != "CI deploy" || len(got.input.Scopes) != 1 {
		t.Errorf("input not threaded: %+v", got.input)
	}
}

func TestAPITokenRevoke_ScopedToSessionOrgAndUser(t *testing.T) {
	uc := &fakeAPITokenUC{}
	sessionOrg, sessionUser := uuid.New(), uuid.New()
	victim, tokenID := uuid.New(), uuid.New()
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), sessionOrg, sessionUser)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete,
		"/api/auth/api-tokens/"+tokenID.String()+"?user_id="+victim.String(), nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if len(uc.revokeCalls) != 1 {
		t.Fatalf("Revoke called %d times", len(uc.revokeCalls))
	}
	got := uc.revokeCalls[0]
	if got.orgID != sessionOrg || got.userID != sessionUser {
		t.Errorf("Revoke(org=%v, user=%v), want the SESSION pair (%v, %v)", got.orgID, got.userID, sessionOrg, sessionUser)
	}
	if got.id != tokenID {
		t.Errorf("Revoke id = %v, want %v", got.id, tokenID)
	}
}

// TestAPITokenRoutes_401WithoutOrgContext pins the `orgID, ok := GetOrgID(c)` guard.
// It is load-bearing for the line right below it: `userID, _ := GetUserID(c)`
// DISCARDS its ok. If the org guard were relaxed to the same discard form, a
// request that somehow reached these handlers without a session would mint and
// list tokens for (uuid.Nil, uuid.Nil) — a shared, unowned token bucket.
func TestAPITokenRoutes_401WithoutOrgContext(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list", http.MethodGet, "/api/auth/api-tokens", ""},
		{"create", http.MethodPost, "/api/auth/api-tokens", `{"name":"x","scopes":["records.write"]}`},
		{"revoke", http.MethodDelete, "/api/auth/api-tokens/" + uuid.New().String(), ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeAPITokenUC{}
			// A user id but NO org id — the shape a bug in the middleware could produce.
			r := mountAPITokenRoutes(NewAPITokenHandler(uc), uuid.Nil, uuid.New())

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body: %s", w.Code, w.Body.String())
			}
			if len(uc.listCalls)+len(uc.createCalls)+len(uc.revokeCalls) != 0 {
				t.Error("an org-less request reached the usecase")
			}
		})
	}
}

// TestAPITokenCreate_ReturnsSecretExactlyOnceAt201: the plaintext secret exists
// outside the caller's machine for exactly this one response. It must be there
// (nothing can recover it later) and it must be there once — a second copy
// embedded in the token object would widen the blast radius of any log or replay
// of this payload.
func TestAPITokenCreate_ReturnsSecretExactlyOnceAt201(t *testing.T) {
	uc := &fakeAPITokenUC{}
	sessionUser := uuid.New()
	const secret = "crm_pat_0123456789abcdef0123456789abcdef"
	tok := apiTokenFixture(sessionUser)
	tok.TokenHash = "sha256-of-the-secret-must-never-serialize"
	uc.created = &domain.CreatedAPIToken{Token: tok, Secret: secret}
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), uuid.New(), sessionUser)

	w := post(r, "/api/auth/api-tokens", `{"name":"CI deploy","scopes":["records.write"]}`)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if n := strings.Count(body, secret); n != 1 {
		t.Errorf("secret appears %d times in the response, want exactly 1: %s", n, body)
	}
	if strings.Contains(body, "sha256-of-the-secret-must-never-serialize") {
		t.Errorf("the stored token hash serialized to the client: %s", body)
	}

	var resp struct {
		Data struct {
			Secret string          `json:"secret"`
			Token  domain.APIToken `json:"token"`
		} `json:"data"`
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v (%s)", err, body)
	}
	if resp.Data.Secret != secret {
		t.Errorf("secret = %q", resp.Data.Secret)
	}
	if resp.Data.Token.Prefix != "crm_pat_a1b2" {
		t.Errorf("display prefix missing: %+v", resp.Data.Token)
	}
	if resp.Error != nil {
		t.Errorf("error = %v, want null", *resp.Error)
	}
}

// TestAPITokenCreate_RequiresNameAndScopes: a token that grants nothing is a
// footgun; one that implicitly grants everything is worse. Both are prevented by
// binding tags, which is exactly the kind of control that vanishes silently.
func TestAPITokenCreate_RequiresNameAndScopes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty object", `{}`},
		{"no scopes key", `{"name":"CI deploy"}`},
		{"null scopes", `{"name":"CI deploy","scopes":null}`},
		{"no name", `{"scopes":["records.write"]}`},
		{"empty name", `{"name":"","scopes":["records.write"]}`},
		{"malformed json", `{"name":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeAPITokenUC{}
			r := mountAPITokenRoutes(NewAPITokenHandler(uc), uuid.New(), uuid.New())

			w := post(r, "/api/auth/api-tokens", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body: %s", w.Code, w.Body.String())
			}
			if len(uc.createCalls) != 0 {
				t.Error("an invalid create reached the usecase")
			}
		})
	}
}

// TestAPITokenCreate_EmptyScopesArrayIsNotCaughtByTheBindingTag documents a real
// layering subtlety rather than asserting what we might wish were true.
//
// CreateAPITokenInput.Scopes carries `binding:"required"`, and the field comment
// says "Scopes must be non-empty". For a SLICE, validator/v10's `required` is a
// nil check — `"scopes":[]` deserializes to a non-nil, zero-length slice and sails
// straight through binding. The "pick at least one thing this token may do" rule
// is enforced one layer down, in apiTokenUseCase.Create.
//
// So the guarantee holds, but not where the tag suggests. Pinned here so that a
// future refactor which moves scope validation OUT of the usecase (e.g. "the
// binding tag already covers it") is caught: this test would then start minting
// zero-scope tokens. `binding:"required,min=1"` would move the control to the tag.
func TestAPITokenCreate_EmptyScopesArrayIsNotCaughtByTheBindingTag(t *testing.T) {
	uc := &fakeAPITokenUC{}
	uc.created = &domain.CreatedAPIToken{Token: apiTokenFixture(uuid.New()), Secret: "crm_pat_x"}
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), uuid.New(), uuid.New())

	w := post(r, "/api/auth/api-tokens", `{"name":"CI deploy","scopes":[]}`)

	if w.Code == http.StatusBadRequest {
		t.Fatalf("the binding tag now rejects an empty scopes array — good news; " +
			"update this test and drop the usecase's len(Scopes)==0 guard note")
	}
	if len(uc.createCalls) != 1 {
		t.Fatalf("Create called %d times, want 1", len(uc.createCalls))
	}
	if len(uc.createCalls[0].input.Scopes) != 0 {
		t.Fatalf("scopes = %v, want the empty slice to arrive intact", uc.createCalls[0].input.Scopes)
	}
	t.Log("confirmed: an empty scopes array passes binding; apiTokenUseCase.Create is the control that rejects it")
}

func TestAPITokenRevoke_BadIDIs400(t *testing.T) {
	uc := &fakeAPITokenUC{}
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), uuid.New(), uuid.New())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/auth/api-tokens/not-a-uuid", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if len(uc.revokeCalls) != 0 {
		t.Error("an unparseable token id reached the usecase")
	}
}

// TestAPITokenHandlers_ResponseEnvelopeMatchesDomainSuccess: these three handlers
// hand-roll `gin.H{"data": …, "error": nil}` instead of calling domain.Success.
// The SPA reads every response through one envelope parser, so the hand-rolled
// form has to serialize byte-identically — this test is what notices if
// domain.APIResponse gains a field or a handler drifts.
func TestAPITokenHandlers_ResponseEnvelopeMatchesDomainSuccess(t *testing.T) {
	sessionUser := uuid.New()
	tokens := []domain.APIToken{apiTokenFixture(sessionUser)}

	uc := &fakeAPITokenUC{tokens: tokens}
	r := mountAPITokenRoutes(NewAPITokenHandler(uc), uuid.New(), sessionUser)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/api-tokens", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	want, err := json.Marshal(domain.Success(tokens))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := strings.TrimSpace(w.Body.String()); got != string(want) {
		t.Errorf("envelope drift:\n got:  %s\n want: %s", got, want)
	}

	// The revoke envelope carries a bare string in data, same shape.
	uc2 := &fakeAPITokenUC{}
	r2 := mountAPITokenRoutes(NewAPITokenHandler(uc2), uuid.New(), sessionUser)
	w2 := httptest.NewRecorder()
	r2.ServeHTTP(w2, httptest.NewRequest(http.MethodDelete, "/api/auth/api-tokens/"+uuid.New().String(), nil))

	wantRevoke, _ := json.Marshal(domain.Success("revoked"))
	if got := strings.TrimSpace(w2.Body.String()); got != string(wantRevoke) {
		t.Errorf("revoke envelope drift:\n got:  %s\n want: %s", got, wantRevoke)
	}
}
