package usecase

import (
	"context"
	"encoding/json"
	"fmt"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ContactMergeUseCase is R8.3: duplicate detection + merge, contacts first.
// It is a SEPARATE usecase from ContactUseCase (rather than a method on it)
// because it depends on domain.RecordService, and RecordService's contact
// adapter wraps ContactUseCase — folding merge into ContactUseCase would make
// that a dependency cycle. Constructed after recordService in main.go.
type ContactMergeUseCase interface {
	ListDuplicates(ctx context.Context, orgID uuid.UUID) ([]domain.DuplicateGroup, error)
	// Merge folds loserID into survivorID: every cross-reference (object_links,
	// tasks, activities, contact_tags) is repointed, CustomFields are unioned
	// (survivor wins on key conflicts), then the loser is deleted through
	// RecordService — so it gets the same ledger redaction and audit-delete
	// entry a normal contact delete gets. Finally writes a "merged from" audit
	// row on the survivor.
	Merge(ctx context.Context, orgID, actorID, survivorID, loserID uuid.UUID) (*domain.Contact, error)
}

type contactMergeUseCase struct {
	contactRepo domain.ContactRepository
	authz       domain.RecordAuthorizer
	records     domain.RecordService
}

func NewContactMergeUseCase(contactRepo domain.ContactRepository, authz domain.RecordAuthorizer, records domain.RecordService) ContactMergeUseCase {
	return &contactMergeUseCase{contactRepo: contactRepo, authz: authz, records: records}
}

func (uc *contactMergeUseCase) ListDuplicates(ctx context.Context, orgID uuid.UUID) ([]domain.DuplicateGroup, error) {
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		return nil, err
	}
	return uc.contactRepo.ListPhoneDuplicateGroups(ctx, orgID)
}

func (uc *contactMergeUseCase) Merge(ctx context.Context, orgID, actorID, survivorID, loserID uuid.UUID) (*domain.Contact, error) {
	if survivorID == loserID {
		return nil, domain.NewAppError(400, "cannot merge a contact into itself")
	}
	// Merging changes the survivor and destroys the loser, so both verbs are
	// checked up front rather than letting RecordService.Delete discover the
	// delete half of this is unauthorized after the repoint already ran.
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		return nil, err
	}
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionDelete); err != nil {
		return nil, err
	}

	survivor, err := uc.contactRepo.GetByID(ctx, orgID, survivorID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if survivor == nil {
		return nil, domain.ErrContactNotFound
	}
	loser, err := uc.contactRepo.GetByID(ctx, orgID, loserID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if loser == nil {
		return nil, domain.ErrContactNotFound
	}

	if err := uc.contactRepo.MergeRepoint(ctx, orgID, survivorID, loserID); err != nil {
		return nil, fmt.Errorf("merge repoint: %w", err)
	}

	merged, changed := unionCustomFields(survivor.CustomFields, loser.CustomFields)
	if changed {
		survivor.CustomFields = merged
		if err := uc.contactRepo.Update(ctx, survivor); err != nil {
			return nil, fmt.Errorf("merge custom fields: %w", err)
		}
	}

	// Through RecordService, not contactRepo.SoftDelete directly: the contact
	// adapter's delete calls ContactUseCase.Delete underneath (lead-ledger +
	// marketing-consent redaction), and RecordService.Delete additionally fires
	// the audit-delete entry and cascades the loser's now-stale object_links.
	if err := uc.records.Delete(ctx, orgID, "contact", loserID); err != nil {
		return nil, fmt.Errorf("delete merged contact: %w", err)
	}

	uc.authz.Audit(ctx, domain.AuditEntry{
		OrgID:      orgID,
		ActorID:    actorID,
		ObjectSlug: "contact",
		RecordID:   survivorID,
		Action:     domain.ActionEdit,
		Changes:    map[string]interface{}{"merged_from": loserID.String()},
	})

	return survivor, nil
}

// unionCustomFields merges loser's keys under survivor's, with survivor
// winning any key both have — same semantics as the plan's "union
// CustomFields JSON, survivor wins conflicts". changed is false (and merged
// is the zero value) when there is nothing to union, so the caller can skip
// a needless Update.
func unionCustomFields(survivor, loser domain.JSON) (domain.JSON, bool) {
	if len(loser) == 0 || string(loser) == "null" {
		return survivor, false
	}
	var loserMap, survivorMap map[string]interface{}
	if err := json.Unmarshal(loser, &loserMap); err != nil || len(loserMap) == 0 {
		return survivor, false
	}
	if len(survivor) > 0 && string(survivor) != "null" {
		if err := json.Unmarshal(survivor, &survivorMap); err != nil {
			survivorMap = nil
		}
	}
	if survivorMap == nil {
		survivorMap = map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(loserMap)+len(survivorMap))
	for k, v := range loserMap {
		out[k] = v
	}
	for k, v := range survivorMap {
		out[k] = v // survivor wins
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return survivor, false
	}
	return domain.JSON(raw), true
}
