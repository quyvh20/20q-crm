package marketing

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"crm-backend/internal/automation"
)

// Conditional content (Brevo-style "show only if"). Compile happens ONCE at
// save, but conditions are per-recipient — so the compiler wraps a conditional
// root section in invisible marker divs (injected via mj-raw, so MJML passes
// them through verbatim), and ApplyConditions evaluates + strips them at
// render time. Every send lane and the view-as-contact preview go through
// RenderForRecipient, so evaluation is automatic; sample preview and test-send
// call StripConditionMarkers instead (show everything, drop the plumbing).

// condOps is the operator allowlist (also enforced at save by the validator).
var condOps = map[string]bool{
	"exists": true, "not_exists": true, "eq": true, "neq": true, "contains": true,
	"gt": true, "lt": true,
}

// ValidCondition reports whether a condition is well-formed enough to compile.
func ValidCondition(c *BlockCondition) bool {
	return c != nil && strings.TrimSpace(c.Field) != "" && condOps[c.Op]
}

// condStartMJML/condEndMJML emit the marker pair around a compiled section.
// The payload is std-base64 JSON: the alphabet is HTML-safe and the trailing
// '=' padding keeps minifiers from unquoting the attribute.
func condStartMJML(c *BlockCondition) string {
	payload, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return `<mj-raw><div data-cond="` + base64.StdEncoding.EncodeToString(payload) + `" style="display:none"></div></mj-raw>`
}

func condEndMJML() string {
	return `<mj-raw><div data-cond-end="1" style="display:none"></div></mj-raw>`
}

// Tolerant of minifier attribute shuffling; the base64 group is the payload.
var condStartRe = regexp.MustCompile(`<div[^>]*\bdata-cond="([A-Za-z0-9+/=]+)"[^>]*></div>`)
var condEndRe = regexp.MustCompile(`<div[^>]*\bdata-cond-end[^>]*></div>`)

// ApplyConditions evaluates every marked range against the recipient context:
// matching ranges keep their content, non-matching ranges are removed whole.
// Markers never survive to the sent email either way. Root-only conditions
// mean ranges never nest, so first-start → next-end pairing is sound.
func ApplyConditions(html string, ec automation.EvalContext) string {
	return processConditions(html, func(c *BlockCondition) bool { return evalCondition(c, ec) })
}

// StripConditionMarkers removes the plumbing but KEEPS all content — the
// sample preview and test-send show everything.
func StripConditionMarkers(html string) string {
	return processConditions(html, func(*BlockCondition) bool { return true })
}

func processConditions(html string, keep func(*BlockCondition) bool) string {
	if !strings.Contains(html, "data-cond") {
		return html
	}
	var b strings.Builder
	rest := html
	for {
		loc := condStartRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:loc[0]])
		payload := rest[loc[2]:loc[3]]
		after := rest[loc[1]:]
		endLoc := condEndRe.FindStringIndex(after)
		if endLoc == nil {
			// Unbalanced (should not happen) — drop the dangling marker, keep content.
			rest = after
			continue
		}
		content := after[:endLoc[0]]
		var cond BlockCondition
		keepIt := true
		if raw, err := base64.StdEncoding.DecodeString(payload); err == nil && json.Unmarshal(raw, &cond) == nil {
			keepIt = keep(&cond)
		}
		if keepIt {
			b.WriteString(content)
		}
		rest = after[endLoc[1]:]
	}
	return b.String()
}

// evalCondition resolves the field through the SAME interpolator merge tags
// use, so conditions and tags always agree on what a recipient's value is.
// Unresolvable/empty both read as "no value".
func evalCondition(c *BlockCondition, ec automation.EvalContext) bool {
	if !ValidCondition(c) {
		return true // malformed ⇒ fail open (show), matching validator leniency
	}
	val := strings.TrimSpace(automation.InterpolateTemplate("{{"+c.Field+"}}", ec))
	want := strings.TrimSpace(c.Value)
	switch c.Op {
	case "exists":
		return val != ""
	case "not_exists":
		return val == ""
	case "eq":
		return strings.EqualFold(val, want)
	case "neq":
		return !strings.EqualFold(val, want)
	case "contains":
		return strings.Contains(strings.ToLower(val), strings.ToLower(want))
	case "gt", "lt":
		// Numeric comparison. A non-numeric recipient value (or rule value) is
		// simply "no match" — fail closed for comparisons, unlike the malformed-
		// condition fail-open above, because the author expressed a threshold.
		a, errA := strconv.ParseFloat(val, 64)
		b, errB := strconv.ParseFloat(want, 64)
		if errA != nil || errB != nil {
			return false
		}
		if c.Op == "gt" {
			return a > b
		}
		return a < b
	default:
		return true
	}
}
