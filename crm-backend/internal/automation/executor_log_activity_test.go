package automation

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This file holds the ActivityExecutor tests for the log_activity action:
//   - Property 5 (2.2): entity-ID resolution maps only valid UUIDs (pure, no DB).
//   - Property 6 (2.3): successful execution inserts one faithful row and returns a
//     matching output (DB-backed via setupTestDB).
//   - Property 7 (2.4): precondition violations error without inserting or returning
//     output (pure (nil, err) contract, plus a DB-backed no-insert check).
//   - Example/integration (2.5): occurred_at window + forced insert failure.
//
// Property tests use Go's testing/quick (stdlib). The package-level generators and
// constants (pbtIterations, laRandString, genValidActivityType, genValidTitle,
// genInvalidActivityType) live in validator_log_activity_test.go and are reused here.
//
// DB-backed tests follow the existing executor-test convention: guard with
// testing.Short(), then setupTestDB(t) (testcontainer Postgres, which itself skips
// when Docker is unavailable). The activities/organizations/deals tables are created
// per-test (contacts is created by setupTestDB), mirroring seedDealStageTables.

// laeDBIterations is the iteration count for the DB-backed property (Property 6).
// Kept at the spec minimum (100) because each iteration performs several round-trips.
const laeDBIterations = 100

// ─────────────────────────────────────────────────────────────────────────
// Property 5 — entity-ID resolution maps only valid UUIDs (pure, no DB)
// ─────────────────────────────────────────────────────────────────────────

// laeGenEntityID generates a candidate value for evalCtx.Contact["id"] /
// evalCtx.Deal["id"], returning whether the "id" key is present, the value, whether
// parseEntityID is expected to resolve it to a non-nil UUID, and the canonical UUID
// string expected when valid.
func laeGenEntityID(r *rand.Rand) (present bool, value any, expectValid bool, canonical string) {
	switch r.Intn(6) {
	case 0: // valid canonical UUID string
		id := uuid.New()
		return true, id.String(), true, id.String()
	case 1: // valid UUID string, upper-cased (uuid.Parse accepts it, canonical is lower)
		id := uuid.New()
		return true, strings.ToUpper(id.String()), true, id.String()
	case 2: // absent key
		return false, nil, false, ""
	case 3: // present but nil value (non-string)
		return true, nil, false, ""
	case 4: // malformed string
		bad := []string{
			"", "not-a-uuid", "123", "abc-def", "xyz",
			uuid.New().String() + "x",           // too long
			"g" + uuid.New().String()[1:],        // non-hex first char
			"   ", laRandString(r, 1+r.Intn(10)), // short random / whitespace
		}
		return true, bad[r.Intn(len(bad))], false, ""
	default: // non-string value
		vals := []any{42, int64(7), true, false, 3.14, []any{uuid.New().String()}, map[string]any{"id": "x"}}
		return true, vals[r.Intn(len(vals))], false, ""
	}
}

type prop5Input struct {
	ContactPresent   bool
	ContactVal       any
	ContactValid     bool
	ContactCanonical string

	DealPresent   bool
	DealVal       any
	DealValid     bool
	DealCanonical string
}

func (prop5Input) Generate(r *rand.Rand, _ int) reflect.Value {
	cp, cv, cValid, cCanon := laeGenEntityID(r)
	dp, dv, dValid, dCanon := laeGenEntityID(r)
	return reflect.ValueOf(prop5Input{
		ContactPresent: cp, ContactVal: cv, ContactValid: cValid, ContactCanonical: cCanon,
		DealPresent: dp, DealVal: dv, DealValid: dValid, DealCanonical: dCanon,
	})
}

