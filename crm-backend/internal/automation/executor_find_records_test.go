package automation

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeLister captures the last List call and returns canned records.
type fakeLister struct {
	lastSlug string
	lastIn   domain.RecordListInput
	recs     []domain.UniformRecord
	err      error
}

func (f *fakeLister) List(_ context.Context, _ uuid.UUID, slug string, in domain.RecordListInput) (*domain.RecordList, error) {
	f.lastSlug, f.lastIn = slug, in
	if f.err != nil {
		return nil, f.err
	}
	return &domain.RecordList{Records: f.recs}, nil
}

func TestFindRecords_OutputsCountAndRecords_WithInterpolatedFilter(t *testing.T) {
	owner := uuid.New()
	f := &fakeLister{recs: []domain.UniformRecord{
		{ID: uuid.New(), Object: "contact", Fields: map[string]interface{}{"email": "a@x.com"}},
		{ID: uuid.New(), Object: "contact", Fields: map[string]interface{}{"email": "b@x.com"}},
	}}
	ex := NewFindRecordsExecutor(f)
	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}

	action := ActionSpec{ID: "f1", Type: ActionFindRecords, Params: map[string]any{
		"object": "contact",
		"limit":  50,
		"filters": []any{
			map[string]any{"field": "contact.owner_user_id", "value": "{{deal.owner_user_id}}"},
			map[string]any{"field": "", "value": "ignored"}, // blank row dropped
		},
	}}
	evalCtx := EvalContext{Deal: map[string]any{"owner_user_id": owner.String()}}

	out, err := ex.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	m := out.(map[string]any)
	require.Equal(t, 2, m["count"])
	require.Len(t, m["records"], 2)
	require.Len(t, m["record_ids"], 2)
	// Filter key prefix stripped + value interpolated; limit honored (<100).
	require.Equal(t, "contact", f.lastSlug)
	require.Equal(t, owner.String(), f.lastIn.Filters["owner_user_id"])
	require.NotContains(t, f.lastIn.Filters, "contact.owner_user_id")
	require.Equal(t, 50, f.lastIn.Limit)
}

func TestFindRecords_ClampsLimitToMax(t *testing.T) {
	f := &fakeLister{}
	ex := NewFindRecordsExecutor(f)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "f1", Type: ActionFindRecords, Params: map[string]any{"object": "contact", "limit": 5000}},
		EvalContext{})
	require.NoError(t, err)
	require.Equal(t, maxFindRecords, f.lastIn.Limit, "an over-limit request is clamped")
}

func TestFindRecords_MissingObject(t *testing.T) {
	ex := NewFindRecordsExecutor(&fakeLister{})
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "f1", Type: ActionFindRecords, Params: map[string]any{}}, EvalContext{})
	require.Error(t, err)
}

func TestFindRecords_ListAppErrorIsPermanent(t *testing.T) {
	f := &fakeLister{err: domain.NewAppError(403, "denied")}
	ex := NewFindRecordsExecutor(f)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "f1", Type: ActionFindRecords, Params: map[string]any{"object": "contact"}}, EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
}
