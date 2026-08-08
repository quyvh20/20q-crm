package repository

import (
	"context"
	"sort"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// report_only_scope_db_test.go is the R9.5 parity gate.
//
// The unit test in report_sql_test.go proves the report builder emits the same
// predicate STRING as the list page. This proves the two return the same ROWS
// against a real Postgres, which is the claim that actually matters: the whole
// reason tasks and activities got shared predicates instead of a second copy is
// that a report showing a rep one set of rows and their task list another is a
// leak nobody would notice until it was quoted in a meeting.
//
// Docker-gated (skips in short mode) — reuses the schema and fixtures from
// task_activity_scope_db_test.go.

// reportRowValues runs a table-mode report and returns the values of one column.
func reportRowValues(t *testing.T, db *gorm.DB, ctx context.Context, orgID uuid.UUID, slug, column string) []string {
	t.Helper()
	ro := domain.ReportOnlyObjectBySlug(slug)
	require.NotNil(t, ro, "descriptor for %q", slug)

	runner := NewReportRunnerRepository(db)
	res, err := runner.Run(ctx, orgID, ro.Def(), ro.Catalog(), domain.ReportConfig{
		Chart:   domain.ReportChartTable,
		Columns: []string{column},
		Limit:   500,
	})
	require.NoError(t, err)

	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		if s, ok := row[column].(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// A row-scoped rep's TASK REPORT must contain exactly the tasks their task LIST
// contains. Titles stand in for ids (each is unique here) because id is not a
// catalog field — deliberately, since a report is an aggregate surface.
func TestReportOverTasks_MatchesListPageExactly_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupTaskActivitySchema(t, db)
	repo := NewTaskRepository(db)

	org := uuid.New()
	me := uuid.New()
	other := uuid.New()

	myContact, otherContact, sharedContact := uuid.New(), uuid.New(), uuid.New()
	myDeal, otherDeal := uuid.New(), uuid.New()
	insContact(t, db, org, myContact, me)
	insContact(t, db, org, otherContact, other)
	insContact(t, db, org, sharedContact, other)
	insDeal(t, db, org, myDeal, me)
	insDeal(t, db, org, otherDeal, other)
	shareToUser(t, db, org, "contact", sharedContact, me)

	insTask := func(title string, assigned, createdBy, contact, deal *uuid.UUID) {
		require.NoError(t, db.Exec(`INSERT INTO tasks (id, org_id, title, assigned_to, created_by, contact_id, deal_id, priority)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'medium')`, uuid.New(), org, title, assigned, createdBy, contact, deal).Error)
	}
	insTask("assigned-to-me", &me, &other, nil, nil)
	insTask("created-by-me", &other, &me, nil, nil)
	insTask("on-my-contact", &other, &other, &myContact, nil)
	insTask("on-my-deal", &other, &other, nil, &myDeal)
	insTask("on-shared-contact", &other, &other, &sharedContact, nil)
	insTask("on-other-contact", &other, &other, &otherContact, nil)
	insTask("on-other-deal", &other, &other, nil, &otherDeal)
	insTask("unlinked-other", &other, &other, nil, nil)

	ctx := ownCtx(me)

	listed, err := repo.List(ctx, org, domain.TaskFilter{Limit: 500})
	require.NoError(t, err)
	listTitles := make([]string, 0, len(listed.Tasks))
	for _, tk := range listed.Tasks {
		listTitles = append(listTitles, tk.Title)
	}
	sort.Strings(listTitles)

	reported := reportRowValues(t, db, ctx, org, "task", "title")

	assert.Equal(t, listTitles, reported,
		"a task report and the task list must return the SAME rows for the same caller")
	// Pin the expected set too, so a change that breaks BOTH identically still fails.
	assert.Equal(t,
		[]string{"assigned-to-me", "created-by-me", "on-my-contact", "on-my-deal", "on-shared-contact"},
		reported)

	// And the all-scoped caller genuinely sees everything — proving the parity
	// above is real scoping, not an accidentally empty report.
	allTitles := reportRowValues(t, db, allCtx(other), org, "task", "title")
	assert.Len(t, allTitles, 8, "an all-scoped caller reports on every task in the org")
}

// Same for activities. The stakes are higher here: activity rows carry call
// notes and, since R9.1, sent-email bodies. body is not in the catalog, but a
// row-scoped caller must not be able to COUNT another rep's activity either.
func TestReportOverActivities_MatchesListPageExactly_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupTaskActivitySchema(t, db)
	repo := NewActivityRepository(db)

	org := uuid.New()
	me := uuid.New()
	other := uuid.New()

	myContact, otherContact, sharedContact := uuid.New(), uuid.New(), uuid.New()
	myDeal, otherDeal := uuid.New(), uuid.New()
	insContact(t, db, org, myContact, me)
	insContact(t, db, org, otherContact, other)
	insContact(t, db, org, sharedContact, other)
	insDeal(t, db, org, myDeal, me)
	insDeal(t, db, org, otherDeal, other)
	shareToUser(t, db, org, "contact", sharedContact, me)

	insAct := func(title string, user, contact, deal *uuid.UUID) {
		require.NoError(t, db.Exec(`INSERT INTO activities (id, org_id, type, user_id, contact_id, deal_id, title, body)
			VALUES (?, ?, 'note', ?, ?, ?, ?, 'secret notes')`, uuid.New(), org, user, contact, deal, title).Error)
	}
	insAct("logged-by-me", &me, nil, nil)
	insAct("on-my-contact", &other, &myContact, nil)
	insAct("on-my-deal", &other, nil, &myDeal)
	insAct("on-shared-contact", &other, &sharedContact, nil)
	insAct("on-other-contact", &other, &otherContact, nil)
	insAct("on-other-deal", &other, nil, &otherDeal)
	// The NULL-user_id case: three writers leave it unset (the deal stage-change
	// path and its automation twin). Such a row is reachable only via its linked
	// deal, and this one's deal belongs to someone else.
	insAct("stage-change-on-other-deal", nil, nil, &otherDeal)

	ctx := ownCtx(me)

	listed, err := repo.List(ctx, org, domain.ActivityFilter{})
	require.NoError(t, err)
	listTitles := make([]string, 0, len(listed))
	for _, a := range listed {
		if a.Title != nil {
			listTitles = append(listTitles, *a.Title)
		}
	}
	sort.Strings(listTitles)

	reported := reportRowValues(t, db, ctx, org, "activity", "title")

	assert.Equal(t, listTitles, reported,
		"an activity report and the activity list must return the SAME rows for the same caller")
	assert.Equal(t,
		[]string{"logged-by-me", "on-my-contact", "on-my-deal", "on-shared-contact"},
		reported)
	assert.NotContains(t, reported, "stage-change-on-other-deal",
		"a user_id-less activity on an unreachable deal must not surface")

	allTitles := reportRowValues(t, db, allCtx(other), org, "activity", "title")
	assert.Len(t, allTitles, 7, "an all-scoped caller reports on every activity in the org")
}

// The aggregate path is what the two shipped templates actually use, and a
// GROUP BY takes a different code path through the builder than table mode. A
// row-scoped rep's counts must be their OWN counts.
func TestReportGroupedByRep_IsRowScoped_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupTaskActivitySchema(t, db)

	org := uuid.New()
	me := uuid.New()
	other := uuid.New()

	otherContact := uuid.New()
	insContact(t, db, org, otherContact, other)

	insAct := func(user *uuid.UUID, contact *uuid.UUID, kind string) {
		require.NoError(t, db.Exec(`INSERT INTO activities (id, org_id, type, user_id, contact_id)
			VALUES (?, ?, ?, ?, ?)`, uuid.New(), org, kind, user, contact).Error)
	}
	insAct(&me, nil, "call")
	insAct(&me, nil, "email")
	insAct(&other, &otherContact, "call")
	insAct(&other, &otherContact, "call")
	insAct(&other, &otherContact, "stage_change")

	activity := domain.ReportOnlyObjectBySlug("activity")
	require.NotNil(t, activity)
	runner := NewReportRunnerRepository(db)

	// The shipped "Activities per rep" template, verbatim.
	cfg := domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "user_id"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
		Filters: &domain.ReportFilterGroup{
			Op:    "AND",
			Rules: []domain.ReportFilterRule{{Field: "type", Operator: "neq", Value: "stage_change"}},
		},
	}

	mine, err := runner.Run(ownCtx(me), org, activity.Def(), activity.Catalog(), cfg)
	require.NoError(t, err)
	require.Len(t, mine.Groups, 1, "a row-scoped rep must see exactly one bar: their own")
	assert.Equal(t, me.String(), mine.Groups[0].Key)
	assert.Equal(t, float64(2), mine.Groups[0].Value)

	everyone, err := runner.Run(allCtx(other), org, activity.Def(), activity.Catalog(), cfg)
	require.NoError(t, err)
	assert.Len(t, everyone.Groups, 2, "an all-scoped caller sees every rep")
	assert.Equal(t, 4, everyone.RowCount, "the stage_change row is filtered out by the template")

	// activities.type is a Postgres ENUM. Every operator the builder offers for a
	// `select` field must actually EXECUTE against it — the emptiness ones
	// compare to the literal '', which an enum rejects outright ("invalid input
	// value for enum activity_type") unless the column is read as ::text. This is
	// a 500 a user reaches by picking "Type — is not empty" in the builder, and
	// no amount of SQL-string inspection proves it; only running it does.
	for _, op := range []string{"eq", "neq", "in", "is_empty", "is_not_empty"} {
		rule := domain.ReportFilterRule{Field: "type", Operator: op, Value: "call"}
		if op == "in" {
			rule.Value = []any{"call", "email"}
		}
		_, err := runner.Run(allCtx(other), org, activity.Def(), activity.Catalog(), domain.ReportConfig{
			Chart:     domain.ReportChartBar,
			GroupBy:   &domain.ReportGroupBy{Field: "user_id"},
			Aggregate: &domain.ReportAggregate{Fn: "count"},
			Filters:   &domain.ReportFilterGroup{Op: "AND", Rules: []domain.ReportFilterRule{rule}},
		})
		assert.NoError(t, err, "operator %q must execute against the activity_type enum", op)
	}

	// Grouping BY the enum must return its labels, not an error or a raw oid.
	byType, err := runner.Run(allCtx(other), org, activity.Def(), activity.Catalog(), domain.ReportConfig{
		Chart:     domain.ReportChartBar,
		GroupBy:   &domain.ReportGroupBy{Field: "type"},
		Aggregate: &domain.ReportAggregate{Fn: "count"},
	})
	require.NoError(t, err)
	labels := map[string]bool{}
	for _, g := range byType.Groups {
		if s, ok := g.Key.(string); ok {
			labels[s] = true
		}
	}
	assert.True(t, labels["call"] && labels["email"] && labels["stage_change"],
		"grouping by the enum should yield its labels, got %v", labels)
}
