package automation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateRecord_ContactUpdate_ColumnCustomFieldAndTag exercises the contact path
// (executeContact) end-to-end across the helpers it shares with the deal path:
// a column set (handleGenericColumn), a custom-field set + templated increment
// (handleCustomField — the JSONB merge), and a tag add (handleContactTags). It is the
// regression guard ensuring the shared-helper changes made for the deal/P14 work did
// not break contact updates.
func TestUpdateRecord_ContactUpdate_ColumnCustomFieldAndTag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	// setupTestDB already creates `contacts`; the contact-tags join table is ours.
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS contact_tags (
		contact_id UUID NOT NULL,
		tag_id UUID NOT NULL,
		PRIMARY KEY (contact_id, tag_id)
	)`).Error)

	orgID := uuid.New()
	contactID := uuid.New()
	tagID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, email, custom_fields) VALUES (?, ?, 'Jane', 'old@x.com', '{"points": 10}'::jsonb)`,
		contactID, orgID,
	).Error)

	exec := NewUpdateRecordExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			map[string]any{"field": "contact.email", "op": "set", "value": "new@x.com"},
			map[string]any{"field": "contact.custom_fields.tier", "op": "set", "value": "gold"},
			map[string]any{"field": "contact.custom_fields.points", "op": "increment", "value": "{{trigger.bump}}"},
			map[string]any{"field": "contact.tags", "op": "add", "value": []any{tagID.String()}},
		},
	}}
	evalCtx := EvalContext{
		Contact: map[string]any{"id": contactID.String()},
		Trigger: map[string]any{"type": "contact_updated", "bump": "5"},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)

	var row struct {
		Email string `gorm:"column:email"`
		CF    string `gorm:"column:cf"`
	}
	require.NoError(t, db.Table("contacts").
		Select("email, custom_fields::text AS cf").
		Where("id = ?", contactID).Scan(&row).Error)
	assert.Equal(t, "new@x.com", row.Email, "email column should be updated")

	var cf map[string]any
	require.NoError(t, json.Unmarshal([]byte(row.CF), &cf))
	assert.Equal(t, "gold", cf["tier"], "custom field should be merged in (not overwrite the whole column)")
	points, _ := toFloat64(cf["points"])
	assert.Equal(t, 15.0, points, "templated increment on a contact custom field must add the resolved value (5), preserving the existing 10")

	var tagCount int64
	require.NoError(t, db.Table("contact_tags").
		Where("contact_id = ? AND tag_id = ?", contactID, tagID).Count(&tagCount).Error)
	assert.Equal(t, int64(1), tagCount, "tag should be added to the contact")
}

// TestUpdateRecord_CustomObjectUpdate_JSONBData exercises the custom-object path
// (executeCustomObject), which is entirely separate from the contact/deal column code
// and stores everything in a JSONB `data` column. The record is resolved from the
// trigger slug (ticket_updated → "ticket") and evalCtx.Extra. Covers a set and an
// increment so a future change can't silently break custom-object updates.
func TestUpdateRecord_CustomObjectUpdate_JSONBData(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_records (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		data JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	orgID := uuid.New()
	recordID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO custom_object_records (id, org_id, data) VALUES (?, ?, '{"status": "open", "count": 3}'::jsonb)`,
		recordID, orgID,
	).Error)

	exec := NewUpdateRecordExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			map[string]any{"field": "ticket.status", "op": "set", "value": "closed"},
			map[string]any{"field": "ticket.count", "op": "increment", "value": 2},
		},
	}}
	evalCtx := EvalContext{
		Trigger: map[string]any{"type": "ticket_updated"},
		Extra:   map[string]any{"ticket": map[string]any{"id": recordID.String()}},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)

	var dataStr string
	require.NoError(t, db.Table("custom_object_records").
		Select("data::text").
		Where("id = ?", recordID).Scan(&dataStr).Error)
	var data map[string]any
	require.NoError(t, json.Unmarshal([]byte(dataStr), &data))
	assert.Equal(t, "closed", data["status"], "set should patch the JSONB key")
	count, _ := toFloat64(data["count"])
	assert.Equal(t, 5.0, count, "increment should add 2 to the existing 3")
}
