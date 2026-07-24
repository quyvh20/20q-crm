package marketing

import (
	"html"
	"regexp"
	"strings"

	"crm-backend/internal/automation"
)

// bareUnsubTokenRe matches the compiler's ALWAYS-appended footer slot
// {{unsubscribe_url|#}} (any/no fallback). It anchors on `{{` immediately followed
// by `unsubscribe_url`, so the author-placeable dotted twin {{campaign.unsubscribe_url}}
// (which the interpolator CAN resolve) is left untouched.
var bareUnsubTokenRe = regexp.MustCompile(`\{\{\s*unsubscribe_url\b[^}]*\}\}`)

// FooterContext supplies the per-send values the CAN-SPAM footer + preheader
// resolve to at render time.
type FooterContext struct {
	OrgName       string // the org's display name (footer {{org.name}})
	PostalAddress string // the org's physical postal address (footer {{org.postal_address}})
	UnsubURL      string // the browser-facing preference-center URL (the visible in-body link)
}

// RenderForRecipient resolves a compiled marketing body for ONE recipient WITHOUT
// mutating the stored BodyHTMLCompiled. The M6.1 compiler already baked the footer
// (org name/address + unsubscribe slot) and the hidden preheader into the compiled
// HTML as {{tokens}}; this fills them in and runs the standard merge interpolation
// over everything else, returning a NEW string.
//
// The bare footer slot {{unsubscribe_url|#}} is substituted directly because the
// interpolator cannot resolve a dot-less root; the author-placeable
// {{campaign.unsubscribe_url}} is seeded into the campaign scope so both spellings
// point at the same URL. org.name / org.postal_address are seeded from the org's
// marketing profile (they are not part of the normal trigger context).
//
// This is called ONLY by the marketing send lane (channel=marketing, M7) — it never
// runs for transactional mail, so shared templates and in-flight runs are untouched.
func RenderForRecipient(compiledHTML string, ctx automation.EvalContext, fc FooterContext) string {
	// Copy the context maps so a caller reusing a base context across recipients is
	// never mutated (postal address is org-wide, but unsubscribe_url is per-recipient).
	org := map[string]any{}
	for k, v := range ctx.Org {
		org[k] = v
	}
	if strings.TrimSpace(fc.OrgName) != "" {
		org["name"] = fc.OrgName
	}
	org["postal_address"] = fc.PostalAddress
	ctx.Org = org

	extra := map[string]any{}
	for k, v := range ctx.Extra {
		extra[k] = v
	}
	camp := map[string]any{}
	if c, ok := extra["campaign"].(map[string]any); ok {
		for k, v := range c {
			camp[k] = v
		}
	}
	camp["unsubscribe_url"] = fc.UnsubURL
	extra["campaign"] = camp
	ctx.Extra = extra

	// ReplaceAllStringFunc (not ReplaceAllString) so a `$` in the URL is never read as
	// a submatch reference. The URL is HTML-escaped for the href-attribute context.
	escaped := html.EscapeString(fc.UnsubURL)
	substituted := bareUnsubTokenRe.ReplaceAllStringFunc(compiledHTML, func(string) string { return escaped })
	return automation.InterpolateTemplateHTML(substituted, ctx)
}

// MarketingUnsubHeaders builds the byte-exact RFC-8058 one-click headers Resend
// carries in the request body (channel=marketing only). List-Unsubscribe-Post is a
// fixed literal — Gmail/Yahoo silently ignore any variation. oneClickURL is the
// BACKEND POST endpoint (built from the API origin), which must be the same URL the
// one-click POST is issued to.
func MarketingUnsubHeaders(oneClickURL string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + oneClickURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

// OneClickUnsubURL builds the server-to-server one-click POST endpoint from the API
// origin (never the request Host — prod fronts /api through a Pages Function). This
// is the URL that goes in the List-Unsubscribe header and is POSTed by mailbox
// providers.
func OneClickUnsubURL(publicAPIBaseURL, token string) string {
	return strings.TrimRight(publicAPIBaseURL, "/") + "/api/marketing/u/" + token
}

// PreferenceCenterURL builds the browser-facing preference-center URL from the
// frontend origin — the visible in-body unsubscribe link a human clicks. It resolves
// to the SPA /u/:token route, which calls the public backend endpoint.
func PreferenceCenterURL(frontendURL, token string) string {
	return strings.TrimRight(frontendURL, "/") + "/u/" + token
}
