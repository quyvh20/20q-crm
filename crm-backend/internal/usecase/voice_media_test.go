package usecase

import (
	"strings"
	"testing"
)

// These tests exist to pin one specific exploit, so they are written as the attack
// rather than as coverage: a "voice note" whose bytes are HTML, uploaded to get a
// text/html response back on the SPA's own origin.

func TestDetectVoiceMedia_RefusesNonAudio(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"html", []byte(`<html><script>fetch('/api/users')</script></html>`)},
		{"html with leading whitespace", []byte("   \n\t<!DOCTYPE html><body>x</body>")},
		{"svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"plain text", []byte("just some text")},
		{"empty", []byte{}},
		{"pdf", []byte("%PDF-1.7\n%\xE2\xE3\xCF\xD3")},
		{"gif", []byte("GIF89a\x01\x00\x01\x00")},
		{"zip", []byte("PK\x03\x04\x14\x00\x00\x00")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := DetectVoiceMedia(tc.body); ok {
				t.Fatalf("%s was accepted as audio — it would be stored and served back", tc.name)
			}
		})
	}
}

// The lie that used to work: an audio-looking NAME wrapped around HTML bytes.
// Nothing about the filename reaches the decision any more.
func TestDetectVoiceMedia_IgnoresTheClaimedName(t *testing.T) {
	html := []byte("<html><body>pwned</body></html>")
	if _, _, ok := DetectVoiceMedia(html); ok {
		t.Fatal("content must be judged by its bytes, never by a claimed extension")
	}
}

// The formats the product itself produces and accepts. An allowlist that rejects
// these gets widened under pressure until it stops being an allowlist — and two of
// these (m4a, raw-frame mp3) are exactly what http.DetectContentType misses, which
// is why this package does its own magic-byte matching.
func TestDetectVoiceMedia_AcceptsRealAudio(t *testing.T) {
	pad := func(b []byte) []byte {
		for len(b) < 64 {
			b = append(b, 0)
		}
		return b
	}
	cases := []struct {
		name    string
		body    []byte
		wantExt string
	}{
		{"mp3 with ID3 tag", pad([]byte("ID3\x03\x00\x00\x00")), ".mp3"},
		{"mp3 bare frame sync", pad([]byte{0xFF, 0xFB, 0x90, 0x64}), ".mp3"},
		{"wav", pad([]byte("RIFF\x24\x08\x00\x00WAVE")), ".wav"},
		{"ogg", pad([]byte("OggS\x00\x02\x00\x00")), ".ogg"},
		// Chrome / Firefox MediaRecorder.
		{"webm", pad([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x00, 0x00, 0x00}), ".webm"},
		// Safari MediaRecorder — the one a DetectContentType allowlist would drop.
		{"m4a", pad(append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A ")...)), ".m4a"},
		{"mp4 audio", pad(append([]byte{0, 0, 0, 0x18}, []byte("ftypmp42")...)), ".m4a"},
		{"aiff", pad([]byte("FORM\x00\x00\x00\x00AIFF")), ".aiff"},
		{"flac", pad([]byte("fLaC\x00\x00\x00\x22")), ".flac"},
		{"amr", pad([]byte("#!AMR\n")), ".amr"},
		{"caf", pad([]byte("caff\x00\x01\x00\x00")), ".caf"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ct, ext, ok := DetectVoiceMedia(tc.body)
			if !ok {
				t.Fatal("real audio rejected — recordings from this client would break")
			}
			if ext != tc.wantExt {
				t.Fatalf("ext = %q, want %q", ext, tc.wantExt)
			}
			if !strings.HasPrefix(ct, "audio/") {
				t.Fatalf("stored audio must be served as an audio type, got %q", ct)
			}
			// Round-trip: what we store under must serve back as what we detected.
			if got := ContentTypeForStoredVoiceFile("abc" + ext); got != ct {
				t.Fatalf("round-trip mismatch: stored as %s, served as %q, detected %q", ext, got, ct)
			}
		})
	}
}

// The serving half. Legacy rows still carry uploader-chosen extensions, so this
// must never resolve one of those to something the browser will execute.
func TestContentTypeForStoredVoiceFile(t *testing.T) {
	cases := []struct{ name, want string }{
		{"3f2504e0-4f89-11d3-9a0c-0305e82c3301.mp3", "audio/mpeg"},
		{"x.wav", "audio/wav"},
		{"x.webm", "audio/webm"},
		{"x.m4a", "audio/mp4"},
		{"X.MP3", "audio/mpeg"}, // case-insensitive
		// The exploit filenames: whatever they are, they are not executable types.
		{"legacy_uuid_evil.html", "application/octet-stream"},
		{"legacy_uuid_evil.svg", "application/octet-stream"},
		{"legacy_uuid_evil.xhtml", "application/octet-stream"},
		{"legacy_uuid_evil.js", "application/octet-stream"},
		{"no-extension", "application/octet-stream"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ContentTypeForStoredVoiceFile(tc.name)
			if got != tc.want {
				t.Fatalf("%s → %q, want %q", tc.name, got, tc.want)
			}
			// The invariant that actually matters, independent of the table above.
			for _, bad := range []string{"html", "javascript", "svg", "xml"} {
				if strings.Contains(got, bad) {
					t.Fatalf("%s resolved to an executable type %q", tc.name, got)
				}
			}
		})
	}
}
