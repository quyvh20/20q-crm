package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedDealStageTables creates the minimal deals / pipeline_stages / activities
// tables the update_record deal-stage path touches. Uses TEXT for activities.type
// to avoid depending on the activity_type enum in the test database.
func seedDealStageTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		name TEXT NOT NULL,
		position INT DEFAULT 0,
		color TEXT DEFAULT '#3B82F6',
		is_won BOOLEAN DEFAULT FALSE,
		is_lost BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT DEFAULT '',
		value NUMERIC DEFAULT 0,
		probability INT DEFAULT 0,
		stage_id UUID,
		contact_id UUID,
		company_id UUID,
		owner_user_id UUID,
		is_won BOOLEAN DEFAULT FALSE,
		is_lost BOOLEAN DEFAULT FALSE,
		closed_at TIMESTAMPTZ,
		custom_fields JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS activities (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		type TEXT NOT NULL,
		deal_id UUID,
		contact_id UUID,
		user_id UUID,
		title TEXT,
		body TEXT,
		occurred_at TIMESTAMPTZ DEFAULT NOW(),
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
}

// TestUpdateRecord_DealStageChange_WritesActivityLog is the core P14 guarantee:
// changing a deal's stage via the update_record action must move the deal AND record
// a "stage_change" activity (plus won/lost + closed_at side effects), exactly as a
// stage change made through the CRM UI.
func TestUpdateRecord_DealStageChange_WritesActivityLog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	seedDealStageTables(t, db)

	orgID := uuid.New()
	leadStageID := uuid.New()
	wonStageID := uuid.New()
	dealID := uuid.New()

	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, is_won) VALUES (?, ?, 'Lead', 0, false)`, leadStageID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, is_won) VALUES (?, ?, 'Closed Won', 1, true)`, wonStageID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, stage_id, is_won) VALUES (?, ?, 'Acme renewal', ?, false)`, dealID, orgID, leadStageID).Error)

	exec := NewUpdateRecordExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			map[string]any{"field": "deal.stage", "op": "set", "value": wonStageID.String()},
		},
	}}
	evalCtx := EvalContext{
		Deal:    map[string]any{"id": dealID.String()},
		Trigger: map[string]any{"type": "deal_updated"},
	}

	out, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err, "stage change should succeed")
	require.NotNil(t, out)

	// 1. Deal moved to the won stage, with won/closed side effects applied.
	var deal struct {
		StageID  *uuid.UUID `gorm:"column:stage_id"`
		IsWon    bool       `gorm:"column:is_won"`
		ClosedAt *string    `gorm:"column:closed_at"`
	}
	require.NoError(t, db.Table("deals").
		Select("stage_id, is_won, closed_at").
		Where("id = ?", dealID).Scan(&deal).Error)
	require.NotNil(t, deal.StageID)
	assert.Equal(t, wonStageID, *deal.StageID, "deal should be moved to the won stage")
	assert.True(t, deal.IsWon, "moving to a won stage should set is_won")
	assert.NotNil(t, deal.ClosedAt, "moving to a won stage should set closed_at")

	// 2. The activity log records the stage change (the P14 guarantee).
	var activities []struct {
		Type  string  `gorm:"column:type"`
		Title *string `gorm:"column:title"`
	}
	require.NoError(t, db.Table("activities").
		Select("type, title").
		Where("deal_id = ? AND org_id = ?", dealID, orgID).Scan(&activities).Error)
	require.Len(t, activities, 1, "exactly one stage_change activity should be created")
	assert.Equal(t, "stage_change", activities[0].Type)
	require.NotNil(t, activities[0].Title)
	assert.Equal(t, "Deal won! 🏆", *activities[0].Title)
}

// TestUpdateRecord_DealStageChange_PlainStage covers the non-won/non-lost branch:
// moving to an ordinary stage sets stage_id, leaves is_won/is_lost/closed_at untouched,
// and logs an activity titled "Stage changed to <name>". Together with the won-path
// test this guards the side-effects that duplicate dealUseCase.ChangeStage.
func TestUpdateRecord_DealStageChange_PlainStage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	seedDealStageTables(t, db)

	orgID := uuid.New()
	leadStageID := uuid.New()
	negotiationStageID := uuid.New()
	dealID := uuid.New()

	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position) VALUES (?, ?, 'Lead', 0)`, leadStageID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position) VALUES (?, ?, 'Negotiation', 1)`, negotiationStageID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, stage_id) VALUES (?, ?, 'Acme', ?)`, dealID, orgID, leadStageID).Error)

	exec := NewUpdateRecordExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			map[string]any{"field": "deal.stage", "op": "set", "value": negotiationStageID.String()},
		},
	}}
	evalCtx := EvalContext{
		Deal:    map[string]any{"id": dealID.String()},
		Trigger: map[string]any{"type": "deal_updated"},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)

	var deal struct {
		StageID  *uuid.UUID `gorm:"column:stage_id"`
		IsWon    bool       `gorm:"column:is_won"`
		IsLost   bool       `gorm:"column:is_lost"`
		ClosedAt *string    `gorm:"column:closed_at"`
	}
	require.NoError(t, db.Table("deals").
		Select("stage_id, is_won, is_lost, closed_at").
		Where("id = ?", dealID).Scan(&deal).Error)
	require.NotNil(t, deal.StageID)
	assert.Equal(t, negotiationStageID, *deal.StageID)
	assert.False(t, deal.IsWon, "a plain stage must not set is_won")
	assert.False(t, deal.IsLost, "a plain stage must not set is_lost")
	assert.Nil(t, deal.ClosedAt, "a plain stage must not set closed_at")

	var activity struct {
		Type  string  `gorm:"column:type"`
		Title *string `gorm:"column:title"`
	}
	require.NoError(t, db.Table("activities").
		Select("type, title").
		Where("deal_id = ?", dealID).Scan(&activity).Error)
	assert.Equal(t, "stage_change", activity.Type)
	require.NotNil(t, activity.Title)
	assert.Equal(t, "Stage changed to Negotiation", *activity.Title)
}

