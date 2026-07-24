package integrations

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// The consent envelope: what a data subject was told, recorded verbatim on the
// delivery that carried it.
//
// SCOPE, stated plainly because the gap is the whole risk. This RECORDS consent. It
// does not ENFORCE it: nothing in this app consults the stored value before sending
// an email or enrolling a workflow, and no send path filters on it. Storing a legal
// basis while acting on none of it is a compliance illusion unless the product says
// so out loud — which is why the copy beside this in the UI is part of the feature
// and not decoration.
//
// The envelope is stored on the EVENT, never on the contact. A per-delivery row is
// immutable and carries its own timestamp and source; a contact field would be an
// ordinary custom field that a workflow, a hand edit, or the AI write path could
// silently overwrite — an evidentiary record anyone can rewrite is not evidence.

const (
	// maxConsentBytes bounds the stored envelope. This is a public write endpoint and
	// the batch route multiplies it by 100.
	maxConsentBytes = 8192
	// maxBasisLen bounds the normalized basis. A legal basis is a short token; a
	// kilobyte of it is an attack or a mistake.
	maxBasisLen = 64
)

// consentAttestationKey is the one key we add to a caller's object. Namespaced and
// stripped from the caller's input first, so what we assert can never be forged by
// the same payload it describes.
const consentAttestationKey = "_crm"

// knownBases are the legal bases we RECOGNISE — not the ones we accept. An
// unrecognised value is stored and flagged, never rejected: real wire traffic
// carries GDPR Art.6 terms alongside TCPA's express_written_consent, CASL's implied,
// and whatever string a provider's form emits. Refusing an unfamiliar term would
// lose a real person's lead over a vocabulary quibble.
var knownBases = map[string]bool{
	"consent":              true,
	"contract":             true,
	"legal_obligation":     true,
	"vital_interests":      true,
	"public_task":          true,
	"legitimate_interests": true,
	// Non-GDPR regimes seen on real lead traffic.
	"express_written_consent": true,
	"implied":                 true,
	"opt_in":                  true,
}

// ConsentRecord is what the pipeline derived from one envelope.
type ConsentRecord struct {
	// Envelope is the JSON to store verbatim (plus our attestation). Nil when the
	// delivery carried no consent.
	Envelope []byte
	// Basis is the normalized legal basis, for display. Empty when absent or unusable.
	Basis string
	// CapturedAt is the caller's timestamp, echoed only when it actually parses.
	CapturedAt string
	// Warnings are said to the CALLER at integration time — a consent field that was
	// dropped or not understood must not be discovered during an audit.
	Warnings []string
}

// sanitizeForJSONB strips bytes Postgres will not accept inside jsonb.
//
// A single NUL is a lead-loss vector from a public endpoint: Go's json.Unmarshal
// accepts 0x00 inside a string, Postgres rejects it in jsonb, and the failed write
// would take the whole delivery down with it. Invalid UTF-8 goes for the same
// reason.
func sanitizeForJSONB(s string) string {
	if !strings.ContainsRune(s, 0) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r == 0 || r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeValue walks a decoded JSON value stripping bytes jsonb will reject.
func sanitizeValue(v any) any {
	switch t := v.(type) {
	case string:
		return sanitizeForJSONB(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[sanitizeForJSONB(k)] = sanitizeValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitizeValue(val)
		}
		return out
	default:
		return v
	}
}

// sanitizeMap strips jsonb/text-hostile bytes from every string in a decoded map,
// returning a clean copy of the same shape. Used for values headed to a record WRITE
// (a native text column and the custom_fields jsonb both reject a NUL), whereas
// marshalJSONB guards values headed to a jsonb ledger column.
func sanitizeMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return sanitizeValue(m).(map[string]any)
}

// nulEscape is how encoding/json renders a NUL byte — the one sequence a well-formed
// JSON document can carry that Postgres jsonb rejects (text cannot hold 0x00; every
// other control escape it accepts). json.Marshal coerces invalid UTF-8 to U+FFFD, so a
// NUL is the only jsonb-hostile byte left to guard.
var nulEscape = []byte{'\\', 'u', '0', '0', '0', '0'}

