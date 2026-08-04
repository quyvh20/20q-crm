package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"crm-backend/internal/domain"
	"crm-backend/internal/integrations"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// datefield_backfill.go closes the scope hole datefield_timers.go's header names:
// materialization is write-driven, so activating a date_field workflow armed
// NOTHING for records that already existed. "Fire 30 days before renewal" fired for
// nobody whose renewal date was already stored — which is every record in the org on
// the day the workflow is switched on. The workflow reports itself active and does
// nothing until each record happens to be edited.
//
// The reconcile is deliberately cancel-then-rearm rather than a bare backfill,
// because a bare backfill would AMPLIFY two pre-existing bugs rather than fix them.
// armWorkflowTimers only cancels a workflow's date_field timers when the workflow is
// NOT an active date_field workflow, so while it stays active:
//
//  1. changing offset_days / at_time / timezone left every already-armed timer
//     pinned to the OLD config. They still fire, at the old moment, forever — a
//     mixed-config fleet with no symptom until volume makes it visible.
//  2. changing the trigger's OBJECT (deal → contact; the FE supports this edit) left
//     the old object's timers orphaned. ActiveDateFieldWorkflowsForObject filters on
//     the NEW slug so no write ever revisits them, and the scheduler fires them
//     anyway because it only checks wf.IsActive.
//
// One unconditional DELETE of the workflow's pending date_field timers before
// re-arming closes both, and makes the backfill idempotent by construction: on → off
// → on converges on N timers, never 2N. It also lets the arm be INSERT-ONLY, which
// matters — see the batching note on swapDateFieldTimers.
//
// ORDERING — DELETE → SCAN → INSERT, all three in ONE transaction. This is load
// bearing, and the reverse (scan first, then delete+insert) silently destroys
// live-armed timers:
//
//	scan snapshot taken … inbound lead arrives, ingest arms its renewal timer …
//	DELETE wipes every pending timer, INSERT replays only the snapshot
//
// That contact is never paged, permanently, and nothing rescans. ON CONFLICT DO
// NOTHING does not help — it only stops a duplicate key from aborting the batch; it
// cannot resurrect a row the DELETE removed and the INSERT list does not contain.
//
// Deleting FIRST closes the window without holding a lock over the live write path:
//
//   - a record that existed before the DELETE is committed before the SCAN too (the
//     SCAN runs after the DELETE in the same transaction), so the scan returns it and
//     the INSERT re-arms it;
//   - a record that the SCAN does not return committed after the SCAN, hence after
//     the DELETE, so the timer its own live write arms is armed after the DELETE has
//     already run and nothing here can wipe it.
//
// Keeping the SCAN inside the transaction also preserves the property the previous
// ordering was reaching for: a scan that fails (bad object slug, dropped connection,
// cap-count error) must not leave the workflow disarmed. It no longer needs the scan
// to happen first — the transaction rolls the DELETE back.
//
// What this does NOT cover, deliberately: records beyond dateFieldBackfillScanLimit.
// The DELETE is unconditional over the workflow's whole timer set while the re-arm is
// capped, so above the cap a reconcile still drops timers it will not replace. That is
// the cap's problem (logged, exactly), not the ordering's.

const (
	// backfillScanOrder is the scan's ORDER BY, and it only matters when the cap bites:
	// it decides WHICH subset of a too-large org gets armed.
	//
	// It used to be `id`. Record ids here are v4 UUIDs — random — so "the first 10k by
	// id" is an arbitrary subset with no meaning to anyone, and the doc comment calling
	// it "newest-id-last" described an ordering that does not exist. created_at at
	// least names the subset ("the oldest 10k records"), which an admin reading the
	// truncation log can reason about. `id` remains as the tiebreaker so the order is
	// total and the truncated subset is stable across re-runs rather than reshuffling
	// on every reconcile.
	backfillScanOrder = "created_at, id"

	// dateFieldBackfillInsertBatch is the multi-row INSERT size for arming.
	dateFieldBackfillInsertBatch = 500

	// dateFieldReconcileTimeout bounds one reconcile. It is generous because the
	// work is background and the failure mode of expiring early (timers cancelled,
	// none re-armed) is worse than taking a minute.
	dateFieldReconcileTimeout = 5 * time.Minute
)

