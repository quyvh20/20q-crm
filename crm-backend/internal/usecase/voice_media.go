package usecase

import (
	"bytes"
	"strings"
)

// voice_media.go is the single place that decides what a voice-note file IS.
//
// It exists because the previous arrangement made the UPLOADER decide. The stored
// filename embedded the uploaded name verbatim, and the preview handler served the
// file with net/http's extension-derived Content-Type — so uploading "x.html"
// produced a text/html response on the SPA's own origin (prod proxies /api through
// a Pages Function, so the API is same-origin with the app). That is stored XSS
// with the session cookies in reach, reachable by anyone holding records.write.
//
// The rule now: the type is decided by the file's own MAGIC BYTES, checked against
// an allowlist. Nothing the client sends — filename, extension, or Content-Type
// header — influences it.
//
// Why not http.DetectContentType? Because it is an allowlist with the wrong shape
// for this job. Measured against Go 1.26: a raw MP3 frame with no ID3 tag, FLAC,
// AMR, CAF, 3GP and — decisively — M4A all sniff as application/octet-stream. M4A
// is what Safari's MediaRecorder produces, so a DetectContentType allowlist would
// have rejected every recording made in Safari. An allowlist that rejects the
// product's own output gets widened under pressure until it stops being one.

type audioSig struct {
	contentType string
	ext         string
	// match reports whether b (already length-checked to >= 12) is this format.
	match func(b []byte) bool
}

// audioSigs is the allowlist, in match order. Every entry is a container we are
// willing to store and serve back; anything not matched here is refused.
var audioSigs = []audioSig{
	{"audio/mpeg", ".mp3", func(b []byte) bool {
		// ID3v2-tagged, or a bare MPEG audio frame sync (11 set bits).
		return bytes.HasPrefix(b, []byte("ID3")) ||
			(b[0] == 0xFF && b[1]&0xE0 == 0xE0)
	}},
	{"audio/wav", ".wav", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))
	}},
	{"audio/ogg", ".ogg", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("OggS"))
	}},
	{"audio/webm", ".webm", func(b []byte) bool {
		// EBML header — Chrome/Firefox MediaRecorder output.
		return bytes.HasPrefix(b, []byte{0x1A, 0x45, 0xDF, 0xA3})
	}},
	{"audio/mp4", ".m4a", func(b []byte) bool {
		// ISO-BMFF: a 4-byte box size then "ftyp". Covers M4A (Safari
		// MediaRecorder), MP4 audio and 3GP.
		return bytes.Equal(b[4:8], []byte("ftyp"))
	}},
	{"audio/aiff", ".aiff", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("FORM")) && bytes.Equal(b[8:12], []byte("AIFF"))
	}},
	{"audio/flac", ".flac", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("fLaC"))
	}},
	{"audio/amr", ".amr", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("#!AMR"))
	}},
	{"audio/x-caf", ".caf", func(b []byte) bool {
		return bytes.HasPrefix(b, []byte("caff"))
	}},
}

// DetectVoiceMedia reports the canonical content type and the extension to store
// under. ok is false when the bytes are not a recognised audio container — in which
// case the upload must be REFUSED, never stored under a fallback type, because a
// stored file we cannot label is exactly the problem.
func DetectVoiceMedia(b []byte) (contentType, ext string, ok bool) {
	// Every matcher indexes up to b[11].
	if len(b) < 12 {
		return "", "", false
	}
	for _, s := range audioSigs {
		if s.match(b) {
			return s.contentType, s.ext, true
		}
	}
	return "", "", false
}

// extToType is the serving-side mirror of audioSigs, built once so the two cannot
// drift: whatever we chose to store under, we serve back as.
var extToType = func() map[string]string {
	m := make(map[string]string, len(audioSigs))
	for _, s := range audioSigs {
		m[s.ext] = s.contentType
	}
	return m
}()

// ContentTypeForStoredVoiceFile reports the type to serve a stored file as.
//
// Keyed on OUR extension, and falling back to application/octet-stream rather than
// to net/http's extension lookup. Legacy rows still carry uploader-chosen
// extensions from before uploads were validated, so this path must assume a
// hostile name: serving a stale file as octet-stream is a broken player, serving it
// as text/html is an XSS.
func ContentTypeForStoredVoiceFile(name string) string {
	lower := strings.ToLower(name)
	if i := strings.LastIndexByte(lower, '.'); i >= 0 {
		if ct, ok := extToType[lower[i:]]; ok {
			return ct
		}
	}
	return "application/octet-stream"
}
