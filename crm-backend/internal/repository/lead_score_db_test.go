package repository

import (
	"context"
	"testing"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// lead_score_db_test.go exercises the R9.4 engine against real Postgres.
//
// A golden-SQL unit test would not do: the whole design rests on ONE compiled
// expression actually running — the bind-argument order across the SET
// expression and the WHERE, the correlated EXISTS against the event ledger, and
// the clamp are all things that either execute correctly or do not, and none of
// them is visible in a string comparison.

func setupLeadScoreSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE organizations (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		name VARCHAR(255) NOT NULL DEFAULT '',
		lead_score_run_at TIMESTAMPTZ,
		lead_score_cursor UUID,
		deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE contacts (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		first_name VARCHAR(100) NOT NULL DEFAULT '',
		last_name VARCHAR(100) NOT NULL DEFAULT '',
		email VARCHAR(255),
		phone VARCHAR(50),
		company_id UUID,
		owner_user_id UUID,
		custom_fields JSONB DEFAULT '{}',
		lead_score INT NOT NULL DEFAULT 0,
		lead_score_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE marketing_email_events (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		event_type VARCHAR(50) NOT NULL,
		email_normalized VARCHAR(320) NOT NULL,
		occurred_at TIMESTAMPTZ,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE lead_scoring_rules (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		name VARCHAR(120) NOT NULL,
		kind VARCHAR(16) NOT NULL DEFAULT 'field',
		points INT NOT NULL DEFAULT 0,
		condition JSONB NOT NULL DEFAULT '{}',
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		position INT NOT NULL DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
	// record_shares is read by the row-access predicate the segment compiler
	// emits for a scoped caller. The rescore is callerless, but the table must
	// exist for the compiled SQL to parse.
	require.NoError(t, db.Exec(`CREATE TABLE record_shares (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL, record_type VARCHAR(50) NOT NULL, record_id UUID NOT NULL,
		target_type VARCHAR(20) NOT NULL, target_id UUID NOT NULL, level VARCHAR(20) NOT NULL,
		deleted_at TIMESTAMPTZ
	)`).Error)
}

// leadScoreCatalog is the field catalog the compiler resolves rules against —
// the same shape reportCatalogForDef produces for a contact, trimmed to what
// these tests reference.
func leadScoreCatalog() []domain.ReportField {
	return []domain.ReportField{
		{Key: "first_name", Label: "First Name", Type: "text", Column: "first_name"},
		{Key: "email", Label: "Email", Type: "text", Column: "email"},
		{Key: "lead_score", Label: "Lead Score", Type: "number", Column: "lead_score"},
	}
}

func seedLeadContact(t *testing.T, db *gorm.DB, orgID uuid.UUID, first, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, email) VALUES (?, ?, ?, ?)`,
		id, orgID, first, email).Error)
	return id
}

func contactScore(t *testing.T, db *gorm.DB, id uuid.UUID) int {
	t.Helper()
	var score int
	require.NoError(t, db.Raw(`SELECT lead_score FROM contacts WHERE id = ?`, id).Scan(&score).Error)
	return score
}

func TestLeadScoring_RecomputeOrg_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupLeadScoreSchema(t, db)

	org := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme')`, org).Error)
	// A second org proves the rescore is org-scoped: its contact must not move.
	otherOrg := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Other')`, otherOrg).Error)

	engaged := seedLeadContact(t, db, org, "Ada", "ada@example.com")
	quiet := seedLeadContact(t, db, org, "Bob", "bob@example.com")
	noEmail := seedLeadContact(t, db, org, "Ada", "")
	stranger := seedLeadContact(t, db, otherOrg, "Ada", "ada@example.com")

	// Ada clicked twice, recently. Bob's click is outside every window.
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Exec(
			`INSERT INTO marketing_email_events (org_id, event_type, email_normalized, occurred_at)
			 VALUES (?, 'email.clicked', 'ada@example.com', NOW() - make_interval(days => 2))`, org).Error)
	}
	require.NoError(t, db.Exec(
		`INSERT INTO marketing_email_events (org_id, event_type, email_normalized, occurred_at)
		 VALUES (?, 'email.clicked', 'bob@example.com', NOW() - make_interval(days => 400))`, org).Error)

	repo := NewLeadScoringRepository(db)
	catalog := leadScoreCatalog()

	rules := []domain.LeadScoringRule{
		{
			ID: uuid.New(), OrgID: org, Name: "Named Ada", Kind: domain.LeadRuleKindField, Points: 30, Enabled: true,
			Condition: domain.JSON(`{"field":"first_name","operator":"eq","value":"Ada"}`),
		},
		{
			ID: uuid.New(), OrgID: org, Name: "Clicked recently", Kind: domain.LeadRuleKindEngagement, Points: 25, Enabled: true,
			Condition: domain.JSON(`{"event":"email.clicked","window_days":30,"min_count":2}`),
		},
		{
			// Disabled rules must contribute nothing, or toggling one off in the
			// UI would appear to do nothing until the next edit.
			ID: uuid.New(), OrgID: org, Name: "Disabled", Kind: domain.LeadRuleKindField, Points: 99, Enabled: false,
			Condition: domain.JSON(`{"field":"first_name","operator":"eq","value":"Ada"}`),
		},
	}

	n, skipped, err := repo.RecomputeOrg(context.Background(), org, catalog, rules)
	require.Empty(t, skipped, "no rule should have been skipped")
	require.NoError(t, err)
	assert.EqualValues(t, 3, n, "every live contact in the org is rescored")

	assert.Equal(t, 55, contactScore(t, db, engaged), "30 (field) + 25 (engagement)")
	assert.Equal(t, 0, contactScore(t, db, quiet), "Bob matches neither rule — his click is outside the window")
	assert.Equal(t, 30, contactScore(t, db, noEmail), "an email-less contact still matches field rules, and cannot match engagement ones")
	assert.Equal(t, 0, contactScore(t, db, stranger), "another org's contact must never be touched")
}

