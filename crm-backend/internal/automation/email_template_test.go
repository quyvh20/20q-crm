package automation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// email_template_test.go covers the A5 email-templates library: the repository
// CRUD + case-insensitive unique name, and the send_email template_id path on the
// EmailExecutor (render from a library template, inline override, permanent
// failure on a soft-deleted template).

// ── Pure (no DB) ──────────────────────────────────────────────────────────────

func TestEmailExecutor_TemplateID_NoStore(t *testing.T) {
	// A template_id with no configured template store is a permanent config error.
	exec := &EmailExecutor{apiKey: "k", fromEmail: "noreply@x.com", baseURL: "http://unused", templates: nil}
	action := ActionSpec{ID: "e1", Type: ActionSendEmail, Params: map[string]any{
		"to":          "to@x.com",
		"template_id": uuid.NewString(),
	}}
	_, err := exec.Execute(context.Background(), &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}, action, EvalContext{})
	require.Error(t, err)
	assert.False(t, isRetryable(err), "a missing template store is a permanent failure")
	assert.Contains(t, err.Error(), "no template store")
}

func TestEmailExecutor_TemplateID_InvalidUUID(t *testing.T) {
	// A malformed template_id can never resolve — permanent failure, no DB touched.
	exec := &EmailExecutor{apiKey: "k", fromEmail: "noreply@x.com", baseURL: "http://unused", templates: NewEmailTemplateRepository(nil)}
	action := ActionSpec{ID: "e1", Type: ActionSendEmail, Params: map[string]any{
		"to":          "to@x.com",
		"template_id": "not-a-uuid",
	}}
	_, err := exec.Execute(context.Background(), &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}, action, EvalContext{})
	require.Error(t, err)
	assert.False(t, isRetryable(err), "a malformed template_id is a permanent failure")
	assert.Contains(t, err.Error(), "invalid template_id")
}

// ── DB-backed (Docker-gated) ──────────────────────────────────────────────────

func TestEmailTemplateRepo_CRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewEmailTemplateRepository(db)
	orgID := uuid.New()
	uid := uuid.New()

	tmpl := &EmailTemplate{OrgID: orgID, Name: "Welcome", Subject: "Hi", BodyHTML: "<p>b</p>", ObjectSlug: "contact", CreatedBy: uid, UpdatedBy: uid}
	require.NoError(t, repo.Create(ctx, tmpl))
	require.NotEqual(t, uuid.Nil, tmpl.ID)

	got, err := repo.Get(ctx, orgID, tmpl.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Welcome", got.Name)
	assert.Equal(t, "contact", got.ObjectSlug)

	list, err := repo.List(ctx, orgID)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Cross-org isolation: another org can't see it.
	other, err := repo.Get(ctx, uuid.New(), tmpl.ID)
	require.NoError(t, err)
	assert.Nil(t, other)

	// Update a subset of fields.
	newName := "Welcome v2"
	newSubject := "Hello"
	upd, err := repo.Update(ctx, orgID, tmpl.ID, uuid.New(), &newName, &newSubject, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, upd)
	assert.Equal(t, "Welcome v2", upd.Name)
	assert.Equal(t, "Hello", upd.Subject)
	assert.Equal(t, "<p>b</p>", upd.BodyHTML, "unspecified fields are unchanged")

	// Soft delete → Get returns nil, second delete reports not-found.
	deleted, err := repo.Delete(ctx, orgID, tmpl.ID)
	require.NoError(t, err)
	assert.True(t, deleted)

	after, err := repo.Get(ctx, orgID, tmpl.ID)
	require.NoError(t, err)
	assert.Nil(t, after, "a soft-deleted template reads as absent")

	again, err := repo.Delete(ctx, orgID, tmpl.ID)
	require.NoError(t, err)
	assert.False(t, again, "deleting an absent template affects no rows")
}