// Feature: log-activity-action, Property 5: For any evaluation context, the contact
// identifier resolves to a non-nil UUID exactly when evalCtx.Contact["id"] is a string
// that parses as a valid UUID, and otherwise resolves to nil; the same holds
// independently for evalCtx.Deal["id"]. Absent, non-string, or malformed values resolve
// to nil. Pure, no DB.
//
// **Validates: Requirements 4.1, 4.2, 4.4**
func TestLogActivity_Property5_EntityIDResolution(t *testing.T) {
	f := func(in prop5Input) bool {
		contact := map[string]any{}
		if in.ContactPresent {
			contact["id"] = in.ContactVal
		}
		deal := map[string]any{}
		if in.DealPresent {
			deal["id"] = in.DealVal
		}

		cID := parseEntityID(contact)
		dID := parseEntityID(deal)

		// Contact resolves to non-nil exactly when expected.
		if (cID != nil) != in.ContactValid {
			t.Logf("contact: present=%v val=%#v got nil=%v want valid=%v", in.ContactPresent, in.ContactVal, cID == nil, in.ContactValid)
			return false
		}
		if in.ContactValid && cID.String() != in.ContactCanonical {
			t.Logf("contact: got %s want %s", cID.String(), in.ContactCanonical)
			return false
		}

		// Deal resolves independently of contact.
		if (dID != nil) != in.DealValid {
			t.Logf("deal: present=%v val=%#v got nil=%v want valid=%v", in.DealPresent, in.DealVal, dID == nil, in.DealValid)
			return false
		}
		if in.DealValid && dID.String() != in.DealCanonical {
			t.Logf("deal: got %s want %s", dID.String(), in.DealCanonical)
			return false
		}
		return true
	}
	cfg := &quick.Config{MaxCount: pbtIterations, Rand: rand.New(rand.NewSource(5))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 5 (entity-ID resolution) failed: %v", err)
	}
}

// TestLogActivity_ParseEntityID_Examples pins the concrete branches of parseEntityID
// with explicit cases that complement the generated Property 5 coverage.
func TestLogActivity_ParseEntityID_Examples(t *testing.T) {
	valid := uuid.New()

	t.Run("valid UUID string resolves", func(t *testing.T) {
		got := parseEntityID(map[string]any{"id": valid.String()})
		require.NotNil(t, got)
		assert.Equal(t, valid.String(), got.String())
	})
	t.Run("absent key resolves nil", func(t *testing.T) {
		assert.Nil(t, parseEntityID(map[string]any{}))
	})
	t.Run("nil map resolves nil", func(t *testing.T) {
		assert.Nil(t, parseEntityID(nil))
	})
	t.Run("non-string value resolves nil", func(t *testing.T) {
		assert.Nil(t, parseEntityID(map[string]any{"id": 42}))
		assert.Nil(t, parseEntityID(map[string]any{"id": true}))
		assert.Nil(t, parseEntityID(map[string]any{"id": []any{valid.String()}}))
	})
	t.Run("malformed string resolves nil", func(t *testing.T) {
		assert.Nil(t, parseEntityID(map[string]any{"id": "not-a-uuid"}))
		assert.Nil(t, parseEntityID(map[string]any{"id": ""}))
		assert.Nil(t, parseEntityID(map[string]any{"id": valid.String() + "x"}))
	})
}

// ─────────────────────────────────────────────────────────────────────────
// DB test helpers (DB-backed properties + examples)
// ─────────────────────────────────────────────────────────────────────────