func TestLeadScoring_ClampsAndNegativePoints_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupLeadScoreSchema(t, db)

	org := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme')`, org).Error)
	hot := seedLeadContact(t, db, org, "Ada", "ada@example.com")
	cold := seedLeadContact(t, db, org, "Zed", "zed@example.com")

	repo := NewLeadScoringRepository(db)
	rules := []domain.LeadScoringRule{
		{ID: uuid.New(), OrgID: org, Name: "A", Kind: domain.LeadRuleKindField, Points: 80, Enabled: true,
			Condition: domain.JSON(`{"field":"first_name","operator":"eq","value":"Ada"}`)},
		{ID: uuid.New(), OrgID: org, Name: "B", Kind: domain.LeadRuleKindField, Points: 60, Enabled: true,
			Condition: domain.JSON(`{"field":"email","operator":"eq","value":"ada@example.com"}`)},
		// Negative points are the highest-signal rules available (bounced,
		// unsubscribed), so they must survive the clamp at the SUM.
		{ID: uuid.New(), OrgID: org, Name: "Penalty", Kind: domain.LeadRuleKindField, Points: -50, Enabled: true,
			Condition: domain.JSON(`{"field":"first_name","operator":"eq","value":"Zed"}`)},
	}

	_, _, err := repo.RecomputeOrg(context.Background(), org, leadScoreCatalog(), rules)
	require.NoError(t, err)

	assert.Equal(t, 100, contactScore(t, db, hot), "80+60=140 clamps to the 100 ceiling")
	assert.Equal(t, 0, contactScore(t, db, cold), "-50 clamps to the 0 floor, never negative")
}

// An empty condition must NOT award its points to everyone. An empty segment
// AST legitimately compiles to "" meaning "match all" — correct for an
// audience, catastrophic for a score.
func TestLeadScoring_EmptyConditionScoresNobody_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupLeadScoreSchema(t, db)

	org := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme')`, org).Error)
	c := seedLeadContact(t, db, org, "Ada", "ada@example.com")

	repo := NewLeadScoringRepository(db)
	_, _, err := repo.RecomputeOrg(context.Background(), org, leadScoreCatalog(), []domain.LeadScoringRule{
		{ID: uuid.New(), OrgID: org, Name: "Half-built", Kind: domain.LeadRuleKindField, Points: 50, Enabled: true,
			Condition: domain.JSON(`{}`)},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, contactScore(t, db, c), "a rule with no condition awards nothing, rather than everything")
}

