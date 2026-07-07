package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// TestDoD_LogActivity_WebhookToTimeline is the end-to-end Definition-of-Done test
// for the log_activity feature:
//
//	trigger = contact_created
//	action  = log_activity { type: note, title: "New lead from {{...source}}", body: "Auto-logged" }
//	A contact is created via the real inbound webhook handler with source=typeform.
//	=> the contact's timeline contains a `note` activity whose title names the source
//	   and whose body is "Auto-logged", linked to the created contact.
//
// IMPORTANT — template path:
// The inbound webhook handler (handlers.go) hardcodes trigger.source = "webhook_inbound"
// and routes any non-standard body field (here, "source") into the contact's
// custom_fields. So the value "typeform" arrives at contact.custom_fields.source, NOT at
// trigger.source. The DoD's intent ("New lead from typeform") is therefore expressed with
// the title template {{contact.custom_fields.source}}. A companion assertion documents
// that {{trigger.source}} would instead resolve to the handler's literal "webhook_inbound".
//
// DB-backed via setupTestDB (testcontainer Postgres); skipped under -short and when
// Docker is unavailable.
func TestDoD_LogActivity_WebhookToTimeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed E2E test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	// activities table — mirrors migrations/000002_schema.up.sql (enum type + FKs to
	// organizations/contacts/deals). contacts is created by setupTestDB. user_id has no
	// FK here (the executor always writes NULL) and organizations/deals are created so
	// the activities FKs resolve.
	require.NoError(t, db.Exec(`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'activity_type') THEN
			CREATE TYPE activity_type AS ENUM ('call', 'email', 'meeting', 'note', 'stage_change');
		END IF;
	END $$;`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (id UUID PRIMARY KEY, name TEXT DEFAULT '')`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (id UUID PRIMARY KEY, org_id UUID NOT NULL, title TEXT DEFAULT '')`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS activities (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		type activity_type NOT NULL,
		deal_id UUID REFERENCES deals(id) ON DELETE CASCADE,
		contact_id UUID REFERENCES contacts(id) ON DELETE CASCADE,
		user_id UUID,
		title VARCHAR(255),
		body TEXT,
		duration_minutes INT,
		occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		sentiment TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)

	orgID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme Inc')`, orgID).Error)

	repo := NewRepository(db)

	// Real ActivityExecutor — this is the component under test.
	engine := makeEngine(db, map[string]ActionExecutor{
		ActionLogActivity: NewActivityExecutor(db),
	})
	defer engine.cancel()

	// Webhook auth token (skip signature verification for the test).
	t.Setenv("WEBHOOK_SKIP_SIGNATURE", "true")
	token := &WorkflowOrgToken{
		OrgID:     orgID,
		Token:     fmt.Sprintf("test-token-%s", uuid.New().String()[:8]),
		Secret:    "test-secret",
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(token).Error)

	// Workflow: contact_created -> log_activity (note). Title references the source
	// where it actually lands (contact.custom_fields.source); body is a literal.
	trigger, _ := json.Marshal(map[string]any{"type": "contact_created"})
	actions, _ := json.Marshal([]ActionSpec{{
		ID:   "log1",
		Type: ActionLogActivity,
		Params: map[string]any{
			"activity_type": "note",
			"title":         "New lead from {{contact.custom_fields.source}}",
			"body":          "Auto-logged",
		},
	}})
	wf := &Workflow{
		ID: uuid.New(), OrgID: orgID, Name: "log-activity-dod",
		IsActive: true, Trigger: datatypes.JSON(trigger), Actions: datatypes.JSON(actions),
		Version: 1, CreatedBy: uuid.New(),
	}
	require.NoError(t, db.Create(wf).Error)
	require.NoError(t, db.Create(&WorkflowVersion{
		ID: uuid.New(), WorkflowID: wf.ID, Version: 1,
		Trigger: wf.Trigger, Actions: wf.Actions, CreatedAt: time.Now(),
	}).Error)

	// Fire the real inbound webhook with source=typeform.
	handler := &Handler{
		engine:      engine,
		repo:        repo,
		db:          db,
		logger:      slog.Default(),
		rateLimiter: newTokenBucket(),
		capChecker:  capAllow{},
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/webhooks/inbound/:org_token", handler.WebhookInbound)

	webhookPayload := map[string]any{
		"email":      "newlead@example.com",
		"first_name": "Jane",
		"last_name":  "Doe",
		"source":     "typeform",
	}
	body, _ := json.Marshal(webhookPayload)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/webhooks/inbound/"+token.Token, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "webhook should return 200, body: %s", w.Body.String())

	var resp WebhookInboundResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.ContactID, "webhook must report the created contact id")
	contactID := uuid.MustParse(resp.ContactID)

	// TriggerEvent is fire-and-forget; poll until the run row exists, then process it.
	var runs []WorkflowRun
	require.Eventually(t, func() bool {
		db.Where("workflow_id = ?", wf.ID).Find(&runs)
		return len(runs) > 0
	}, 5*time.Second, 100*time.Millisecond, "contact_created trigger should create a run")
	engine.processRun(runs[0].ID)

	finalRun, err := repo.GetRunByID(context.Background(), runs[0].ID)
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, finalRun.Status, "run must complete; last error: %s", finalRun.LastError)

	// The DoD assertion: exactly one note activity on the new contact's timeline,
	// title naming the source, body "Auto-logged", user_id NULL.
	var row struct {
		Type      string     `gorm:"column:type"`
		Title     *string    `gorm:"column:title"`
		Body      *string    `gorm:"column:body"`
		ContactID *uuid.UUID `gorm:"column:contact_id"`
		UserID    *uuid.UUID `gorm:"column:user_id"`
	}
	var count int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM activities WHERE contact_id = ?`, contactID).Scan(&count).Error)
	require.Equal(t, int64(1), count, "exactly one activity should be logged on the contact timeline")

	require.NoError(t, db.Table("activities").
		Select("type, title, body, contact_id, user_id").
		Where("contact_id = ?", contactID).Scan(&row).Error)

	assert.Equal(t, "note", row.Type, "activity type must be note")
	require.NotNil(t, row.Title)
	assert.Equal(t, "New lead from typeform", *row.Title,
		"title must resolve {{contact.custom_fields.source}} to the webhook's source=typeform")
	require.NotNil(t, row.Body)
	assert.Equal(t, "Auto-logged", *row.Body, "body must be stored verbatim")
	require.NotNil(t, row.ContactID)
	assert.Equal(t, contactID, *row.ContactID, "activity must link to the created contact")
	assert.Nil(t, row.UserID, "automation-logged activity has a NULL user_id")
}

// TestDoD_LogActivity_TriggerSourceIsHandlerLiteral documents — at the unit level, no DB —
// the reason the DoD title uses contact.custom_fields.source rather than trigger.source:
// the inbound webhook handler sets trigger.source to the literal "webhook_inbound", so a
// title templated on {{trigger.source}} would read "New lead from webhook_inbound", while
// the body field source=typeform is the value the DoD actually wants to surface.
func TestDoD_LogActivity_TriggerSourceIsHandlerLiteral(t *testing.T) {
	evalCtx := EvalContext{
		Contact: map[string]any{
			"custom_fields": map[string]any{"source": "typeform"},
		},
		Trigger: map[string]any{"type": "contact_created", "source": "webhook_inbound"},
	}

	assert.Equal(t, "New lead from webhook_inbound",
		InterpolateTemplate("New lead from {{trigger.source}}", evalCtx),
		"{{trigger.source}} resolves to the handler's hardcoded literal")
	assert.Equal(t, "New lead from typeform",
		InterpolateTemplate("New lead from {{contact.custom_fields.source}}", evalCtx),
		"{{contact.custom_fields.source}} resolves to the webhook body's source value")
}
