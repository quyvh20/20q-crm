package automation

import (
	"context"
	"fmt"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// maxFindRecords caps how many records find_records / enroll_records pull in one
// action. RecordService.List itself caps at 100; we mirror it so the action output
// (and any downstream enrollment) stays bounded.
const maxFindRecords = 100

// RecordLister is the narrow read port the find_records / enroll_records executors
// use — satisfied by the platform RecordService. Listing through RecordService
// applies OLS + FLS as the workflow author (P8), so a find only ever returns
// records the author may read.
type RecordLister interface {
	List(ctx context.Context, orgID uuid.UUID, slug string, in domain.RecordListInput) (*domain.RecordList, error)
}

// FindRecordsExecutor queries records of an object into the action output so
// downstream steps can branch on the count or reference the matches.
type FindRecordsExecutor struct {
	lister RecordLister
}

func NewFindRecordsExecutor(lister RecordLister) *FindRecordsExecutor {
	return &FindRecordsExecutor{lister: lister}
}

func (e *FindRecordsExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	if e.lister == nil {
		return nil, fmt.Errorf("find_records: record service is not configured")
	}
	object := getStringParam(action.Params, "object", evalCtx)
	if object == "" {
		return nil, fmt.Errorf("find_records: object is required")
	}

	list, err := listRecords(ctx, e.lister, run.OrgID, object, action.Params, evalCtx)
	if err != nil {
		return nil, err
	}

	records := make([]map[string]any, 0, len(list.Records))
	ids := make([]string, 0, len(list.Records))
	for i := range list.Records {
		rec := list.Records[i]
		m := make(map[string]any, len(rec.Fields)+1)
		for k, v := range rec.Fields {
			m[k] = v
		}
		m["id"] = rec.ID.String()
		records = append(records, m)
		ids = append(ids, rec.ID.String())
	}

	// Output shape: {{actions.<id>.count}} drives conditions; records/record_ids are
	// available to downstream steps.
	return map[string]any{
		"object":     object,
		"count":      len(records),
		"records":    records,
		"record_ids": ids,
	}, nil
}

// listRecords is the shared query path for find_records + enroll_records: it builds
// the registry filter map from the action's filter rows and runs RecordService.List
// bounded to maxFindRecords. Errors are classified (authz/validation permanent,
// transient retryable) by the caller via classifyRecordServiceError.
func listRecords(ctx context.Context, lister RecordLister, orgID uuid.UUID, object string, params map[string]any, evalCtx EvalContext) (*domain.RecordList, error) {
	in := domain.RecordListInput{
		Filters: buildFilterMap(object, params, evalCtx),
		Q:       getStringParam(params, "q", evalCtx),
		Limit:   maxFindRecords,
	}
	if n := getIntParam(params, "limit"); n > 0 && n < maxFindRecords {
		in.Limit = n
	}
	list, err := lister.List(ctx, orgID, object, in)
	if err != nil {
		return nil, classifyRecordServiceError(err, err)
	}
	return list, nil
}

// buildFilterMap turns the builder's filter rows ([{field: "<object>.<key>",
// value}]) into RecordService's relation-filter map (bare key → interpolated
// value). Mirrors buildCreateFields: the object prefix is stripped and string
// values are interpolated. Empty keys/values are dropped so a blank row is ignored.
func buildFilterMap(object string, params map[string]any, evalCtx EvalContext) map[string]string {
	raw, ok := params["filters"]
	if !ok {
		return nil
	}
	entries, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(entries))
	prefix := object + "."
	for _, item := range entries {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		field := strings.TrimSpace(getStringVal(m["field"]))
		if field == "" {
			continue
		}
		key := strings.TrimPrefix(field, prefix)
		val := strings.TrimSpace(InterpolateTemplate(getStringVal(m["value"]), evalCtx))
		if val == "" {
			continue
		}
		out[key] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// getStringVal coerces a JSON param value to a string ("" for nil/non-strings we
// don't stringify further — filters are always string keys/ids).
func getStringVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