// dateFieldBackfillScanLimit caps how many records ONE activation examines.
//
// Two orders of magnitude above this codebase's other scan caps (100–200) on
// purpose: those bound a request-path read that a human is waiting on, whereas this
// runs once, in the background, under its own timeout, and every row it skips is a
// reminder that silently never fires. The cost model is what makes 10k affordable —
// the scan is one query and the arm is ~20 batched INSERTs (see swapDateFieldTimers),
// not 10k round trips — and it bounds peak memory at roughly 10k record snapshots.
//
// Hitting it is LOGGED with the EXACT number of records left unexamined (a count
// query runs only when the scan comes back full). A cap nobody can see is a cap that
// lies.
//
// A var rather than a const solely so the cap-truncation test can exercise the
// boundary without inserting 10001 rows; nothing in production writes it, and the
// tests that do are serial (each owns its own Postgres container).
var dateFieldBackfillScanLimit = 10000

// dateFieldBackfillStats reports what one reconcile did. Every field is logged;
// the test asserts on them directly.
//
// The counters are meant to ADD UP: Scanned = Armed + ExcludedTestLeads +
// SkippedNoFutureDate (barring an insert conflict, which is the concurrent case).
// That only holds because Scanned is pre-filter on EVERY object path — it used to be
// post-filter for contacts alone, which made `scanned` in the completion log mean two
// different things depending on the trigger's object.
type dateFieldBackfillStats struct {
	// Scanned is how many rows the scan returned (after the cap, before ANY filter —
	// test leads and unusable dates are both counted here as well as in their own
	// field, so the numbers below are independent of this one).
	Scanned int
	// Armed is how many timer rows were actually inserted.
	Armed int
	// ExcludedTestLeads is how many scanned contacts were synthetic test leads.
	ExcludedTestLeads int
	// SkippedNoFutureDate is how many scanned records had no parseable date, or one
	// already in the past.
	SkippedNoFutureDate int
	// Truncated is how many records the cap left UNEXAMINED (0 when the cap was not
	// reached). Exact, not "at least" — a count query runs when the scan comes back
	// full.
	Truncated int64
}

// ── Reconcile ─────────────────────────────────────────────────────────────────

// reconcileDateFieldTimers rebuilds a workflow's entire pending date_field timer set
// from the records that exist right now: cancel everything pending, then re-arm from
// a scan. Synchronous; callers on the request path use scheduleDateFieldReconcile.
//
// A no-op for anything that is not a date_field workflow (armWorkflowTimers already
// cancels those). An INACTIVE date_field workflow takes the cancel and arms nothing,
// so the same call serves deactivation.
//
// Cancel, scan and arm share ONE transaction, in that order. See the ORDERING note in
// this file's header — it is the difference between "a record that arrived mid-scan is
// armed by its own write" and "a record that arrived mid-scan is never paged".
func (e *Engine) reconcileDateFieldTimers(ctx context.Context, wf *Workflow) (dateFieldBackfillStats, error) {
	var stats dateFieldBackfillStats
	if wf == nil {
		return stats, nil
	}
	var trig TriggerSpec
	if err := json.Unmarshal(wf.Trigger, &trig); err != nil {
		return stats, fmt.Errorf("parse trigger: %w", err)
	}
	p, ok := dateFieldParamsFromTrigger(trig)
	if !ok {
		return stats, nil // not a date_field trigger — nothing here owns its timers
	}

	// Resolved BEFORE the transaction opens: it is a `users` read with nothing to do
	// with the timer swap, and holding the swap's locks across it is pure added
	// contention. Hoisted out of the per-record loop for the same reason
	// materializeDateFieldTimers memoizes it per EVENT (one record) — a backfill
	// resolving it per record would re-query `users` once per row.
	if wf.IsActive && p.Timezone == "" {
		p.Timezone = e.repo.UserTimezone(ctx, wf.CreatedBy)
	}

	if err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		stats, err = e.swapDateFieldTimers(ctx, tx, wf, p)
		return err
	}); err != nil {
		// The transaction rolled back, so nothing happened — not even the cancel.
		// Reporting the counters the closure managed to set before it failed would
		// describe work that does not exist.
		return dateFieldBackfillStats{}, err
	}

	if stats.Truncated > 0 {
		e.logger.Warn("automation: date_field backfill hit its scan cap — records left unarmed",
			"workflow_id", wf.ID.String(), "org_id", wf.OrgID.String(), "object", p.Object,
			"scan_cap", dateFieldBackfillScanLimit, "unexamined_records", stats.Truncated)
	}
	e.logger.Info("automation: date_field backfill complete",
		"workflow_id", wf.ID.String(), "org_id", wf.OrgID.String(), "object", p.Object,
		"active", wf.IsActive, "scanned", stats.Scanned, "armed", stats.Armed,
		"skipped_test_leads", stats.ExcludedTestLeads,
		"skipped_no_future_date", stats.SkippedNoFutureDate,
		"unexamined_records", stats.Truncated)
	return stats, nil
}

