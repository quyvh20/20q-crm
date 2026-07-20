package integrations

import (
	"encoding/json"
	"net/url"
	"strings"

	"crm-backend/internal/domain"

	"gorm.io/datatypes"
)

// Web-to-lead form embeds (plan L4): a form on the customer's OWN website, posting
// straight from the visitor's browser.
//
// This is the first kind with no secret of any sort. The token sits in the page
// source by construction — anyone who views source has it — so nothing on this
// route authenticates the caller, and the design has to be honest about that
// rather than dressing an allowlist up as a credential. See AllowedOrigins below.

// FormField is one field the customer's form collects.
type FormField struct {
	// Name is the key the snippet posts and the key the field map is keyed on.
	Name string `json:"name"`
	// Label is what the visitor sees. Rendered into the generated snippet only.
	Label string `json:"label"`
	// Type is an HTML input type ("text", "email", "tel", "textarea").
	Type string `json:"type"`
	// Required renders the HTML attribute. Never enforced server-side: a lead that
	// arrives missing a field is still a lead, and rejecting it here would be the
	// silent loss this whole subsystem exists to prevent.
	Required bool `json:"required"`
}

// FormConfig is a form_embed source's definition.
//
// Lives inside the source's existing `config` JSONB under the "form" key — the
// same slot and the same reasoning as the deal option: that column has been in the
// CREATE TABLE since the table existed, so unlike an ALTER-added column it cannot
// be missing where the table is, and nesting under a key lets several features
// share it. The allowlist deliberately does NOT live here (see AllowedOrigins).
type FormConfig struct {
	Enabled bool        `json:"enabled"`
	Fields  []FormField `json:"fields,omitempty"`
	// Honeypot is the name of a field no human ever fills — the snippet renders it
	// visually hidden. A submission that carries a value for it is a bot.
	Honeypot string `json:"honeypot,omitempty"`
	// ThankYou is what the snippet shows on success.
	ThankYou string `json:"thank_you,omitempty"`
	// TurnstileSiteKey is the PUBLIC half of a Cloudflare Turnstile pair. Public by
	// nature (it goes in the page), so it lives here; the secret half does not.
	TurnstileSiteKey string `json:"turnstile_site_key,omitempty"`
}

const formConfigKey = "form"

// Bounds applied at SAVE time. A form definition is an order of magnitude bigger
// than the deal option that shares this column, and it is admin-authored, so the
// place to refuse an absurd one is in front of the admin.
const (
	maxFormFields      = 50
	maxFormLabelLen    = 200
	maxAllowedOrigins  = 20
	defaultHoneypotKey = "company_website"
)

// ParseFormConfig reads a source's form definition. It NEVER returns an error:
// missing, {}, null and junk all mean an unconfigured form. Same polarity as
// ParseDealConfig and ParseFieldMap — config that cannot be read degrades the
// feature and must never be the reason a customer's lead is refused.
//
// Note this is the polarity that makes it the WRONG home for the origin allowlist.
func ParseFormConfig(raw datatypes.JSON) FormConfig {
	if len(raw) == 0 {
		return FormConfig{}
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return FormConfig{}
	}
	body, ok := envelope[formConfigKey]
	if !ok || len(body) == 0 {
		return FormConfig{}
	}
	var cfg FormConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return FormConfig{}
	}
	return cfg
}

// MergeFormConfig folds a form definition into an existing config blob, preserving
// every other key — the deal option lives next door.
func MergeFormConfig(raw datatypes.JSON, cfg FormConfig) (datatypes.JSON, error) {
	envelope := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			envelope = map[string]any{}
		}
	}
	envelope[formConfigKey] = cfg
	out, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(out), nil
}

// DeclaredFields returns the set of field names this form says it collects.
//
// It is the allowlist that the google_ads route gets from its key: on a route
// anyone can post to, the declared list is what stops a stranger writing arbitrary
// keys into raw_payload — which the mapping UI samples for its suggestions, so
// undeclared keys would let anyone seed the admin's picker from outside.
func (c FormConfig) DeclaredFields() map[string]bool {
	out := make(map[string]bool, len(c.Fields)+1)
	for _, f := range c.Fields {
		if n := strings.TrimSpace(f.Name); n != "" {
			out[n] = true
		}
	}
	return out
}

