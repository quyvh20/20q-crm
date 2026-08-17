package automation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

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