// swapDateFieldTimers is the whole reconcile, inside one caller-supplied transaction:
// DELETE the workflow's pending date_field timers, SCAN the object, INSERT the timers
// the scan implies. All three statements run on `tx`, in that order — the scan being
// on it, and after the delete, is the point (see the ORDERING note in this file's
// header). The scan's diagnostic side queries deliberately are not; scanRecordsForBackfill
// says why.
//
// The arm is INSERT-ONLY by design. MaterializeDateFieldTimer (the live write path)
// prefixes every insert with a `dedupe_key LIKE 'df:<record>:%'` DELETE, and under a
// non-C collation that LIKE cannot drive the (workflow_id, dedupe_key) b-tree — it
// degrades to a sequential scan of the workflow's whole timer set. Measured against
// real Postgres with 10k timers on one workflow: 0.917 ms per call as a Seq Scan
// versus 0.047 ms as a range scan. Calling it once per record would make the
// backfill quadratic. The single unconditional DELETE below removes the need for a
// per-record one, so the arm is a plain batched INSERT.
//
// The DELETE is inlined rather than calling Repository.CancelWorkflowTimers because
// that method is bound to the repository's own *gorm.DB and cannot join this
// transaction. The predicate is deliberately identical to it.
//
// OnConflict-DoNothing covers the one case the ordering leaves open: a live write (or
// a second admin's toggle) arming the SAME occurrence between the scan and the insert.
// The unique (workflow_id, dedupe_key) index makes that a no-op rather than an error
// that aborts the batch. It is NOT what protects a concurrently-armed timer from the
// DELETE — nothing in ON CONFLICT can restore a row that is not in the insert list.
func (e *Engine) swapDateFieldTimers(ctx context.Context, tx *gorm.DB, wf *Workflow, p dateFieldParams) (dateFieldBackfillStats, error) {
	var stats dateFieldBackfillStats

	if err := tx.
		Where("workflow_id = ? AND kind = ? AND status = ?", wf.ID, TimerKindDateField, timerStatusPending).
		Delete(&AutomationTimer{}).Error; err != nil {
		return stats, fmt.Errorf("cancel pending date_field timers: %w", err)
	}
	if !wf.IsActive {
		return stats, nil // deactivation: the cancel above is the whole job
	}

	scan, err := e.scanRecordsForBackfill(ctx, tx, wf.OrgID, p.Object, dateFieldBackfillScanLimit)
	if err != nil {
		return stats, fmt.Errorf("scan %s records: %w", p.Object, err)
	}
	stats.Scanned = scan.scanned
	stats.Truncated = scan.truncated
	stats.ExcludedTestLeads = scan.excludedTestLeads

	now := time.Now()
	timers := make([]AutomationTimer, 0, len(scan.records))
	for i := range scan.records {
		rec := &scan.records[i]
		// The scanned map is placed under the object slug so the SAME dotted path the
		// live emitters resolve — "deal.expected_close_at",
		// "contact.custom_fields.renewal_date" — resolves here too. Reading the column
		// directly instead would miss every custom field, which is where all contact
		// and company date fields live.
		fireAt, ok := computeDateFieldFireAt(getNestedValue(map[string]any{p.Object: rec.record}, p.Field), p, now)
		if !ok || !fireAt.After(now) {
			stats.SkippedNoFutureDate++
			continue
		}
		timers = append(timers, buildDateFieldTimer(wf, p, rec, fireAt))
	}
	if len(timers) == 0 {
		return stats, nil
	}

	res := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "workflow_id"}, {Name: "dedupe_key"}},
		DoNothing: true,
	}).CreateInBatches(timers, dateFieldBackfillInsertBatch)
	if res.Error != nil {
		return stats, fmt.Errorf("arm date_field timers: %w", res.Error)
	}
	stats.Armed = int(res.RowsAffected)
	return stats, nil
}

