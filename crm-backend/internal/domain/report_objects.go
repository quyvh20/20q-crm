package domain

// ============================================================
// Report-only objects (R9.5)
// ============================================================
//
// Tasks and activities are reportable, but they are NOT registry objects.
//
// Registering them would have been the obvious move and it is the wrong one. An
// object_defs row is not just a reporting fact — it is a sidebar nav entry, a
// generic /objects/<slug> list page that would compete with the purpose-built
// TasksPage, an "Edit fields" button in Objects & Fields that 400s because there
// is no field table behind it, an owner picker that writes to an owner_user_id
// column neither table has, and a record detail page that 404s outright because
// RecordService's adapter map is a hardcoded three entries. Exactly one of those
// surfaces was wanted. So the descriptor below is a report-catalog entry and
// nothing else: no def row, no adapter, no CRUD, no nav.
//
// The cost of the cheap path, stated plainly: no related-list panel on contact
// and deal pages. object_links (P4) is the right answer for that and needs no
// registry membership either.
//
// These live in domain, not usecase, because both sides need them: usecase
// builds the catalog from them, and repository asserts in test that every slug
// declared here has a row-access arm in rowPredicateFor.

// ReportOnlyObject is a table that reports can query but the object registry
// does not describe.
type ReportOnlyObject struct {
	Slug        string
	Label       string
	LabelPlural string
	Icon        string
	Color       string
	// Table is the physical table. It is a compile-time constant on purpose —
	// the report SQL builder splices it into the FROM clause.
	Table string
	// Fields is the complete catalog. Every entry MUST set Column: these tables
	// have no custom_fields blob, and the runner would otherwise emit
	// `tasks.custom_fields->>'x'` and 500. Enforced by test.
	Fields []ReportField
}

// ReportOnlyObjects is the whole list. Adding one means adding an arm to
// rowPredicateFor (repository/report_sql.go) and a slug to permSystemObjectSlugs
// (repository/permission_repository.go) in the SAME commit — see
// ReportOnlyObjectSlugs.
var ReportOnlyObjects = []ReportOnlyObject{
	{
		Slug: "task", Label: "Task", LabelPlural: "Tasks",
		Icon: "✅", Color: "#0EA5E9", Table: "tasks",
		Fields: []ReportField{
			{Key: "title", Label: "Title", Type: "text", Column: "title"},
			{Key: "priority", Label: "Priority", Type: "select", Column: "priority",
				Options: []string{"low", "medium", "high"}},
			{Key: "due_at", Label: "Due At", Type: "date", Column: "due_at"},
			// The completion signal is a nullable timestamp, not a boolean, so
			// "completed" is `completed_at is_not_empty` and "how long did it
			// take" is a date bucket. Both operators already exist.
			{Key: "completed_at", Label: "Completed At", Type: "date", Column: "completed_at"},
			{Key: "assigned_to", Label: "Assigned To", Type: "relation", Column: "assigned_to", LabelKind: "user"},
			{Key: "created_by", Label: "Created By", Type: "relation", Column: "created_by", LabelKind: "user"},
			{Key: "created_at", Label: "Created At", Type: "date", Column: "created_at"},
			{Key: "updated_at", Label: "Updated At", Type: "date", Column: "updated_at"},
		},
	},
	{
		Slug: "activity", Label: "Activity", LabelPlural: "Activities",
		Icon: "📋", Color: "#F59E0B", Table: "activities",
		Fields: []ReportField{
			// stage_change is in the options even though it is machine-written:
			// those rows dominate the table, and the only way to express "real
			// rep activity" in the builder is `type neq stage_change`. Hiding the
			// value from the picker would make the useful filter unbuildable.
			// CastText because activities.type is a Postgres ENUM
			// (activity_type), not a varchar — see ReportField.CastText.
			{Key: "type", Label: "Type", Type: "select", Column: "type", CastText: true,
				Options: []string{"call", "email", "meeting", "note", "stage_change"}},
			{Key: "title", Label: "Title", Type: "text", Column: "title"},
			{Key: "duration_minutes", Label: "Duration (min)", Type: "number", Column: "duration_minutes"},
			{Key: "occurred_at", Label: "Occurred At", Type: "date", Column: "occurred_at"},
			{Key: "user_id", Label: "Logged By", Type: "relation", Column: "user_id", LabelKind: "user"},
			{Key: "created_at", Label: "Created At", Type: "date", Column: "created_at"},
			{Key: "updated_at", Label: "Updated At", Type: "date", Column: "updated_at"},
		},
	},
}

// Deliberately ABSENT from the catalogs above, so nobody re-adds them by
// reflex:
//
//   - activities.body — carries call notes and, since R9.1, the full HTML of
//     every sent 1:1 email. A text field with `contains` in a report builder is
//     a disclosure surface with no reporting value.
//   - activities.sentiment — written by an out-of-band AI worker. It is model
//     output, not a rep metric.
//   - tasks.last_reminded_at — scanner bookkeeping, json:"-".
//   - tasks/activities contact_id and deal_id — the ROWS are row-scoped, but
//     ResolveGroupLabels resolves contact and deal names by org_id alone. A rep
//     can hold a task assigned to them that links a contact they cannot open;
//     grouping by it would render that contact's name, and the tasks list page
//     shows no such thing today. Row-scoped label resolution is the fix, and it
//     is a separate piece of work.
//   - an owner field of any kind — neither table has owner_user_id.

// ReportOnlyObjectBySlug returns the descriptor, or nil.
func ReportOnlyObjectBySlug(slug string) *ReportOnlyObject {
	for i := range ReportOnlyObjects {
		if ReportOnlyObjects[i].Slug == slug {
			return &ReportOnlyObjects[i]
		}
	}
	return nil
}

// ReportOnlyObjectSlugs are the slugs, for the places that need the set rather
// than the descriptors — notably the OLS seed, which MUST list them or the
// objects are readable by nobody but the owner role.
func ReportOnlyObjectSlugs() []string {
	out := make([]string, 0, len(ReportOnlyObjects))
	for _, o := range ReportOnlyObjects {
		out = append(out, o.Slug)
	}
	return out
}

// Def synthesises the ObjectDef the report runner needs. It is built per request
// and never persisted: the runner reads Storage/RecordTable/Slug to resolve the
// physical table and nothing else, and ID stays uuid.Nil because no code path
// looks up registry fields for a report-only object.
func (o ReportOnlyObject) Def() *ObjectDef {
	table := o.Table
	return &ObjectDef{
		Slug:        o.Slug,
		Label:       o.Label,
		LabelPlural: o.LabelPlural,
		Icon:        o.Icon,
		Color:       o.Color,
		IsSystem:    true,
		Storage:     "table",
		RecordTable: &table,
	}
}

// Catalog returns a copy of the field catalog. A copy, because the report
// usecase's callers are free to append virtual fields to what they get back and
// a shared backing array would corrupt the package-level descriptor.
func (o ReportOnlyObject) Catalog() []ReportField {
	out := make([]ReportField, len(o.Fields))
	copy(out, o.Fields)
	return out
}
