package automation

import (
	"context"
	"errors"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

type enrolledCall struct {
	tc  map[string]any
	key string
}

// fakeEnroller records EnrollRun calls and can be primed to fail loads/enrolls.
type fakeEnroller struct {
	wf        *Workflow
	loadErr   error
	enrollErr error
	calls     []enrolledCall
}

func (f *fakeEnroller) LoadWorkflow(_ context.Context, _, _ uuid.UUID) (*Workflow, error) {
	return f.wf, f.loadErr
}
func (f *fakeEnroller) EnrollRun(_ context.Context, _ uuid.UUID, _ *Workflow, tc map[string]any, key string) (bool, error) {
	if f.enrollErr != nil {
		return false, f.enrollErr
	}
	f.calls = append(f.calls, enrolledCall{tc: tc, key: key})
	return true, nil
}

func activeTarget() *Workflow {
	return &Workflow{ID: uuid.New(), OrgID: uuid.New(), Version: 1, IsActive: true,
		Trigger: datatypes.JSON([]byte(`{"type":"contact_created"}`))}
}

func enrollAction(targetID uuid.UUID) ActionSpec {
	return ActionSpec{ID: "e1", Type: ActionEnrollRecords, Params: map[string]any{
		"workflow_id": targetID.String(),
		"object":      "contact",
	}}
}

func TestEnrollRecords_EnrollsEachMatchWithDepthAndContext(t *testing.T) {
	target := activeTarget()
	enroller := &fakeEnroller{wf: target}
	c1, c2 := uuid.New(), uuid.New()
	lister := &fakeLister{recs: []domain.UniformRecord{
		{ID: c1, Object: "contact", Fields: map[string]interface{}{"email": "a@x.com"}},
		{ID: c2, Object: "contact", Fields: map[string]interface{}{"email": "b@x.com"}},
	}}
	ex := NewEnrollRecordsExecutor(enroller, lister)
	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()} // depth 0

	out, err := ex.Execute(context.Background(), run, enrollAction(target.ID), EvalContext{})
	require.NoError(t, err)
	m := out.(map[string]any)
	require.Equal(t, 2, m["matched"])
	require.Equal(t, 2, m["enrolled"])
	require.Len(t, enroller.calls, 2)

	// Each enrolled run carries the record under "contact", entity_id, and depth 1.
	call := enroller.calls[0]
	require.Equal(t, 1, call.tc["_enroll_depth"])
	require.Contains(t, call.tc, "contact")
	require.Equal(t, c1.String(), call.tc["entity_id"])
	trig := call.tc["trigger"].(map[string]any)
	require.Equal(t, "contact_created", trig["type"])
	// Idempotency keys are distinct per record.
	require.NotEqual(t, enroller.calls[0].key, enroller.calls[1].key)
}

func TestEnrollRecords_DepthLimitStopsRunaway(t *testing.T) {
	enroller := &fakeEnroller{wf: activeTarget()}
	ex := NewEnrollRecordsExecutor(enroller, &fakeLister{})
	// Source run already at the depth limit.
	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New(),
		TriggerContext: datatypes.JSON([]byte(`{"_enroll_depth":2}`))}

	_, err := ex.Execute(context.Background(), run, enrollAction(uuid.New()), EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
	require.Empty(t, enroller.calls, "no enrollment past the depth limit")
}

func TestEnrollRecords_TargetNotFound(t *testing.T) {
	enroller := &fakeEnroller{wf: nil} // LoadWorkflow returns (nil, nil)
	ex := NewEnrollRecordsExecutor(enroller, &fakeLister{})
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()},
		enrollAction(uuid.New()), EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
}

func TestEnrollRecords_TargetInactive(t *testing.T) {
	target := activeTarget()
	target.IsActive = false
	ex := NewEnrollRecordsExecutor(&fakeEnroller{wf: target}, &fakeLister{})
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()},
		enrollAction(target.ID), EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
}

func TestEnrollRecords_InvalidWorkflowID(t *testing.T) {
	ex := NewEnrollRecordsExecutor(&fakeEnroller{wf: activeTarget()}, &fakeLister{})
	action := ActionSpec{ID: "e1", Type: ActionEnrollRecords, Params: map[string]any{
		"workflow_id": "not-a-uuid", "object": "contact",
	}}
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}, action, EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
}

func TestEnrollRecords_EnrollRunErrorIsRetryable(t *testing.T) {
	target := activeTarget()
	enroller := &fakeEnroller{wf: target, enrollErr: errors.New("db down")}
	lister := &fakeLister{recs: []domain.UniformRecord{{ID: uuid.New(), Object: "contact"}}}
	ex := NewEnrollRecordsExecutor(enroller, lister)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()},
		enrollAction(target.ID), EvalContext{})
	require.Error(t, err)
	require.True(t, isRetryable(err), "a transient enroll failure retries (idempotent)")
}

func TestEnrollDepthOf_DefaultsZero(t *testing.T) {
	require.Equal(t, 0, enrollDepthOf(&WorkflowRun{}))
	require.Equal(t, 0, enrollDepthOf(&WorkflowRun{TriggerContext: datatypes.JSON([]byte(`{}`))}))
	require.Equal(t, 3, enrollDepthOf(&WorkflowRun{TriggerContext: datatypes.JSON([]byte(`{"_enroll_depth":3}`))}))
}
