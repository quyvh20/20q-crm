package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// handlers_update_workflow_test.go pins what PUT /api/workflows/:id does with the steps
// requirement R5 deploy 1 introduced — specifically the case the requirement was not
// written for: a PATCH-shaped body that changes only the name.
//
// UpdateWorkflow merges the request over the STORED workflow and then re-validates the
// merged result, so the steps check applies to a body that never mentioned steps. On a
// healthy workflow that is invisible (the stored tree validates). On a workflow whose
// steps are NULL or '[]' it means a rename returns 400 "a workflow needs at least one
// step" — and such rows can exist while the teardown gate reads CLEAR, because CLEAR
// only means "no row would LOSE behaviour if the column were dropped": a row with no
// steps AND an empty `actions` is counted as inert, not as blocking.
//
// THE BEHAVIOUR IS KEPT, NOT CHANGED, and this test is the record of that decision:
//
//   - relaxing it means adding a second, weaker validation path whose only user is a
//     rename of a workflow that is already broken — a rename cannot repair it, and the
//     repair (writing a steps tree) is accepted by the strict path already;
//   - the builder always PUTs the whole workflow including steps, so nothing in the
//     product hits this. An API client renaming a steps-less workflow does, once, with
//     an error that names the real problem with the row;
//   - and the strictness is load-bearing in the other direction: it is what stops a PUT
//     from blanking a live workflow's steps, which would leave it enabled and executing
//     nothing.
//
// If that trade is ever revisited, revisit it HERE — the 400 is asserted deliberately.

func updateWFHandler(db *gorm.DB) *Handler {
	return &Handler{
		repo:       NewRepository(db),
		db:         db,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		capChecker: capAllow{},
	}
}

func updateWFRouter(h *Handler, orgID, userID uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Set("role", "admin")
		c.Next()
	})
	r.PUT("/api/workflows/:id", h.UpdateWorkflow)
	return r
}

func updateWFPut(t *testing.T, router *gin.Engine, id uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/workflows/"+id.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

// updateWFStepsError digs the per-field `steps` message out of the error envelope.
func updateWFStepsError(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
	assert.Equal(t, "VALIDATION_FAILED", body.Error.Code)
	for _, d := range body.Error.Details {
		if d.Field == "steps" {
			return d.Message
		}
	}
	return ""
}

func TestUpdateWorkflow_RenameOnly(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID, userID := uuid.New(), uuid.New()
	h := updateWFHandler(db)
	router := updateWFRouter(h, orgID, userID)

	t.Run("a rename of a HEALTHY workflow succeeds and leaves its steps alone", func(t *testing.T) {
		wf := &Workflow{
			OrgID: orgID, Name: "before", Description: "d",
			Trigger:   datatypes.JSON(`{"type":"contact_created"}`),
			Steps:     actionStepsJSON(t, ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x@test.com"}}),
			CreatedBy: userID,
		}
		require.NoError(t, h.repo.CreateWorkflow(context.Background(), wf))

		w := updateWFPut(t, router, wf.ID, `{"name":"after"}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

		var resp struct {
			Data WorkflowResponse `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "after", resp.Data.Name)
		assert.Equal(t, 1, resp.Data.ActionCount, "the stored steps tree must survive a rename")
		assert.Len(t, resp.Data.Actions, 1,
			"and the response still carries the derived `actions` a cached bundle needs to save")
	})

	// The guard doing its actual job: a live workflow cannot be emptied.
	t.Run("blanking the steps of a live workflow is rejected", func(t *testing.T) {
		wf := &Workflow{
			OrgID: orgID, Name: "live", Trigger: datatypes.JSON(`{"type":"contact_created"}`),
			Steps:     actionStepsJSON(t, ActionSpec{ID: "a1", Type: "send_email", Params: map[string]any{"to": "x@test.com"}}),
			CreatedBy: userID,
		}
		require.NoError(t, h.repo.CreateWorkflow(context.Background(), wf))

		w := updateWFPut(t, router, wf.ID, `{"steps":[]}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		assert.Contains(t, updateWFStepsError(t, w), "at least one step")

		stored, err := h.repo.GetWorkflowByID(context.Background(), orgID, wf.ID)
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, 1, ToWorkflowResponse(stored).ActionCount, "the rejected PUT must not have persisted")
	})

	// The pinned consequence. Documented at the top of this file: kept, not fixed.
	t.Run("a rename of a STEPS-LESS legacy workflow is rejected — deliberately", func(t *testing.T) {
		// Seeded through raw SQL because no API path can create this shape any more
		// (a steps-less legacy row, from before R5 deploy 1 — the `actions` column
		// itself is gone as of deploy 2, so this no longer seeds one).
		id := seedStepsLessLegacyWorkflow(t, db, orgID)

		w := updateWFPut(t, router, id, `{"name":"renamed"}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
		assert.Contains(t, updateWFStepsError(t, w), "at least one step",
			"the rejection names the row's real problem, not the rename")

		// …and the same PUT carrying a steps tree fixes the row, which is why the strict
		// path needs no relaxation: the remedy is always available in one request.
		fixed, err := json.Marshal(map[string]any{
			"name": "renamed",
			"steps": []map[string]any{{
				"type": "action", "id": "a1",
				"action": map[string]any{"type": "send_email", "id": "a1", "params": map[string]any{"to": "x@test.com"}},
			}},
		})
		require.NoError(t, err)
		w = updateWFPut(t, router, id, string(fixed))
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

		stored, err := h.repo.GetWorkflowByID(context.Background(), orgID, id)
		require.NoError(t, err)
		require.NotNil(t, stored)
		assert.Equal(t, "renamed", stored.Name)
		assert.Equal(t, 1, ToWorkflowResponse(stored).ActionCount)
	})
}

// seedStepsLessLegacyWorkflow inserts one automation_workflows row with steps SQL-NULL —
// the shape a pre-R5 workflow had before any migration touched it. Raw SQL because no Go
// struct (and, since R5 deploy 2, no `actions` column either) can produce this any more.
func seedStepsLessLegacyWorkflow(t *testing.T, db *gorm.DB, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`
		INSERT INTO automation_workflows
			(id, org_id, name, description, is_active, trigger, conditions, steps,
			 version, created_by, created_at, updated_at)
		VALUES (?, ?, ?, '', false, '{"type":"webhook_inbound"}'::jsonb, NULL, NULL,
			 1, ?, NOW(), NOW())`,
		id, orgID, "legacy-"+id.String()[:8], uuid.New()).Error)
	return id
}