// buildDateFieldTimer produces the same row materializeDateFieldTimers would have
// produced for this record, including the payload shape fireTimerRun reads.
func buildDateFieldTimer(wf *Workflow, p dateFieldParams, rec *backfillRecord, fireAt time.Time) AutomationTimer {
	payload := map[string]any{
		"entity_id": rec.id,
		p.Object:    rec.record, // record snapshot for the run's eval context
		"trigger": map[string]any{
			"type":        TriggerDateField,
			"object":      p.Object,
			"field":       p.Field,
			"offset_days": p.OffsetDays,
			"fire_at":     fireAt.Format(time.RFC3339),
		},
	}
	pj, _ := json.Marshal(payload)
	return AutomationTimer{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		OrgID:      wf.OrgID,
		Kind:       TimerKindDateField,
		Status:     timerStatusPending,
		FireAt:     fireAt,
		DedupeKey:  dateFieldDedupeKey(rec.id, fireAt),
		Payload:    datatypes.JSON(pj),
	}
}

// ── Hook: when a reconcile is owed ────────────────────────────────────────────

// dateFieldParamsFromJSON resolves a stored trigger blob's date_field params.
func dateFieldParamsFromJSON(raw datatypes.JSON) (dateFieldParams, bool) {
	var trig TriggerSpec
	if err := json.Unmarshal(raw, &trig); err != nil {
		return dateFieldParams{}, false
	}
	return dateFieldParamsFromTrigger(trig)
}

// dateFieldReconcileNeededOnUpdate reports whether a PUT changed an active
// date_field workflow's trigger configuration.
//
// The comparison is the point. UpdateWorkflow fires on EVERY save — a rename, a step
// tweak, a canvas nudge — and re-scanning the object on each of those would put a
// full table scan behind the builder's save button. Only a params change needs the
// timer set rebuilt, and when it does the rebuild is mandatory: without it a live
// offset change leaves every armed timer on the old config, and an object change
// orphans the previous object's timers where nothing will ever revisit them.
//
// prevOK false with next ok true is the trigger-became-date_field case, which also
// needs the backfill.
func dateFieldReconcileNeededOnUpdate(prevTrigger datatypes.JSON, wf *Workflow) bool {
	if !isActiveDateFieldWorkflow(wf) {
		return false
	}
	next, ok := dateFieldParamsFromJSON(wf.Trigger)
	if !ok {
		return false
	}
	prev, prevOK := dateFieldParamsFromJSON(prevTrigger)
	return !prevOK || prev != next
}

// scheduleDateFieldReconcile runs the reconcile off the request path.
//
// It MUST be async: a 10k-record org is seconds of batched INSERTs, and the
// pre-batching cost model was minutes — either way, past every proxy timeout for a
// toggle whose HTTP response carries no backfill information anyway.
//
// context.Background(), never the gin context: gin cancels the request context the
// instant the handler returns, which would kill the scan mid-flight and leave the
// workflow's timers deleted and un-rearmed. Same reasoning as the inbound-webhook
// dispatch in handlers.go and fireLifecycleEvent in the record service.
//
// The workflow is copied because the handler's `wf` keeps being read (and serialized)
// after this returns.
// The nil guard LOGS. It used to return in silence, which made a wiring regression —
// a Handler built without an engine — indistinguishable from a healthy toggle: the
// route still answers 200, the workflow still reports itself active, and not one
// pre-existing record is ever armed. There is no later symptom to notice, so the
// early return is the only place it can be said out loud.
func (h *Handler) scheduleDateFieldReconcile(wf *Workflow) {
	if h == nil || h.engine == nil || wf == nil {
		if h != nil && h.logger != nil {
			h.logger.Error("automation: date_field backfill NOT scheduled — handler wiring incomplete",
				"has_engine", h.engine != nil, "has_workflow", wf != nil)
		}
		return
	}
	snapshot := *wf
	go func() {
		// A panic in a bare goroutine takes the whole server with it, and this one
		// runs unattended after the response has already been written.
		defer func() {
			if r := recover(); r != nil {
				h.logger.Error("automation: date_field backfill panicked",
					"panic", fmt.Sprintf("%v", r), "workflow_id", snapshot.ID.String())
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), dateFieldReconcileTimeout)
		defer cancel()
		if _, err := h.engine.reconcileDateFieldTimers(ctx, &snapshot); err != nil {
			h.logger.Warn("automation: date_field backfill failed",
				"error", err, "workflow_id", snapshot.ID.String())
		}
	}()
}

