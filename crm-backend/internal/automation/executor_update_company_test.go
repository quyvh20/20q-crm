package automation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// executor_update_company_test.go covers the A2 executeCompany path: company
// became a first-class trigger object (Step 2), so a company_updated workflow's
// update_record action must write to the typed companies table (native columns +
// custom_fields JSONB) — not the custom_object_records path. Docker-gated.

func seedCompaniesTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS companies (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		industry TEXT,
		website TEXT,
		custom_fields JSONB DEFAULT '{}',
		deleted_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`).Error)
}

func TestUpdateRecord_Company_NativeAndCustomFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	seedCompaniesTable(t, db)

	orgID := uuid.New()
	companyID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO companies (id, org_id, name, industry) VALUES (?, ?, 'Acme', 'Retail')`, companyID, orgID).Error)

	exec := NewUpdateRecordExecutor(db, nil)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionUpdateRecord, ID: "ur1", Params: map[string]any{
		"updates": []any{
			map[string]any{"field": "company.industry", "op": "set", "value": "Software"},
			map[string]any{"field": "company.custom_fields.tier", "op": "set", "value": "gold"},
		},
	}}
	// A company_updated trigger routes the record under Extra["company"].
	evalCtx := EvalContext{
		Extra:   map[string]any{"company": map[string]any{"id": companyID.String()}},
		Trigger: map[string]any{"type": "company_updated"},
	}

	out, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.NotNil(t, out)

	var industry, tier string
	require.NoError(t, db.Raw(`SELECT industry FROM companies WHERE id = ?`, companyID).Scan(&industry).Error)
	assert.Equal(t, "Software", industry, "native column must be updated")
	require.NoError(t, db.Raw(`SELECT custom_fields->>'tier' FROM companies WHERE id = ?`, companyID).Scan(&tier).Error)
	assert.Equal(t, "gold", tier, "custom field must be patched into the JSONB blob")
}

func TestUpdateRecord_Company_ResolvesEntityFromTrigger(t *testing.T) {
	// The trigger type alone (company_updated) must resolve to the company entity,
	// not fall through to the custom-object path.
	assert.Equal(t, "company", resolveEntity(EvalContext{Trigger: map[string]any{"type": "company_updated"}}))
	assert.Equal(t, "company", resolveEntity(EvalContext{Trigger: map[string]any{"type": "company_created"}}))
	assert.Equal(t, "company", resolveEntity(EvalContext{Trigger: map[string]any{"type": "company_deleted"}}))
}

func TestLoadCompanyForTrigger_And_Hydration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	seedCompaniesTable(t, db)

	orgID := uuid.New()
	companyID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO companies (id, org_id, name, industry, custom_fields) VALUES (?, ?, 'Acme', 'Retail', '{"tier":"gold"}')`,
		companyID, orgID).Error)

	// Direct load.
	m := loadCompanyForTrigger(context.Background(), db, orgID, companyID)
	require.NotNil(t, m)
	assert.Equal(t, "Acme", m["name"])
	assert.Equal(t, "Retail", m["industry"])
	if cf, ok := m["custom_fields"].(map[string]any); assert.True(t, ok) {
		assert.Equal(t, "gold", cf["tier"])
	}

	// Missing company → nil (best-effort, no error).
	assert.Nil(t, loadCompanyForTrigger(context.Background(), db, orgID, uuid.New()))

	// buildEvalContext hydrates company.* from a deal's company_id so
	// {{company.name}} resolves on a deal-triggered run.
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()
	tcJSON, _ := json.Marshal(map[string]any{
		"trigger": map[string]any{"type": "deal_updated"},
		"deal":    map[string]any{"id": uuid.New().String(), "company_id": companyID.String()},
	})
	run := &WorkflowRun{
		ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID,
		TriggerContext: datatypes.JSON(tcJSON),
	}
	ctx := engine.buildEvalContext(run)
	co, ok := ctx.Extra["company"].(map[string]any)
	require.True(t, ok, "company must be hydrated into Extra from deal.company_id")
	assert.Equal(t, "Acme", co["name"])
}