func TestLeadScoring_OrgsDueForRescore_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupLeadScoreSchema(t, db)

	never := uuid.New()
	stale := uuid.New()
	fresh := uuid.New()
	gone := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'never')`, never).Error)
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name, lead_score_run_at) VALUES (?, 'stale', NOW() - make_interval(days => 2))`, stale).Error)
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name, lead_score_run_at) VALUES (?, 'fresh', NOW())`, fresh).Error)
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name, deleted_at) VALUES (?, 'gone', NOW())`, gone).Error)

	repo := NewLeadScoringRepository(db)
	due, err := repo.OrgsDueForRescore(context.Background(), time.Hour, 10)
	require.NoError(t, err)

	assert.Contains(t, due, never)
	assert.Contains(t, due, stale)
	assert.NotContains(t, due, fresh, "a recently scored org must not be re-swept")
	assert.NotContains(t, due, gone, "a deleted org must never be swept")
	require.NotEmpty(t, due)
	assert.Equal(t, never, due[0], "NULLS FIRST puts never-scored orgs at the front")

	// After marking, it drops out of the due set.
	require.NoError(t, repo.MarkOrgRescored(context.Background(), never))
	due2, err := repo.OrgsDueForRescore(context.Background(), time.Hour, 10)
	require.NoError(t, err)
	assert.NotContains(t, due2, never)
}

// A rule that no longer compiles must cost only ITS OWN points, never the whole
// workspace's scores.
//
// The path is mundane: an admin builds a rule on a custom field, then later
// deletes that field. The catalog is rebuilt per pass, so the key vanishes and
// the rule stops compiling. Aborting the expression there would return before a
// single UPDATE ran — every contact frozen at its last score, on every pass,
// forever, with nothing on screen to explain it.
func TestLeadScoring_UncompilableRuleIsSkippedNotFatal_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupLeadScoreSchema(t, db)

	org := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme')`, org).Error)
	c := seedLeadContact(t, db, org, "Ada", "ada@example.com")

	repo := NewLeadScoringRepository(db)
	rules := []domain.LeadScoringRule{
		// References a field that is not in the catalog — the shape left behind
		// when the custom field it named is deleted.
		{ID: uuid.New(), OrgID: org, Name: "Orphaned", Kind: domain.LeadRuleKindField, Points: 40, Enabled: true,
			Condition: domain.JSON(`{"field":"industry","operator":"eq","value":"Retail"}`)},
		// A perfectly good rule sitting behind it.
		{ID: uuid.New(), OrgID: org, Name: "Healthy", Kind: domain.LeadRuleKindField, Points: 25, Enabled: true,
			Condition: domain.JSON(`{"field":"first_name","operator":"eq","value":"Ada"}`)},
	}

	n, skipped, err := repo.RecomputeOrg(context.Background(), org, leadScoreCatalog(), rules)
	require.NoError(t, err, "one broken rule must not fail the pass")
	assert.EqualValues(t, 1, n, "the contact is still rescored")
	require.Len(t, skipped, 1, "the broken rule is reported, not swallowed")
	assert.Contains(t, skipped[0], "Orphaned", "the report names the rule so an admin can find it")
	assert.Equal(t, 25, contactScore(t, db, c), "the healthy rule still scores; only the broken rule's points are lost")
}

// An engagement rule must not match a blank-email contact against a blank-email
// event. lower(btrim('')) is '' on both sides, so IS NOT NULL alone would join
// unrelated rows together.
func TestLeadScoring_BlankEmailNeverMatchesEngagement_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupLeadScoreSchema(t, db)

	org := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme')`, org).Error)
	blank := seedLeadContact(t, db, org, "NoEmail", "")

	// An event row whose recipient normalized to empty — reachable when a
	// provider payload carries no usable address.
	require.NoError(t, db.Exec(
		`INSERT INTO marketing_email_events (org_id, event_type, email_normalized, occurred_at)
		 VALUES (?, 'email.clicked', '', NOW())`, org).Error)

	repo := NewLeadScoringRepository(db)
	_, _, err := repo.RecomputeOrg(context.Background(), org, leadScoreCatalog(), []domain.LeadScoringRule{
		{ID: uuid.New(), OrgID: org, Name: "Clicked", Kind: domain.LeadRuleKindEngagement, Points: 50, Enabled: true,
			Condition: domain.JSON(`{"event":"email.clicked","window_days":30,"min_count":1}`)},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, contactScore(t, db, blank), "a blank email must never join a blank event recipient")
}