// ── Record scan ───────────────────────────────────────────────────────────────

// backfillRecord is one scanned record: its id, plus the event-payload map the live
// emitters would have built for it.
type backfillRecord struct {
	id     string
	record map[string]any
}

// backfillScan is one object scan's result.
//
// scanned is deliberately separate from len(records): the contact path drops test
// leads, so on that path records is SHORTER, and collapsing the two would make the
// completion log's `scanned` mean "rows read" for deals and "rows kept" for contacts.
type backfillScan struct {
	// records are the rows to arm, after any path-specific filtering.
	records []backfillRecord
	// scanned is how many rows the query returned, BEFORE filtering.
	scanned int
	// truncated is how many rows the cap left unexamined (0 when not reached).
	truncated int64
	// excludedTestLeads is how many scanned contacts were synthetic test leads.
	excludedTestLeads int
}

// scanRecordsForBackfill loads an object's records for arming, oldest first, up to
// limit.
//
// `db` is the reconcile's TRANSACTION, and the record SELECT — the one statement that
// decides which records get armed — must run on it, after the cancel (see this file's
// ORDERING note). Everything else here deliberately does NOT:
//
//   - the truncation count and the contact tag lookup are best-effort/diagnostic, and
//     Postgres aborts the WHOLE transaction on any statement error ("commands ignored
//     until end of transaction block"). Inside the transaction, `if err == nil` stops
//     being best-effort: a deployment missing contact_tags would lose the entire
//     reconcile, not just the tags, and the cancel would roll back with it.
//   - they gain nothing by being inside it either. READ COMMITTED gives every
//     statement its own snapshot, so a second statement in the same transaction is no
//     more consistent with the scan than one on another connection.
//
// Raw SQL with an explicit org_id, NOT RecordService, and both halves of that matter:
//
//   - RecordService reads run through OLS/FLS. Inheriting the toggling admin's
//     request context would scope the scan to the rows THAT ADMIN can see and let
//     field-level security strip the very date field being read — the backfill would
//     arm zero timers and report success.
//   - RecordService also caps at 100 and pages custom objects by OFFSET, which is
//     O(n²) over the range this scan covers.
//
// The maps built below reproduce contactAutomationMap / dealAutomationMap /
// companyAutomationMap (usecase/record_service_system.go) and fireEvent's uniform
// shape for custom objects. They have to: the trigger's field is a dotted PAYLOAD
// path, and every contact and company date field is a nested custom field
// ("contact.custom_fields.renewal_date"), not a column. A map that does not match
// resolves to nil and arms nothing, silently.
func (e *Engine) scanRecordsForBackfill(ctx context.Context, db *gorm.DB, orgID uuid.UUID, slug string, limit int) (backfillScan, error) {
	switch slug {
	case "contact":
		return e.scanContactsForBackfill(ctx, db, orgID, limit)
	case "deal":
		return e.scanDealsForBackfill(ctx, db, orgID, limit)
	case "company":
		return e.scanCompaniesForBackfill(ctx, db, orgID, limit)
	case "":
		return backfillScan{}, fmt.Errorf("empty object slug")
	default:
		return e.scanCustomObjectForBackfill(ctx, db, orgID, slug, limit)
	}
}

// backfillTruncation returns how many rows the cap left unexamined. Called only when
// the scan came back full, so ordinary orgs never pay for the count. `base` must be
// the scan's FROM+WHERE with no Select/Limit applied, built on the ENGINE's handle
// rather than the reconcile's transaction — see scanRecordsForBackfill.
//
// A failed count is logged HERE rather than folded into a silent 0. Returning 0 is
// what the caller has to do — there is no honest number — but reaching this function
// at all means the cap WAS hit, and that fact must not disappear just because the
// follow-up count failed. Silence is precisely the failure mode the cap-logging rule
// exists to prevent.
func (e *Engine) backfillTruncation(base *gorm.DB, scanned, limit int) int64 {
	if scanned < limit {
		return 0
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		e.logger.Warn("automation: date_field backfill hit its scan cap; the remainder could not be counted",
			"error", err, "scan_cap", limit)
		return 0
	}
	if total <= int64(scanned) {
		return 0
	}
	return total - int64(scanned)
}

