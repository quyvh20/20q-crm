package integrations

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
)

// testCodec builds a real envelope codec over a single throwaway key, for tests
// that need genuine seal/open behaviour without a database.
func testCodec(t *testing.T) *envelope.Codec {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(make([]byte, envelope.KeySize)) // all-zero key, fine for a test
	ring, err := envelope.ParseKeyring(key)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}
	return envelope.NewCodec(ring)
}

func TestSafeReturnTo(t *testing.T) {
	cases := map[string]string{
		"/settings/integrations":   "/settings/integrations",
		"/a/b?q=1":                 "/a/b?q=1",
		"":                         "",
		"  ":                       "",
		"//evil.example/phish":     "", // protocol-relative → another origin
		"https://evil.example":     "", // absolute → not same-site
		"javascript:alert(1)":      "", // not a path
		"settings/integrations":    "", // not rooted
	}
	for in, want := range cases {
		if got := safeReturnTo(in); got != want {
			t.Errorf("safeReturnTo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConnectionService_URLBuilders(t *testing.T) {
	// A trailing slash on either base must not double up in the joined URL.
	svc := NewConnectionService(nil, nil, nil, "https://api.example/", "https://app.example/", nil)

	if got := svc.redirectURI("facebook"); got != "https://api.example/api/integrations/providers/facebook/callback" {
		t.Errorf("redirectURI = %q (must byte-match the registered providers/:provider/callback route)", got)
	}
	if got := svc.PickerRedirect("facebook", "tok123"); got != "https://app.example/settings/integrations?connect=facebook#selection=tok123" {
		t.Errorf("PickerRedirect = %q", got)
	}
	if got := svc.ErrorRedirect("denied"); got != "https://app.example/settings/integrations?connect_error=denied" {
		t.Errorf("ErrorRedirect = %q", got)
	}
}

func TestPkcePair(t *testing.T) {
	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatalf("pkcePair: %v", err)
	}
	if verifier == "" || challenge == "" || verifier == challenge {
		t.Fatalf("verifier=%q challenge=%q", verifier, challenge)
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge != want {
		t.Errorf("challenge = %q, want S256(verifier) = %q", challenge, want)
	}
}

func TestRandToken(t *testing.T) {
	p1, h1, err := randToken()
	if err != nil {
		t.Fatalf("randToken: %v", err)
	}
	p2, _, _ := randToken()
	if p1 == p2 {
		t.Error("two tokens must differ")
	}
	if h1 != hashToken(p1) {
		t.Error("hash must be sha256 of the plaintext")
	}
	if p1 == h1 {
		t.Error("the plaintext must never equal its stored hash")
	}
}

func TestConnectionService_CredentialBindingIsLoadBearing(t *testing.T) {
	svc := NewConnectionService(nil, testCodec(t), nil, "", "", nil)
	org := uuid.New()
	id := uuid.New()
	creds := Credentials{AccessToken: "page-token-abc", Extra: map[string]any{"page_id": "123"}}

	blob, kv, err := svc.sealCredentials(org, id, creds)
	if err != nil {
		t.Fatalf("sealCredentials: %v", err)
	}
	if kv != 1 {
		t.Errorf("key version = %d, want 1", kv)
	}

	// Round-trips when opened under the SAME binding.
	got, err := svc.openCredentials(&IntegrationConnection{ID: id, OrgID: org, EncryptedCredentials: blob})
	if err != nil {
		t.Fatalf("openCredentials: %v", err)
	}
	if got.AccessToken != "page-token-abc" || got.Extra["page_id"] != "123" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Fails when the row id differs — a blob lifted onto another connection cannot
	// be opened. This is the whole point of binding the ciphertext to the row.
	if _, err := svc.openCredentials(&IntegrationConnection{ID: uuid.New(), OrgID: org, EncryptedCredentials: blob}); err == nil {
		t.Error("opening under a different connection id must fail")
	}
	// Fails when the org differs.
	if _, err := svc.openCredentials(&IntegrationConnection{ID: id, OrgID: uuid.New(), EncryptedCredentials: blob}); err == nil {
		t.Error("opening under a different org must fail")
	}
}

func TestConnectionService_SealRequiresCodec(t *testing.T) {
	// A nil codec is a valid "not configured" value; sealing must return an error,
	// not panic.
	svc := NewConnectionService(nil, nil, nil, "", "", nil)
	if _, _, err := svc.sealCredentials(uuid.New(), uuid.New(), Credentials{AccessToken: "x"}); err == nil {
		t.Error("sealing without a codec must error")
	}
}
