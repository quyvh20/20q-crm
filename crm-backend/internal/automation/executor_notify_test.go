package automation

import (
	"context"
	"errors"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeNotifier captures the last NotificationCreateInput and can be primed to fail.
type fakeNotifier struct {
	last domain.NotificationCreateInput
	err  error
	n    int
}

func (f *fakeNotifier) Create(_ context.Context, in domain.NotificationCreateInput) (*domain.Notification, error) {
	f.n++
	if f.err != nil {
		return nil, f.err
	}
	f.last = in
	return &domain.Notification{ID: uuid.New(), OrgID: in.OrgID, UserID: in.UserID}, nil
}

func newNotifyRun() *WorkflowRun { return &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()} }

func TestNotifyUser_SpecificRecipient(t *testing.T) {
	f := &fakeNotifier{}
	ex := NewNotifyUserExecutor(nil, f) // nil db → per-run cap skipped
	uid := uuid.New()
	run := newNotifyRun()

	action := ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{
		"recipient": "specific",
		"user_id":   uid.String(),
		"title":     "Hi {{contact.first_name}}",
		"body":      "Deal {{deal.title}} needs you",
	}}
	evalCtx := EvalContext{
		Contact: map[string]any{"first_name": "Jane"},
		Deal:    map[string]any{"id": uuid.New().String(), "title": "Acme"},
	}

	out, err := ex.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, uid, f.last.UserID)
	require.Equal(t, run.OrgID, f.last.OrgID)
	require.Equal(t, "Hi Jane", f.last.Title)
	require.Equal(t, "Deal Acme needs you", f.last.Body)
	// Deal present → anchor + default link derived from it.
	require.Equal(t, "deal", f.last.EntityType)
	require.NotNil(t, f.last.EntityID)
	require.Contains(t, f.last.Link, "/deals/")
}

func TestNotifyUser_OwnerField_ResolvesDealOwner(t *testing.T) {
	f := &fakeNotifier{}
	ex := NewNotifyUserExecutor(nil, f)
	owner := uuid.New()
	run := newNotifyRun()

	action := ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{
		"recipient":   "owner_field",
		"owner_field": "deal.owner_user_id",
		"title":       "Your deal moved",
	}}
	evalCtx := EvalContext{Deal: map[string]any{"id": uuid.New().String(), "owner_user_id": owner.String()}}

	_, err := ex.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.Equal(t, owner, f.last.UserID)
}

func TestNotifyUser_OwnerField_FallsBackToContactOwner(t *testing.T) {
	f := &fakeNotifier{}
	ex := NewNotifyUserExecutor(nil, f)
	owner := uuid.New()
	run := newNotifyRun()

	// owner_field path resolves empty (no deal) → fall back to contact owner.
	action := ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{
		"recipient":   "owner_field",
		"owner_field": "deal.owner_user_id",
		"title":       "Ping",
	}}
	evalCtx := EvalContext{Contact: map[string]any{"id": uuid.New().String(), "owner_user_id": owner.String()}}

	_, err := ex.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.Equal(t, owner, f.last.UserID)
	require.Equal(t, "contact", f.last.EntityType)
	require.Contains(t, f.last.Link, "/contacts/")
}

func TestNotifyUser_MissingTitle(t *testing.T) {
	ex := NewNotifyUserExecutor(nil, &fakeNotifier{})
	_, err := ex.Execute(context.Background(), newNotifyRun(),
		ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{"recipient": "specific", "user_id": uuid.New().String()}},
		EvalContext{})
	require.Error(t, err)
}

func TestNotifyUser_SpecificMissingUserID(t *testing.T) {
	ex := NewNotifyUserExecutor(nil, &fakeNotifier{})
	_, err := ex.Execute(context.Background(), newNotifyRun(),
		ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{"recipient": "specific", "title": "x"}},
		EvalContext{})
	require.Error(t, err)
}

func TestNotifyUser_UnresolvableOwner(t *testing.T) {
	ex := NewNotifyUserExecutor(nil, &fakeNotifier{})
	// owner_field mode, but nothing in context resolves to an owner.
	_, err := ex.Execute(context.Background(), newNotifyRun(),
		ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{"recipient": "owner_field", "title": "x"}},
		EvalContext{})
	require.Error(t, err)
}

func TestNotifyUser_CreateErrorIsRetryable(t *testing.T) {
	f := &fakeNotifier{err: errors.New("db down")}
	ex := NewNotifyUserExecutor(nil, f)
	_, err := ex.Execute(context.Background(), newNotifyRun(),
		ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{"recipient": "specific", "user_id": uuid.New().String(), "title": "x"}},
		EvalContext{})
	require.Error(t, err)
	require.True(t, isRetryable(err), "a create failure should be retryable")
}

func TestNotifyUser_NoNotifierConfigured(t *testing.T) {
	ex := NewNotifyUserExecutor(nil, nil)
	_, err := ex.Execute(context.Background(), newNotifyRun(),
		ActionSpec{ID: "n1", Type: ActionNotifyUser, Params: map[string]any{"title": "x"}},
		EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err), "a missing notifier is a permanent misconfiguration")
}
