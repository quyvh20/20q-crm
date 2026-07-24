package marketing

import (
	"fmt"
	"strings"

	"crm-backend/internal/automation"
)

// knownLeaves is the built-in field set per merge root. custom_fields.<key> is
// always allowed (org-defined, dynamic) for roots that carry custom fields.
var knownLeaves = map[string]map[string]bool{
	ScopeContact:  {"id": true, "first_name": true, "last_name": true, "name": true, "email": true, "phone": true, "owner_user_id": true, "company_id": true},
	ScopeCompany:  {"id": true, "name": true, "industry": true, "website": true},
	ScopeOrg:      {"name": true},
	ScopeCampaign: {"name": true, "unsubscribe_url": true},
}

// rootsWithCustomFields carry a dynamic custom_fields.<key> namespace.
var rootsWithCustomFields = map[string]bool{ScopeContact: true, ScopeCompany: true}

// guaranteedTags are the only merge tags exempt from the mandatory-fallback rule:
// a sendable contact always has an email (M1 IsSendable), and an org always has a
// name. Everything else can be empty per recipient, so it needs a fallback.
var guaranteedTags = map[string]bool{"contact.email": true, "org.name": true}

// ContentError is one validation failure, tied to the field it was found in.
type ContentError struct {
	Field  string `json:"field"`  // "subject" | "preheader" | "block:<id>"
	Tag    string `json:"tag"`    // the offending {{...}} token
	Reason string `json:"reason"` // human-readable
}

// ValidateContent enforces the B4 save-time gates on a marketing campaign's authored
// fields: every merge tag's ROOT must be in the declared scope, its LEAF must be a
// known field (or a custom_fields.* path), and every non-guaranteed tag must carry a
// fallback so nothing renders blank at scale. The compiler-injected footer tokens
// are NOT author fields and are not checked here.
func ValidateContent(subject, preheader string, doc BlockDocument, scope []string) []ContentError {
	allowed := map[string]bool{}
	for _, r := range scope {
		if validMergeRoots[r] {
			allowed[r] = true
		}
	}
	if len(allowed) == 0 {
		for _, r := range DefaultMergeScope() {
			allowed[r] = true
		}
	}

	var errs []ContentError
	check := func(field, text string) {
		for _, ref := range automation.ExtractMergeTags(text) {
			if e := validateTag(ref, allowed); e != nil {
				e.Field = field
				errs = append(errs, *e)
			}
		}
	}
	check("subject", subject)
	check("preheader", preheader)
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for _, blk := range blocks {
			field := "block:" + blk.ID
			// Every field that carries merge tokens into the compiled HTML must be
			// gated — including URL/alt attributes, or a {{deal.id}} in a CTA href or a
			// fallback-less token in an image src escapes the scope + fallback rules and
			// renders a dead link / broken image at scale.
			check(field, blk.Text)
			check(field, blk.Label)
			check(field, blk.Href)
			check(field, blk.Src)
			check(field, blk.Alt)
			for _, col := range blk.Columns {
				walk(col)
			}
		}
	}
	walk(doc.Blocks)
	return errs
}

func validateTag(ref automation.MergeRef, allowed map[string]bool) *ContentError {
	root, leaf, _ := strings.Cut(ref.Path, ".")
	if !allowed[root] {
		return &ContentError{Tag: ref.Raw, Reason: fmt.Sprintf("%q is not in this campaign's merge scope", root)}
	}
	if leaf == "" {
		return &ContentError{Tag: ref.Raw, Reason: "a bare root is not a mergeable field — use e.g. " + root + ".name"}
	}
	// custom_fields.<key> is always allowed for roots that have them.
	isCustom := strings.HasPrefix(leaf, "custom_fields.") && rootsWithCustomFields[root]
	if !isCustom {
		if set := knownLeaves[root]; set != nil && !set[leaf] {
			return &ContentError{Tag: ref.Raw, Reason: fmt.Sprintf("%q is not a known %s field", ref.Path, root)}
		}
	}
	if !ref.HasFallback && !guaranteedTags[ref.Path] {
		return &ContentError{Tag: ref.Raw, Reason: fmt.Sprintf("this tag needs a fallback so it never renders blank, e.g. {{%s|…}}", ref.Path)}
	}
	return nil
}

// NormalizeMergeScope filters an incoming scope to valid roots, always includes the
// defaults, and de-dups. An empty/garbage input collapses to the default.
func NormalizeMergeScope(scope []string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(r string) {
		if validMergeRoots[r] && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, r := range DefaultMergeScope() {
		add(r)
	}
	for _, r := range scope {
		add(r)
	}
	return out
}
