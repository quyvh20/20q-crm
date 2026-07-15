package usecase

import (
	"encoding/json"
	"testing"

	"crm-backend/internal/domain"
)

func TestMergeJSONObjects_PartialOverlayPreservesUntouchedKeys(t *testing.T) {
	// The wipe bug: editing one field of a blob-stored record must not blank the rest.
	base := domain.JSON(`{"name":"Acme","tier":"gold","notes":"vip","secret":"hidden"}`)
	overlay := domain.JSON(`{"name":"Acme Corp"}`) // only the name changed

	got, err := mergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got["name"] != "Acme Corp" {
		t.Errorf("name = %v, want overlay value", got["name"])
	}
	for k, want := range map[string]string{"tier": "gold", "notes": "vip", "secret": "hidden"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %q preserved (a partial edit wiped it)", k, got[k], want)
		}
	}
}

func TestMergeJSONObjects_ExplicitNullOrEmptyClears(t *testing.T) {
	// Omission preserves; an EXPLICIT null/"" is how a field is cleared.
	base := domain.JSON(`{"a":"keep","b":"drop","c":"blank"}`)
	overlay := domain.JSON(`{"b":null,"c":""}`)

	got, err := mergeJSONObjects(base, overlay)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got["a"] != "keep" {
		t.Errorf("a = %v, want preserved", got["a"])
	}
	if v, ok := got["b"]; !ok || v != nil {
		t.Errorf("b = %v (present=%v), want explicit null", v, ok)
	}
	if got["c"] != "" {
		t.Errorf("c = %v, want empty string", got["c"])
	}
}

func TestMergeJSONObjects_EmptyOverlayReturnsBase(t *testing.T) {
	base := domain.JSON(`{"x":1}`)
	for _, overlay := range []domain.JSON{nil, domain.JSON(``), domain.JSON(`{}`)} {
		got, err := mergeJSONObjects(base, overlay)
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if got["x"] != float64(1) {
			t.Errorf("overlay %q: x = %v, want base preserved", overlay, got["x"])
		}
	}
}

func TestMergeJSONObjects_MalformedBaseToleratedOverlayWins(t *testing.T) {
	// A corrupt stored blob must not brick the write — the overlay still applies.
	got, err := mergeJSONObjects(domain.JSON(`not json`), domain.JSON(`{"ok":true}`))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want overlay applied over a malformed base", got["ok"])
	}
}

func TestMergeJSONObjects_MalformedOverlayErrors(t *testing.T) {
	if _, err := mergeJSONObjects(domain.JSON(`{"a":1}`), domain.JSON(`{bad`)); err == nil {
		t.Fatal("expected an error for a malformed overlay blob")
	}
}

func TestMergeJSONBlob_RoundTrips(t *testing.T) {
	out, err := mergeJSONBlob(domain.JSON(`{"a":1,"b":2}`), domain.JSON(`{"b":9,"c":3}`))
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var got map[string]float64
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result not valid JSON: %v (%s)", err, out)
	}
	if got["a"] != 1 || got["b"] != 9 || got["c"] != 3 {
		t.Errorf("merged blob = %s, want a=1 b=9 c=3", out)
	}
}