// ValidateFormConfig checks a definition at SAVE time, failing closed.
func ValidateFormConfig(cfg FormConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if len(cfg.Fields) == 0 {
		return domain.NewAppError(400, "a form needs at least one field")
	}
	if len(cfg.Fields) > maxFormFields {
		return domain.NewAppError(400, "a form cannot collect more than 50 fields")
	}
	seen := map[string]bool{}
	for _, f := range cfg.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return domain.NewAppError(400, "every field needs a name")
		}
		if seen[name] {
			return domain.NewAppError(400, "duplicate field name: "+name)
		}
		seen[name] = true
		if len(f.Label) > maxFormLabelLen {
			return domain.NewAppError(400, "the label for "+name+" is too long")
		}
	}
	// The honeypot must not collide with a real field, or the form would quarantine
	// every genuine submission that filled it in.
	if h := strings.TrimSpace(cfg.Honeypot); h != "" && seen[h] {
		return domain.NewAppError(400, "the honeypot field name is also a real field: "+h)
	}
	return nil
}

// ── Origin allowlist ─────────────────────────────────────────────────────────
//
// WHAT THIS IS AND IS NOT. An origin allowlist is a NUISANCE FILTER, not an
// authentication check, and the code says so here because the UI says so to the
// admin and the two must not drift.
//
// A browser sends Origin and refuses to hand the response back to a page whose
// origin we did not echo. curl sends no Origin at all — and gin-contrib/cors'
// first branch returns immediately when the header is absent, as does ours. So
// this list stops another SITE from embedding your form and reading the result;
// it stops nothing whatsoever from a script. The bounds that actually apply to a
// script are the rate limiters, the daily cap, and Turnstile.
//
// Anyone who later proposes dropping Turnstile "because we have an origin
// allowlist" is making exactly the mistake this comment exists to prevent.

// NormalizeOrigin reduces an origin to its comparable form: scheme://host[:port],
// lowercased, no trailing slash, no path.
//
// Returns "" for anything that is not a usable origin — a path, a bare hostname,
// a wildcard, or the literal "null" (which browsers send for sandboxed iframes and
// file:// pages and which must never be allowlistable).
func NormalizeOrigin(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || strings.EqualFold(s, "null") || strings.Contains(s, "*") {
		return ""
	}
	s = strings.TrimRight(s, "/")
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	// A path, query or fragment means the admin pasted a page URL rather than an
	// origin. Refusing is better than silently keeping the origin part, because the
	// admin would then not learn the difference.
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	return scheme + "://" + strings.ToLower(u.Host)
}

// ValidateAllowedOrigins normalizes and bounds an admin-supplied list, failing
// closed on anything unusable.
func ValidateAllowedOrigins(in []string) ([]string, error) {
	if len(in) > maxAllowedOrigins {
		return nil, domain.NewAppError(400, "that is more origins than a form plausibly runs on")
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		n := NormalizeOrigin(raw)
		if n == "" {
			return nil, domain.NewAppError(400,
				"not a usable origin: "+strings.TrimSpace(raw)+" — use the scheme and domain only, like https://example.com")
		}
		if seen[n] {
			continue // a duplicate is a typo, not an error worth refusing a save over
		}
		seen[n] = true
		out = append(out, n)
	}
	return out, nil
}

// OriginAllowed reports whether a request origin may submit to this source.
//
// Exact match on the normalized form. No wildcards in v1, deliberately: the
// obvious suffix implementation (`strings.HasSuffix(origin, ".example.com")`
// without requiring the dot) matches `evilexample.com`, and a subdomain wildcard
// is worth having only once it is written carefully.
//
// An EMPTY list allows nothing. That is the state of every freshly created source,
// so it must not mean "allow everything" — the single most likely way this feature
// could silently fail open.
func OriginAllowed(allowed []string, origin string) bool {
	n := NormalizeOrigin(origin)
	if n == "" {
		return false
	}
	for _, a := range allowed {
		if a == n {
			return true
		}
	}
	return false
}