// TestUpdateRecord_DealNumericColumnIncrement verifies increment/decrement on the
// built-in numeric columns (value, probability) actually mutates the row — the
// validator already permitted these ops; this is the executor side that now matches.
// A fractional delta must survive on the money column.
func TestUpdateRecord_DealNumericColumnIncrement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	seedDealStageTables(t, db)

	orgID := uuid.New()
	dealID := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, value, probability) VALUES (?, ?, 'Acme', 1000.00, 40)`, dealID, orgID).Error)

	exec := NewUpdateRecordExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			map[string]any{"field": "deal.value", "op": "increment", "value": 250.50},
			map[string]any{"field": "deal.probability", "op": "decrement", "value": 15},
		},
	}}
	evalCtx := EvalContext{
		Deal:    map[string]any{"id": dealID.String()},
		Trigger: map[string]any{"type": "deal_updated"},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)

	var deal struct {
		Value       float64 `gorm:"column:value"`
		Probability int     `gorm:"column:probability"`
	}
	require.NoError(t, db.Table("deals").
		Select("value, probability").
		Where("id = ?", dealID).Scan(&deal).Error)
	assert.InDelta(t, 1250.50, deal.Value, 0.001, "value should increment by the fractional delta")
	assert.Equal(t, 25, deal.Probability, "probability should decrement by 15")
}

// TestUpdateRecord_DealStageChange_UnknownStageRolledBack verifies that pointing at a
// stage from another org (or a non-existent one) fails and writes nothing — no deal
// mutation, no orphan activity.
func TestUpdateRecord_DealStageChange_UnknownStageRolledBack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	seedDealStageTables(t, db)

	orgID := uuid.New()
	leadStageID := uuid.New()
	dealID := uuid.New()

	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position) VALUES (?, ?, 'Lead', 0)`, leadStageID, orgID).Error)
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, stage_id) VALUES (?, ?, 'Acme renewal', ?)`, dealID, orgID, leadStageID).Error)

	exec := NewUpdateRecordExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			// A random stage ID that does not belong to this org.
			map[string]any{"field": "deal.stage", "op": "set", "value": uuid.New().String()},
		},
	}}
	evalCtx := EvalContext{
		Deal:    map[string]any{"id": dealID.String()},
		Trigger: map[string]any{"type": "deal_updated"},
	}

	_, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.Error(t, err, "unknown stage should fail")

	// Deal still in the original stage, and no activity was written.
	var stageID uuid.UUID
	require.NoError(t, db.Table("deals").Select("stage_id").Where("id = ?", dealID).Scan(&stageID).Error)
	assert.Equal(t, leadStageID, stageID, "deal stage must be unchanged on failure")

	var count int64
	require.NoError(t, db.Table("activities").Where("deal_id = ?", dealID).Count(&count).Error)
	assert.Equal(t, int64(0), count, "no activity should be written on failure")
}
