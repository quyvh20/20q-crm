package automation

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// context_hydrate_db_test.go covers the deal-trigger contact-context hydration:
// a deal event carries only deal.contact_id, so buildEvalContext loads the deal's
// contact so {{contact.email}} (and friends) resolve the same as a contact
// trigger. Docker-gated (needs the contacts table); skips in short mode.

func TestBuildEvalContext_HydratesDealContact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// The minimal test contacts table omits these production columns that
	// loadContactForTrigger selects; add them (p8 test convention).
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)

	orgID := uuid.New()
	contactID := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email, phone) VALUES (?, ?, 'Jane', 'Doe', 'jane@acme.com', '+1555')`,
		contactID, orgID).Error)

	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	// A deal-trigger context as emitted by the delivery layer / Run Now: it has
	// deal.contact_id but NO hydrated contact object.
	triggerCtx := datatypes.JSON(`{
		"entity_id": "` + uuid.New().String() + `",
		"deal": {"id": "` + uuid.New().String() + `", "title": "Acme renewal", "contact_id": "` + contactID.String() + `"},
		"trigger": {"type": "deal_stage_changed"}
	}`)
	run := &WorkflowRun{OrgID: orgID, TriggerContext: triggerCtx}

	ctx := engine.buildEvalContext(run)

	require.NotNil(t, ctx.Contact, "the deal's contact must be hydrated into the eval context")
	assert.Equal(t, "jane@acme.com", ctx.Contact["email"], "{{contact.email}} must resolve for a deal trigger")
	assert.Equal(t, "Jane", ctx.Contact["first_name"])
	assert.Equal(t, "Doe", ctx.Contact["last_name"])
	assert.Equal(t, contactID.String(), ctx.Contact["id"])
	// The deal is still present alongside the hydrated contact.
	require.NotNil(t, ctx.Deal)
	assert.Equal(t, "Acme renewal", ctx.Deal["title"])

	// Resolving a template proves the end-to-end fix (the reported failure was an
	// email action with To = {{contact.email}} rendering empty).
	assert.Equal(t, "jane@acme.com", InterpolateTemplate("{{contact.email}}", ctx))
}

// A deal trigger whose deal references a MISSING/deleted contact leaves contact.*
// empty (best-effort) rather than erroring or fabricating a bogus contact.
func TestBuildEvalContext_MissingContact_LeavesEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := setupTestDB(t)
	defer cleanup()
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE contacts ADD COLUMN IF NOT EXISTS company_id UUID`).Error)

	orgID := uuid.New()
	engine := makeEngine(db, map[string]ActionExecutor{})
	defer engine.cancel()

	triggerCtx := datatypes.JSON(`{
		"deal": {"id": "` + uuid.New().String() + `", "contact_id": "` + uuid.New().String() + `"},
		"trigger": {"type": "deal_stage_changed"}
	}`)
	run := &WorkflowRun{OrgID: orgID, TriggerContext: triggerCtx}

	ctx := engine.buildEvalContext(run)
	assert.Nil(t, ctx.Contact, "a deal pointing at a missing contact must not fabricate a contact")
	assert.Equal(t, "", InterpolateTemplate("{{contact.email}}", ctx), "unresolved contact.email renders empty, not an error")
}

// A contact trigger (contact already present) is untouched — hydration only fills
// a MISSING contact, never overrides one the event already carried. No DB needed:
// the contact is present so the hydration branch (and its e.db read) is skipped.
func TestBuildEvalContext_ExistingContactNotOverridden(t *testing.T) {
	engine := &Engine{ctx: context.Background()} // db unused: hydration is skipped when contact is present

	triggerCtx := datatypes.JSON(`{
		"contact": {"id": "abc", "email": "event@acme.com"},
		"deal": {"contact_id": "` + uuid.New().String() + `"}
	}`)
	run := &WorkflowRun{OrgID: uuid.New(), TriggerContext: triggerCtx}

	ctx := engine.buildEvalContext(run)
	require.NotNil(t, ctx.Contact)
	assert.Equal(t, "event@acme.com", ctx.Contact["email"], "an event-provided contact must not be overwritten by hydration")
}
