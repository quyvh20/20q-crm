package envelope

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// testKey returns deterministic 32-byte key material. The byte value seeds it
// so two calls can produce keys that are different, or deliberately identical.
func testKey(fill byte) string {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = fill + byte(i)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func mustCodec(t *testing.T, raw string) *Codec {
	t.Helper()
	ring, err := ParseKeyring(raw)
	if err != nil {
		t.Fatalf("ParseKeyring(%q): %v", raw, err)
	}
	return NewCodec(ring)
}

func testBinding() Binding {
	return Binding{
		OrgID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Purpose: PurposeConnectionCredentials,
		ID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
}

func TestSealOpen_RoundTrips(t *testing.T) {
	c := mustCodec(t, testKey(1))
	b := testBinding()
	const secret = `{"access_token":"EAAG…","expires_in":0}`

	blob, err := c.SealString(b, secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(blob, secret) {
		t.Fatal("the sealed blob contains its own plaintext")
	}
	if !strings.HasPrefix(blob, blobPrefix+".1.") {
		t.Fatalf("blob does not carry its format tag and version: %q", blob)
	}

	got, err := c.OpenString(b, blob)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != secret {
		t.Fatalf("round trip changed the secret: got %q", got)
	}
}

func TestSeal_IsNonDeterministic(t *testing.T) {
	c := mustCodec(t, testKey(1))
	b := testBinding()

	first, err := c.SealString(b, "same-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := c.SealString(b, "same-secret")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// A fresh DEK and a fresh nonce per seal: identical plaintext must not
	// produce identical ciphertext, or the ledger leaks which connections share
	// a credential.
	if first == second {
		t.Fatal("sealing the same plaintext twice produced identical blobs")
	}
}

// The binding tests below are the reason this package exists rather than a
// direct call to two_factor_crypto's shape. Each one relocates a valid blob and
// asserts it fails.
func TestOpen_RejectsRelocatedCiphertext(t *testing.T) {
	c := mustCodec(t, testKey(1))
	original := testBinding()

	blob, err := c.SealString(original, "provider-token")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	cases := map[string]Binding{
		"another org": {
			OrgID:   uuid.MustParse("99999999-9999-9999-9999-999999999999"),
			Purpose: original.Purpose,
			ID:      original.ID,
		},
		"another purpose": {
			OrgID:   original.OrgID,
			Purpose: PurposePendingToken,
			ID:      original.ID,
		},
		"another row": {
			OrgID:   original.OrgID,
			Purpose: original.Purpose,
			ID:      uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		},
	}

	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.OpenString(b, blob); err == nil {
				t.Fatal("a ciphertext moved to " + name + " opened successfully")
			}
		})
	}
}

// TestOpen_RejectsRelabelledVersion is the load-bearing half of binding the key
// version into the AAD, and it is built so it can only pass for that reason.
//
// Both versions hold the SAME key bytes, so relabelling the blob's version
// selects an identical KEK. Nothing about the key material can reject it. If
// the version were left out of the additional data — the design the plan
// originally implied, with the version living only in a neighbouring column —
// this blob would open cleanly under the wrong label.
func TestOpen_RejectsRelabelledVersion(t *testing.T) {
	shared := testKey(7)
	c := mustCodec(t, "1:"+shared+",2:"+shared)

	b := testBinding()
	blob, err := c.SealString(b, "provider-token")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(blob, blobPrefix+".2.") {
		t.Fatalf("expected the seal to use the highest version, got %q", blob)
	}

	// Sanity: the two keys really are interchangeable, so the rejection below
	// cannot be attributed to key material.
	parts := strings.Split(blob, ".")
	relabelled := strings.Join([]string{parts[0], "1", parts[2], parts[3]}, ".")

	if _, err := c.OpenString(b, relabelled); err == nil {
		t.Fatal("a blob relabelled from version 2 to version 1 opened successfully — the key version is not bound into the additional data")
	}
}

func TestOpen_RejectsTamperedPayload(t *testing.T) {
	c := mustCodec(t, testKey(1))
	b := testBinding()

	blob, err := c.SealString(b, "provider-token")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	parts := strings.Split(blob, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	raw[len(raw)-1] ^= 0xFF
	parts[3] = base64.RawURLEncoding.EncodeToString(raw)

	if _, err := c.OpenString(b, strings.Join(parts, ".")); err == nil {
		t.Fatal("a tampered payload opened successfully")
	}
}

func TestOpen_StructuralErrorsAreDistinctFromAuthFailure(t *testing.T) {
	c := mustCodec(t, testKey(1))
	b := testBinding()

	cases := map[string]string{
		"not our format":  "totally-not-an-envelope",
		"wrong prefix":    "ienv9.1.AAAA.AAAA",
		"non-numeric ver": blobPrefix + ".x.AAAA.AAAA",
		"bad base64":      blobPrefix + ".1.!!!!.AAAA",
	}
	for name, blob := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := c.OpenString(b, blob)
			if err == nil {
				t.Fatal("expected an error")
			}
			// Structural problems must say what is structurally wrong. The
			// opaque "could not open" message is reserved for authentication
			// failures, where wrong-key and tampered-row are indistinguishable
			// and guessing between them sends operators down the wrong path.
			if strings.Contains(err.Error(), "could not open the stored credential") {
				t.Fatalf("structural error was reported as an auth failure: %v", err)
			}
		})
	}
}