func TestEmailTemplateRepo_UniqueNamePerOrg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewEmailTemplateRepository(db)
	orgID := uuid.New()
	uid := uuid.New()

	first := &EmailTemplate{OrgID: orgID, Name: "Promo", Subject: "s", BodyHTML: "b", CreatedBy: uid, UpdatedBy: uid}
	require.NoError(t, repo.Create(ctx, first))

	// Case-insensitive clash within the org is rejected.
	err := repo.Create(ctx, &EmailTemplate{OrgID: orgID, Name: "promo", Subject: "s", BodyHTML: "b", CreatedBy: uid, UpdatedBy: uid})
	assert.ErrorIs(t, err, ErrDuplicateTemplateName)

	// The same name in another org is fine.
	require.NoError(t, repo.Create(ctx, &EmailTemplate{OrgID: uuid.New(), Name: "Promo", Subject: "s", BodyHTML: "b", CreatedBy: uid, UpdatedBy: uid}))

	// Soft-deleting frees the name for reuse (partial index excludes deleted rows).
	_, err = repo.Delete(ctx, orgID, first.ID)
	require.NoError(t, err)
	require.NoError(t, repo.Create(ctx, &EmailTemplate{OrgID: orgID, Name: "Promo", Subject: "s2", BodyHTML: "b2", CreatedBy: uid, UpdatedBy: uid}))
}

// captureResendServer returns an httptest server that records the last payload it
// received and always returns 200, plus a pointer to the captured payload.
func captureResendServer(t *testing.T) (*httptest.Server, *resendEmailPayload) {
	t.Helper()
	captured := &resendEmailPayload{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, captured)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"msg_test"}`))
	}))
	return srv, captured
}

func TestEmailExecutor_TemplateID_RendersTemplate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewEmailTemplateRepository(db)
	orgID := uuid.New()
	tmpl := &EmailTemplate{
		OrgID:     orgID,
		Name:      "Welcome",
		Subject:   "Hi {{contact.first_name}}",
		BodyHTML:  "<p>Hello {{contact.first_name}}</p>",
		CreatedBy: uuid.New(),
		UpdatedBy: uuid.New(),
	}
	require.NoError(t, repo.Create(ctx, tmpl))

	srv, captured := captureResendServer(t)
	defer srv.Close()
	exec := &EmailExecutor{apiKey: "k", fromEmail: "noreply@x.com", baseURL: srv.URL, templates: repo}
	run := &WorkflowRun{ID: uuid.New(), OrgID: orgID}
	evalCtx := EvalContext{Contact: map[string]any{"first_name": "Jane"}}

	// Template supplies subject + body, interpolated against the run context.
	action := ActionSpec{ID: "e1", Type: ActionSendEmail, Params: map[string]any{
		"to":          "to@x.com",
		"template_id": tmpl.ID.String(),
	}}
	_, err := exec.Execute(ctx, run, action, evalCtx)
	require.NoError(t, err)
	assert.Equal(t, "Hi Jane", captured.Subject)
	assert.Equal(t, "<p>Hello Jane</p>", captured.HTML)

	// An inline subject overrides the template; the body still comes from it.
	action2 := ActionSpec{ID: "e2", Type: ActionSendEmail, Params: map[string]any{
		"to":          "to@x.com",
		"template_id": tmpl.ID.String(),
		"subject":     "Override {{contact.first_name}}",
	}}
	_, err = exec.Execute(ctx, run, action2, evalCtx)
	require.NoError(t, err)
	assert.Equal(t, "Override Jane", captured.Subject)
	assert.Equal(t, "<p>Hello Jane</p>", captured.HTML, "body still comes from the template")
}

func TestEmailExecutor_TemplateID_SoftDeletedPermanent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	repo := NewEmailTemplateRepository(db)
	orgID := uuid.New()
	tmpl := &EmailTemplate{OrgID: orgID, Name: "Gone", Subject: "s", BodyHTML: "b", CreatedBy: uuid.New(), UpdatedBy: uuid.New()}
	require.NoError(t, repo.Create(ctx, tmpl))
	_, err := repo.Delete(ctx, orgID, tmpl.ID)
	require.NoError(t, err)

	srv, _ := captureResendServer(t)
	defer srv.Close()
	exec := &EmailExecutor{apiKey: "k", fromEmail: "noreply@x.com", baseURL: srv.URL, templates: repo}
	run := &WorkflowRun{ID: uuid.New(), OrgID: orgID}
	action := ActionSpec{ID: "e1", Type: ActionSendEmail, Params: map[string]any{
		"to":          "to@x.com",
		"template_id": tmpl.ID.String(),
	}}
	_, err = exec.Execute(ctx, run, action, EvalContext{})
	require.Error(t, err)
	assert.False(t, isRetryable(err), "a soft-deleted template is a permanent failure")
	assert.Contains(t, err.Error(), "not found")
}