// isBackfillTestLeadContact reports whether a scanned contact is one of the admin
// "Send test lead" synthetic contacts.
//
// This is the ONE part of write-time silence a backfill can honour, and it has to be
// honoured explicitly. isAutomationSilenced reads a key that exists only on a live
// write's event payload; a scan has no payload, and there is deliberately nothing on
// the record to read (the plan refused an is_test column on customer schema). But
// silence's own justification — integrations/ingest.go: "no date_field timer armed to
// page a rep next week about a contact that does not exist" — is exactly what a naive
// backfill would undo, re-arming every timer WithAutomationSilenced was added to
// prevent.
//
// The substitute is the test lead's persisted identity: integrations.testLeadEmail
// derives a @lead-test.invalid address, and IsTestContactEmail is the single
// definition of what counts as one. Matching on that here rather than on a SQL
// literal keeps one definition, in the package that owns the property.
//
// Deals and companies need no equivalent: a test lead only ever creates a contact.
func isBackfillTestLeadContact(email *string) bool {
	return email != nil && integrations.IsTestContactEmail(*email)
}

type contactBackfillRow struct {
	ID           uuid.UUID      `gorm:"column:id"`
	FirstName    string         `gorm:"column:first_name"`
	LastName     string         `gorm:"column:last_name"`
	Email        *string        `gorm:"column:email"`
	Phone        *string        `gorm:"column:phone"`
	CompanyID    *uuid.UUID     `gorm:"column:company_id"`
	OwnerUserID  *uuid.UUID     `gorm:"column:owner_user_id"`
	CustomFields datatypes.JSON `gorm:"column:custom_fields"`
}

// scanContactsForBackfill mirrors contactAutomationMap.
func (e *Engine) scanContactsForBackfill(ctx context.Context, db *gorm.DB, orgID uuid.UUID, limit int) (backfillScan, error) {
	// One predicate, applied to whichever handle the statement belongs on: the scan
	// goes on the transaction, the count does not.
	base := func(h *gorm.DB) *gorm.DB {
		return h.WithContext(ctx).Table("contacts").Where("org_id = ? AND deleted_at IS NULL", orgID)
	}

	var rows []contactBackfillRow
	if err := base(db).
		Select("id, first_name, last_name, email, phone, company_id, owner_user_id, custom_fields").
		Order(backfillScanOrder).Limit(limit).Find(&rows).Error; err != nil {
		return backfillScan{}, err
	}
	out := backfillScan{scanned: len(rows)}
	out.truncated = e.backfillTruncation(base(e.db), len(rows), limit)

	kept := make([]contactBackfillRow, 0, len(rows))
	for _, r := range rows {
		if isBackfillTestLeadContact(r.Email) {
			out.excludedTestLeads++
			continue
		}
		kept = append(kept, r)
	}

	ids := make([]uuid.UUID, 0, len(kept))
	for _, r := range kept {
		ids = append(ids, r.ID)
	}
	tags := e.contactTagIDsForBackfill(ctx, orgID, ids)

	out.records = make([]backfillRecord, 0, len(kept))
	for _, r := range kept {
		m := map[string]any{
			"id":         r.ID.String(),
			"first_name": r.FirstName,
			"last_name":  r.LastName,
		}
		if r.Email != nil {
			m["email"] = *r.Email
		}
		if r.Phone != nil {
			m["phone"] = *r.Phone
		}
		if r.CompanyID != nil {
			m["company_id"] = r.CompanyID.String()
		}
		if r.OwnerUserID != nil {
			m["owner_user_id"] = r.OwnerUserID.String()
		}
		if ids := tags[r.ID]; len(ids) > 0 {
			m["tags"] = ids
		}
		domain.SetAutomationCustomFields(m, r.CustomFields)
		out.records = append(out.records, backfillRecord{id: r.ID.String(), record: m})
	}
	return out, nil
}

