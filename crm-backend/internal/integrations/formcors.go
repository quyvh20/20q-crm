package integrations

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS for the form-embed capture route.
//
// This is the app's only credentials-free CORS surface, and it exists because the
// global handler cannot serve it: that one allows three fixed origins WITH
// credentials, and a form embed runs on the customer's own domain. main.go skips
// the global handler for FormCapturePrefix and this takes over.
//
// HAND-ROLLED on purpose rather than a second cors.New. That constructor PANICS
// when its config has no origins and no origin function — which is exactly the
// shape a per-request, DB-driven allowlist has — and a panic here is a boot
// failure that takes down the entire API. Thirty explicit lines beat that.
//
// WHAT THIS DOES AND DOES NOT PROTECT. It is a nuisance filter, not an
// authentication check. A browser sends Origin and will not hand the response to a
// page whose origin we did not echo, so this stops another SITE from embedding a
// customer's form and reading the result. It stops nothing at all from a script:
// curl sends no Origin, and the no-Origin branch below passes straight through, as
// it must — the same request shape L1 and L3 integrators use legitimately. The
// bounds that actually apply to a script are the rate limiters, the daily cap and
// Turnstile. Anyone who later proposes dropping Turnstile "because we have an
// origin allowlist" is making the mistake this paragraph exists to prevent.

// FormCapturePrefix is the path main.go skips the global CORS handler for. Exported
// so the skip and the routes below can never drift apart into two spellings.
const FormCapturePrefix = "/api/capture/forms"

// formPreflightMaxAge caps how long a browser may cache the preflight. Short
// deliberately: an admin who removes an origin should see it take effect in
// minutes, not hours.
const formPreflightMaxAge = "600"

// formCORS resolves the source's allowlist and either authorizes the browser or
// refuses before anything is written.
//
// It also carries the RATE-LIMIT CHARGE for this route. That is not tidiness: the
// module's standing rule is to throttle before the DB probe (an unauthenticated
// flood must not be a DB amplifier), and this middleware performs the probe. The
// handler therefore does not charge again — one charge per request, here, and
// preflights count, because a preflight is a DB read too.
func (h *Handler) formCORS(c *gin.Context) {
	token := strings.TrimSpace(c.Param("public_token"))
	origin := strings.TrimSpace(c.Request.Header.Get("Origin"))

	// Vary on Origin whatever happens: the response differs by it, and a cache that
	// does not know that would serve one site's ACAO to another.
	c.Writer.Header().Add("Vary", "Origin")

	if !h.allow(c, "ft:"+token) || !h.allow(c, "ip:"+c.ClientIP()) {
		c.Abort() // allow() already wrote 429 + Retry-After
		return
	}

	// No Origin header at all: not a browser CORS request. Pass through without any
	// CORS headers — there is no origin to echo, and nothing to protect a caller
	// from who is not a browser. The handler's other bounds still apply.
	if origin == "" {
		if c.Request.Method == http.MethodOptions {
			// A preflight without an Origin is malformed; answer 204 and stop rather
			// than letting it reach the handler.
			c.AbortWithStatus(http.StatusNoContent)
		}
		return
	}

	allowed, known := h.originAllowed(c, token, origin)
	if !known {
		// Unknown token, wrong kind, or an unreadable allowlist. Answered exactly like
		// a disallowed origin so the response cannot be used to probe which tokens are
		// live — a distinguishable answer here would be a token-validity oracle.
		h.refuseFormOrigin(c)
		return
	}
	if !allowed {
		h.refuseFormOrigin(c)
		return
	}

	// Authorized. Set (never Add) — two Access-Control-Allow-Origin values is a hard
	// browser error, not a warning.
	c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
	// Deliberately NO Access-Control-Allow-Credentials, in any form. Absent is
	// unambiguous; sending "false" invites someone to "fix" it to true later.
	//
	// X-Request-ID is exposed so the snippet's error hook can surface an id that
	// actually appears in our logs; cross-origin, a page reads no header we do not
	// name here.
	c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")

	if c.Request.Method == http.MethodOptions {
		c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		c.Writer.Header().Set("Access-Control-Max-Age", formPreflightMaxAge)
		c.AbortWithStatus(http.StatusNoContent)
		return
	}

	// The header is set BEFORE the handler runs, so every response it writes —
	// including a 429, a 422 or a 500 — carries it and is readable by the page. A
	// browser that cannot read an error sees only "network error", which is the
	// worst possible diagnostic for someone debugging their own website.
	c.Next()
}

// originAllowed answers whether this origin may post to this token's source, and
// whether the source was resolvable at all.
//
// The (allowed, known) split exists so the caller can respond identically to both
// negatives while the LOGS still distinguish them.
func (h *Handler) originAllowed(c *gin.Context, token, origin string) (allowed, known bool) {
	if token == "" {
		return false, false
	}
	src, err := h.repo.FindFormSourceByPublicToken(c.Request.Context(), token)
	if err != nil {
		h.logger.Error("integrations: form source lookup failed", "error", err)
		return false, false
	}
	if src == nil || !src.IsLive() || src.Kind != KindFormEmbed {
		return false, false
	}
	origins, err := src.AllowedOriginList()
	if err != nil {
		// The allowlist exists but could not be decoded. Refuse: the alternatives are
		// to treat it as empty (looks like a deliberate deny and sends an admin
		// hunting the wrong thing) or to allow everything (a hole). This is the case
		// the column exists outside `config` to keep distinguishable.
		h.logger.Error("integrations: unreadable allowed_origins",
			"source_id", src.ID.String(), "error", err)
		return false, false
	}
	return OriginAllowed(origins, origin), true
}

// refuseFormOrigin denies a browser origin without echoing anything.
//
// A 403 with no Access-Control-Allow-Origin is, to the page, indistinguishable
// from a network failure — which is correct: a site we did not authorize gets no
// information back. The body is for curl and for our own logs.
//
// It must abort BEFORE the handler, so a refused origin can never write a lead.
// "Blocked" that still stores the submission would be a false claim.
func (h *Handler) refuseFormOrigin(c *gin.Context) {
	if c.Request.Method == http.MethodOptions {
		c.AbortWithStatus(http.StatusNoContent)
		return
	}
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
}