// marshalJSONB marshals a decoded value for storage in a jsonb event column,
// stripping bytes Postgres rejects (a NUL, invalid UTF-8) BEFORE they are encoded.
//
// This is the ledger's load-bearing write guard, and every event-payload marshal
// goes through it. A single 0x00 anywhere in a public payload would otherwise fail
// the row INSERT — and because no event row lands, there is nothing to dedupe on, so
// the caller's retry re-reads the same byte and fails identically forever (the
// Idempotency-Key makes the loss permanent, not recoverable). Stripping at the value
// level, not the marshaled bytes, is deliberate: an encoded NUL is the six-character
//  escape, and a naive text strip would also corrupt a legitimate literal
// "\\u0000" string. sanitizeValue's fast path makes this free for clean data.
func marshalJSONB(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	if !bytes.Contains(b, nulEscape) {
		return b
	}
	var generic any
	if json.Unmarshal(b, &generic) != nil {
		return b // unreachable in practice; better than dropping the whole payload
	}
	out, _ := json.Marshal(sanitizeValue(generic))
	return out
}

// parseConsent turns a caller's consent object into what gets stored.
//
// It NEVER fails the lead. Every degraded path — junk types, an unparseable date, an
// oversized body, an unknown basis — produces a stored record plus a warning. A lead
// refused over a malformed consent field is a real person lost to a formatting
// mistake, and the envelope is meant to be evidence about them, not a gate.
func parseConsent(raw map[string]any) ConsentRecord {
	if len(raw) == 0 {
		return ConsentRecord{}
	}
	var rec ConsentRecord

	// The caller's own `_crm` is removed BEFORE ours is added: what we attest must not
	// be forgeable by the payload it describes.
	clean := make(map[string]any, len(raw)+1)
	for k, v := range raw {
		if k == consentAttestationKey {
			rec.Warnings = append(rec.Warnings, "the reserved consent key \"_crm\" was ignored")
			continue
		}
		clean[k] = sanitizeValue(v)
	}

	// basis: a string or nothing. A non-string is recorded verbatim in the envelope
	// but cannot be displayed, so say so rather than coercing it.
	if b, ok := clean["basis"].(string); ok {
		rec.Basis = truncate(strings.ToLower(strings.TrimSpace(b)), maxBasisLen)
		if rec.Basis != "" && !knownBases[rec.Basis] {
			rec.Warnings = append(rec.Warnings,
				"consent basis \""+rec.Basis+"\" is not one we recognise; it was recorded as sent")
		}
	} else if _, present := clean["basis"]; present {
		rec.Warnings = append(rec.Warnings, "consent.basis was not text, so it was recorded but cannot be displayed")
	}

	// captured_at: echoed ONLY when it genuinely parses. Never defaulted to now() —
	// inventing a capture time is fabricating the one fact the record exists to prove.
	if ts, ok := clean["captured_at"].(string); ok {
		if parsed, err := parseConsentTime(ts); err == nil {
			rec.CapturedAt = parsed
		} else {
			rec.Warnings = append(rec.Warnings,
				"consent.captured_at could not be read as a date; it was recorded as sent but not interpreted")
		}
	}

	envelope := map[string]any{}
	for k, v := range clean {
		envelope[k] = v
	}
	envelope[consentAttestationKey] = map[string]any{
		// Recorded, not enforced — asserted in the data itself so an exported row
		// cannot be read as proof of suppression.
		"enforced":    false,
		"recorded_at": time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		// Unmarshalable input cannot be stored, but must not cost the lead.
		return ConsentRecord{Warnings: []string{"the consent object could not be stored"}}
	}
	if len(body) > maxConsentBytes {
		// `text` is dropped WHOLESALE rather than clipped: a truncated consent
		// statement reads as the wording the subject saw, and it is not.
		delete(envelope, "text")
		envelope[consentAttestationKey].(map[string]any)["dropped"] = []string{"text"}
		envelope[consentAttestationKey].(map[string]any)["original_bytes"] = len(body)
		rec.Warnings = append(rec.Warnings,
			"consent.text was too large to store and was dropped; the rest of the envelope was kept")
		if body, err = json.Marshal(envelope); err != nil || len(body) > maxConsentBytes {
			return ConsentRecord{Warnings: append(rec.Warnings, "the consent object was too large to store")}
		}
	}
	rec.Envelope = body
	return rec
}

// consentTimeLayouts are the shapes a provider plausibly sends. Deliberately narrow:
// guessing at an ambiguous date is how a consent record acquires a wrong timestamp.
var consentTimeLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseConsentTime(s string) (string, error) {
	s = strings.TrimSpace(s)
	var lastErr error
	for _, layout := range consentTimeLayouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
		lastErr = err
	}
	return "", lastErr
}