// contactTagIDsForBackfill is the batched form of Handler.contactTagIDs: the same
// unified view (legacy contact_tags first, then object_links 'tags' edges, deduped),
// resolved for every scanned contact in two queries instead of two per contact.
//
// Best-effort like its per-record twin — a missing join table costs tags, not the
// backfill — which is exactly why it runs on e.db and NOT on the reconcile's
// transaction: a swallowed error inside a transaction is not swallowed, it poisons
// every statement after it. See scanRecordsForBackfill.
func (e *Engine) contactTagIDsForBackfill(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) map[uuid.UUID][]string {
	out := map[uuid.UUID][]string{}
	if len(ids) == 0 {
		return out
	}
	seen := map[uuid.UUID]map[string]bool{}
	add := func(contactID, tagID uuid.UUID) {
		s := tagID.String()
		if seen[contactID] == nil {
			seen[contactID] = map[string]bool{}
		}
		if seen[contactID][s] {
			return
		}
		seen[contactID][s] = true
		out[contactID] = append(out[contactID], s)
	}

	type tagEdge struct {
		ContactID uuid.UUID `gorm:"column:contact_id"`
		TagID     uuid.UUID `gorm:"column:tag_id"`
	}
	// Chunked so the placeholder count stays well inside Postgres' 65535 parameter
	// ceiling even at the scan cap.
	const chunk = 1000
	for start := 0; start < len(ids); start += chunk {
		end := min(start+chunk, len(ids))
		batch := ids[start:end]

		var legacy []tagEdge
		if err := e.db.WithContext(ctx).
			Table("contact_tags").
			Joins("JOIN tags ON tags.id = contact_tags.tag_id AND tags.deleted_at IS NULL").
			Where("contact_tags.contact_id IN ?", batch).
			Select("contact_tags.contact_id AS contact_id, contact_tags.tag_id AS tag_id").
			Scan(&legacy).Error; err == nil {
			for _, r := range legacy {
				add(r.ContactID, r.TagID)
			}
		}

		var edges []tagEdge
		if err := e.db.WithContext(ctx).
			Table("object_links").
			Where("org_id = ? AND from_slug = 'contact' AND relation_key = 'tags' AND to_slug = 'tag' AND deleted_at IS NULL AND from_id IN ?", orgID, batch).
			Select("from_id AS contact_id, to_id AS tag_id").
			Scan(&edges).Error; err == nil {
			for _, r := range edges {
				add(r.ContactID, r.TagID)
			}
		}
	}
	return out
}

type dealBackfillRow struct {
	ID              uuid.UUID      `gorm:"column:id"`
	Title           string         `gorm:"column:title"`
	Value           float64        `gorm:"column:value"`
	Probability     int            `gorm:"column:probability"`
	IsWon           bool           `gorm:"column:is_won"`
	IsLost          bool           `gorm:"column:is_lost"`
	ContactID       *uuid.UUID     `gorm:"column:contact_id"`
	CompanyID       *uuid.UUID     `gorm:"column:company_id"`
	StageID         *uuid.UUID     `gorm:"column:stage_id"`
	OwnerUserID     *uuid.UUID     `gorm:"column:owner_user_id"`
	ExpectedCloseAt *time.Time     `gorm:"column:expected_close_at"`
	ClosedAt        *time.Time     `gorm:"column:closed_at"`
	CustomFields    datatypes.JSON `gorm:"column:custom_fields"`
}

// scanDealsForBackfill mirrors dealAutomationMap — including the RFC3339 formatting
// of expected_close_at/closed_at, which is what parseDateValue's first layout expects.
func (e *Engine) scanDealsForBackfill(ctx context.Context, db *gorm.DB, orgID uuid.UUID, limit int) (backfillScan, error) {
	base := func(h *gorm.DB) *gorm.DB {
		return h.WithContext(ctx).Table("deals").Where("org_id = ? AND deleted_at IS NULL", orgID)
	}

	var rows []dealBackfillRow
	if err := base(db).
		Select("id, title, value, probability, is_won, is_lost, contact_id, company_id, stage_id, owner_user_id, expected_close_at, closed_at, custom_fields").
		Order(backfillScanOrder).Limit(limit).Find(&rows).Error; err != nil {
		return backfillScan{}, err
	}
	out := backfillScan{scanned: len(rows)}
	out.truncated = e.backfillTruncation(base(e.db), len(rows), limit)

	out.records = make([]backfillRecord, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":          r.ID.String(),
			"title":       r.Title,
			"value":       r.Value,
			"probability": r.Probability,
			"is_won":      r.IsWon,
			"is_lost":     r.IsLost,
		}
		if r.ContactID != nil {
			m["contact_id"] = r.ContactID.String()
		}
		if r.CompanyID != nil {
			m["company_id"] = r.CompanyID.String()
		}
		if r.StageID != nil {
			m["stage_id"] = r.StageID.String()
		}
		if r.OwnerUserID != nil {
			m["owner_user_id"] = r.OwnerUserID.String()
		}
		if r.ExpectedCloseAt != nil {
			m["expected_close_at"] = r.ExpectedCloseAt.Format(time.RFC3339)
		}
		if r.ClosedAt != nil {
			m["closed_at"] = r.ClosedAt.Format(time.RFC3339)
		}
		domain.SetAutomationCustomFields(m, r.CustomFields)
		out.records = append(out.records, backfillRecord{id: r.ID.String(), record: m})
	}
	return out, nil
}

