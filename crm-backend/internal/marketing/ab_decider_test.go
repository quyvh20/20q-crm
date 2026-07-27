package marketing

import "testing"

// TestDecideABWinner covers the two-proportion significance verdict + its fallbacks.
func TestDecideABWinner(t *testing.T) {
	cases := []struct {
		name       string
		a, b       ABVariantStat
		wantWinner string
		wantSig    bool
	}{
		{"cell too small", ABVariantStat{Sent: 10, Opened: 5}, ABVariantStat{Sent: 10, Opened: 2}, "A", false},
		{"A wins, significant", ABVariantStat{Sent: 1000, Opened: 400}, ABVariantStat{Sent: 1000, Opened: 200}, "A", true},
		{"B wins, significant", ABVariantStat{Sent: 1000, Opened: 200}, ABVariantStat{Sent: 1000, Opened: 400}, "B", true},
		{"close, not significant", ABVariantStat{Sent: 1000, Opened: 300}, ABVariantStat{Sent: 1000, Opened: 305}, "B", false},
		{"exact tie defaults to A", ABVariantStat{Sent: 1000, Opened: 300}, ABVariantStat{Sent: 1000, Opened: 300}, "A", false},
		{"zero sent both", ABVariantStat{}, ABVariantStat{}, "A", false},
	}
	for _, c := range cases {
		w, sig, _ := decideABWinner(c.a, c.b)
		if w != c.wantWinner || sig != c.wantSig {
			t.Errorf("%s: got (%s, %v), want (%s, %v)", c.name, w, sig, c.wantWinner, c.wantSig)
		}
	}
}
