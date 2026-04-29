package automation

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

// TestGetWorkflowSchema_NoNPlus1_QueryCount verifies that a single
// GET /api/workflows/schema request issues at most 6 SQL queries,
// regardless of how many stages, tags, users, or custom objects exist.
// This catches N+1 regressions (e.g., querying per-stage or per-tag).
func TestGetWorkflowSchema_NoNPlus1_QueryCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	// --- Seed tables with MANY rows to expose any N+1 ---
	db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, position INT DEFAULT 0,
		color TEXT DEFAULT '#000', is_won BOOLEAN DEFAULT FALSE, is_lost BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, color TEXT DEFAULT '#000',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY, org_id UUID, email TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '',
		full_name TEXT DEFAULT '', role TEXT DEFAULT 'viewer',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL, org_id UUID NOT NULL, role_id UUID NOT NULL,
		status TEXT DEFAULT 'active', joined_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id))`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_settings (
		org_id UUID PRIMARY KEY, custom_field_defs JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW())`)
	db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, slug TEXT, label TEXT, label_plural TEXT,
		icon TEXT DEFAULT '📦', fields JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)

	// Insert 10 pipeline stages
	for i := 0; i < 10; i++ {
		db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, ?, ?, ?)`,
			uuid.New(), orgID, fmt.Sprintf("Stage %d", i), i, fmt.Sprintf("#%02x%02x%02x", i*25, 100, 200))
	}
	// Insert 10 tags
	for i := 0; i < 10; i++ {
		db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, ?, ?)`,
			uuid.New(), orgID, fmt.Sprintf("Tag %d", i), fmt.Sprintf("#%02x%02x%02x", 200, i*25, 100))
	}
	// Insert 10 users
	for i := 0; i < 10; i++ {
		uid := uuid.New()
		db.Exec(`INSERT INTO users (id, org_id, email, first_name, last_name) VALUES (?, ?, ?, ?, ?)`,
			uid, orgID, fmt.Sprintf("user%d@test.com", i), fmt.Sprintf("User%d", i), "Test")
		db.Exec(`INSERT INTO org_users (user_id, org_id, role_id) VALUES (?, ?, ?)`, uid, orgID, uuid.New())
	}
	// Insert 5 custom objects with 3 fields each
	for i := 0; i < 5; i++ {
		fields := fmt.Sprintf(`[{"key":"f1","label":"Field 1","type":"string"},{"key":"f2","label":"Field 2","type":"number"},{"key":"f3","label":"Field 3","type":"boolean"}]`)
		db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug, label, label_plural, icon, fields) VALUES (?, ?, ?, ?, ?, '📦', ?::jsonb)`,
			uuid.New(), orgID, fmt.Sprintf("obj_%d", i), fmt.Sprintf("Object %d", i), fmt.Sprintf("Objects %d", i), fields)
	}
	// Insert org settings with custom field defs
	customFields := `[
		{"key":"source","label":"Source","type":"select","entity_type":"contact","options":["Web","Ads"]},
		{"key":"tier","label":"Tier","type":"string","entity_type":"deal","options":null}
	]`
	db.Exec(`INSERT INTO org_settings (org_id, custom_field_defs) VALUES (?, ?::jsonb)`, orgID, customFields)

	// --- Register a GORM callback to count SQL queries ---
	var queryCount int64
	const callbackName = "test:count_queries"
	db.Callback().Query().After("gorm:query").Register(callbackName, func(d *gorm.DB) {
		atomic.AddInt64(&queryCount, 1)
	})
	db.Callback().Raw().After("gorm:raw").Register(callbackName, func(d *gorm.DB) {
		atomic.AddInt64(&queryCount, 1)
	})
	defer func() {
		// Clean up callbacks to avoid affecting other tests
		db.Callback().Query().Remove(callbackName)
		db.Callback().Raw().Remove(callbackName)
	}()

	// --- Build handler + router ---
	gin.SetMode(gin.TestMode)
	router := gin.New()
	userID := uuid.New()
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

	// --- Reset counter and fire the request ---
	atomic.StoreInt64(&queryCount, 0)

	req := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "expected 200 OK, got %d: %s", w.Code, w.Body.String())

	// --- Assert: at most 6 queries (currently 5: org_settings, custom_object_defs, pipeline_stages, tags, users+org_users) ---
	finalCount := atomic.LoadInt64(&queryCount)
	t.Logf("Schema endpoint executed %d SQL queries (with 10 stages, 10 tags, 10 users, 5 custom objects)", finalCount)

	assert.LessOrEqual(t, finalCount, int64(6),
		"Schema endpoint must issue ≤ 6 SQL queries (no N+1). Got %d queries.", finalCount)

	// Also verify the response actually contains all the seeded data
	var resp struct {
		Data SchemaResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data.Stages, 10, "should return all 10 stages")
	assert.Len(t, resp.Data.Tags, 10, "should return all 10 tags")
	assert.Len(t, resp.Data.Users, 10, "should return all 10 users")
	assert.Len(t, resp.Data.CustomObjects, 5, "should return all 5 custom objects")
}

// TestGetWorkflowSchema_CacheHitAndInvalidate verifies:
// 1. Second request hits cache → 0 SQL queries
// 2. InvalidateSchemaCache → next request queries DB again
func TestGetWorkflowSchema_CacheHitAndInvalidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	// Seed minimal data
	db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, position INT DEFAULT 0,
		color TEXT DEFAULT '#000', is_won BOOLEAN DEFAULT FALSE, is_lost BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, color TEXT DEFAULT '#000',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY, org_id UUID, email TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '',
		full_name TEXT DEFAULT '', role TEXT DEFAULT 'viewer',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL, org_id UUID NOT NULL, role_id UUID NOT NULL,
		status TEXT DEFAULT 'active', joined_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id))`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_settings (
		org_id UUID PRIMARY KEY, custom_field_defs JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW())`)
	db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, slug TEXT, label TEXT, label_plural TEXT,
		icon TEXT DEFAULT '📦', fields JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)

	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'Lead', 0, '#3B82F6')`, uuid.New(), orgID)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'VIP', '#F59E0B')`, uuid.New(), orgID)

	// Register SQL counter callbacks
	var queryCount int64
	const callbackName = "test:cache_query_count"
	db.Callback().Query().After("gorm:query").Register(callbackName, func(d *gorm.DB) {
		atomic.AddInt64(&queryCount, 1)
	})
	db.Callback().Raw().After("gorm:raw").Register(callbackName, func(d *gorm.DB) {
		atomic.AddInt64(&queryCount, 1)
	})
	defer func() {
		db.Callback().Query().Remove(callbackName)
		db.Callback().Raw().Remove(callbackName)
	}()

	// Build handler with cache
	gin.SetMode(gin.TestMode)
	router := gin.New()
	userID := uuid.New()
	fakeAuth := func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	}
	fakeRequireRole := func(roles ...string) gin.HandlerFunc {
		return func(c *gin.Context) { c.Next() }
	}
	handler := &Handler{
		engine:      makeEngine(db, nil),
		repo:        NewRepository(db),
		logger:      slog.Default(),
		db:          db,
		schemaCache: NewSchemaCache(60 * time.Second),
	}
	handler.RegisterRoutes(router, fakeAuth, fakeRequireRole)

	// --- Request 1: cold cache (DB hit) ---
	atomic.StoreInt64(&queryCount, 0)
	req1 := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusOK, w1.Code)

	coldQueries := atomic.LoadInt64(&queryCount)
	t.Logf("Request 1 (cold cache): %d SQL queries", coldQueries)
	assert.Greater(t, coldQueries, int64(0), "cold cache should hit DB")

	// --- Request 2: warm cache (zero DB queries) ---
	atomic.StoreInt64(&queryCount, 0)
	req2 := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	warmQueries := atomic.LoadInt64(&queryCount)
	t.Logf("Request 2 (warm cache): %d SQL queries", warmQueries)
	assert.Equal(t, int64(0), warmQueries, "warm cache should issue 0 SQL queries")

	// Verify same response body
	assert.Equal(t, w1.Body.String(), w2.Body.String(), "cached response should match original")

	// --- Invalidate cache ---
	handler.InvalidateSchemaCache(orgID)

	// --- Request 3: after invalidation (DB hit again) ---
	atomic.StoreInt64(&queryCount, 0)
	req3 := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusOK, w3.Code)

	postInvalidateQueries := atomic.LoadInt64(&queryCount)
	t.Logf("Request 3 (after invalidate): %d SQL queries", postInvalidateQueries)
	assert.Greater(t, postInvalidateQueries, int64(0), "after invalidation should hit DB again")

	// --- Add a new stage and verify invalidation picks it up ---
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'Closed Won', 1, '#10B981')`, uuid.New(), orgID)
	handler.InvalidateSchemaCache(orgID)

	req4 := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w4 := httptest.NewRecorder()
	router.ServeHTTP(w4, req4)
	require.Equal(t, http.StatusOK, w4.Code)

	var resp struct {
		Data SchemaResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &resp))
	assert.Len(t, resp.Data.Stages, 2, "after invalidation + new stage insert, should see 2 stages")
}

