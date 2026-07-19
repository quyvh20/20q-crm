package domain

import "encoding/json"

// MergeJSONObjects overlays the top-level keys of `overlay` onto `base` and returns
// the merged object as a map.
//
// This is how a PARTIAL edit of a wholesale-stored JSON blob — a custom object's
// `data` or a record's `custom_fields` — preserves the fields it does not mention
// instead of replacing the whole blob. The uniform edit form PATCHes only the keys
// the user actually changed, so without this a one-field edit would blank every other
// field on the blob (and an FLS read-only field, which is never in the diff, would be
// wiped on every save). To CLEAR a field, send it explicitly as null/"" — omission
// preserves it, it does not delete it.
//
// It lives in domain rather than usecase because the AI write path needs the same
// rule and package `ai` cannot import `usecase` — usecase imports ai. Two copies of
// this rule is how the AI path drifted into replacing the blob in the first place.
//
// A nil/empty overlay yields base unchanged; malformed base is treated as empty (the
// overlay still wins) rather than failing the whole write.
func MergeJSONObjects(base, overlay JSON) (map[string]interface{}, error) {
	merged := map[string]interface{}{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &merged); err != nil {
			merged = map[string]interface{}{}
		}
	}
	if len(overlay) > 0 {
		var ov map[string]interface{}
		if err := json.Unmarshal(overlay, &ov); err != nil {
			return nil, err
		}
		for k, v := range ov {
			merged[k] = v
		}
	}
	return merged, nil
}

// MergeJSONBlob is MergeJSONObjects re-encoded to a JSON blob, for callers that only
// need to store the result (custom_fields on the system objects).
func MergeJSONBlob(base, overlay JSON) (JSON, error) {
	merged, err := MergeJSONObjects(base, overlay)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return JSON(out), nil
}
