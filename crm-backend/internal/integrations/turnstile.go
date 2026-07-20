package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Cloudflare Turnstile — the optional bot check on a form-embed source.
//
// Optional per source, and the UI says plainly what a source without it has:
// a honeypot, the rate limiters and the daily cap, none of which stop a determined
// script. Keys are the CUSTOMER'S, not ours — Cloudflare's free tier allows 20
// widgets per account, so putting every customer's form on our account would cap
// the whole platform at twenty forms.

const (
	turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileTimeout   = 5 * time.Second
)

// TurnstileVerdict is what the check decided and what to do about it.
type TurnstileVerdict struct {
	OK bool
	// TellVisitor distinguishes "this person should try again" from "say nothing".
	// It is true ONLY for a stale or already-spent token, which almost always means
	// a real human who left the tab open — telling them lets the widget
	// re-challenge and the submission succeed. Everything else stays silent, because
	// a bot that learns why it failed simply adapts.
	TellVisitor bool
	LedgerNote  string
}

var turnstileOK = TurnstileVerdict{OK: true}

// verifyTurnstile checks a submission's token when the source has Turnstile on.
//
// FAILURE POLARITY. Cloudflare documents no recommended posture for their own
// endpoint being unreachable, so this is our choice and it is written down rather
// than left implicit. Neither pole is acceptable on its own: failing open makes an
// outage a spam window, and failing closed makes it a lead-loss window. So the
// third branch this codebase already uses for the daily cap applies — accept the
// submission, write NO contact, store it on the ledger for an admin to replay. It
// costs no record, no workflow and no billable email, and nothing is lost.
func (h *Handler) verifyTurnstile(c *gin.Context, source *FormSource, token string) TurnstileVerdict {
	secret, err := h.repo.GetTurnstileSecret(c.Request.Context(), source.OrgID, source.ID)
	if err != nil {
		h.logger.Error("integrations: could not read turnstile secret", "error", err, "source_id", source.ID.String())
		return TurnstileVerdict{LedgerNote: "the bot check could not be read, so this submission was stored rather than written"}
	}
	if strings.TrimSpace(secret) == "" {
		return turnstileOK // not configured: the honeypot and the limiters are the bounds
	}

	token = strings.TrimSpace(token)
	if token == "" {
		// The widget never ran. A real visitor cannot submit the generated snippet
		// without it, so this is a script posting straight at the endpoint.
		return TurnstileVerdict{LedgerNote: "no bot-check token was sent — recorded as spam, no contact written"}
	}

	res, err := h.turnstileSiteverify(c.Request.Context(), secret, token, c.ClientIP())
	if err != nil {
		h.logger.Error("integrations: turnstile verify failed", "error", err, "source_id", source.ID.String())
		return TurnstileVerdict{LedgerNote: "the bot check was unreachable, so this submission was stored rather than written"}
	}
	if res.Success {
		return turnstileOK
	}

	for _, code := range res.ErrorCodes {
		switch code {
		case "timeout-or-duplicate", "invalid-input-response":
			// A stale token (they sat on the page) or one already spent (a double
			// submit). Overwhelmingly a real person.
			return TurnstileVerdict{
				TellVisitor: true,
				LedgerNote:  "the bot-check token had expired or was already used; the visitor was asked to try again",
			}
		case "invalid-input-secret", "missing-input-secret", "bad-request":
			// OUR misconfiguration, most often right after a key rotation. Loud in the
			// log, silent to the visitor, and never a dropped lead.
			h.logger.Error("integrations: turnstile is misconfigured for this source",
				"source_id", source.ID.String(), "code", code)
			return TurnstileVerdict{LedgerNote: "the bot check is misconfigured (" + code + "), so this submission was stored rather than written"}
		}
	}
	return TurnstileVerdict{LedgerNote: "the bot check rejected this submission — recorded as spam, no contact written"}
}

// turnstileResponse is siteverify's documented shape.
type turnstileResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Hostname   string   `json:"hostname"`
	ChallengeT string   `json:"challenge_ts"`
}

// turnstileSiteverify posts one token to Cloudflare.
//
// Verified EXACTLY ONCE per submission, never on a retry: tokens are single-use
// with a short life, so a second verification of the same token returns
// timeout-or-duplicate and would drop a lead that had already passed.
func (h *Handler) turnstileSiteverify(ctx context.Context, secret, token, remoteIP string) (*turnstileResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, turnstileTimeout)
	defer cancel()

	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileVerifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := h.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out turnstileResponse
	// Bounded: a response that started streaming would otherwise hold the visitor's
	// submission open. A body cut at the limit fails the decode, which is the right
	// outcome — an unreadable verdict is not a pass.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// httpClient returns the outbound client, defaulting so tests can inject one.
func (h *Handler) httpClient() *http.Client {
	if h.http != nil {
		return h.http
	}
	return &http.Client{Timeout: turnstileTimeout}
}
