package integrations

import (
	"encoding/json"
	"strings"
)

// The mapping engine: what a third party calls a field vs what this CRM calls it.
//
// Real sources do not send {"email": …}. They send "Work Email", "Email Address",
// or a Facebook question id. Without mapping, every one of those is an unknown key
// — quarantined, correct, and useless.

// Transforms. Deliberately a tiny closed set: a mapping UI has to be able to
// explain each one in a sentence, and anything richer belongs in a workflow, not in
// a config row a third party's payload shape depends on.
const (
	TransformNone      = ""
	TransformSplitName = "split_name" // "Ada Lovelace" → first_name + last_name
	TransformLower     = "lower"
	TransformTrim      = "trim"
)

var validTransforms = map[string]bool{
	TransformNone: true, TransformSplitName: true, TransformLower: true, TransformTrim: true,
}

// IsValidTransform reports whether a transform is known.
func IsValidTransform(t string) bool { return validTransforms[t] }

// FieldMapEntry maps one inbound key onto one CRM field.
type FieldMapEntry struct {
	TargetKey string `json:"target_key"`
	Transform string `json:"transform,omitempty"`
}

// FieldMap is source_key → entry.
type FieldMap map[string]FieldMapEntry

// ParseFieldMap decodes a source's stored field_map.
//
// An empty/absent map means — and must keep meaning — identity over the
// schema-valid keys. That is the L1 behaviour, and every source created before this
// engine existed has an empty map: reinterpreting empty as "strict, map nothing"
// would silently stop every live integration on the day this deploys.
func ParseFieldMap(raw []byte) (FieldMap, error) {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return FieldMap{}, nil
	}
	var m FieldMap
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// IsIdentity reports whether this map leaves inbound keys untouched.
func (m FieldMap) IsIdentity() bool { return len(m) == 0 }

// Apply rewrites an inbound payload into CRM field keys.
//
// Returns the mapped fields plus the mapping FAILURES (source_key → reason). A
// failure never rejects the lead: it is recorded and quarantined alongside the
// unmapped keys, because a lead half-understood is worth incomparably more than a
// lead refused. The one thing this must never do is drop a value silently.
//
// Mapping runs BEFORE the allowlist, and does not bypass it: a map pointing at
// owner_user_id still gets quarantined downstream. Save-time validation rejects
// such a mapping outright, but a row edited by hand must not become a hole.
func (m FieldMap) Apply(in map[string]any) (mapped map[string]any, failures map[string]string) {
	mapped = make(map[string]any, len(in))
	failures = map[string]string{}

	for srcKey, val := range in {
		entry, ok := m[srcKey]
		if !ok {
			// Unmapped: pass the key through unchanged so identity behaviour and
			// partial maps both work. The allowlist decides whether it is writable.
			mapped[srcKey] = val
			continue
		}
		if entry.TargetKey == "" {
			failures[srcKey] = "mapping has no target field"
			continue
		}
		switch entry.Transform {
		case TransformSplitName:
			first, last := splitFullName(stringOf(val))
			if first == "" {
				failures[srcKey] = "could not split into a first and last name"
				continue
			}
			mapped["first_name"] = first
			if last != "" {
				mapped["last_name"] = last
			}
		case TransformLower:
			mapped[entry.TargetKey] = strings.ToLower(stringOf(val))
		case TransformTrim:
			mapped[entry.TargetKey] = strings.TrimSpace(stringOf(val))
		default:
			mapped[entry.TargetKey] = val
		}
	}
	return mapped, failures
}

// splitFullName splits a single name field into first/last.
//
// Last-space split, not first: "Ada Byron King" is far more likely to be
// first="Ada" last="Byron King" than the reverse, and ad platforms overwhelmingly
// send one "Full Name" field. This is a heuristic and it will be wrong for some
// names — which is exactly why the raw payload is kept verbatim on the event row.
func splitFullName(full string) (first, last string) {
	full = strings.Join(strings.Fields(full), " ") // collapse runs of whitespace
	if full == "" {
		return "", ""
	}
	i := strings.LastIndex(full, " ")
	if i < 0 {
		return full, "" // a single token is a first name
	}
	return full[:i], full[i+1:]
}

// ValidateFieldMap checks a map at SAVE time, against the object's writable keys.
//
// Save time is the right time: a mapping that can never work should fail in front
// of the admin who wrote it, not silently quarantine every lead at 3am. The
// allowlist is the authority on what is writable, so a map pointing at
// owner_user_id or a relation is rejected here for the same reasons it would be
// quarantined there.
func ValidateFieldMap(m FieldMap, allow *Allowlist) map[string]string {
	problems := map[string]string{}
	for srcKey, entry := range m {
		if entry.Transform != "" && !IsValidTransform(entry.Transform) {
			problems[srcKey] = "unknown transform: " + entry.Transform
			continue
		}
		// split_name writes first_name/last_name itself, so its target is implied.
		if entry.Transform == TransformSplitName {
			if !allow.Permits("first_name") {
				problems[srcKey] = "this object has no first_name to split into"
			}
			continue
		}
		if entry.TargetKey == "" {
			problems[srcKey] = "choose a field to map this into"
			continue
		}
		if blacklistedKeys[entry.TargetKey] {
			problems[srcKey] = entry.TargetKey + " cannot be set by an inbound lead"
			continue
		}
		if !allow.Permits(entry.TargetKey) {
			problems[srcKey] = "unknown field: " + entry.TargetKey
		}
	}
	return problems
}
