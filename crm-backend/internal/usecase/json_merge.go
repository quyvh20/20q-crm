package usecase

import (
	"crm-backend/internal/domain"
)

// The blob-merge rule itself now lives in domain.MergeJSONObjects — the AI contact
// write path needs the identical rule and package `ai` cannot import `usecase`
// (usecase imports ai). These stay as unexported aliases so the usecase call sites
// and their tests read unchanged; there is still exactly one implementation.

func mergeJSONObjects(base, overlay domain.JSON) (map[string]interface{}, error) {
	return domain.MergeJSONObjects(base, overlay)
}

func mergeJSONBlob(base, overlay domain.JSON) (domain.JSON, error) {
	return domain.MergeJSONBlob(base, overlay)
}