func TestOpen_UnknownVersionNamesWhatIsConfigured(t *testing.T) {
	sealer := mustCodec(t, "2:"+testKey(9))
	b := testBinding()
	blob, err := sealer.SealString(b, "provider-token")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// A deployment holding only version 1 meets a blob sealed under 2 — the
	// dropped-keyring-entry scenario.
	opener := mustCodec(t, "1:"+testKey(1))
	_, err = opener.OpenString(b, blob)
	if err == nil {
		t.Fatal("expected an unknown-version failure")
	}
	if !strings.Contains(err.Error(), "version 2") || !strings.Contains(err.Error(), "[1]") {
		t.Fatalf("error should name both the wanted and the configured versions, got: %v", err)
	}
}

func TestRotation_OldBlobsStillOpenAndNewOnesUsePrimary(t *testing.T) {
	b := testBinding()

	before := mustCodec(t, "1:"+testKey(1))
	old, err := before.SealString(b, "sealed-before-rotation")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Operator adds version 2 and keeps version 1.
	after := mustCodec(t, "1:"+testKey(1)+",2:"+testKey(50))
	if after.PrimaryVersion() != 2 {
		t.Fatalf("primary should be 2, got %d", after.PrimaryVersion())
	}

	got, err := after.OpenString(b, old)
	if err != nil {
		t.Fatalf("a pre-rotation blob must still open: %v", err)
	}
	if got != "sealed-before-rotation" {
		t.Fatalf("rotation changed the plaintext: %q", got)
	}

	fresh, err := after.SealString(b, "sealed-after-rotation")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if v, _ := VersionOf(fresh); v != 2 {
		t.Fatalf("a new seal should use the primary version, got %d", v)
	}
}

func TestRewrap_MovesVersionWithoutChangingPlaintextOrBinding(t *testing.T) {
	b := testBinding()
	before := mustCodec(t, "1:"+testKey(1))
	old, err := before.SealString(b, "provider-token")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	after := mustCodec(t, "1:"+testKey(1)+",2:"+testKey(50))
	rewrapped, version, err := after.Rewrap(b, old)
	if err != nil {
		t.Fatalf("Rewrap: %v", err)
	}
	if version != 2 {
		t.Fatalf("rewrap should land on the primary version, got %d", version)
	}
	got, err := after.OpenString(b, rewrapped)
	if err != nil {
		t.Fatalf("Open after rewrap: %v", err)
	}
	if got != "provider-token" {
		t.Fatalf("rewrap changed the plaintext: %q", got)
	}

	// A rewrap must not be usable to relocate a credential.
	other := b
	other.OrgID = uuid.MustParse("99999999-9999-9999-9999-999999999999")
	if _, err := after.OpenString(other, rewrapped); err == nil {
		t.Fatal("a rewrapped blob opened under a different org")
	}
}

