package envelope

import (
	"strings"
	"testing"
)

const testPurpose = Purpose("marketing_unsubscribe")

func TestSealStateless_RoundTrip(t *testing.T) {
	c := mustCodec(t, testKey(1))
	blob, err := c.SealStatelessString(testPurpose, "org-and-email-payload")
	if err != nil {
		t.Fatalf("SealStateless: %v", err)
	}
	if !strings.HasPrefix(blob, statelessPrefix+".") {
		t.Fatalf("blob missing stateless prefix: %q", blob)
	}
	got, err := c.OpenStatelessString(testPurpose, blob)
	if err != nil {
		t.Fatalf("OpenStateless: %v", err)
	}
	if got != "org-and-email-payload" {
		t.Fatalf("round-trip mismatch: %q", got)
	}
}

func TestSealStateless_WrongPurposeFails(t *testing.T) {
	c := mustCodec(t, testKey(1))
	blob, _ := c.SealStatelessString(testPurpose, "x")
	if _, err := c.OpenStateless(Purpose("something_else"), blob); err == nil {
		t.Fatal("opening under a different purpose must fail (AAD binds the purpose)")
	}
}

func TestSealStateless_TamperFails(t *testing.T) {
	c := mustCodec(t, testKey(1))
	blob, _ := c.SealStatelessString(testPurpose, "x")
	parts := strings.Split(blob, ".")
	// Flip a byte in the ciphertext segment.
	b := []byte(parts[2])
	b[0] ^= 0xFF
	parts[2] = string(b)
	if _, err := c.OpenStateless(testPurpose, strings.Join(parts, ".")); err == nil {
		t.Fatal("a tampered token must fail its GCM tag")
	}
}

func TestSealStateless_WrongKeyFails(t *testing.T) {
	a := mustCodec(t, testKey(1))
	b := mustCodec(t, testKey(9)) // different key material
	blob, _ := a.SealStatelessString(testPurpose, "x")
	if _, err := b.OpenStateless(testPurpose, blob); err == nil {
		t.Fatal("opening under a different key must fail")
	}
}

func TestSealStateless_RotationKeepsOldTokensOpenable(t *testing.T) {
	// Mint under a single-version ring, then open under a ring that has rotated to a
	// new primary but retained the old version.
	old := mustCodec(t, "1:"+testKey(1))
	blob, err := old.SealStatelessString(testPurpose, "keep-me")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	rotated := mustCodec(t, "2:"+testKey(2)+",1:"+testKey(1))
	got, err := rotated.OpenStatelessString(testPurpose, blob)
	if err != nil {
		t.Fatalf("a token minted under v1 must still open after rotating to v2: %v", err)
	}
	if got != "keep-me" {
		t.Fatalf("mismatch after rotation: %q", got)
	}
}

func TestSealStateless_RejectsRowBoundBlob(t *testing.T) {
	// A row-bound (Seal) blob and a stateless token must not be interchangeable —
	// the prefixes differ, so OpenStateless rejects an ienv1 blob before any key work.
	c := mustCodec(t, testKey(1))
	rowBlob, err := c.SealString(testBinding(), "secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := c.OpenStateless(testPurpose, rowBlob); err == nil {
		t.Fatal("OpenStateless must reject a row-bound (ienv1) blob")
	}
	// And vice-versa.
	tok, _ := c.SealStatelessString(testPurpose, "x")
	if _, err := c.Open(testBinding(), tok); err == nil {
		t.Fatal("Open must reject a stateless (senv1) token")
	}
}

func TestSealStateless_NilCodec(t *testing.T) {
	var c *Codec
	if _, err := c.SealStateless(testPurpose, []byte("x")); err != ErrNotConfigured {
		t.Fatalf("nil codec SealStateless = %v, want ErrNotConfigured", err)
	}
	if _, err := c.OpenStateless(testPurpose, "senv1.1.AAAA"); err != ErrNotConfigured {
		t.Fatalf("nil codec OpenStateless = %v, want ErrNotConfigured", err)
	}
}
