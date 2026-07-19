package integrations

import (
	"encoding/json"
	"strings"
	"testing"
)

// Consent is a legal claim recorded on behalf of a data subject. These tests pin two
// things: that a malformed envelope never costs the lead, and that we never assert
// more than we were actually told.

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("stored envelope is not valid JSON: %v", err)
	}
	return out
}

// TestParseConsent_NulByteNeverCostsTheLead is the sharpest one: a single 0x00 from a
// public endpoint would otherwise fail the jsonb write and take the delivery with it.
// Go accepts NUL inside a string; Postgres does not accept it inside jsonb.
func TestParseConsent_NulByteNeverCostsTheLead(t *testing.T) {
	rec := parseConsent(map[string]any{
		"basis": "consent\x00",
		"text":  "I agree\x00 to be contacted",
		"channels": []any{"email\x00"},
	})

	if len(rec.Envelope) == 0 {
		t.Fatal("a NUL byte must not prevent the envelope being stored")
	}
	if strings.ContainsRune(string(rec.Envelope), 0) {
		t.Error("the stored envelope still carries a NUL — Postgres will reject the jsonb write and kill the lead")
	}
	if rec.Basis != "consent" {
		t.Errorf("basis should sanitize to %q, got %q", "consent", rec.Basis)
	}
}

// TestParseConsent_MalformedNeverFails walks every shape a careless integrator can
// send. Not one may produce an unstorable envelope, because the lead behind it is a
// real person and the envelope is evidence about them, not a gate on them.
func TestParseConsent_MalformedNeverFails(t *testing.T) {
	cases := map[string]map[string]any{
		"numeric basis":      {"basis": 42},
		"null basis":         {"basis": nil},
		"array basis":        {"basis": []any{"consent"}},
		"object basis":       {"basis": map[string]any{"x": "y"}},
		"unknown basis":      {"basis": "vibes"},
		"unparseable date":   {"basis": "consent", "captured_at": "last Tuesday"},
		"numeric date":       {"basis": "consent", "captured_at": 1700000000},
		"nested junk":        {"basis": "consent", "meta": map[string]any{"a": []any{1, 2, map[string]any{"b": nil}}}},
		"empty strings":      {"basis": "", "text": ""},
		"many channels":      {"basis": "consent", "channels": make([]any, 500)},
	}
	for name, in := range cases {
		rec := parseConsent(in)
		if len(rec.Envelope) == 0 {
			t.Errorf("%s: produced no storable envelope — a formatting mistake must not lose the record", name)
			continue
		}
		decode(t, rec.Envelope) // must be valid JSON in every case
	}
}

// TestParseConsent_AbsentIsSilent: no envelope means no record and no warning noise.
func TestParseConsent_AbsentIsSilent(t *testing.T) {
	rec := parseConsent(nil)
	if len(rec.Envelope) != 0 || len(rec.Warnings) != 0 {
		t.Errorf("an absent consent object must produce nothing at all: %+v", rec)
	}
}

// TestParseConsent_AttestsRatherThanClaims. We record that consent was REPORTED; we
// do not enforce it. Asserting that in the data means an exported row cannot be read
// as proof of suppression.
func TestParseConsent_AttestsRatherThanClaims(t *testing.T) {
	rec := parseConsent(map[string]any{"basis": "consent"})
	env := decode(t, rec.Envelope)

	att, ok := env[consentAttestationKey].(map[string]any)
	if !ok {
		t.Fatalf("the stored envelope must carry our attestation: %v", env)
	}
	if att["enforced"] != false {
		t.Error("the record must state plainly that nothing enforces it")
	}
	if att["recorded_at"] == "" {
		t.Error("the attestation needs our own timestamp, independent of the caller's")
	}
}

// TestParseConsent_CallersAttestationCannotBeForged: what we assert must not be
// settable by the same payload it describes.
func TestParseConsent_CallersAttestationCannotBeForged(t *testing.T) {
	rec := parseConsent(map[string]any{
		"basis":               "consent",
		consentAttestationKey: map[string]any{"enforced": true, "recorded_at": "1999-01-01T00:00:00Z"},
	})
	env := decode(t, rec.Envelope)
	att := env[consentAttestationKey].(map[string]any)

	if att["enforced"] != false {
		t.Fatal("a caller must not be able to claim their consent is enforced")
	}
	if att["recorded_at"] == "1999-01-01T00:00:00Z" {
		t.Fatal("a caller must not be able to backdate our attestation")
	}
	if len(rec.Warnings) == 0 {
		t.Error("silently dropping a reserved key the caller sent is the discard this feature exists to fix")
	}
}