type companyBackfillRow struct {
	ID           uuid.UUID      `gorm:"column:id"`
	Name         string         `gorm:"column:name"`
	Industry     *string        `gorm:"column:industry"`
	Website      *string        `gorm:"column:website"`
	CustomFields datatypes.JSON `gorm:"column:custom_fields"`
}

// scanCompaniesForBackfill mirrors companyAutomationMap.
func (e *Engine) scanCompaniesForBackfill(ctx context.Context, db *gorm.DB, orgID uuid.UUID, limit int) (backfillScan, error) {
	base := func(h *gorm.DB) *gorm.DB {
		return h.WithContext(ctx).Table("companies").Where("org_id = ? AND deleted_at IS NULL", orgID)
	}

	var rows []companyBackfillRow
	if err := base(db).
		Select("id, name, industry, website, custom_fields").
		Order(backfillScanOrder).Limit(limit).Find(&rows).Error; err != nil {
		return backfillScan{}, err
	}
	out := backfillScan{scanned: len(rows)}
	out.truncated = e.backfillTruncation(base(e.db), len(rows), limit)

	out.records = make([]backfillRecord, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":   r.ID.String(),
			"name": r.Name,
		}
		if r.Industry != nil {
			m["industry"] = *r.Industry
		}
		if r.Website != nil {
			m["website"] = *r.Website
		}
		domain.SetAutomationCustomFields(m, r.CustomFields)
		out.records = append(out.records, backfillRecord{id: r.ID.String(), record: m})
	}
	return out, nil
}

type customObjectBackfillRow struct {
	ID          uuid.UUID      `gorm:"column:id"`
	DisplayName string         `gorm:"column:display_name"`
	OwnerUserID *uuid.UUID     `gorm:"column:owner_user_id"`
	Data        datatypes.JSON `gorm:"column:data"`
}

// scanCustomObjectForBackfill mirrors customToUniform + fireEvent: {id, display_name}
// overlaid with the data blob's keys FLAT (custom objects have no nested
// "custom_fields" wrapper — their trigger field paths are "<slug>.<key>"), then the
// owner_user_id COLUMN last, because customToUniform sets it after unmarshalling data
// so the column wins over a same-named data key.
func (e *Engine) scanCustomObjectForBackfill(ctx context.Context, db *gorm.DB, orgID uuid.UUID, slug string, limit int) (backfillScan, error) {
	base := func(h *gorm.DB) *gorm.DB {
		return h.WithContext(ctx).Table("custom_object_records").
			Joins("JOIN object_defs ON object_defs.id = custom_object_records.object_def_id AND object_defs.deleted_at IS NULL").
			Where("custom_object_records.org_id = ? AND object_defs.org_id = ? AND object_defs.slug = ? AND custom_object_records.deleted_at IS NULL",
				orgID, orgID, slug)
	}

	var rows []customObjectBackfillRow
	if err := base(db).
		Select("custom_object_records.id AS id, custom_object_records.display_name AS display_name, custom_object_records.owner_user_id AS owner_user_id, custom_object_records.data AS data").
		// Spelled out rather than reusing backfillScanOrder: the join puts an `id` and a
		// `created_at` on object_defs too, so an unqualified column here is ambiguous.
		Order("custom_object_records.created_at, custom_object_records.id").
		Limit(limit).Find(&rows).Error; err != nil {
		return backfillScan{}, err
	}
	out := backfillScan{scanned: len(rows)}
	out.truncated = e.backfillTruncation(base(e.db), len(rows), limit)

	out.records = make([]backfillRecord, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":           r.ID.String(),
			"display_name": r.DisplayName,
		}
		if len(r.Data) > 0 {
			var fields map[string]any
			if err := json.Unmarshal(r.Data, &fields); err == nil {
				for k, v := range fields {
					m[k] = v
				}
			}
		}
		if r.OwnerUserID != nil {
			m["owner_user_id"] = r.OwnerUserID.String()
		}
		out.records = append(out.records, backfillRecord{id: r.ID.String(), record: m})
	}
	return out, nil
}
