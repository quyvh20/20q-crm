package automation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// engagement.go is the automation-side half of the email-engagement bridge
// (arc G, engagement_and_split_plan.md): the marketing Resend webhook
// processor resolves an opened email to a contact through this loader, then
// starts workflows via Engine.TriggerEvent with the returned field map under
// the "contact" payload key. package marketing imports package automation
// (never the reverse), so handing these methods to the processor from main.go
// is the established dependency direction.

// isEngagementTrigger reports whether an event type is one of the marketing
// engagement triggers, which share a campaign filter and a per-message run key.
func isEngagementTrigger(eventType string) bool {
	return eventType == TriggerEmailOpened || eventType == TriggerEmailClicked
}

// emailOpenedIdempKey keys an email_opened run once per (workflow, contact,
// message) — repeated pixel loads of one email are absorbed; a different
// message still enrolls. Extracted pure so the dedupe contract is pinned by
// tests (a payload-key rename silently reverting to the per-minute key is the
// regression class this guards).
func emailOpenedIdempKey(wfID uuid.UUID, eventType, entityID, emailID string) string {
	return fmt.Sprintf("%x", sha256.Sum256(
		[]byte(fmt.Sprintf("%s:%s:%s:%s", wfID.String(), eventType, entityID, emailID)),
	))
}

// campaignPinMatches compares a trigger's pinned campaign id against the
// event's canonical id, tolerating the non-canonical but parseable forms the
// validator accepts (uppercase, braced, urn:) — a raw string compare would
// silently never-match them and the workflow would never fire.
func campaignPinMatches(req, actual string) bool {
	if req == actual {
		return true
	}
	ru, err1 := uuid.Parse(req)
	au, err2 := uuid.Parse(actual)
	return err1 == nil && err2 == nil && ru == au
}

// resumeEventWaits wakes every run parked on a wait-for-event step that this
// engagement event satisfies. Best-effort by design: a failure here leaves the
// run parked on its timeout deadline, which completes the step with
// happened=false — the same outcome as an event that never arrived.
//
// The claim is a single status-guarded UPDATE ... RETURNING (ClaimEventWaits),
// so concurrent webhook drains and multiple app instances can never resume the
// same run twice.
func (e *Engine) resumeEventWaits(ctx context.Context, orgID uuid.UUID, eventType string, payload map[string]any) {
	if e.repo == nil {
		return
	}
	contactID, ok := payloadContactID(payload)
	if !ok {
		return
	}
	var campaignID *uuid.UUID
	if raw, _ := payload["campaign_id"].(string); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			campaignID = &id
		}
	}

	waits, err := e.repo.ClaimEventWaits(ctx, orgID, eventType, contactID, campaignID)
	if err != nil {
		e.logger.Warn("automation: claim event waits failed", "error", err, "org_id", orgID.String(), "event", eventType)
		return
	}
	now := time.Now()
	for _, w := range waits {
		detail := map[string]any{"event_satisfied": true, "event_at": now, "event_type": eventType}
		if link, _ := payload["link"].(string); link != "" {
			detail["link"] = link
		}
		woken, err := e.repo.SatisfyWaitingRun(ctx, w.RunID, w.StepPath, now, detail)
		if err != nil {
			e.logger.Warn("automation: satisfy waiting run failed", "error", err, "run_id", w.RunID.String())
			continue
		}
		if !woken {
			// The run left `waiting` first (timeout swept it, or it failed) —
			// its own path owns the outcome now.
			continue
		}
		e.logger.Info("automation: resuming run on engagement event",
			"run_id", w.RunID.String(), "event", eventType, "step_path", w.StepPath)
		// Hand it to a worker immediately; the 30s clock sweep is the fallback
		// (SatisfyWaitingRun set next_retry_at = NOW()).
		select {
		case e.jobs <- WorkflowRunJob{RunID: w.RunID}:
		default:
			e.logger.Warn("automation: jobs channel full — the sweeper will pick the resumed run up",
				"run_id", w.RunID.String())
		}
	}
}

// payloadContactID reads the contact the event is about. entity_id is the
// contact for engagement events specifically (the marketing emitter resolves a
// contact before emitting); the contact map is the fallback.
func payloadContactID(payload map[string]any) (uuid.UUID, bool) {
	if raw, _ := payload["entity_id"].(string); raw != "" {
		if id, err := uuid.Parse(raw); err == nil {
			return id, true
		}
	}
	if c, _ := payload["contact"].(map[string]any); c != nil {
		if raw, _ := c["id"].(string); raw != "" {
			if id, err := uuid.Parse(raw); err == nil {
				return id, true
			}
		}
	}
	return uuid.Nil, false
}

// LoadContactForTrigger resolves the contact a marketing engagement event
// belongs to and returns its event-map form (the exact shape natural CRM
// emits produce — see loadContactMapDB). Resolution order:
//
//  1. by contactID when non-Nil (the send's contact_id attribution tag);
//  2. by normalized email — newest live contact wins, mirroring how analytics
//     attribute ledger rows by email_normalized. Also the single fallback when
//     the stamped contact no longer exists (merged/deleted since the send).
//
// Returns (nil, uuid.Nil, nil) when no contact matches: the caller skips the
// emit (an open by an address that is no longer a contact is not an event any
// workflow can act on).
func (e *Engine) LoadContactForTrigger(ctx context.Context, orgID, contactID uuid.UUID, email string) (map[string]any, uuid.UUID, error) {
	if e.db == nil {
		return nil, uuid.Nil, errors.New("engagement loader: engine has no db")
	}

	// Attempt 1: the stamped contact id, when the send carried one.
	if contactID != uuid.Nil {
		m, err := loadContactMapDB(ctx, e.db, orgID, contactID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, uuid.Nil, err
		}
		if m != nil {
			return m, contactID, nil
		}
		// Stamped contact gone — fall through to the email identity once.
	}

	// Attempt 2: newest live contact with the recipient's normalized email.
	norm := strings.ToLower(strings.TrimSpace(email))
	if norm == "" {
		return nil, uuid.Nil, nil
	}
	// Deterministic tiebreak (", id"): duplicate (org_id, email) contacts are an
	// acknowledged prod state and bulk imports stamp identical updated_at — an
	// arbitrary tied pick would resolve DIFFERENT contacts across a repend,
	// breaking the once-per-message run dedupe (its key includes the entity id).
	var ids []uuid.UUID
	err := e.db.WithContext(ctx).
		Table("contacts").
		Where("org_id = ? AND lower(email) = ? AND deleted_at IS NULL", orgID, norm).
		Order("updated_at DESC, id").
		Limit(1).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, uuid.Nil, err
	}
	if len(ids) == 0 {
		return nil, uuid.Nil, nil
	}
	m, err := loadContactMapDB(ctx, e.db, orgID, ids[0])
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, uuid.Nil, err
	}
	if m == nil {
		return nil, uuid.Nil, nil
	}
	return m, ids[0], nil
}