// TestParseConsent_CapturedAtIsNeverInvented. Defaulting an unparseable timestamp to
// now() would fabricate the single fact the record exists to prove.
func TestParseConsent_CapturedAtIsNeverInvented(t *testing.T) {
	bad := parseConsent(map[string]any{"basis": "consent", "captured_at": "whenever"})
	if bad.CapturedAt != "" {
		t.Errorf("an unreadable date must stay empty, not become now(): %q", bad.CapturedAt)
	}
	if len(bad.Warnings) == 0 {
		t.Error("the caller must be told their date was not understood")
	}

	good := parseConsent(map[string]any{"basis": "consent", "captured_at": "2026-07-19T10:04:00Z"})
	if good.CapturedAt != "2026-07-19T10:04:00Z" {
		t.Errorf("a valid RFC3339 date must survive: %q", good.CapturedAt)
	}
	if d := parseConsent(map[string]any{"captured_at": "2026-07-19"}); d.CapturedAt == "" {
		t.Error("a plain date is a shape providers really send and must parse")
	}
}

// TestParseConsent_UnknownBasisIsRecordedNotRejected. Real traffic carries TCPA and
// CASL vocabulary alongside GDPR's; refusing an unfamiliar term would lose a real
// person's lead over a wording difference.
func TestParseConsent_UnknownBasisIsRecordedNotRejected(t *testing.T) {
	rec := parseConsent(map[string]any{"basis": "express_written_consent"})
	if rec.Basis != "express_written_consent" || len(rec.Warnings) != 0 {
		t.Errorf("a recognised non-GDPR basis must pass cleanly: %+v", rec)
	}

	odd := parseConsent(map[string]any{"basis": "Checkbox_V2"})
	if odd.Basis != "checkbox_v2" {
		t.Errorf("basis should normalize case, got %q", odd.Basis)
	}
	if len(odd.Warnings) == 0 {
		t.Error("an unrecognised basis must be flagged — recorded, but not silently blessed")
	}
	if len(odd.Envelope) == 0 {
		t.Error("an unrecognised basis must still be stored verbatim")
	}
}

// TestParseConsent_OversizedTextIsDroppedWholesale. A clipped consent statement reads
// as the wording the subject saw, and it is not — that is a worse artifact than
// having no text at all.
func TestParseConsent_OversizedTextIsDroppedWholesale(t *testing.T) {
	rec := parseConsent(map[string]any{
		"basis": "consent",
		"text":  strings.Repeat("x", maxConsentBytes*2),
	})

	if len(rec.Envelope) == 0 {
		t.Fatal("an oversized text must not lose the whole envelope")
	}
	if len(rec.Envelope) > maxConsentBytes {
		t.Errorf("stored envelope is %d bytes, over the %d ceiling", len(rec.Envelope), maxConsentBytes)
	}
	env := decode(t, rec.Envelope)
	if _, present := env["text"]; present {
		t.Error("oversized text must be dropped wholesale, never truncated into a misleading excerpt")
	}
	if env["basis"] != "consent" {
		t.Error("the rest of the envelope must survive the drop")
	}
	att := env[consentAttestationKey].(map[string]any)
	if att["dropped"] == nil || att["original_bytes"] == nil {
		t.Error("the drop must be recorded in the envelope, or the gap is invisible later")
	}
	if len(rec.Warnings) == 0 {
		t.Error("the caller must be told at integration time")
	}
}

// TestTruncateRunes_DoesNotCorrupt: byte-slicing a multi-byte rune yields U+FFFD with
// no error — silent mangling of a value someone may later have to defend.
func TestTruncateRunes_DoesNotCorrupt(t *testing.T) {
	// "日本語" is 3 bytes per rune; cutting at 4 must land on a boundary.
	if got := truncateRunes("日本語", 4); !isValidUTF8(got) {
		t.Errorf("truncateRunes produced invalid UTF-8: %q", got)
	}
	if got := truncateRunes("abc", 10); got != "abc" {
		t.Errorf("a short string must pass through unchanged, got %q", got)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}
