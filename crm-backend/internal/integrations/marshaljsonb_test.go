package integrations

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// marshalJSONB is the ledger's guard: a single NUL byte in a public payload would
// otherwise fail an event's jsonb INSERT, and because no row then lands there is
// nothing to dedupe on — so the caller's Idempotency-Key retry re-reads the same byte
// and loops forever instead of recovering. These pin that the guard strips the byte
// while leaving clean data untouched.

// testNUL is a single 0x00 byte, built at runtime so no NUL literal lives in this
// source file.
var testNUL = string(rune(0))

func TestMarshalJSONB_StripsEncodedNUL(t *testing.T) {
	in := map[string]any{
		"first_name": "Jo" + testNUL + "hn",
		"nested":     map[string]any{"note": "a" + testNUL + "b"},
		"list":       []any{"x" + testNUL + "y", "clean"},
	}
	out := marshalJSONB(in)

	require.False(t, bytes.Contains(out, nulEscape), "output still carries the encoded NUL jsonb rejects")
	require.NotContains(t, string(out), testNUL, "output still carries a raw NUL byte")
	require.True(t, json.Valid(out), "output must be valid JSON")

	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, "John", got["first_name"], "the NUL is stripped; the rest of the value survives")
	require.Equal(t, "ab", got["nested"].(map[string]any)["note"])
}

// A struct-bearing value (the shape recordGoogleMismatch stores via redactedEnvelope)
// must be sanitized too: sanitizeValue alone cannot recurse into a Go struct, which is
// exactly why marshalJSONB round-trips through a generic decode before stripping.
func TestMarshalJSONB_StripsNULInsideStructValues(t *testing.T) {
	type col struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	in := map[string]any{"user_column_data": []col{{Name: "email", Value: "a" + testNUL + "b@x.com"}}}
	out := marshalJSONB(in)

	require.False(t, bytes.Contains(out, nulEscape), "a NUL inside a struct field slipped through")
	require.True(t, json.Valid(out))
	require.Contains(t, string(out), "ab@x.com")
}

// Clean data takes the fast path and is byte-identical to a plain json.Marshal — the
// guard adds nothing on the overwhelmingly common path.
func TestMarshalJSONB_CleanDataIsUntouched(t *testing.T) {
	in := map[string]any{"email": "a@b.com", "n": float64(3), "ok": true}
	plain, err := json.Marshal(in)
	require.NoError(t, err)
	require.Equal(t, plain, marshalJSONB(in))
}

// A genuine literal backslash-u-0000 a caller actually sent (the six characters, NOT
// an encoded NUL) must survive unchanged. Its escaped form shares the guard's
// fast-path marker bytes, so it takes the slow round-trip — which sanitizes at the
// VALUE level precisely so a naive text delete cannot corrupt it.
func TestMarshalJSONB_PreservesLiteralBackslashSequence(t *testing.T) {
	literal := string([]byte{'\\', 'u', '0', '0', '0', '0'}) // the six chars a caller typed, not a NUL
	in := map[string]any{"note": "x" + literal + "y"}
	out := marshalJSONB(in)

	require.True(t, json.Valid(out))
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, "x"+literal+"y", got["note"], "a caller's literal backslash-u-0000 must survive unchanged")
}

// sanitizeMap guards values headed to a record WRITE (native text columns and the
// custom_fields jsonb both reject 0x00), so a NUL in a mapped field lands a clean
// contact instead of failing the write into a permanent Idempotency-Key retry loop.
func TestSanitizeMap_StripsNULFromWrittenValues(t *testing.T) {
	in := map[string]any{
		"first_name": "Jo" + testNUL + "hn",
		"email":      "a" + testNUL + "b@x.com",
		"custom":     map[string]any{"note": "x" + testNUL + "y"},
	}
	out := sanitizeMap(in)

	require.Equal(t, "John", out["first_name"])
	require.Equal(t, "ab@x.com", out["email"])
	require.Equal(t, "xy", out["custom"].(map[string]any)["note"])
	require.Nil(t, sanitizeMap(nil), "nil in, nil out")
}

// rejectionSubmission must produce a jsonb-storable value even when a caller slips a
// NUL escape INSIDE valid JSON — the case sanitizeFormBody's verbatim passthrough used
// to lose silently, taking the only evidence of a rejected public submission with it.
func TestRejectionSubmission_ValidJSONWithNULEscapeIsStorable(t *testing.T) {
	esc := string([]byte{'\\', 'u', '0', '0', '0', '0'}) // a  escape as six literal chars
	body := []byte(`{"name":"x` + esc + `y","email":"a@b.com"}`)
	require.True(t, json.Valid(body), "the hostile body is valid JSON — that is the whole trap")

	stored := marshalJSONB(map[string]any{"submission": rejectionSubmission(body)})
	require.False(t, bytes.Contains(stored, nulEscape), "the rejection row still carries an encoded NUL jsonb rejects")
	require.True(t, json.Valid(stored))

	// An unparseable body degrades to a sanitized string rather than being lost.
	garbage := rejectionSubmission([]byte("not json" + testNUL))
	require.Equal(t, "not json", garbage)
}

// TestMarshalJSONB_JSONBRejectsRawNUL pins the ROOT CAUSE the guard exists for:
// Postgres rejects a jsonb value carrying an encoded NUL, while marshalJSONB's output
// is accepted. If this ever stops being true the guard is unnecessary; until then it
// is load-bearing on every public write path.
func TestMarshalJSONB_JSONBRejectsRawNUL(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()

	nulMap := map[string]any{"first_name": "Jo" + testNUL + "hn"}

	rawWithNUL, err := json.Marshal(nulMap)
	require.NoError(t, err)
	require.Error(t, db.Exec("SELECT ?::jsonb", string(rawWithNUL)).Error,
		"Postgres must reject a jsonb carrying an encoded NUL — the reason marshalJSONB exists")

	require.NoError(t, db.Exec("SELECT ?::jsonb", string(marshalJSONB(nulMap))).Error,
		"marshalJSONB output must be jsonb-safe")
}