func TestBinding_RequiresEveryField(t *testing.T) {
	c := mustCodec(t, testKey(1))
	full := testBinding()

	cases := map[string]Binding{
		"no org":     {Purpose: full.Purpose, ID: full.ID},
		"no purpose": {OrgID: full.OrgID, ID: full.ID},
		// A zero row id would give every row in an org the same binding, which
		// is indistinguishable from not binding at all.
		"no row id": {OrgID: full.OrgID, Purpose: full.Purpose},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.SealString(b, "x"); err == nil {
				t.Fatal("expected Seal to reject a binding with " + name)
			}
		})
	}
}

func TestNilCodec_IsUsableAndNeverPanics(t *testing.T) {
	var c *Codec // the shape a dev boot without INTEGRATION_ENC_KEY produces

	if c.Configured() {
		t.Fatal("a nil codec must not report itself configured")
	}
	if c.PrimaryVersion() != 0 {
		t.Fatal("a nil codec must not claim a key version")
	}
	if _, err := c.SealString(testBinding(), "x"); err != ErrNotConfigured {
		t.Fatalf("Seal on a nil codec: want ErrNotConfigured, got %v", err)
	}
	if _, err := c.OpenString(testBinding(), "x"); err != ErrNotConfigured {
		t.Fatalf("Open on a nil codec: want ErrNotConfigured, got %v", err)
	}
	if err := c.Canary(nil); err != ErrNotConfigured {
		t.Fatalf("Canary on a nil codec: want ErrNotConfigured, got %v", err)
	}
}

func TestParseKeyring(t *testing.T) {
	valid := testKey(1)

	t.Run("bare key is version 1", func(t *testing.T) {
		ring, err := ParseKeyring(valid)
		if err != nil {
			t.Fatalf("ParseKeyring: %v", err)
		}
		if ring.Primary() != 1 {
			t.Fatalf("want primary 1, got %d", ring.Primary())
		}
	})

	t.Run("primary is the highest version, not the first entry", func(t *testing.T) {
		// Listed newest-last on purpose: entry order in a Railway variable box
		// is invisible and must not decide which key new credentials use.
		ring, err := ParseKeyring("3:" + testKey(3) + ",1:" + testKey(1))
		if err != nil {
			t.Fatalf("ParseKeyring: %v", err)
		}
		if ring.Primary() != 3 {
			t.Fatalf("want primary 3, got %d", ring.Primary())
		}
		reordered, err := ParseKeyring("1:" + testKey(1) + ",3:" + testKey(3))
		if err != nil {
			t.Fatalf("ParseKeyring: %v", err)
		}
		if reordered.Primary() != 3 {
			t.Fatalf("reordering the entries changed the primary to %d", reordered.Primary())
		}
	})

	t.Run("accepts every base64 alphabet", func(t *testing.T) {
		raw := make([]byte, KeySize)
		for i := range raw {
			raw[i] = byte(i) * 7
		}
		for name, enc := range map[string]*base64.Encoding{
			"std":    base64.StdEncoding,
			"rawstd": base64.RawStdEncoding,
			"url":    base64.URLEncoding,
			"rawurl": base64.RawURLEncoding,
		} {
			if _, err := ParseKeyring(enc.EncodeToString(raw)); err != nil {
				t.Fatalf("%s encoding rejected: %v", name, err)
			}
		}
	})

	t.Run("rejections", func(t *testing.T) {
		cases := map[string]string{
			"empty":            "",
			"whitespace":       "   ",
			"not base64":       "!!!!not-base64!!!!",
			"wrong length":     base64.StdEncoding.EncodeToString([]byte("too-short")),
			"duplicate ver":    "1:" + valid + ",1:" + testKey(2),
			"non-numeric ver":  "one:" + valid,
			"zero version":     "0:" + valid,
			"negative version": "-1:" + valid,
		}
		for name, raw := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := ParseKeyring(raw); err == nil {
					t.Fatal("expected ParseKeyring to reject this")
				}
			})
		}
	})

	t.Run("absent is distinguishable from malformed", func(t *testing.T) {
		// Local development legitimately runs without a key; a typo never does.
		// Boot policy branches on exactly this difference.
		if _, err := ParseKeyring(""); err != ErrNoKeys {
			t.Fatalf("want ErrNoKeys for an empty value, got %v", err)
		}
		if _, err := ParseKeyring("garbage"); err == ErrNoKeys {
			t.Fatal("a malformed key must not be reported as absent")
		}
	})

	t.Run("errors never echo key material", func(t *testing.T) {
		secret := testKey(1)
		_, err := ParseKeyring("1:" + secret + ",1:" + secret)
		if err == nil {
			t.Fatal("expected an error")
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), secret[:16]) {
			t.Fatalf("error message leaked key material: %v", err)
		}
	})
}

