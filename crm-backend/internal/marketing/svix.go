package marketing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Svix verification errors. The handler maps every one to a 401 (fail-closed): a
// caller learns only that verification failed, never why.
var (
	ErrWebhookNotConfigured = errors.New("marketing: resend webhook secret is not configured")
	ErrWebhookHeaders       = errors.New("marketing: resend webhook is missing its signature headers")
	ErrWebhookTimestamp     = errors.New("marketing: resend webhook timestamp is outside the tolerance window")
	ErrWebhookSignature     = errors.New("marketing: resend webhook signature did not verify")
)

// svixTolerance bounds replay: an event whose svix-timestamp is more than this away
// from now is rejected before the HMAC is even computed.
const svixTolerance = 5 * time.Minute

// svixHeaders are the three Svix signing headers.
type svixHeaders struct{ id, timestamp, signature string }

// readSvixHeaders reads svix-id/svix-timestamp/svix-signature, falling back to the
// StandardWebhooks webhook-* aliases some Resend docs use.
func readSvixHeaders(get func(string) string) svixHeaders {
	pick := func(a, b string) string {
		if v := strings.TrimSpace(get(a)); v != "" {
			return v
		}
		return strings.TrimSpace(get(b))
	}
	return svixHeaders{
		id:        pick("svix-id", "webhook-id"),
		timestamp: pick("svix-timestamp", "webhook-timestamp"),
		signature: pick("svix-signature", "webhook-signature"),
	}
}

// verifySvix verifies a Svix-signed webhook (the scheme Resend delivers with),
// mirroring the raw-bytes + constant-time discipline of the Facebook verifier but
// with Svix's own scheme:
//   - the signing secret is `whsec_<base64>`; strip the prefix and base64(std)-decode
//     the rest to the raw HMAC key;
//   - the signed content is `{svix-id}.{svix-timestamp}.{rawBody}`;
//   - the svix-signature header is a SPACE-delimited list of `v1,<base64sig>` entries
//     (multiple during a secret rotation) — accept if ANY v1 entry matches.
//
// The timestamp tolerance is enforced FIRST (bounds replay), then the HMAC compare.
// `body` MUST be the exact raw bytes received — a re-marshal breaks the signature.
func verifySvix(h svixHeaders, body []byte, secret string, now time.Time) error {
	if strings.TrimSpace(secret) == "" {
		return ErrWebhookNotConfigured
	}
	if h.id == "" || h.timestamp == "" || h.signature == "" {
		return ErrWebhookHeaders
	}

	ts, err := strconv.ParseInt(h.timestamp, 10, 64)
	if err != nil {
		return ErrWebhookTimestamp
	}
	delta := now.Unix() - ts
	if delta < 0 {
		delta = -delta
	}
	if delta > int64(svixTolerance.Seconds()) {
		return ErrWebhookTimestamp
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(strings.TrimSpace(secret), "whsec_"))
	if err != nil || len(key) == 0 {
		// A malformed secret is an operator misconfiguration, but surfacing it as
		// "not configured" keeps the handler's response uniform (401) and material-free.
		return ErrWebhookNotConfigured
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(h.id + "." + h.timestamp + "." + string(body)))
	expected := mac.Sum(nil)

	for _, part := range strings.Fields(h.signature) {
		version, sigB64, found := strings.Cut(part, ",")
		if !found {
			// A bare token with no scheme prefix — treat the whole thing as a v1 sig.
			version, sigB64 = "v1", part
		}
		if version != "v1" {
			continue
		}
		sig, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			continue
		}
		if hmac.Equal(sig, expected) {
			return nil
		}
	}
	return ErrWebhookSignature
}