// laeSeedActivitySchema creates the activities table (with the real activity_type
// enum) plus the organizations / deals parent tables, and inserts a single
// org + contact + deal fixture, returning their IDs. The contacts table is already
// created by setupTestDB. user_id carries no FK (the executor always writes NULL),
// matching how seedDealStageTables keeps the test schema standalone while still
// exercising the executor's real INSERT statement.
func laeSeedActivitySchema(t *testing.T, db *gorm.DB) (orgID, contactID, dealID uuid.UUID) {
	t.Helper()

	require.NoError(t, db.Exec(`DO $$ BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'activity_type') THEN
			CREATE TYPE activity_type AS ENUM ('call', 'email', 'meeting', 'note', 'stage_change');
		END IF;
	END $$;`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS organizations (
		id UUID PRIMARY KEY,
		name TEXT DEFAULT ''
	)`).Error)

	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS deals (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		title TEXT DEFAULT ''
	)`).Error)

	// activities mirrors migrations/000002_schema.up.sql (enum type + FKs to
	// organizations/contacts/deals). user_id is a plain nullable UUID here since the
	// users table is not part of the automation test schema and the executor always
	// writes NULL.
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

	orgID = uuid.New()
	contactID = uuid.New()
	dealID = uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme Inc')`, orgID).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO contacts (id, org_id, first_name, last_name, email) VALUES (?, ?, 'Jane', 'Doe', 'jane@acme.com')`,
		contactID, orgID,
	).Error)
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title) VALUES (?, ?, 'Acme Deal')`, dealID, orgID).Error)
	return orgID, contactID, dealID
}

// laeActivityCount returns the number of activities rows for an org.
func laeActivityCount(t *testing.T, db *gorm.DB, orgID uuid.UUID) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM activities WHERE org_id = ?`, orgID).Scan(&n).Error)
	return n
}