func TestCanary(t *testing.T) {
	b := testBinding()
	sealer := mustCodec(t, "1:"+testKey(1))
	blob, err := sealer.SealString(b, "provider-token")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	t.Run("no rows is a pass", func(t *testing.T) {
		// A workspace that has never connected a provider has nothing to
		// verify; refusing to boot over it would make this codec's arrival a
		// breaking change for every existing deployment.
		if err := sealer.Canary(nil); err != nil {
			t.Fatalf("empty sample should pass: %v", err)
		}
	})

	t.Run("real rows under the right key pass", func(t *testing.T) {
		if err := sealer.Canary([]CanaryRow{{Binding: b, Blob: blob}}); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})

	t.Run("a key that merely parses does not pass", func(t *testing.T) {
		// The whole point: swapped key material is internally consistent and
		// would satisfy any self-generated fixture.
		wrong := mustCodec(t, "1:"+testKey(200))
		err := wrong.Canary([]CanaryRow{{Binding: b, Blob: blob}})
		if err == nil {
			t.Fatal("a wrong-but-valid key passed the canary")
		}
		if !strings.Contains(err.Error(), "INTEGRATION_ENC_KEY") {
			t.Fatalf("canary failure should name the variable to fix, got: %v", err)
		}
	})

	t.Run("a keyring version with no rows yet passes", func(t *testing.T) {
		// Staging the next key before anything uses it is the safe rotation
		// order and must not fail boot.
		staged := mustCodec(t, "1:"+testKey(1)+",2:"+testKey(50))
		if err := staged.Canary([]CanaryRow{{Binding: b, Blob: blob}}); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})

	t.Run("a corrupt blob does not brick boot", func(t *testing.T) {
		// One truncated/garbled credential is per-row data corruption, not a key
		// mismatch — it must not fail the whole deployment's boot. A good row under
		// the right key alongside it still confirms the key is fine.
		rows := []CanaryRow{
			{Binding: b, Blob: blob},
			{Binding: testBinding(), Blob: "not-a-valid-envelope-blob"},
		}
		if err := sealer.Canary(rows); err != nil {
			t.Fatalf("a corrupt blob must be skipped, not fatal: %v", err)
		}
	})

	t.Run("a wrong key still fails even with a corrupt row present", func(t *testing.T) {
		// The corruption skip must not become an escape hatch that hides a real KEK
		// mismatch: a well-formed blob that will not open is still fatal.
		wrong := mustCodec(t, "1:"+testKey(201))
		rows := []CanaryRow{
			{Binding: b, Blob: blob},
			{Binding: testBinding(), Blob: "not-a-valid-envelope-blob"},
		}
		if err := wrong.Canary(rows); err == nil {
			t.Fatal("a wrong key must still fail the canary despite a corrupt row")
		}
	})
}
