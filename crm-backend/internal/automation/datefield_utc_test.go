package automation

import (
	"testing"
	"time"
)

// An automation payload's timestamps must not change shape with the server's
// clock. This is machine-independent on purpose: the DB-backed backfill test
// only catches the regression on a non-UTC machine (it passes by accident in
// CI's UTC), so without this the invariant has no gate that can fail anywhere.
func TestRFC3339UTC_IsIndependentOfTheValuesZone(t *testing.T) {
	instant := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	want := "2026-09-01T09:00:00Z"

	zones := map[string]*time.Location{
		"UTC":            time.UTC,
		"UTC+07 (local)": time.FixedZone("ICT", 7*60*60),
		"UTC-05":         time.FixedZone("EST", -5*60*60),
	}
	for name, loc := range zones {
		// The SAME instant, carried in a different location — exactly what pgx
		// hands back versus what an in-process value holds.
		if got := rfc3339UTC(instant.In(loc)); got != want {
			t.Fatalf("%s: rendered %q, want %q — the same instant must render identically whatever zone it arrives in", name, got, want)
		}
	}
}

// The live task path and the backfill scan arm the same timers, so a task
// activated after already being completed must not diverge depending on which
// path built its payload.
func TestRFC3339UTC_LiveAndBackfillPathsAgree(t *testing.T) {
	instant := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	fromDB := instant.In(time.FixedZone("ICT", 7*60*60)) // what a timestamptz read looks like
	inProcess := instant                                 // what the live path holds

	if rfc3339UTC(fromDB) != rfc3339UTC(inProcess) {
		t.Fatalf("paths disagree: backfill %q vs live %q", rfc3339UTC(fromDB), rfc3339UTC(inProcess))
	}
	// And the rendering must still be parseable by its counterpart.
	parsed, ok := parseDateValue(rfc3339UTC(fromDB))
	if !ok || !parsed.Equal(instant) {
		t.Fatalf("parseDateValue must round-trip the rendering: ok=%v parsed=%v", ok, parsed)
	}
}
