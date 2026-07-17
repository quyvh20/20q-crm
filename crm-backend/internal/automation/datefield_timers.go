package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// datefield_timers.go implements the A4 `date_field` trigger: "fire N days
// before/after <record>.<date field> at <time>". Timers are materialized
// event-driven from the record write chokepoint (RecordService.fireEvent →
// Engine.TriggerEvent) rather than by scanning records, so firing is O(due) not
// O(records). Correctness mirrors the schedule trigger (timers.go):
//   - the dedupe_key embeds the record id AND the resolved fire time, so a moved
//     date produces a new occurrence and the stale (not-yet-fired) one is cancelled;
//   - the unique (workflow_id, dedupe_key) index + the run's occurrence-derived
//     idempotency key give exactly-once firing even under re-materialization/retry.
//
// Scope note: materialization is write-driven. Records that already exist when a
// date_field workflow is activated are armed on their NEXT write — a full backfill
// scan of pre-existing records is a deliberate follow-up (kept out to avoid a
// generic per-object record scan). Config changes (field/offset/time) apply to
// timers materialized AFTER the change; already-armed timers fire with the config
// captured at materialization.

const TimerKindDateField = "date_field"

// dateFieldParams holds the resolved trigger.params for a date_field trigger.
type dateFieldParams struct {
	Object     string
	Field      string // dotted payload path, e.g. "deal.expected_close_at"
	OffsetDays int    // negative = before the date, positive = after
	AtTime     string // "HH:MM"; empty → default 09:00
	Timezone   string // IANA zone; empty → UTC
}

// dateFieldParamsFromTrigger extracts a date_field trigger's params. ok is false
// unless it's a date_field trigger with a non-empty object and field.
func dateFieldParamsFromTrigger(t TriggerSpec) (dateFieldParams, bool) {
	var p dateFieldParams
	if t.Type != TriggerDateField || t.Params == nil {
		return p, false
	}
	p.Object, _ = t.Params["object"].(string)
	p.Field, _ = t.Params["field"].(string)
	switch v := t.Params["offset_days"].(type) {
	case float64:
		p.OffsetDays = int(v)
	case int:
		p.OffsetDays = v
	}
	p.AtTime, _ = t.Params["at_time"].(string)
	p.Timezone, _ = t.Params["timezone"].(string)
	return p, p.Object != "" && p.Field != ""
}