// TestSchemaEndpoint_ReturnsAllCategories asserts that the schema response
// contains all 7 categories:
//  1. entities[].key = "contact"   (built-in)
//  2. entities[].key = "deal"      (built-in)
//  3. entities[].key = "trigger"   (built-in)
//  4. custom_objects               (org-scoped)
//  5. stages                       (org-scoped)
//  6. tags                         (org-scoped)
//  7. users                        (org-scoped)
func TestSchemaEndpoint_ReturnsAllCategories(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgID := uuid.New()

	// --- Seed all tables so every category is populated ---
	db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, position INT DEFAULT 0,
		color TEXT DEFAULT '#000', is_won BOOLEAN DEFAULT FALSE, is_lost BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, color TEXT DEFAULT '#000',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY, org_id UUID, email TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '',
		full_name TEXT DEFAULT '', role TEXT DEFAULT 'viewer',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL, org_id UUID NOT NULL, role_id UUID NOT NULL,
		status TEXT DEFAULT 'active', joined_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id))`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_settings (
		org_id UUID PRIMARY KEY, custom_field_defs JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW())`)
	db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, slug TEXT, label TEXT, label_plural TEXT,
		icon TEXT DEFAULT '📦', fields JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)

	// Seed 1 row per category
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'Lead', 0, '#3B82F6')`, uuid.New(), orgID)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'VIP', '#F59E0B')`, uuid.New(), orgID)
	userID := uuid.New()
	db.Exec(`INSERT INTO users (id, org_id, email, first_name) VALUES (?, ?, 'alice@test.com', 'Alice')`, userID, orgID)
	db.Exec(`INSERT INTO org_users (user_id, org_id, role_id) VALUES (?, ?, ?)`, userID, orgID, uuid.New())
	db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug, label, label_plural, fields) VALUES (?, ?, 'ticket', 'Ticket', 'Tickets', '[{"key":"priority","label":"Priority","type":"select","options":["Low","High"]}]'::jsonb)`, uuid.New(), orgID)
	db.Exec(`INSERT INTO org_settings (org_id, custom_field_defs) VALUES (?, '[{"key":"source","label":"Source","type":"string","entity_type":"contact"}]'::jsonb)`, orgID)

	// --- Build handler + router ---
	gin.SetMode(gin.TestMode)
	router := gin.New()
	fakeAuth := func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	}
	handler := &Handler{
		engine:      makeEngine(db, nil),
		repo:        NewRepository(db),
		logger:      slog.Default(),
		db:          db,
		schemaCache: NewSchemaCache(60 * time.Second),
	}
	handler.RegisterRoutes(router, fakeAuth, func(roles ...string) gin.HandlerFunc {
		return func(c *gin.Context) { c.Next() }
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp struct {
		Data SchemaResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	schema := resp.Data

	// ==========================================================
	// Category 1-3: Built-in entities (contact, deal, trigger)
	// ==========================================================
	require.GreaterOrEqual(t, len(schema.Entities), 3,
		"entities must contain at least 3 built-in categories (contact, deal, trigger)")

	entityKeys := map[string]*SchemaEntity{}
	for i := range schema.Entities {
		entityKeys[schema.Entities[i].Key] = &schema.Entities[i]
	}

	// --- Category 1: Contact ---
	contact, ok := entityKeys["contact"]
	require.True(t, ok, "entities must include 'contact'")
	assert.Equal(t, "Contact", contact.Label)
	assert.Equal(t, "👤", contact.Icon)
	assert.GreaterOrEqual(t, len(contact.Fields), 8, "contact should have ≥8 built-in fields")
	// Verify custom field was appended
	contactPaths := map[string]bool{}
	for _, f := range contact.Fields {
		contactPaths[f.Path] = true
	}
	assert.True(t, contactPaths["contact.custom_fields.source"],
		"custom field 'source' should be appended to contact entity")

	// --- Category 2: Deal ---
	deal, ok := entityKeys["deal"]
	require.True(t, ok, "entities must include 'deal'")
	assert.Equal(t, "Deal", deal.Label)
	assert.Equal(t, "💰", deal.Icon)
	assert.GreaterOrEqual(t, len(deal.Fields), 8, "deal should have ≥8 built-in fields")

	// --- Category 3: Trigger ---
	trigger, ok := entityKeys["trigger"]
	require.True(t, ok, "entities must include 'trigger'")
	assert.Equal(t, "Trigger Event", trigger.Label)
	assert.Equal(t, "⚡", trigger.Icon)
	assert.GreaterOrEqual(t, len(trigger.Fields), 3, "trigger should have ≥3 fields (type, from_stage, to_stage)")

	// ==========================================================
	// Category 4: Custom Objects
	// ==========================================================
	require.GreaterOrEqual(t, len(schema.CustomObjects), 1,
		"custom_objects must have ≥1 entry")
	assert.Equal(t, "ticket", schema.CustomObjects[0].Key)
	assert.Equal(t, "Ticket", schema.CustomObjects[0].Label)
	assert.GreaterOrEqual(t, len(schema.CustomObjects[0].Fields), 1)

	// ==========================================================
	// Category 5: Pipeline Stages
	// ==========================================================
	require.GreaterOrEqual(t, len(schema.Stages), 1,
		"stages must have ≥1 entry")
	assert.Equal(t, "Lead", schema.Stages[0].Name)
	assert.NotEmpty(t, schema.Stages[0].ID, "stage must have an ID")
	assert.NotEmpty(t, schema.Stages[0].Color, "stage must have a color")
	assert.Equal(t, 0, schema.Stages[0].Order, "first stage should have order=0")

	// ==========================================================
	// Category 6: Tags
	// ==========================================================
	require.GreaterOrEqual(t, len(schema.Tags), 1,
		"tags must have ≥1 entry")
	assert.Equal(t, "VIP", schema.Tags[0].Name)
	assert.NotEmpty(t, schema.Tags[0].ID, "tag must have an ID")
	assert.NotEmpty(t, schema.Tags[0].Color, "tag must have a color")

	// ==========================================================
	// Category 7: Users (org members)
	// ==========================================================
	require.GreaterOrEqual(t, len(schema.Users), 1,
		"users must have ≥1 entry")
	assert.Equal(t, "alice@test.com", schema.Users[0].Email)
	assert.NotEmpty(t, schema.Users[0].ID, "user must have an ID")
	assert.NotEmpty(t, schema.Users[0].Name, "user must have a name")

	// ==========================================================
	// Summary: all 7 categories accounted for
	// ==========================================================
	t.Logf("All 7 categories present: contact(%d fields), deal(%d fields), trigger(%d fields), "+
		"custom_objects(%d), stages(%d), tags(%d), users(%d)",
		len(contact.Fields), len(deal.Fields), len(trigger.Fields),
		len(schema.CustomObjects), len(schema.Stages), len(schema.Tags), len(schema.Users))
}

// TestSchemaEndpoint_ScopedByOrg verifies that org A does NOT see
// org B's custom fields, custom objects, stages, tags, or users.
// Covers all 5 org-scoped categories — the 3 built-in entities are
// static and identical for every org, so they are excluded from this test.
func TestSchemaEndpoint_ScopedByOrg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	orgA := uuid.New()
	orgB := uuid.New()

	// --- Create all tables ---
	db.Exec(`CREATE TABLE IF NOT EXISTS pipeline_stages (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, position INT DEFAULT 0,
		color TEXT DEFAULT '#000', is_won BOOLEAN DEFAULT FALSE, is_lost BOOLEAN DEFAULT FALSE,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, name TEXT, color TEXT DEFAULT '#000',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id UUID PRIMARY KEY, org_id UUID, email TEXT, first_name TEXT DEFAULT '', last_name TEXT DEFAULT '',
		full_name TEXT DEFAULT '', role TEXT DEFAULT 'viewer',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_users (
		user_id UUID NOT NULL, org_id UUID NOT NULL, role_id UUID NOT NULL,
		status TEXT DEFAULT 'active', joined_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
		PRIMARY KEY (user_id, org_id))`)
	db.Exec(`CREATE TABLE IF NOT EXISTS org_settings (
		org_id UUID PRIMARY KEY, custom_field_defs JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW())`)
	db.Exec(`CREATE TABLE IF NOT EXISTS custom_object_defs (
		id UUID PRIMARY KEY, org_id UUID NOT NULL, slug TEXT, label TEXT, label_plural TEXT,
		icon TEXT DEFAULT '📦', fields JSONB DEFAULT '[]',
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ)`)

	// --- Seed Org A data ---
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'A-Lead', 0, '#A00')`, uuid.New(), orgA)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'A-VIP', '#A11')`, uuid.New(), orgA)
	userA := uuid.New()
	db.Exec(`INSERT INTO users (id, org_id, email, first_name) VALUES (?, ?, 'alice@orga.com', 'Alice')`, userA, orgA)
	db.Exec(`INSERT INTO org_users (user_id, org_id, role_id) VALUES (?, ?, ?)`, userA, orgA, uuid.New())
	db.Exec(`INSERT INTO org_settings (org_id, custom_field_defs) VALUES (?, '[{"key":"industry","label":"Industry","type":"select","entity_type":"contact","options":["Tech","Finance"]}]'::jsonb)`, orgA)
	db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug, label, label_plural, fields) VALUES (?, ?, 'project', 'Project', 'Projects', '[{"key":"status","label":"Status","type":"string"}]'::jsonb)`, uuid.New(), orgA)

	// --- Seed Org B data (different everything) ---
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'B-Discovery', 0, '#B00')`, uuid.New(), orgB)
	db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, color) VALUES (?, ?, 'B-Closed', 1, '#B11')`, uuid.New(), orgB)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'B-Enterprise', '#B22')`, uuid.New(), orgB)
	db.Exec(`INSERT INTO tags (id, org_id, name, color) VALUES (?, ?, 'B-Partner', '#B33')`, uuid.New(), orgB)
	userB := uuid.New()
	db.Exec(`INSERT INTO users (id, org_id, email, first_name) VALUES (?, ?, 'bob@orgb.com', 'Bob')`, userB, orgB)
	db.Exec(`INSERT INTO org_users (user_id, org_id, role_id) VALUES (?, ?, ?)`, userB, orgB, uuid.New())
	db.Exec(`INSERT INTO org_settings (org_id, custom_field_defs) VALUES (?, '[{"key":"segment","label":"Segment","type":"string","entity_type":"deal"},{"key":"region","label":"Region","type":"select","entity_type":"contact","options":["NA","EMEA","APAC"]}]'::jsonb)`, orgB)
	db.Exec(`INSERT INTO custom_object_defs (id, org_id, slug, label, label_plural, fields) VALUES (?, ?, 'subscription', 'Subscription', 'Subscriptions', '[{"key":"plan","label":"Plan","type":"select","options":["Free","Pro"]}]'::jsonb)`, uuid.New(), orgB)

	// --- Helper: fetch schema as a given org ---
	fetchSchema := func(orgID, userID uuid.UUID) SchemaResponse {
		gin.SetMode(gin.TestMode)
		router := gin.New()
		auth := func(c *gin.Context) {
			c.Set("org_id", orgID)
			c.Set("user_id", userID)
			c.Next()
		}
		handler := &Handler{
			engine:      makeEngine(db, nil),
			repo:        NewRepository(db),
			logger:      slog.Default(),
			db:          db,
			schemaCache: NewSchemaCache(60 * time.Second),
		}
		handler.RegisterRoutes(router, auth, func(roles ...string) gin.HandlerFunc {
			return func(c *gin.Context) { c.Next() }
		})

		req := httptest.NewRequest(http.MethodGet, "/api/workflows/schema", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

		var resp struct {
			Data SchemaResponse `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		return resp.Data
	}

	// ==========================================================
	// Request as Org A
	// ==========================================================
	schemaA := fetchSchema(orgA, userA)

	// --- Stages: only A-Lead ---
	require.Len(t, schemaA.Stages, 1, "org A should see exactly 1 stage")
	assert.Equal(t, "A-Lead", schemaA.Stages[0].Name)

	// --- Tags: only A-VIP ---
	require.Len(t, schemaA.Tags, 1, "org A should see exactly 1 tag")
	assert.Equal(t, "A-VIP", schemaA.Tags[0].Name)

	// --- Users: only Alice ---
	require.Len(t, schemaA.Users, 1, "org A should see exactly 1 user")
	assert.Equal(t, "alice@orga.com", schemaA.Users[0].Email)

	// --- Custom Objects: only 'project' ---
	require.Len(t, schemaA.CustomObjects, 1, "org A should see exactly 1 custom object")
	assert.Equal(t, "project", schemaA.CustomObjects[0].Key)

	// --- Custom Fields: only 'industry' appended to contact ---
	contactA := findEntity(schemaA.Entities, "contact")
	require.NotNil(t, contactA)
	customFieldPathsA := collectCustomFieldPaths(contactA.Fields)
	assert.Contains(t, customFieldPathsA, "contact.custom_fields.industry",
		"org A should have custom field 'industry' on contact")
	assert.NotContains(t, customFieldPathsA, "contact.custom_fields.region",
		"org A must NOT see org B's custom field 'region'")

	dealA := findEntity(schemaA.Entities, "deal")
	require.NotNil(t, dealA)
	customFieldPathsDealA := collectCustomFieldPaths(dealA.Fields)
	assert.NotContains(t, customFieldPathsDealA, "deal.custom_fields.segment",
		"org A must NOT see org B's deal custom field 'segment'")

	// ==========================================================
	// Request as Org B
	// ==========================================================
	schemaB := fetchSchema(orgB, userB)

	// --- Stages: B-Discovery and B-Closed ---
	require.Len(t, schemaB.Stages, 2, "org B should see exactly 2 stages")
	stageNamesB := []string{schemaB.Stages[0].Name, schemaB.Stages[1].Name}
	assert.Contains(t, stageNamesB, "B-Discovery")
	assert.Contains(t, stageNamesB, "B-Closed")

	// --- Tags: B-Enterprise and B-Partner ---
	require.Len(t, schemaB.Tags, 2, "org B should see exactly 2 tags")
	tagNamesB := []string{schemaB.Tags[0].Name, schemaB.Tags[1].Name}
	assert.Contains(t, tagNamesB, "B-Enterprise")
	assert.Contains(t, tagNamesB, "B-Partner")

	// --- Users: only Bob ---
	require.Len(t, schemaB.Users, 1, "org B should see exactly 1 user")
	assert.Equal(t, "bob@orgb.com", schemaB.Users[0].Email)

	// --- Custom Objects: only 'subscription' ---
	require.Len(t, schemaB.CustomObjects, 1, "org B should see exactly 1 custom object")
	assert.Equal(t, "subscription", schemaB.CustomObjects[0].Key)

	// --- Custom Fields: 'region' on contact, 'segment' on deal ---
	contactB := findEntity(schemaB.Entities, "contact")
	require.NotNil(t, contactB)
	customFieldPathsB := collectCustomFieldPaths(contactB.Fields)
	assert.Contains(t, customFieldPathsB, "contact.custom_fields.region",
		"org B should have custom field 'region' on contact")
	assert.NotContains(t, customFieldPathsB, "contact.custom_fields.industry",
		"org B must NOT see org A's custom field 'industry'")

	dealB := findEntity(schemaB.Entities, "deal")
	require.NotNil(t, dealB)
	customFieldPathsDealB := collectCustomFieldPaths(dealB.Fields)
	assert.Contains(t, customFieldPathsDealB, "deal.custom_fields.segment",
		"org B should have custom field 'segment' on deal")

	t.Logf("Org isolation verified: A sees %d stages, %d tags, %d users, %d objects, %d custom fields; "+
		"B sees %d stages, %d tags, %d users, %d objects, %d custom fields",
		len(schemaA.Stages), len(schemaA.Tags), len(schemaA.Users), len(schemaA.CustomObjects), len(customFieldPathsA),
		len(schemaB.Stages), len(schemaB.Tags), len(schemaB.Users), len(schemaB.CustomObjects), len(customFieldPathsB)+len(customFieldPathsDealB))
}

// findEntity looks up a SchemaEntity by key within a slice.
func findEntity(entities []SchemaEntity, key string) *SchemaEntity {
	for i := range entities {
		if entities[i].Key == key {
			return &entities[i]
		}
	}
	return nil
}

// collectCustomFieldPaths filters field paths that contain "custom_fields".
func collectCustomFieldPaths(fields []SchemaField) []string {
	var paths []string
	for _, f := range fields {
		if len(f.Path) > 0 && contains(f.Path, "custom_fields") {
			paths = append(paths, f.Path)
		}
	}
	return paths
}

// contains checks if a string contains a substring (avoids importing strings).
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
