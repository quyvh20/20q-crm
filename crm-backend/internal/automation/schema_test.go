package automation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
)

// TestGetWorkflowSchema_FullCoverage verifies that GET /api/workflows/schema
// returns ALL of: built-in contact fields, built-in deal fields, trigger fields,
// custom field defs (per entity), custom object defs, pipeline stages (with color),
// tags (with color), org members (id + name + email).
func TestGetWorkflowSchema_FullCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	// --- Seed ALL data types ---

	// 1. Pipeline stages (with color)
	stage1ID := uuid.New()
	stage2ID := uuid.New()
	db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (
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
	)`)
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color, is_won) VALUES (?, ?, 'Lead', 0, '#3B82F6', false)`, stage1ID, orgID)
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color, is_won) VALUES (?, ?, 'Closed Won', 1, '#10B981', true)`, stage2ID, orgID)

	// 2. Tags (with color)
	tag1ID := uuid.New()
	tag2ID := uuid.New()
	db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		name TEXT NOT NULL,
		color TEXT DEFAULT '#6B7280',
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'VIP', '#F59E0B')`, tag1ID, orgID)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'Enterprise', '#8B5CF6')`, tag2ID, orgID)

	// 3. Users + org_users (org members)
	userID := uuid.New()
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY,
		org_id UUID,
		email TEXT NOT NULL,
		first_name TEXT DEFAULT '',
		last_name TEXT DEFAULT '',
		full_name TEXT DEFAULT '',
		role TEXT DEFAULT 'viewer',
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL,
		org_id UUID NOT NULL,
		role_id UUID NOT NULL,
		status TEXT DEFAULT 'active',
		joined_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id)
	)`)
	db.Exec(`INSERT INTO users (id, org_id, email, first_name, last_name) VALUES (?, ?, 'alex@test.com', 'Alex', 'Chen')`, userID, orgID)
	db.Exec(`INSERT INTO org_users (user_id, org_id, role_id, status) VALUES (?, ?, ?, 'active')`, userID, orgID, uuid.New())

	// 4. Org settings with custom field defs
	db.Exec(`CREATE TABLE IF NOT EXISTS org_settings (
		org_id UUID PRIMARY KEY,
		industry_template_slug TEXT,
		ai_context_override TEXT,
		custom_field_defs JSONB DEFAULT '[]',
		onboarding_completed BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	customFields := `[
		{"key":"lead_source","label":"Lead Source","type":"select","entity_type":"contact","options":["Web","Referral","Cold Call"],"required":false,"position":0},
		{"key":"contract_type","label":"Contract Type","type":"select","entity_type":"deal","options":["Monthly","Annual"],"required":false,"position":1},
		{"key":"linkedin","label":"LinkedIn URL","type":"url","entity_type":"contact","options":null,"required":false,"position":2}
	]`
	db.Exec(`INSERT INTO org_settings (org_id, custom_field_defs) VALUES (?, ?::jsonb)`, orgID, customFields)

	// 5. Custom object definitions
	db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		slug TEXT NOT NULL,
		label TEXT NOT NULL,
		label_plural TEXT NOT NULL,
		icon TEXT DEFAULT '📦',
		fields JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`)
	objID := uuid.New()
	objFields := `[
		{"key":"plan","label":"Plan","type":"select","options":["Free","Pro","Enterprise"]},
		{"key":"mrr","label":"MRR","type":"number","options":null},
		{"key":"is_active","label":"Active","type":"boolean","options":null}
	]`
	db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug, label, label_plural, icon, fields) VALUES (?, ?, 'subscription', 'Subscription', 'Subscriptions', '📦', ?::jsonb)`, objID, orgID, objFields)

	// --- Setup gin router with fake auth ---
	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Fake auth middleware that sets org_id and user_id
	fakeAuth := func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	}
	fakeRequireRole := func(roles ...string) gin.HandlerFunc {
		return func(c *gin.Context) { c.Next() }
	}

	handler := &Handler{
		engine: makeEngine(db, nil),
		repo:   NewRepository(db),
		logger: slog.Default(),
		db:     db,
	}
	handler.RegisterRoutes(router, fakeAuth, fakeRequireRole)

	// --- Hit the endpoint ---
	req := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "expected 200 OK, got %d: %s", w.Code, w.Body.String())

	// --- Parse response ---
	var resp struct {
		Data SchemaResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	schema := resp.Data

	// ============================================================
	// ASSERT 1: Built-in Contact fields (8)
	// ============================================================
	var contactEntity *SchemaEntity
	for i := range schema.Entities {
		if schema.Entities[i].Key == "contact" {
			contactEntity = &schema.Entities[i]
			break
		}
	}
	require.NotNil(t, contactEntity, "contact entity must exist")
	assert.Equal(t, "Contact", contactEntity.Label)
	assert.Equal(t, "👤", contactEntity.Icon)

	contactPaths := map[string]bool{}
	for _, f := range contactEntity.Fields {
		contactPaths[f.Path] = true
	}
	for _, expected := range []string{
		"contact.first_name", "contact.last_name", "contact.email",
		"contact.phone", "contact.owner_id", "contact.tags",
		"contact.company.name", "contact.id",
	} {
		assert.True(t, contactPaths[expected], "contact must have field %s", expected)
	}
	// Check picker types on special fields
	for _, f := range contactEntity.Fields {
		if f.Path == "contact.owner_id" {
			assert.Equal(t, "user", f.PickerType, "owner_id should have picker_type=user")
		}
		if f.Path == "contact.tags" {
			assert.Equal(t, "tag", f.PickerType, "tags should have picker_type=tag")
			assert.Equal(t, "array", f.Type, "tags should have type=array")
		}
	}

	// ============================================================
	// ASSERT 2: Built-in Deal fields (8)
	// ============================================================
	var dealEntity *SchemaEntity
	for i := range schema.Entities {
		if schema.Entities[i].Key == "deal" {
			dealEntity = &schema.Entities[i]
			break
		}
	}
	require.NotNil(t, dealEntity, "deal entity must exist")
	assert.Equal(t, "Deal", dealEntity.Label)

	dealPaths := map[string]bool{}
	for _, f := range dealEntity.Fields {
		dealPaths[f.Path] = true
	}
	for _, expected := range []string{
		"deal.title", "deal.value", "deal.stage", "deal.probability",
		"deal.is_won", "deal.is_lost", "deal.owner_id", "deal.id",
	} {
		assert.True(t, dealPaths[expected], "deal must have field %s", expected)
	}
	// Check deal.stage has picker_type=stage
	for _, f := range dealEntity.Fields {
		if f.Path == "deal.stage" {
			assert.Equal(t, "stage", f.PickerType)
		}
	}

	// ============================================================
	// ASSERT 3: Trigger fields
	// ============================================================
	var triggerEntity *SchemaEntity
	for i := range schema.Entities {
		if schema.Entities[i].Key == "trigger" {
			triggerEntity = &schema.Entities[i]
			break
		}
	}
	require.NotNil(t, triggerEntity, "trigger entity must exist")
	triggerPaths := map[string]bool{}
	for _, f := range triggerEntity.Fields {
		triggerPaths[f.Path] = true
	}
	assert.True(t, triggerPaths["trigger.type"])
	assert.True(t, triggerPaths["trigger.from_stage"])
	assert.True(t, triggerPaths["trigger.to_stage"])

	// ============================================================
	// ASSERT 4: Custom field defs merged into correct entities
	// ============================================================
	// lead_source and linkedin should be appended to contact
	assert.True(t, contactPaths["contact.custom_fields.lead_source"] || findField(contactEntity.Fields, "contact.custom_fields.lead_source") != nil,
		"contact should have custom field lead_source")
	leadSource := findField(contactEntity.Fields, "contact.custom_fields.lead_source")
	require.NotNil(t, leadSource, "lead_source field must exist in contact")
	assert.Equal(t, "Lead Source", leadSource.Label)
	assert.Equal(t, "select", leadSource.Type)
	assert.Equal(t, []string{"Web", "Referral", "Cold Call"}, leadSource.Options)

	linkedin := findField(contactEntity.Fields, "contact.custom_fields.linkedin")
	require.NotNil(t, linkedin, "linkedin field must exist in contact")
	assert.Equal(t, "LinkedIn URL", linkedin.Label)
	assert.Equal(t, "url", linkedin.Type)

	// contract_type should be appended to deal
	contractType := findField(dealEntity.Fields, "deal.custom_fields.contract_type")
	require.NotNil(t, contractType, "contract_type field must exist in deal")
	assert.Equal(t, "Contract Type", contractType.Label)
	assert.Equal(t, []string{"Monthly", "Annual"}, contractType.Options)

	// ============================================================
	// ASSERT 5: Custom object defs
	// ============================================================
	require.Len(t, schema.CustomObjects, 1, "should have 1 custom object")
	sub := schema.CustomObjects[0]
	assert.Equal(t, "subscription", sub.Key)
	assert.Equal(t, "Subscription", sub.Label)
	assert.Equal(t, "📦", sub.Icon)
	require.Len(t, sub.Fields, 3)

	subPaths := map[string]string{}
	for _, f := range sub.Fields {
		subPaths[f.Path] = f.Type
	}
	assert.Equal(t, "select", subPaths["subscription.plan"])
	assert.Equal(t, "number", subPaths["subscription.mrr"])
	assert.Equal(t, "boolean", subPaths["subscription.is_active"])

	// Check options on select field
	planField := findField(sub.Fields, "subscription.plan")
	require.NotNil(t, planField)
	assert.Equal(t, []string{"Free", "Pro", "Enterprise"}, planField.Options)

	// ============================================================
	// ASSERT 6: Pipeline stages (with id, name, color)
	// ============================================================
	require.Len(t, schema.Stages, 2)
	assert.Equal(t, "Lead", schema.Stages[0].Name)
	assert.Equal(t, "#3B82F6", schema.Stages[0].Color)
	assert.Equal(t, stage1ID.String(), schema.Stages[0].ID)
	assert.Equal(t, "Closed Won", schema.Stages[1].Name)
	assert.Equal(t, "#10B981", schema.Stages[1].Color)
	assert.Equal(t, stage2ID.String(), schema.Stages[1].ID)

	// ============================================================
	// ASSERT 7: Tags (with id, name, color)
	// ============================================================
	require.Len(t, schema.Tags, 2)
	tagMap := map[string]SchemaTag{}
	for _, tag := range schema.Tags {
		tagMap[tag.Name] = tag
	}
	assert.Equal(t, "#8B5CF6", tagMap["Enterprise"].Color)
	assert.Equal(t, tag2ID.String(), tagMap["Enterprise"].ID)
	assert.Equal(t, "#F59E0B", tagMap["VIP"].Color)
	assert.Equal(t, tag1ID.String(), tagMap["VIP"].ID)

	// ============================================================
	// ASSERT 8: Org members (id + name + email)
	// ============================================================
	require.Len(t, schema.Users, 1)
	assert.Equal(t, userID.String(), schema.Users[0].ID)
	assert.Equal(t, "Alex Chen", schema.Users[0].Name)
	assert.Equal(t, "alex@test.com", schema.Users[0].Email)
}

// TestGetWorkflowSchema_Unauthenticated verifies that hitting the endpoint
// without auth context returns 401.
func TestGetWorkflowSchema_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Auth middleware that does NOT set org_id (simulates missing auth)
	noAuth := func(c *gin.Context) { c.Next() }
	fakeRequireRole := func(roles ...string) gin.HandlerFunc {
		return func(c *gin.Context) { c.Next() }
	}

	handler := &Handler{
		engine: makeEngine(db, nil),
		repo:   NewRepository(db),
		logger: slog.Default(),
		db:     db,
	}
	handler.RegisterRoutes(router, noAuth, fakeRequireRole)

	req := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code, "should return 401 without org context")
}

// TestGetWorkflowSchema_CrossOrgIsolation verifies that org A cannot see org B's data.
func TestGetWorkflowSchema_CrossOrgIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgA := uuid.New()
	orgB := uuid.New()

	// Seed tables
	db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, position INT DEFAULT 0, color TEXT DEFAULT '#000', is_won BOOLEAN DEFAULT FALSE, is_lost BOOLEAN DEFAULT FALSE, created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS tags (id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, color TEXT DEFAULT '#000', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY, org_id UUID, email TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '', full_name TEXT DEFAULT '', role TEXT DEFAULT 'viewer', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_users (user_id UUID NOT NULL, org_id UUID NOT NULL, role_id UUID NOT NULL, status TEXT DEFAULT 'active', joined_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ, PRIMARY KEY (user_id, org_id))`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_settings (org_id UUID PRIMARY KEY, custom_field_defs JSONB DEFAULT '[]', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW())`)
	db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (id UUID PRIMARY KEY, org_id UUID NOT NULL, slug TEXT, label TEXT, label_plural TEXT, icon TEXT DEFAULT '📦', fields JSONB DEFAULT '[]', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)

	// Org A: has stages, tags, users
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'Org A Stage', 0, '#AAA')`, uuid.New(), orgA)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'Org A Tag', '#AAA')`, uuid.New(), orgA)
	userA := uuid.New()
	db.Exec(`INSERT INTO users (id, org_id, email, first_name) VALUES (?, ?, 'usera@test.com', 'UserA')`, userA, orgA)
	db.Exec(`INSERT INTO org_users (user_id, org_id, role_id) VALUES (?, ?, ?)`, userA, orgA, uuid.New())

	// Org B: has different stages, tags, users
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'Org B Stage', 0, '#BBB')`, uuid.New(), orgB)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'Org B Tag', '#BBB')`, uuid.New(), orgB)
	userB := uuid.New()
	db.Exec(`INSERT INTO users (id, org_id, email, first_name) VALUES (?, ?, 'userb@test.com', 'UserB')`, userB, orgB)
	db.Exec(`INSERT INTO org_users (user_id, org_id, role_id) VALUES (?, ?, ?)`, userB, orgB, uuid.New())

	// --- Request as Org A ---
	gin.SetMode(gin.TestMode)
	router := gin.New()
	authA := func(c *gin.Context) {
		c.Set("org_id", orgA)
		c.Set("user_id", userA)
		c.Next()
	}
	handler := &Handler{engine: makeEngine(db, nil), repo: NewRepository(db), logger: slog.Default(), db: db}
	handler.RegisterRoutes(router, authA, func(roles ...string) gin.HandlerFunc { return func(c *gin.Context) { c.Next() } })

	req := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var resp struct{ Data SchemaResponse `json:"data"` }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	// Should see ONLY org A data
	require.Len(t, resp.Data.Stages, 1)
	assert.Equal(t, "Org A Stage", resp.Data.Stages[0].Name)

	require.Len(t, resp.Data.Tags, 1)
	assert.Equal(t, "Org A Tag", resp.Data.Tags[0].Name)

	require.Len(t, resp.Data.Users, 1)
	assert.Equal(t, "usera@test.com", resp.Data.Users[0].Email)
}

// findField is a helper to look up a SchemaField by path.
func findField(fields []SchemaField, path string) *SchemaField {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
}