// parseHHMM parses a "HH:MM" 24-hour time. Shared by the validator and fire-time
// computation so a value that validates also resolves.
func parseHHMM(s string) (hour, minute int, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// dateFieldLayouts are the accepted stored date/datetime formats: RFC3339 (deal
// expected_close_at/closed_at), a timezone-less datetime, and a plain date.
var dateFieldLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"}

// parseDateValue interprets a date field value from an event payload.
func parseDateValue(v any) (time.Time, bool) {
	switch val := v.(type) {
	case time.Time:
		return val, true
	case string:
		if val == "" {
			return time.Time{}, false
		}
		for _, layout := range dateFieldLayouts {
			if t, err := time.Parse(layout, val); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// computeDateFieldFireAt resolves the absolute UTC firing moment for a record's
// date value: take the field's calendar date, set the time-of-day to at_time in
// the target timezone, then shift by offset_days. The date's calendar day is used
// as-is (tz-agnostic) so "3 days before 2026-07-15" is always relative to Jul 15;
// only the time-of-day is interpreted in the trigger's timezone.
func computeDateFieldFireAt(dateVal any, p dateFieldParams, now time.Time) (time.Time, bool) {
	base, ok := parseDateValue(dateVal)
	if !ok {
		return time.Time{}, false
	}
	loc := time.UTC
	if p.Timezone != "" {
		if l, err := time.LoadLocation(p.Timezone); err == nil {
			loc = l
		}
	}
	hour, minute := 9, 0
	if h, m, valid := parseHHMM(p.AtTime); valid {
		hour, minute = h, m
	}
	y, mon, d := base.Date()
	fire := time.Date(y, mon, d, hour, minute, 0, 0, loc).AddDate(0, 0, p.OffsetDays)
	return fire.UTC(), true
}

// dateFieldDedupePrefix scopes a record's date_field timers within a workflow so
// stale (moved-date) occurrences can be cancelled by prefix.
func dateFieldDedupePrefix(recordID string) string {
	return "df:" + recordID + ":"
}

// dateFieldDedupeKey identifies one (record, fire time) occurrence. A moved date →
// new fire time → new key; the fired run derives its idempotency key from this.
func dateFieldDedupeKey(recordID string, fireAt time.Time) string {
	return dateFieldDedupePrefix(recordID) + strconv.FormatInt(fireAt.Unix(), 10)
}

// splitObjectEvent parses "{slug}_{created|updated|deleted}" into its parts. ok is
// false for any other event type (deal_stage_changed, schedule, webhook, …).
func splitObjectEvent(eventType string) (slug, event string, ok bool) {
	for _, ev := range []string{"created", "updated", "deleted"} {
		if strings.HasSuffix(eventType, "_"+ev) {
			return strings.TrimSuffix(eventType, "_"+ev), ev, true
		}
	}
	return "", "", false
}

// ── Repository: date_field timer persistence ──────────────────────────────────

// ActiveDateFieldWorkflowsForObject lists active date_field workflows in an org
// whose trigger targets the given object slug (the materialization candidates for
// one record write).
func (r *Repository) ActiveDateFieldWorkflowsForObject(ctx context.Context, orgID uuid.UUID, slug string) ([]Workflow, error) {
	var wfs []Workflow
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND is_active = ? AND trigger->>'type' = ? AND trigger->'params'->>'object' = ?",
			orgID, true, TriggerDateField, slug).
		Find(&wfs).Error
	return wfs, err
}

// MaterializeDateFieldTimer arms a record's next date_field occurrence: in one
// transaction it cancels the record's other pending timers for this workflow (a
// moved date → different dedupe_key) then upserts the current occurrence. The
// upsert is OnConflict-DoNothing so re-materializing the SAME occurrence (an
// unrelated field changed, or a fired occurrence) is a no-op and never resurrects
// or duplicates a row.
func (r *Repository) MaterializeDateFieldTimer(ctx context.Context, t *AutomationTimer, recordID string) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	prefix := dateFieldDedupePrefix(recordID)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("workflow_id = ? AND kind = ? AND status = ? AND dedupe_key LIKE ? AND dedupe_key <> ?",
				t.WorkflowID, TimerKindDateField, timerStatusPending, prefix+"%", t.DedupeKey).
			Delete(&AutomationTimer{}).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "workflow_id"}, {Name: "dedupe_key"}},
			DoNothing: true,
		}).Create(t).Error
	})
}

// CancelDateFieldTimersForRecord DELETES a record's pending date_field timers for a
// workflow (the record was deleted, or its date cleared / moved to the past).
func (r *Repository) CancelDateFieldTimersForRecord(ctx context.Context, workflowID uuid.UUID, recordID string) error {
	return r.db.WithContext(ctx).
		Where("workflow_id = ? AND kind = ? AND status = ? AND dedupe_key LIKE ?",
			workflowID, TimerKindDateField, timerStatusPending, dateFieldDedupePrefix(recordID)+"%").
		Delete(&AutomationTimer{}).Error
}

// ── Engine: materialization ───────────────────────────────────────────────────

