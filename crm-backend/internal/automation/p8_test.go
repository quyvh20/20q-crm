package automation

import (
	"context"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p8_test.go covers the run-as-creator enforcement wiring that does NOT need a
// database: the OLS/FLS gate the record-writing executors apply before mutating,
// the audit attribution, the FLS key mapping, and the fail-closed restricted
// caller. The full DB-backed enforcement (own-scope SQL, real PermissionUseCase
// OLS) is exercised by the Docker integration suite.

// fakeAuthz is a scriptable domain.RecordAuthorizer for unit tests.
type fakeAuthz struct {
	authorizeErr error
	mask         domain.FieldMask
	audits       []domain.AuditEntry
}

func (f *fakeAuthz) Authorize(context.Context, uuid.UUID, string, domain.RecordAction) error {
	return f.authorizeErr
}
func (f *fakeAuthz) FieldMask(context.Context, uuid.UUID, string) domain.FieldMask { return f.mask }
func (f *fakeAuthz) Audit(_ context.Context, e domain.AuditEntry)                  { f.audits = append(f.audits, e) }

func p8Run() *WorkflowRun {
	return &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: uuid.New()}
}

func p8Ctx(caller domain.Caller) context.Context {
	return domain.WithCallerIdentity(context.Background(), caller)
}

// --- flsFieldKey mapping ---

func TestFLSFieldKey(t *testing.T) {
	cases := []struct{ field, slug, want string }{
		{"contact.first_name", "contact", "first_name"},
		{"first_name", "contact", "first_name"},
		{"custom_fields.score", "contact", "score"},
		{"contact.custom_fields.score", "contact", "score"},
		{"ticket.status", "ticket", "status"},
		{"deal.value", "deal", "value"},
		{"tags", "contact", "tags"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, flsFieldKey(c.field, c.slug), "flsFieldKey(%q,%q)", c.field, c.slug)
	}
}

// --- update_record OLS/FLS gate ---

func TestUpdateRecord_AuthorizeWrite_OLSDenyPropagates(t *testing.T) {
	az := &fakeAuthz{authorizeErr: domain.NewAppError(403, "no edit")}
	e := NewUpdateRecordExecutor(nil, az) // nil db: the gate runs before any DB access
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New()})
	err := e.authorizeWrite(ctx, p8Run(), "contact",
		[]FieldUpdate{{Field: "first_name", Op: "set", Value: "x"}}, EvalContext{})
	require.Error(t, err, "an OLS denial from the authorizer must fail the write")
}

func TestUpdateRecord_AuthorizeWrite_FLSDenyPropagates(t *testing.T) {
	az := &fakeAuthz{mask: domain.FieldMask{Hidden: map[string]bool{"salary": true}}}
	e := NewUpdateRecordExecutor(nil, az)
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New()})
	err := e.authorizeWrite(ctx, p8Run(), "deal",
		[]FieldUpdate{{Field: "salary", Op: "set", Value: 1}}, EvalContext{})
	require.Error(t, err, "a write to an FLS-hidden field must be rejected")
}

func TestUpdateRecord_AuthorizeWrite_AllowsPermittedWrite(t *testing.T) {
	az := &fakeAuthz{} // no OLS error, empty mask
	e := NewUpdateRecordExecutor(nil, az)
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New()})
	err := e.authorizeWrite(ctx, p8Run(), "contact",
		[]FieldUpdate{{Field: "first_name", Op: "set", Value: "x"}}, EvalContext{})
	require.NoError(t, err)
}

func TestUpdateRecord_NilAuthz_NoEnforcement(t *testing.T) {
	e := NewUpdateRecordExecutor(nil, nil) // unit-test wiring: enforcement disabled
	err := e.authorizeWrite(context.Background(), p8Run(), "contact",
		[]FieldUpdate{{Field: "first_name", Op: "set", Value: "x"}}, EvalContext{})
	require.NoError(t, err, "a nil authorizer must be a no-op (pre-P8 trusted behavior)")
}

// --- audit attribution ---

func TestUpdateRecord_Audit_AttributesToAuthor(t *testing.T) {
	az := &fakeAuthz{}
	e := NewUpdateRecordExecutor(nil, az)
	actor := uuid.New()
	run := p8Run()
	recID := uuid.New()
	e.audit(p8Ctx(domain.Caller{UserID: actor}), run, "contact", recID,
		[]map[string]any{{"field": "first_name", "op": "set", "value": "x"}})
	require.Len(t, az.audits, 1, "a successful automation write must emit exactly one audit row")
	got := az.audits[0]
	assert.Equal(t, actor, got.ActorID, "the audit actor is the workflow author")
	assert.Equal(t, run.OrgID, got.OrgID)
	assert.Equal(t, "contact", got.ObjectSlug)
	assert.Equal(t, recID, got.RecordID)
	assert.Equal(t, domain.ActionEdit, got.Action)
	assert.Contains(t, got.Changes, "first_name")
}

// --- assign_user gate ---

func TestAssign_Authorize_OLSDenyPropagates(t *testing.T) {
	az := &fakeAuthz{authorizeErr: domain.NewAppError(403, "no edit")}
	e := NewAssignUserExecutor(nil, az)
	// DataScope "all" so the own-scope branch (which needs a DB) is never reached.
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	err := e.authorize(ctx, p8Run(), "deal", uuid.New())
	require.Error(t, err)
}

func TestAssign_Authorize_FLSDenyOnOwnerField(t *testing.T) {
	az := &fakeAuthz{mask: domain.FieldMask{Hidden: map[string]bool{"owner_user_id": true}}}
	e := NewAssignUserExecutor(nil, az)
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	err := e.authorize(ctx, p8Run(), "contact", uuid.New())
	require.Error(t, err, "a role that can't write owner_user_id may not reassign ownership")
}

func TestAssign_Authorize_AllowsAllScopeWriter(t *testing.T) {
	az := &fakeAuthz{}
	e := NewAssignUserExecutor(nil, az)
	ctx := p8Ctx(domain.Caller{UserID: uuid.New(), RoleID: uuid.New(), DataScope: domain.DataScopeAll})
	require.NoError(t, e.authorize(ctx, p8Run(), "deal", uuid.New()))
}

// --- fail-closed restricted caller ---

func TestRestrictedCaller_FailsClosed(t *testing.T) {
	c := restrictedCaller(uuid.New())
	// No role identity ⇒ PermissionUseCase.resolveRoleID fails ⇒ OLS default-denies.
	assert.Equal(t, uuid.Nil, c.RoleID)
	assert.Empty(t, c.Role)
	assert.False(t, c.IsOwner)
}
