package automation

import (
	"context"
	"errors"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeRecordCreator captures the last Create call and can be primed to fail.
type fakeRecordCreator struct {
	lastOrg    uuid.UUID
	lastUser   uuid.UUID
	lastSlug   string
	lastFields map[string]interface{}
	err        error
}

func (f *fakeRecordCreator) Create(_ context.Context, orgID, userID uuid.UUID, slug string, in domain.RecordWriteInput) (*domain.UniformRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.lastOrg, f.lastUser, f.lastSlug, f.lastFields = orgID, userID, slug, in.Fields
	return &domain.UniformRecord{ID: uuid.New(), Object: slug}, nil
}

func TestCreateRecord_StripsPrefixInterpolatesAndPassesCaller(t *testing.T) {
	f := &fakeRecordCreator{}
	ex := NewCreateRecordExecutor(f)
	author := uuid.New()
	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: author})

	action := ActionSpec{ID: "c1", Type: ActionCreateRecord, Params: map[string]any{
		"object": "company",
		"fields": []any{
			map[string]any{"field": "company.name", "value": "{{deal.title}} Inc"},
			map[string]any{"field": "company.website", "value": "https://acme.test"},
		},
	}}
	evalCtx := EvalContext{Deal: map[string]any{"title": "Acme"}}

	out, err := ex.Execute(ctx, run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, run.OrgID, f.lastOrg)
	require.Equal(t, author, f.lastUser, "create runs as the workflow author")
	require.Equal(t, "company", f.lastSlug)
	// Object prefix stripped → bare field keys; template interpolated.
	require.Equal(t, "Acme Inc", f.lastFields["name"])
	require.Equal(t, "https://acme.test", f.lastFields["website"])
	require.NotContains(t, f.lastFields, "company.name")
}

func TestCreateRecord_MissingObject(t *testing.T) {
	ex := NewCreateRecordExecutor(&fakeRecordCreator{})
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "c1", Type: ActionCreateRecord, Params: map[string]any{
			"fields": []any{map[string]any{"field": "company.name", "value": "x"}},
		}}, EvalContext{})
	require.Error(t, err)
}

func TestCreateRecord_NoFields(t *testing.T) {
	ex := NewCreateRecordExecutor(&fakeRecordCreator{})
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "c1", Type: ActionCreateRecord, Params: map[string]any{"object": "company", "fields": []any{}}},
		EvalContext{})
	require.Error(t, err)
}

func TestCreateRecord_AppErrorIsPermanent(t *testing.T) {
	// An OLS/FLS/validation failure surfaces as *domain.AppError → must not retry.
	f := &fakeRecordCreator{err: domain.NewAppError(403, "insufficient permissions")}
	ex := NewCreateRecordExecutor(f)
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: uuid.New()})
	_, err := ex.Execute(ctx, &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()},
		ActionSpec{ID: "c1", Type: ActionCreateRecord, Params: map[string]any{
			"object": "company", "fields": []any{map[string]any{"field": "company.name", "value": "x"}},
		}}, EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err), "authz/validation failure must be permanent")
}

func TestCreateRecord_RawErrorIsRetryable(t *testing.T) {
	f := &fakeRecordCreator{err: errors.New("connection reset")}
	ex := NewCreateRecordExecutor(f)
	ctx := domain.WithCallerIdentity(context.Background(), domain.Caller{UserID: uuid.New()})
	_, err := ex.Execute(ctx, &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()},
		ActionSpec{ID: "c1", Type: ActionCreateRecord, Params: map[string]any{
			"object": "company", "fields": []any{map[string]any{"field": "company.name", "value": "x"}},
		}}, EvalContext{})
	require.Error(t, err)
	require.True(t, isRetryable(err), "a transient DB error should be retryable")
}

func TestCreateRecord_NoCreatorConfigured(t *testing.T) {
	ex := NewCreateRecordExecutor(nil)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "c1", Type: ActionCreateRecord, Params: map[string]any{"object": "company"}}, EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
}
