package main

import "testing"

// The correlation id is echoed on a response header and written into every log line
// for the request. An inbound one is honored (so a caller's tracing survives) but it
// is client-controlled, so it must be validated rather than trusted — otherwise a
// caller can forge log entries with a newline or attempt header splitting with CR/LF.
func TestSanitizeRequestID(t *testing.T) {
	keep := []string{
		"3f2a1b0c",
		"abc-123_x.y",
		"0123456789012345678901234567890123456789012345678901234567890123", // exactly 64
	}
	for _, s := range keep {
		if got := sanitizeRequestID(s); got != s {
			t.Errorf("sanitizeRequestID(%q) = %q, want it kept", s, got)
		}
	}

	reject := map[string]string{
		"empty":           "",
		"log injection":   "abc\ninfo: forged log line",
		"header splitting": "abc\r\nX-Admin: true",
		"too long":        "01234567890123456789012345678901234567890123456789012345678901234", // 65
		"spaces":          "hello world",
		"punctuation":     "abc;drop",
		"unicode":         "abc‮",
	}
	for name, s := range reject {
		if got := sanitizeRequestID(s); got != "" {
			t.Errorf("%s: sanitizeRequestID(%q) = %q, want \"\" (caller generates a fresh id)", name, s, got)
		}
	}
}