// laeGenBody generates a body parameter: absent, empty, whitespace-only, literal
// text, or text with templates. The blank variants exercise the body => NULL branch.
func laeGenBody(r *rand.Rand) (present bool, raw string) {
	switch r.Intn(5) {
	case 0:
		return false, "" // absent
	case 1:
		return true, "" // empty string
	case 2: // whitespace-only (=> NULL after trim)
		ws := []string{" ", "   ", "\t", "\n", " \t \n "}
		return true, ws[r.Intn(len(ws))]
	case 3: // literal text
		return true, "Discussed the renewal " + laRandString(r, 1+r.Intn(8))
	default: // text with templates that resolve against the populated evalCtx
		return true, "Notes for {{contact.first_name}} on {{deal.title}}"
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Property 6 — successful execution inserts one faithful row and matching output
// ─────────────────────────────────────────────────────────────────────────

type prop6Input struct {
	AType        string
	RawTitle     string
	BodyPresent  bool
	RawBody      string
	EntityCombo  int // 0 = contact only, 1 = deal only, 2 = both
}

func (prop6Input) Generate(r *rand.Rand, _ int) reflect.Value {
	present, raw := laeGenBody(r)
	return reflect.ValueOf(prop6Input{
		AType:       genValidActivityType(r),
		RawTitle:    genValidTitle(r),
		BodyPresent: present,
		RawBody:     raw,
		EntityCombo: r.Intn(3),
	})
}

// Feature: log-activity-action, Property 6: For any log_activity action with a valid
// activity_type, a title that resolves to a non-empty string, and at least one valid
// contact or deal identifier present, executing the action inserts exactly one
// activities row in which org_id == run.OrgID, type == the resolved activity_type,
// title == the interpolated title, body == the interpolated body (or NULL when blank),
// user_id is NULL, and contact_id/deal_id == the resolved identifiers (NULL when
// absent); and returns an output map whose activity_id/activity_type/contact_id/deal_id
// match the inserted row. DB-backed via setupTestDB.
//
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4, 3.5, 3.7, 3.8, 4.3, 9.3**
func TestLogActivity_Property6_SuccessfulInsertFaithful(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	orgID, contactID, dealID := laeSeedActivitySchema(t, db)

	exec := NewActivityExecutor(db)
	ctx := context.Background()

	f := func(in prop6Input) bool {
		// Build evalCtx so templates always resolve to non-empty values; the "id"
		// keys are added per the entity combo to control association independently of
		// template resolution.
		contact := map[string]any{"first_name": "Jane", "last_name": "Doe", "email": "jane@acme.com"}
		deal := map[string]any{"title": "Acme Deal"}
		wantContact, wantDeal := false, false
		switch in.EntityCombo {
		case 0:
			contact["id"] = contactID.String()
			wantContact = true
		case 1:
			deal["id"] = dealID.String()
			wantDeal = true
		default:
			contact["id"] = contactID.String()
			deal["id"] = dealID.String()
			wantContact, wantDeal = true, true
		}
		evalCtx := EvalContext{Contact: contact, Deal: deal, Trigger: map[string]any{"type": "contact_created"}}

		// Expected resolved values mirror the executor: interpolate then trim.
		wantTitle := strings.TrimSpace(InterpolateTemplate(in.RawTitle, evalCtx))
		if wantTitle == "" {
			// Generator guarantees a resolvable non-empty title; skip if it ever isn't.
			return true
		}
		var wantBody *string
		if in.BodyPresent {
			if b := strings.TrimSpace(InterpolateTemplate(in.RawBody, evalCtx)); b != "" {
				wantBody = &b
			}
		}

		params := map[string]any{"activity_type": in.AType, "title": in.RawTitle}
		if in.BodyPresent {
			params["body"] = in.RawBody
		}
		action := ActionSpec{Type: ActionLogActivity, ID: "a1", Params: params}
		run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}

		before := laeActivityCount(t, db, orgID)
		out, err := exec.Execute(ctx, run, action, evalCtx)
		if err != nil {
			t.Logf("unexpected error for %#v: %v", in, err)
			return false
		}
		after := laeActivityCount(t, db, orgID)
		if after-before != 1 {
			t.Logf("expected exactly 1 new row, got delta %d", after-before)
			return false
		}

		m, ok := out.(map[string]any)
		if !ok {
			t.Logf("output is not a map: %T", out)
			return false
		}
		activityIDStr, _ := m["activity_id"].(string)
		activityID, perr := uuid.Parse(activityIDStr)
		if perr != nil {
			t.Logf("activity_id %q is not a valid uuid: %v", activityIDStr, perr)
			return false
		}

		var row struct {
			OrgID      uuid.UUID  `gorm:"column:org_id"`
			Type       string     `gorm:"column:type"`
			Title      *string    `gorm:"column:title"`
			Body       *string    `gorm:"column:body"`
			UserID     *uuid.UUID `gorm:"column:user_id"`
			ContactID  *uuid.UUID `gorm:"column:contact_id"`
			DealID     *uuid.UUID `gorm:"column:deal_id"`
			OccurredAt time.Time  `gorm:"column:occurred_at"`
		}
		if scanErr := db.Table("activities").
			Select("org_id, type, title, body, user_id, contact_id, deal_id, occurred_at").
			Where("id = ?", activityID).Scan(&row).Error; scanErr != nil {
			t.Fatalf("scan inserted row: %v", scanErr)
		}

		// Column fidelity.
		if row.OrgID != orgID {
			t.Logf("org_id: got %s want %s", row.OrgID, orgID)
			return false
		}
		if row.Type != in.AType {
			t.Logf("type: got %s want %s", row.Type, in.AType)
			return false
		}
		if row.Title == nil || *row.Title != wantTitle {
			t.Logf("title: got %v want %q", row.Title, wantTitle)
			return false
		}
		if wantBody == nil {
			if row.Body != nil {
				t.Logf("body: got %q want NULL", *row.Body)
				return false
			}
		} else if row.Body == nil || *row.Body != *wantBody {
			t.Logf("body: got %v want %q", row.Body, *wantBody)
			return false
		}
		if row.UserID != nil {
			t.Logf("user_id: got %s want NULL", row.UserID)
			return false
		}
		if wantContact {
			if row.ContactID == nil || *row.ContactID != contactID {
				t.Logf("contact_id: got %v want %s", row.ContactID, contactID)
				return false
			}
		} else if row.ContactID != nil {
			t.Logf("contact_id: got %s want NULL", row.ContactID)
			return false
		}
		if wantDeal {
			if row.DealID == nil || *row.DealID != dealID {
				t.Logf("deal_id: got %v want %s", row.DealID, dealID)
				return false
			}
		} else if row.DealID != nil {
			t.Logf("deal_id: got %s want NULL", row.DealID)
			return false
		}

		// Output map matches the row.
		if at, _ := m["activity_type"].(string); at != row.Type {
			t.Logf("output activity_type: got %v want %s", m["activity_type"], row.Type)
			return false
		}
		if wantContact {
			if cid, _ := m["contact_id"].(string); cid != contactID.String() {
				t.Logf("output contact_id: got %v want %s", m["contact_id"], contactID)
				return false
			}
		} else if m["contact_id"] != nil {
			t.Logf("output contact_id: got %v want nil", m["contact_id"])
			return false
		}
		if wantDeal {
			if did, _ := m["deal_id"].(string); did != dealID.String() {
				t.Logf("output deal_id: got %v want %s", m["deal_id"], dealID)
				return false
			}
		} else if m["deal_id"] != nil {
			t.Logf("output deal_id: got %v want nil", m["deal_id"])
			return false
		}
		return true
	}

	cfg := &quick.Config{MaxCount: laeDBIterations, Rand: rand.New(rand.NewSource(6))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 6 (faithful insert + matching output) failed: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Property 7 — precondition violations error without inserting or returning output
// ─────────────────────────────────────────────────────────────────────────

// prop7Input is a fully-formed scenario that violates exactly one executor
// precondition. Generate builds the params/contact/deal directly so the test body
// stays declarative.
type prop7Input struct {
	Violation string // "title" | "type" | "entity"
	Params    map[string]any
	Contact   map[string]any
	Deal      map[string]any
}

// laeBlankTitle returns (present, value) for a title that resolves to empty/whitespace
// at execution time. Only strings (or absence) are used — a non-string would be
// coerced by getStringParam to a non-empty string and would NOT be a blank-title
// violation at the executor level.
func laeBlankTitle(r *rand.Rand) (present bool, value string) {
	switch r.Intn(8) {
	case 0:
		return false, "" // absent => getStringParam returns ""
	case 1:
		return true, ""
	case 2, 3:
		ws := []string{" ", "   ", "\t", "\n", " \t \n ", "\r\n", "\v", "\f"}
		return true, ws[r.Intn(len(ws))]
	default: // template that resolves to empty (unknown path) possibly with whitespace
		tpl := []string{"{{contact.unknown}}", "  {{deal.nope}}  ", "{{contact.missing}}\t", "\n{{trigger.absent}}\n"}
		return true, tpl[r.Intn(len(tpl))]
	}
}

func (prop7Input) Generate(r *rand.Rand, _ int) reflect.Value {
	in := prop7Input{
		Contact: map[string]any{"first_name": "Jane", "last_name": "Doe", "email": "jane@acme.com"},
		Deal:    map[string]any{"title": "Acme Deal"},
	}
	contactID := uuid.New().String()
	dealID := uuid.New().String()

	// addValidEntity gives at least one valid contact/deal id (for the title/type cases).
	addValidEntity := func() {
		switch r.Intn(3) {
		case 0:
			in.Contact["id"] = contactID
		case 1:
			in.Deal["id"] = dealID
		default:
			in.Contact["id"] = contactID
			in.Deal["id"] = dealID
		}
	}

	switch r.Intn(3) {
	case 0: // (a) blank title, valid type, valid entity
		in.Violation = "title"
		params := map[string]any{"activity_type": genValidActivityType(r)}
		if present, val := laeBlankTitle(r); present {
			params["title"] = val
		}
		in.Params = params
		addValidEntity()
	case 1: // (b) invalid activity_type, valid title, valid entity
		in.Violation = "type"
		params := map[string]any{"title": genValidTitle(r)}
		if present, val := genInvalidActivityType(r); present {
			params["activity_type"] = val
		}
		in.Params = params
		addValidEntity()
	default: // (c) valid type + title but no valid entity id
		in.Violation = "entity"
		in.Params = map[string]any{"activity_type": genValidActivityType(r), "title": genValidTitle(r)}
		// Neither contact nor deal carries a valid id: absent or malformed.
		if r.Intn(2) == 0 {
			in.Contact["id"] = "not-a-uuid"
		}
		if r.Intn(2) == 0 {
			in.Deal["id"] = []any{"x"} // non-string => parseEntityID nil
		}
	}
	return reflect.ValueOf(in)
}

// Feature: log-activity-action, Property 7: For any log_activity action where the
// resolved title is empty/whitespace, the resolved activity_type is not one of the four
// valid values, or neither a valid contact nor a valid deal identifier is present,
// executing the action returns a non-nil error and a nil output map. Each precondition
// fails before any DB access, so this is asserted purely; the companion DB-backed test
// (TestLogActivity_PreconditionViolations_NoInsert) verifies zero rows are inserted.
//
// **Validates: Requirements 4.5, 9.1, 9.4, 9.5**
func TestLogActivity_Property7_PreconditionViolationsError(t *testing.T) {
	// db is nil: every precondition is checked before the executor touches the DB,
	// so a correct executor never dereferences it. A regression that reached the
	// INSERT would surface here instead of silently passing.
	exec := NewActivityExecutor(nil)
	ctx := context.Background()

	f := func(in prop7Input) bool {
		action := ActionSpec{Type: ActionLogActivity, ID: "a1", Params: in.Params}
		run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: uuid.New()}
		evalCtx := EvalContext{Contact: in.Contact, Deal: in.Deal, Trigger: map[string]any{"type": "contact_created"}}

		out, err := exec.Execute(ctx, run, action, evalCtx)
		if err == nil || out != nil {
			t.Logf("violation=%s params=%#v contact=%#v deal=%#v => out=%#v err=%v (want nil,err)",
				in.Violation, in.Params, in.Contact, in.Deal, out, err)
			return false
		}
		return true
	}

	cfg := &quick.Config{MaxCount: pbtIterations, Rand: rand.New(rand.NewSource(7))}
	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Property 7 (precondition violations => (nil, err)) failed: %v", err)
	}
}

// TestLogActivity_PreconditionViolations_NoInsert is the DB-backed companion to
// Property 7: it confirms that each precondition violation inserts zero rows
// (Req 9.5 — no row on error). DB-backed via setupTestDB.
//
// **Validates: Requirements 4.5, 9.1, 9.4, 9.5**
func TestLogActivity_PreconditionViolations_NoInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	orgID, contactID, _ := laeSeedActivitySchema(t, db)

	exec := NewActivityExecutor(db)
	ctx := context.Background()
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}

	cases := []struct {
		name    string
		params  map[string]any
		evalCtx EvalContext
	}{
		{
			name:   "blank title (valid type + entity)",
			params: map[string]any{"activity_type": "call", "title": "   "},
			evalCtx: EvalContext{
				Contact: map[string]any{"id": contactID.String()},
			},
		},
		{
			name:   "invalid activity_type (valid title + entity)",
			params: map[string]any{"activity_type": "telepathy", "title": "Logged a call"},
			evalCtx: EvalContext{
				Contact: map[string]any{"id": contactID.String()},
			},
		},
		{
			name:   "no valid entity (valid type + title)",
			params: map[string]any{"activity_type": "note", "title": "Standalone note"},
			evalCtx: EvalContext{
				Contact: map[string]any{"id": "not-a-uuid"},
				Deal:    map[string]any{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := laeActivityCount(t, db, orgID)
			action := ActionSpec{Type: ActionLogActivity, ID: "a1", Params: tc.params}
			out, err := exec.Execute(ctx, run, action, tc.evalCtx)
			require.Error(t, err, "precondition violation must return an error")
			assert.Nil(t, out, "no output map on error (Req 9.5)")
			after := laeActivityCount(t, db, orgID)
			assert.Equal(t, before, after, "no activities row may be inserted on a precondition violation")
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Example / integration tests (Task 2.5 — Requirements 3.6, 9.2)
// ─────────────────────────────────────────────────────────────────────────

// TestLogActivity_OccurredAt_WithinWindow asserts that a successful insert sets
// occurred_at to the database server's current time — non-null and within a small
// window of the moment the action ran. DB-backed via setupTestDB. (Req 3.6)
func TestLogActivity_OccurredAt_WithinWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()
	orgID, contactID, _ := laeSeedActivitySchema(t, db)

	exec := NewActivityExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionLogActivity, ID: "a1", Params: map[string]any{
		"activity_type": "meeting",
		"title":         "Kickoff meeting",
	}}
	evalCtx := EvalContext{Contact: map[string]any{"id": contactID.String()}}

	before := time.Now().Add(-2 * time.Minute)
	out, err := exec.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	after := time.Now().Add(2 * time.Minute)

	m := out.(map[string]any)
	activityID := uuid.MustParse(m["activity_id"].(string))

	var occurredAt *time.Time
	require.NoError(t, db.Table("activities").
		Select("occurred_at").
		Where("id = ?", activityID).Scan(&occurredAt).Error)

	require.NotNil(t, occurredAt, "occurred_at must be non-null (Req 3.6)")
	assert.False(t, occurredAt.IsZero(), "occurred_at must be a real timestamp")
	assert.True(t, occurredAt.After(before) && occurredAt.Before(after),
		"occurred_at %s must fall within [%s, %s] (DB NOW() at insert)", occurredAt, before, after)
}

// TestLogActivity_InsertFailure_WrapsCauseNoRow forces the INSERT to fail with an
// always-false CHECK constraint and asserts the executor returns a non-retryable error
// that wraps the underlying cause (via %w) and that no row persists. DB-backed via
// setupTestDB. (Req 9.2)
//
// The activities table here is a standalone variant (no FKs, TEXT type) carrying a
// CHECK (1 = 0) constraint, so every INSERT is rejected by Postgres while the table
// remains queryable to prove zero rows were written. This is the cleanest reliable way
// to force a deterministic insert failure in the test environment.
func TestLogActivity_InsertFailure_WrapsCauseNoRow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed integration test in short mode")
	}

	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Exec(`CREATE TABLE activities (
		id UUID PRIMARY KEY,
		org_id UUID NOT NULL,
		type TEXT NOT NULL,
		deal_id UUID,
		contact_id UUID,
		user_id UUID,
		title TEXT,
		body TEXT,
		occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		CONSTRAINT activities_force_fail CHECK (1 = 0)
	)`).Error)

	orgID := uuid.New()
	contactID := uuid.New()

	exec := NewActivityExecutor(db)
	run := &WorkflowRun{ID: uuid.New(), WorkflowID: uuid.New(), OrgID: orgID}
	action := ActionSpec{Type: ActionLogActivity, ID: "a1", Params: map[string]any{
		"activity_type": "call",
		"title":         "Will never persist",
	}}
	evalCtx := EvalContext{Contact: map[string]any{"id": contactID.String()}}

	out, err := exec.Execute(context.Background(), run, action, evalCtx)

	require.Error(t, err, "a failed insert must return an error")
	assert.Nil(t, out, "no output map on insert failure (Req 9.5)")
	assert.Contains(t, err.Error(), "log_activity:", "error must carry the executor's prefix")
	assert.NotNil(t, errors.Unwrap(err), "error must wrap the underlying cause via %w (Req 9.2)")
	assert.Contains(t, strings.ToLower(err.Error()), "constraint",
		"wrapped error should include the underlying DB failure cause")

	var count int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM activities WHERE org_id = ?`, orgID).Scan(&count).Error)
	assert.Equal(t, int64(0), count, "no activities row may persist after a failed insert (Req 9.2)")
}
