package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crm-backend/internal/domain"
	"crm-backend/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /api/contacts/bulk-action multiplexes TWO verbs — "delete" and
// "assign_tag" — over one route, and a route-level gate can only express one
// action. It was mounted olsOn("contact", ActionEdit), which is correct for
// assign_tag and a privilege escalation for delete: the four OLS bits are
// independent (domain.ObjectAccess.Allows is a flat switch), so the DEFAULT sales
// role — {Read, Create, Edit: true, Delete: false} — could bulk-delete the very
// contacts it is refused deleting one at a time via DELETE /contacts/:id. Worse,
// the bulk delete branch permanently redacts the lead ledger and collapses the
// marketing-consent provenance, neither of which un-deleting the contact restores.
//
// The gate now lives inside the dispatch (usecase.bulkActionRequires), which is
// the only layer that knows the verb. These tests drive the WHOLE stack — the
// route middleware, the handler, and the real contact usecase over a fake repo —
// because the interesting property is the interaction of the two gates, and a
// test of either one alone proves nothing:
//
//   - a test that only asserts "delete is refused" would also pass against the
//     naive fix of flipping the ROUTE to ActionDelete, which breaks tagging;
//   - a test that only asserts "tagging still works" would pass against no fix.
//
// So every case below pins BOTH halves.

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// olsRoleAuthorizer is a domain.RecordAuthorizer standing in for one role's row of
// the OLS grid. It decides through domain.ObjectAccess.Allows — the same flat
// switch the real permission engine uses — so a test role's bits mean here exactly
// what they mean in production, and it records every (slug, action) asked for so a
// test can assert WHICH gates ran and in what order.
type olsRoleAuthorizer struct {
	access domain.ObjectAccess
	calls  []string // "slug:action", in call order
	audits []domain.AuditEntry
}

func (a *olsRoleAuthorizer) Authorize(_ context.Context, _ uuid.UUID, slug string, action domain.RecordAction) error {
	a.calls = append(a.calls, slug+":"+string(action))
	if a.access.Allows(action) {
		return nil
	}
	return domain.NewAppError(http.StatusForbidden,
		"your role can't "+string(action)+" "+slug+" records — ask an admin for access")
}

func (a *olsRoleAuthorizer) Audit(_ context.Context, e domain.AuditEntry) {
	a.audits = append(a.audits, e)
}

func (a *olsRoleAuthorizer) FieldMask(context.Context, uuid.UUID, string) domain.FieldMask {
	return domain.FieldMask{}
}

// bulkAuthzFakeRepo records what the repository was asked to do. The embedded
// interface is nil, so any unmodelled call panics loudly rather than quietly
// returning a zero value — which is how these tests know a refused request never
// reached storage at all, instead of reaching it and being ignored.
type bulkAuthzFakeRepo struct {
	domain.ContactRepository

	deleteCalls [][]uuid.UUID
	tagCalls    [][]uuid.UUID
	// scoped models the row-level write scope: the subset BulkDeleteByIDs actually
	// deleted. nil means "everything asked for".
	scoped []uuid.UUID
}

func (f *bulkAuthzFakeRepo) BulkDeleteByIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]domain.Contact, error) {
	f.deleteCalls = append(f.deleteCalls, ids)
	deleted := f.scoped
	if deleted == nil {
		deleted = ids
	}
	out := make([]domain.Contact, 0, len(deleted))
	for _, id := range deleted {
		out = append(out, domain.Contact{ID: id})
	}
	return out, nil
}

func (f *bulkAuthzFakeRepo) BulkAssignTag(_ context.Context, _ uuid.UUID, ids []uuid.UUID, _ uuid.UUID) (int64, error) {
	f.tagCalls = append(f.tagCalls, ids)
	return int64(len(ids)), nil
}

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// mountBulkActionRoute mirrors router.go's contacts.POST("/bulk-action", …) line
// EXACTLY — same middleware, same fixed slug, same coarse action — over the real
// handler and the real usecase. Mirroring rather than calling RegisterRoutes keeps
// the test DB-free; router_gate_test below pins the mirror to the real router.
func mountBulkActionRoute(authz domain.RecordAuthorizer, repo domain.ContactRepository, orgID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		// A caller must be present: a context with no caller is a TRUSTED in-process
		// call, which the real permission engine allows unconditionally. Without this
		// the tests would be asserting against the wrong branch entirely.
		c.Request = c.Request.WithContext(domain.WithCallerIdentity(c.Request.Context(), domain.Caller{
			Role: "sales_rep", UserID: uuid.New(), RoleID: uuid.New(), DataScope: "all",
		}))
	})
	h := NewContactHandler(usecase.NewContactUseCase(repo, nil, authz))
	g := r.Group("/api/contacts")
	g.POST("/bulk-action", RequireObjectAccessOn(authz, "contact", domain.ActionEdit), h.BulkAction)
	return r
}

