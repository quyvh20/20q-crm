package marketing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testWhsec = "whsec_" + "c2VjcmV0LWtleS1mb3ItdGVzdGluZy0zMi1ieXRlcyE=" // base64 of a 32-byte key

// signSvix produces a valid v1 svix-signature for (id, ts, body) under secret —
// exactly what Resend/Svix send. Shared by the handler test.
func signSvix(secret, id, ts string, body []byte) string {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "." + string(body)))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifySvix_Valid(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"type":"email.bounced"}`)
	h := svixHeaders{id: "msg_1", timestamp: ts, signature: signSvix(testWhsec, "msg_1", ts, body)}
	if err := verifySvix(h, body, testWhsec, now); err != nil {
		t.Fatalf("valid signature must verify: %v", err)
	}
}

func TestVerifySvix_RotationMultiSig(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"x":1}`)
	// One bogus sig + the real one, space-delimited (Svix rotation format).
	valid := signSvix(testWhsec, "msg_2", ts, body)
	h := svixHeaders{id: "msg_2", timestamp: ts, signature: "v1,AAAABBBBCCCCDDDD " + valid}
	if err := verifySvix(h, body, testWhsec, now); err != nil {
		t.Fatalf("must accept when ANY v1 sig matches (rotation): %v", err)
	}
}

func TestVerifySvix_TamperedBodyFails(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"amount":1}`)
	h := svixHeaders{id: "msg_3", timestamp: ts, signature: signSvix(testWhsec, "msg_3", ts, body)}
	if err := verifySvix(h, []byte(`{"amount":999}`), testWhsec, now); err == nil {
		t.Fatal("a body that differs from the signed bytes must fail")
	}
}

func TestVerifySvix_ExpiredTimestamp(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	oldTs := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10) // beyond 5m tolerance
	body := []byte(`{}`)
	h := svixHeaders{id: "msg_4", timestamp: oldTs, signature: signSvix(testWhsec, "msg_4", oldTs, body)}
	if err := verifySvix(h, body, testWhsec, now); err != ErrWebhookTimestamp {
		t.Fatalf("stale timestamp must fail with ErrWebhookTimestamp, got %v", err)
	}
}

func TestVerifySvix_MissingHeaders(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if err := verifySvix(svixHeaders{}, []byte(`{}`), testWhsec, now); err != ErrWebhookHeaders {
		t.Fatalf("missing headers must fail with ErrWebhookHeaders, got %v", err)
	}
}

func TestVerifySvix_NotConfigured(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	h := svixHeaders{id: "m", timestamp: ts, signature: "v1,x"}
	if err := verifySvix(h, []byte(`{}`), "", now); err != ErrWebhookNotConfigured {
		t.Fatalf("empty secret must fail closed with ErrWebhookNotConfigured, got %v", err)
	}
}

func TestVerifySvix_WrongSecretFails(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{}`)
	sig := signSvix(testWhsec, "m", ts, body)
	other := "whsec_" + base64.StdEncoding.EncodeToString([]byte("a-different-32-byte-key-aaaaaaaa"))
	h := svixHeaders{id: "m", timestamp: ts, signature: sig}
	if err := verifySvix(h, body, other, now); err != ErrWebhookSignature {
		t.Fatalf("a sig from another secret must fail with ErrWebhookSignature, got %v", err)
	}
}

func TestReadSvixHeaders_Aliases(t *testing.T) {
	get := func(k string) string {
		switch k {
		case "webhook-id":
			return "id-1"
		case "webhook-timestamp":
			return "123"
		case "webhook-signature":
			return "v1,sig"
		}
		return ""
	}
	h := readSvixHeaders(get)
	if h.id != "id-1" || h.timestamp != "123" || h.signature != "v1,sig" {
		t.Fatalf("must fall back to webhook-* aliases, got %+v", h)
	}
}