// materializeDateFieldTimers reconciles every active date_field workflow's timer
// for the record in a create/update/delete event. Called for every record write
// (from TriggerEvent), it is a cheap no-op when the org has no date_field workflow
// for the object. Best-effort per workflow — one bad config doesn't stop the rest.
func (e *Engine) materializeDateFieldTimers(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any) error {
	slug, event, ok := splitObjectEvent(eventType)
	if !ok {
		return nil // not a record create/update/delete (e.g. schedule, stage change)
	}
	wfs, err := e.repo.ActiveDateFieldWorkflowsForObject(ctx, orgID, slug)
	if err != nil {
		return fmt.Errorf("query date_field workflows: %w", err)
	}
	if len(wfs) == 0 {
		return nil
	}
	recordID := ""
	if id, ok := payload["entity_id"]; ok {
		recordID = fmt.Sprintf("%v", id)
	}
	if recordID == "" {
		return nil
	}

	now := time.Now()
	tzCache := map[uuid.UUID]string{} // memoize the author-timezone lookup within this call
	for i := range wfs {
		wf := &wfs[i]
		var trig TriggerSpec
		if err := json.Unmarshal(wf.Trigger, &trig); err != nil {
			continue
		}
		p, ok := dateFieldParamsFromTrigger(trig)
		if !ok || p.Object != slug {
			continue
		}

		// A delete removes the record → cancel its pending occurrence. Handled
		// BEFORE resolving the timezone since the delete path never uses it.
		if event == "deleted" {
			if err := e.repo.CancelDateFieldTimersForRecord(ctx, wf.ID, recordID); err != nil {
				e.logger.Warn("automation: cancel date_field timer (delete) failed", "error", err, "workflow_id", wf.ID.String())
			}
			continue
		}

		// No explicit timezone on the trigger → inherit the author's preference
		// (U2); empty stays UTC as before. Cached so N no-tz workflows for the
		// same author cost one lookup, not N.
		if p.Timezone == "" {
			tz, seen := tzCache[wf.CreatedBy]
			if !seen {
				tz = e.repo.UserTimezone(ctx, wf.CreatedBy)
				tzCache[wf.CreatedBy] = tz
			}
			p.Timezone = tz
		}

		fireAt, ok := computeDateFieldFireAt(getNestedValue(payload, p.Field), p, now)
		if !ok || !fireAt.After(now) {
			// No valid future occurrence (date empty/unparseable, or already past) →
			// drop any stale pending timer for this record.
			if err := e.repo.CancelDateFieldTimersForRecord(ctx, wf.ID, recordID); err != nil {
				e.logger.Warn("automation: cancel stale date_field timer failed", "error", err, "workflow_id", wf.ID.String())
			}
			continue
		}

		// A silenced write (a test lead) arms nothing. Placed HERE, around the arm
		// alone, rather than at the top of the function: both branches above CANCEL,
		// and a cancel only ever disarms. Skipping the function wholesale would strand
		// the very timer silence exists to prevent — the record's delete would stop
		// cancelling it.
		//
		// Arming is also the only moment silence can be honored. fireTimerRun builds
		// its run straight from the stored payload and consults no suppression
		// predicate, so carrying the flag into timerPayload below would read like a
		// fix and change nothing.
		if isAutomationSilenced(payload) {
			continue
		}

		timerPayload := map[string]any{
			"entity_id": recordID,
			slug:        payload[slug], // record snapshot for the run's eval context
			"trigger": map[string]any{
				"type":        TriggerDateField,
				"object":      p.Object,
				"field":       p.Field,
				"offset_days": p.OffsetDays,
				"fire_at":     fireAt.Format(time.RFC3339),
			},
		}
		pj, _ := json.Marshal(timerPayload)
		timer := &AutomationTimer{
			WorkflowID: wf.ID,
			OrgID:      orgID,
			Kind:       TimerKindDateField,
			Status:     timerStatusPending,
			FireAt:     fireAt,
			DedupeKey:  dateFieldDedupeKey(recordID, fireAt),
			Payload:    datatypes.JSON(pj),
		}
		if err := e.repo.MaterializeDateFieldTimer(ctx, timer, recordID); err != nil {
			e.logger.Warn("automation: materialize date_field timer failed", "error", err, "workflow_id", wf.ID.String())
		}
	}
	return nil
}