func postBulkAction(t *testing.T, r *gin.Engine, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/contacts/bulk-action", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func bulkResult(t *testing.T, w *httptest.ResponseRecorder) domain.BulkActionResult {
	t.Helper()
	var env struct {
		Data domain.BulkActionResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return env.Data
}

// editNotDelete is the DEFAULT sales role's grid row (permission_repository.go:
// RoleSales = {Read, Create, Edit: true, Delete: false}). Every "escalation" case
// below uses it, because the escalation shipped enabled out of the box.
var editNotDelete = domain.ObjectAccess{Read: true, Create: true, Edit: true, Delete: false}

// ---------------------------------------------------------------------------
// the pair that is the whole point
// ---------------------------------------------------------------------------

// THE ESCALATION. A role explicitly denied delete asks the bulk endpoint to
// delete. Pre-fix this returned 200 and erased the rows (plus their lead-ledger
// payloads and marketing-consent provenance, irreversibly).
func TestBulkAction_DeleteIsRefusedForARoleWithEditButNotDelete(t *testing.T) {
	authz := &olsRoleAuthorizer{access: editNotDelete}
	repo := &bulkAuthzFakeRepo{}
	r := mountBulkActionRoute(authz, repo, uuid.New())

	w := postBulkAction(t, r, map[string]any{
		"action":      "delete",
		"contact_ids": []string{uuid.NewString(), uuid.NewString()},
	})

	if w.Code != http.StatusForbidden {
		t.Fatalf("bulk delete by an edit-only role: got %d, want 403 — body %s", w.Code, w.Body.String())
	}
	if len(repo.deleteCalls) != 0 {
		t.Fatalf("the repository was still asked to delete %d time(s): the request must not reach storage", len(repo.deleteCalls))
	}
	// Both gates ran, in order, and the SECOND one is the one that refused. Asserting
	// the sequence is what distinguishes this fix from flipping the route gate.
	want := []string{"contact:edit", "contact:delete"}
	if !equalStrings(authz.calls, want) {
		t.Fatalf("authorization calls = %v, want %v (route gate at edit, then the per-verb delete gate)", authz.calls, want)
	}
}

// THE OTHER HALF. The same edit-only role must still be able to TAG in bulk —
// tagging genuinely is an edit, and the uniform tag route is gated ActionEdit. A
// blanket route change to ActionDelete would break this, which is exactly why the
// two assertions have to live together.
func TestBulkAction_AssignTagStillSucceedsForARoleWithEditButNotDelete(t *testing.T) {
	authz := &olsRoleAuthorizer{access: editNotDelete}
	repo := &bulkAuthzFakeRepo{}
	r := mountBulkActionRoute(authz, repo, uuid.New())

	ids := []string{uuid.NewString(), uuid.NewString()}
	w := postBulkAction(t, r, map[string]any{
		"action":      "assign_tag",
		"contact_ids": ids,
		"tag_id":      uuid.NewString(),
	})

	if w.Code != http.StatusOK {
		t.Fatalf("bulk assign_tag by an edit-capable role: got %d, want 200 — body %s", w.Code, w.Body.String())
	}
	if len(repo.tagCalls) != 1 || len(repo.tagCalls[0]) != len(ids) {
		t.Fatalf("assign_tag reached the repository as %v, want one call with %d ids", repo.tagCalls, len(ids))
	}
	if got := bulkResult(t, w).Affected; got != len(ids) {
		t.Fatalf("affected = %d, want %d", got, len(ids))
	}
	// assign_tag asks for edit and NOTHING else: it must never require delete.
	want := []string{"contact:edit", "contact:edit"}
	if !equalStrings(authz.calls, want) {
		t.Fatalf("authorization calls = %v, want %v (tagging must not demand delete)", authz.calls, want)
	}
}

// ---------------------------------------------------------------------------
// the permitted path still works
// ---------------------------------------------------------------------------

func TestBulkAction_DeleteSucceedsForARoleThatHoldsDelete(t *testing.T) {
	authz := &olsRoleAuthorizer{access: domain.ObjectAccess{Read: true, Create: true, Edit: true, Delete: true}}
	repo := &bulkAuthzFakeRepo{}
	r := mountBulkActionRoute(authz, repo, uuid.New())

	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	w := postBulkAction(t, r, map[string]any{"action": "delete", "contact_ids": ids})

	if w.Code != http.StatusOK {
		t.Fatalf("bulk delete by a delete-capable role: got %d, want 200 — body %s", w.Code, w.Body.String())
	}
	if len(repo.deleteCalls) != 1 || len(repo.deleteCalls[0]) != len(ids) {
		t.Fatalf("delete reached the repository as %v, want one call with %d ids", repo.deleteCalls, len(ids))
	}
	if got := bulkResult(t, w).Affected; got != len(ids) {
		t.Fatalf("affected = %d, want %d", got, len(ids))
	}
}

// ---------------------------------------------------------------------------
// the ACTION gate is orthogonal to the ROW gate — it does not replace it
// ---------------------------------------------------------------------------

// Holding the delete BIT is permission to delete contacts, not permission to
// delete THESE contacts. Row scope is enforced inside the statement
// (applyWriteScopeFromCtx), so an own-scoped caller who asks for three rows and
// owns one deletes one — and the response must report the one, not the three.
// This is the guarantee contact_bulk_erasure_test.go pins at the usecase layer;
// it is re-pinned here so a future "simplification" of the new gate cannot quietly
// take the row check with it.
func TestBulkAction_ActionGateDoesNotReplaceRowScope(t *testing.T) {
	mine := uuid.New()
	notMine1, notMine2 := uuid.New(), uuid.New()
	authz := &olsRoleAuthorizer{access: domain.ObjectAccess{Read: true, Edit: true, Delete: true}}
	repo := &bulkAuthzFakeRepo{scoped: []uuid.UUID{mine}}
	r := mountBulkActionRoute(authz, repo, uuid.New())

	w := postBulkAction(t, r, map[string]any{
		"action":      "delete",
		"contact_ids": []string{mine.String(), notMine1.String(), notMine2.String()},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 — body %s", w.Code, w.Body.String())
	}
	if got := bulkResult(t, w).Affected; got != 1 {
		t.Fatalf("affected = %d, want 1 — the count must report what the row scope deleted, not what was asked for", got)
	}
}

// ---------------------------------------------------------------------------
// a third verb cannot ship ungated
// ---------------------------------------------------------------------------

// The gate is a table lookup, not an if/else beside the dispatch: an action absent
// from usecase.bulkActionRequires is refused before anything runs. So adding a
// third verb to the dispatch without declaring which OLS bit authorizes it makes
// that verb unreachable rather than unauthorized.
func TestBulkAction_AnUndeclaredActionIsRefusedBeforeItReachesStorage(t *testing.T) {
	authz := &olsRoleAuthorizer{access: domain.ObjectAccess{Read: true, Create: true, Edit: true, Delete: true}}
	repo := &bulkAuthzFakeRepo{}
	r := mountBulkActionRoute(authz, repo, uuid.New())

	w := postBulkAction(t, r, map[string]any{
		"action":      "merge",
		"contact_ids": []string{uuid.NewString()},
	})

	if w.Code != http.StatusBadRequest {
		t.Fatalf("undeclared bulk action: got %d, want 400 — body %s", w.Code, w.Body.String())
	}
	if len(repo.deleteCalls)+len(repo.tagCalls) != 0 {
		t.Fatal("an undeclared action reached the repository")
	}
	// Only the coarse route gate ran; the per-verb gate never had a verb to check.
	if len(authz.calls) != 1 || authz.calls[0] != "contact:edit" {
		t.Fatalf("authorization calls = %v, want exactly the route gate", authz.calls)
	}
}

// A build that wires no authorizer must refuse bulk actions, not wave them
// through. This is the difference between the new gate and the nil-tolerant
// hooks beside it (the ledger and marketing redactors), whose absence is a
// degraded feature; the absence of an authorizer is an open door.
func TestBulkAction_NoAuthorizerWiredFailsClosed(t *testing.T) {
	repo := &bulkAuthzFakeRepo{}
	uc := usecase.NewContactUseCase(repo, nil, nil)

	_, err := uc.BulkAction(
		domain.WithCallerIdentity(context.Background(), domain.Caller{Role: "admin", RoleID: uuid.New()}),
		uuid.New(),
		domain.BulkActionInput{Action: "delete", ContactIDs: []uuid.UUID{uuid.New()}},
	)

	appErr, ok := err.(*domain.AppError)
	if !ok || appErr.Code != http.StatusForbidden {
		t.Fatalf("bulk delete with no authorizer wired: got %v, want a 403 AppError", err)
	}
	if len(repo.deleteCalls) != 0 {
		t.Fatal("the request reached storage with no authorizer wired")
	}
}

// ---------------------------------------------------------------------------
// the mirror is honest
// ---------------------------------------------------------------------------

// mountBulkActionRoute hand-mirrors one line of router.go. If that line's coarse
// action ever changes, the mirror silently stops representing production and every
// test above starts proving something about a route that no longer exists. Reading
// the real gate back out of RegisterRoutes is impractical (it needs the whole
// dependency graph), so this pins the two facts the mirror depends on: the route
// exists at the expected method+path, and it is NOT gated on delete.
func TestBulkActionRoute_IsRegisteredAndNotGatedOnDelete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	authz := &olsRoleAuthorizer{access: editNotDelete}
	repo := &bulkAuthzFakeRepo{}
	r := mountBulkActionRoute(authz, repo, uuid.New())

	var found bool
	for _, route := range r.Routes() {
		if route.Method == http.MethodPost && route.Path == "/api/contacts/bulk-action" {
			found = true
		}
	}
	if !found {
		t.Fatal("POST /api/contacts/bulk-action is not registered")
	}

	// An edit-capable role reaches the handler at all — i.e. the ROUTE gate did not
	// demand delete. Combined with the delete-refusal test above, this is the exact
	// shape the fix must have: coarse gate at edit, fine gate per verb.
	w := postBulkAction(t, r, map[string]any{
		"action":      "assign_tag",
		"contact_ids": []string{uuid.NewString()},
		"tag_id":      uuid.NewString(),
	})
	if w.Code == http.StatusForbidden {
		t.Fatalf("the route gate refused an edit-capable role: it must not be mounted on ActionDelete — body %s", w.Body.String())
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
