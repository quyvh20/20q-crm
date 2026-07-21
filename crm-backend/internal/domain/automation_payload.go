package domain

import "encoding/json"

// SetAutomationCustomFields folds a record's custom_fields JSONB into the payload
// the automation engine evaluates against.
//
// It MUST write a nested map, because every reader resolves dotted paths by
// splitting on "." and walking maps: automation.getNestedValue (engine.go) and
// resolveNestedPath (template.go) both do. Writing the flattened form
//
//	m["custom_fields."+k] = v
//
// produces a key containing a literal dot that no reader can ever reach, so
// `{{contact.custom_fields.tier}}` renders empty and a condition on it fails
// closed — with no error and no failed run. The workflow installs, reports itself
// active, and silently does nothing.
//
// Three call sites had independently flattened it (the uniform RecordService write
// path, the run-now entity loader, and the legacy contact handler), each with a
// comment saying it was matching one of the others. Meanwhile the trigger loaders
// that DID nest — loadContactForTrigger / loadCompanyForTrigger — worked. So the
// same field path resolved or didn't depending on which trigger fired, which is
// the hardest version of this bug to diagnose from the outside.
//
// One definition, so the shapes cannot drift apart again.
func SetAutomationCustomFields(m map[string]any, raw []byte) {
	if len(raw) == 0 {
		return
	}
	s := string(raw)
	if s == "null" || s == "{}" {
		return
	}
	var cf map[string]any
	if err := json.Unmarshal(raw, &cf); err != nil {
		return
	}
	if len(cf) == 0 {
		return
	}
	m["custom_fields"] = cf
}
