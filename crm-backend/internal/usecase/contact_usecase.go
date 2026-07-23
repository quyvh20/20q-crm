package usecase

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"path/filepath"
	"strings"

	"crm-backend/internal/ai"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xuri/excelize/v2"
)

// contactEmailUniqueIndex is the partial unique index behind contact email
// dedupe: UNIQUE (org_id, email) WHERE email IS NOT NULL AND deleted_at IS NULL
// (migrations/000003_contact_enhancements.up.sql).
const contactEmailUniqueIndex = "idx_contacts_org_email"

// isContactEmailConflict reports whether a repository error is specifically the
// duplicate-contact-email unique violation.
//
// It matches on the CONSTRAINT NAME, not just SQLSTATE 23505: contacts could grow
// another unique index later, and a constraint-blind check would silently report
// that unrelated conflict as an email duplicate — which, for lead ingestion, means
// taking the "update the existing contact" branch against the wrong row.
//
// Note gorm.ErrDuplicatedKey deliberately is NOT used: the shared DB is opened
// without gorm's TranslateError, so that sentinel never fires here and any
// errors.Is against it would be dead code that always reports false.
func isContactEmailConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == contactEmailUniqueIndex
}

type contactUseCase struct {
	contactRepo domain.ContactRepository
	queue       domain.EmbeddingQueue
	embedSvc    *ai.EmbeddingService
	// ledgerRedactor strips the personal data the lead-capture ledger holds about a
	// contact when that contact is deleted. Nil-tolerant: unset in unit tests and in
	// any build without the integrations module.
	ledgerRedactor LeadLedgerRedactor
	// marketingRedactor collapses a deleted contact's marketing consent PROVENANCE
	// (keeping only the email + status so an opt-out keeps being honored). Nil-
	// tolerant, same as ledgerRedactor.
	marketingRedactor MarketingStateRedactor
}

// LeadLedgerRedactor erases what the inbound-lead ledger stored about one contact —
// the raw payload it arrived in, its capture context, and its consent envelope.
//
// The ledger deliberately outlives the source that fed it, so nothing else would
// ever remove this. A consent record that survives the person it describes is the
// failure a consent feature must not have.
type LeadLedgerRedactor interface {
	RedactForRecord(ctx context.Context, orgID, recordID uuid.UUID) error
	// RedactForRecords is the same erasure for many contacts at once — the bulk
	// delete's path. Separate from a loop over the singular form because a bulk
	// action can carry hundreds of ids.
	RedactForRecords(ctx context.Context, orgID uuid.UUID, recordIDs []uuid.UUID) error
}

// SetLeadLedgerRedactor wires the ledger erasure hook. Called once at startup.
func (uc *contactUseCase) SetLeadLedgerRedactor(r LeadLedgerRedactor) { uc.ledgerRedactor = r }

// MarketingStateRedactor collapses the marketing consent state for one email on
// GDPR erasure: it nulls every provenance column (ip, region, source, CASL/opt-in
// timestamps, the contact link) while PRESERVING email + marketing_status, so an
// opt-out survives the erasure of the person it describes. It is keyed on email,
// not contact id, because marketing consent is email-authoritative and several
// contacts can share one normalized address. The sibling suppression row is left
// standing — retaining the opt-out is the whole point.
type MarketingStateRedactor interface {
	RedactMarketingStateForEmail(ctx context.Context, orgID uuid.UUID, email string) error
}

// SetMarketingStateRedactor wires the marketing-state collapse hook. Called once at
// startup (interface assertion, so usecase never imports the marketing module).
func (uc *contactUseCase) SetMarketingStateRedactor(r MarketingStateRedactor) { uc.marketingRedactor = r }

func NewContactUseCase(repo domain.ContactRepository, queue domain.EmbeddingQueue, embedSvc ...*ai.EmbeddingService) domain.ContactUseCase {
	uc := &contactUseCase{contactRepo: repo, queue: queue}
	if len(embedSvc) > 0 {
		uc.embedSvc = embedSvc[0]
	}
	return uc
}

// ============================================================
// SemanticSearch
// ============================================================

func (uc *contactUseCase) SemanticSearch(ctx context.Context, orgID uuid.UUID, query string, limit int) ([]domain.Contact, error) {
	if uc.embedSvc == nil {
		return nil, domain.NewAppError(503, "semantic search not configured")
	}
	vec, err := uc.embedSvc.EmbedText(ctx, query)
	if err != nil {
		return nil, domain.NewAppError(502, "failed to embed query: "+err.Error())
	}
	return uc.contactRepo.SemanticSearch(ctx, orgID, vec, 0.5, limit)
}

// ============================================================
// List
// ============================================================

func (uc *contactUseCase) List(ctx context.Context, orgID uuid.UUID, f domain.ContactFilter) ([]domain.Contact, string, error) {
	return uc.contactRepo.List(ctx, orgID, f)
}

// ============================================================
// GetByID
// ============================================================

func (uc *contactUseCase) GetByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Contact, error) {
	contact, err := uc.contactRepo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if contact == nil {
		return nil, domain.ErrContactNotFound
	}
	return contact, nil
}

// ============================================================
// Create
// ============================================================

func (uc *contactUseCase) Create(ctx context.Context, orgID uuid.UUID, input domain.CreateContactInput) (*domain.Contact, error) {
	contact := &domain.Contact{
		OrgID:        orgID,
		FirstName:    input.FirstName,
		LastName:     input.LastName,
		Email:        input.Email,
		Phone:        input.Phone,
		CompanyID:    input.CompanyID,
		OwnerUserID:  input.OwnerUserID,
		CustomFields: input.CustomFields,
	}

	if err := uc.contactRepo.Create(ctx, contact); err != nil {
		// A duplicate email is the caller's problem, not a server fault: report it as
		// itself (409) rather than the blanket 500 this used to return. Lead ingestion
		// additionally DEPENDS on telling the two apart — its upsert loop recovers from
		// a lost create race by re-matching and updating, which it cannot do if a
		// duplicate is indistinguishable from a dead connection.
		if isContactEmailConflict(err) {
			return nil, domain.ErrContactEmailExists
		}
		return nil, domain.ErrInternal
	}

	// Handle tags
	if len(input.TagIDs) > 0 {
		if err := uc.contactRepo.ReplaceContactTags(ctx, contact.ID, input.TagIDs); err != nil {
			return nil, domain.ErrInternal
		}
	}

	// Re-fetch with preloads
	result, err := uc.contactRepo.GetByID(ctx, orgID, contact.ID)

	// Queue for embedding
	if err == nil && result != nil && uc.queue != nil {
		uc.queue.EnqueueContact(result)
	}

	return result, err
}

// ============================================================
// Update
// ============================================================

func (uc *contactUseCase) Update(ctx context.Context, orgID, id uuid.UUID, input domain.UpdateContactInput) (*domain.Contact, error) {
	contact, err := uc.contactRepo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if contact == nil {
		return nil, domain.ErrContactNotFound
	}

	// Apply partial updates
	if input.FirstName != nil {
		contact.FirstName = *input.FirstName
	}
	if input.LastName != nil {
		contact.LastName = *input.LastName
	}
	if input.Email != nil {
		contact.Email = input.Email
	}
	if input.Phone != nil {
		contact.Phone = input.Phone
	}
	if input.CompanyID != nil {
		contact.CompanyID = input.CompanyID
	}
	if input.ClearOwner {
		contact.OwnerUserID = nil
	} else if input.OwnerUserID != nil {
		contact.OwnerUserID = input.OwnerUserID
	}
	if input.CustomFields != nil {
		// Merge over the stored custom_fields so a partial uniform edit (the edit form
		// PATCHes only changed keys) doesn't blank the custom fields it didn't touch.
		merged, err := mergeJSONBlob(contact.CustomFields, *input.CustomFields)
		if err != nil {
			return nil, domain.NewAppError(400, "invalid custom field data")
		}
		contact.CustomFields = merged
	}

	if err := uc.contactRepo.Update(ctx, contact); err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			return nil, appErr // e.g. view-only share → 403, not a masked 500
		}
		return nil, domain.ErrInternal
	}

	// Handle tags
	if input.TagIDs != nil {
		if err := uc.contactRepo.ReplaceContactTags(ctx, contact.ID, *input.TagIDs); err != nil {
			return nil, domain.ErrInternal
		}
	}

	result, err := uc.contactRepo.GetByID(ctx, orgID, contact.ID)

	// Queue for embedding
	if err == nil && result != nil && uc.queue != nil {
		uc.queue.EnqueueContact(result)
	}

	return result, err
}

// ============================================================
// Delete
// ============================================================

func (uc *contactUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	// Capture the email BEFORE the soft delete — GetByID filters deleted_at IS NULL,
	// so it cannot be read back afterward, and the marketing-state collapse is keyed
	// on email, not id. Best-effort: a read miss just skips the collapse.
	var email string
	if uc.marketingRedactor != nil {
		if c, err := uc.contactRepo.GetByID(ctx, orgID, id); err == nil && c != nil && c.Email != nil {
			email = *c.Email
		}
	}
	if err := uc.contactRepo.SoftDelete(ctx, orgID, id); err != nil {
		return domain.ErrContactNotFound
	}
	// Erase what the lead ledger holds about this person. Best-effort: the contact is
	// already gone, and failing the delete would leave the customer unable to honour a
	// deletion request at all. The ledger ROW survives (the delivery history is what
	// answers "what happened to this lead"); only what the subject supplied is removed.
	if uc.ledgerRedactor != nil {
		if err := uc.ledgerRedactor.RedactForRecord(ctx, orgID, id); err != nil {
			log.Printf("contact delete: could not redact the lead ledger for %s: %v", id, err)
		}
	}
	// Collapse the marketing consent provenance for this email (keeping email +
	// status so an opt-out keeps being honored). Best-effort, same rationale.
	if uc.marketingRedactor != nil && email != "" {
		if err := uc.marketingRedactor.RedactMarketingStateForEmail(ctx, orgID, email); err != nil {
			log.Printf("contact delete: could not collapse marketing state for %s: %v", id, err)
		}
	}
	return nil
}

// ============================================================
// Count
// ============================================================

func (uc *contactUseCase) Count(ctx context.Context, orgID uuid.UUID) (int64, error) {
	return uc.contactRepo.Count(ctx, orgID)
}

// ============================================================
// BulkAction — delete or assign tag to multiple contacts
// ============================================================

func (uc *contactUseCase) BulkAction(ctx context.Context, orgID uuid.UUID, input domain.BulkActionInput) (*domain.BulkActionResult, error) {
	if len(input.ContactIDs) == 0 {
		return nil, domain.NewAppError(400, "contact_ids must not be empty")
	}

	switch input.Action {
	case "delete":
		deleted, err := uc.contactRepo.BulkDeleteByIDs(ctx, orgID, input.ContactIDs)
		if err != nil {
			return nil, domain.ErrInternal
		}
		// Erase what the lead ledger holds about these people — the half of deletion
		// the bulk path never did. Single-contact delete has redacted since L2.7, so a
		// customer honouring a data-protection request one person at a time was covered
		// and the same customer honouring it over a LIST was not: the raw payload, the
		// capture context and the consent envelope all survived, for every subject.
		//
		// Keyed on `deleted`, never on input.ContactIDs. The delete carries a row-level
		// write scope, so an own-scoped caller's request silently skips records they do
		// not own — and redacting the requested set would let any member with bulk
		// access destroy ledger evidence for contacts they were never allowed to touch.
		//
		// Best-effort, matching the single path: the contacts are already gone, and
		// failing here would report a deletion that did happen as having failed, which
		// is the one answer that makes a deletion request impossible to honour at all.
		if uc.ledgerRedactor != nil && len(deleted) > 0 {
			deletedIDs := make([]uuid.UUID, 0, len(deleted))
			for i := range deleted {
				deletedIDs = append(deletedIDs, deleted[i].ID)
			}
			if err := uc.ledgerRedactor.RedactForRecords(ctx, orgID, deletedIDs); err != nil {
				log.Printf("contact bulk delete: could not redact the lead ledger for %d contact(s): %v", len(deletedIDs), err)
			}
		}
		// Collapse the marketing consent provenance for each actually-deleted contact,
		// keyed on the email from the returned set (same scope discipline as the
		// ledger). Skip nil/empty emails. Best-effort.
		if uc.marketingRedactor != nil {
			for i := range deleted {
				if deleted[i].Email == nil || *deleted[i].Email == "" {
					continue
				}
				if err := uc.marketingRedactor.RedactMarketingStateForEmail(ctx, orgID, *deleted[i].Email); err != nil {
					log.Printf("contact bulk delete: could not collapse marketing state for %s: %v", deleted[i].ID, err)
				}
			}
		}
		return &domain.BulkActionResult{
			Affected: len(deleted),
			Message:  fmt.Sprintf("%d contact(s) deleted", len(deleted)),
		}, nil

	case "assign_tag":
		if input.TagID == nil {
			return nil, domain.NewAppError(400, "tag_id is required for assign_tag action")
		}
		n, err := uc.contactRepo.BulkAssignTag(ctx, orgID, input.ContactIDs, *input.TagID)
		if err != nil {
			return nil, domain.ErrInternal
		}
		return &domain.BulkActionResult{
			Affected: int(n),
			Message:  fmt.Sprintf("tag assigned to %d contact(s)", len(input.ContactIDs)),
		}, nil

	default:
		return nil, domain.NewAppError(400, "unsupported action: must be 'delete' or 'assign_tag'")
	}
}

// ============================================================
// BulkImport — CSV / XLSX
// ============================================================

func (uc *contactUseCase) BulkImport(ctx context.Context, orgID uuid.UUID, file multipart.File, filename string, conflictMode string) (*domain.ImportResult, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	var rows [][]string
	var err error

	switch ext {
	case ".csv":
		rows, err = parseCSV(file)
	case ".xlsx":
		rows, err = parseXLSX(file)
	default:
		return nil, domain.ErrInvalidFile
	}

	if err != nil {
		return nil, domain.NewAppError(400, fmt.Sprintf("failed to parse file: %v", err))
	}

	if len(rows) < 2 {
		return nil, domain.NewAppError(400, "file must contain a header row and at least one data row")
	}

	// Map column headers
	header := rows[0]
	colMap := mapColumns(header)

	result := &domain.ImportResult{}
	seen := make(map[string]bool)               // within-batch email dedup
	companyCache := make(map[string]*uuid.UUID) // company name → ID cache (avoids per-row DB lookup)
	var contacts []domain.Contact

	for i, row := range rows[1:] {
		lineNum := i + 2

		firstName := getCol(row, colMap, "first_name")
		lastName := getCol(row, colMap, "last_name")
		email := getCol(row, colMap, "email")
		phone := getCol(row, colMap, "phone")
		companyName := getCol(row, colMap, "company_name")
		tagsStr := getCol(row, colMap, "tags")

		if firstName == "" {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("row %d: missing first_name", lineNum))
			continue
		}

		// Deduplicate by email within batch
		if email != "" {
			key := strings.ToLower(email)
			if seen[key] {
				result.Skipped++
				continue
			}
			seen[key] = true
		}

		c := domain.Contact{
			OrgID:     orgID,
			FirstName: firstName,
			LastName:  lastName,
		}
		if email != "" {
			c.Email = &email
		}
		if phone != "" {
			c.Phone = &phone
		}

		// Resolve company — use cache to avoid N DB round-trips for same name
		if companyName != "" {
			cacheKey := strings.ToLower(companyName)
			if cachedID, ok := companyCache[cacheKey]; ok {
				c.CompanyID = cachedID
			} else {
				company, err := uc.contactRepo.FindCompanyByName(ctx, orgID, companyName)
				if err != nil {
					result.Errors++
					result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("row %d: failed to lookup company", lineNum))
					continue
				}
				if company == nil {
					company = &domain.Company{OrgID: orgID, Name: companyName}
					if err := uc.contactRepo.CreateCompany(ctx, company); err != nil {
						result.Errors++
						result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("row %d: failed to create company", lineNum))
						continue
					}
				}
				companyCache[cacheKey] = &company.ID
				c.CompanyID = &company.ID
			}
		}

		// When overwrite mode: check for existing contact by email and update instead
		if conflictMode == "overwrite" && email != "" {
			// Email, not Q: Q is a fuzzy search (company name, phone, last-word prefix),
			// so it can return a near-miss that the EqualFold below then rejects. The row
			// then falls through to the insert path, where ON CONFLICT DO NOTHING drops
			// it because the address already exists — so the edit is SILENTLY LOST and
			// the import still reports success. See contact_import_dedupe_test.go.
			existing, _, _ := uc.contactRepo.List(ctx, orgID, domain.ContactFilter{Email: email, Limit: 1})
			if len(existing) > 0 && existing[0].Email != nil && strings.EqualFold(*existing[0].Email, email) {
				// Mutate the existing contact and save via repo.Update(*Contact)
				updated := existing[0]
				updated.FirstName = firstName
				updated.LastName = lastName
				updated.Email = c.Email
				updated.Phone = c.Phone
				updated.CompanyID = c.CompanyID
				err := uc.contactRepo.Update(ctx, &updated)
				if err != nil {
					result.Errors++
					result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("row %d: failed to update existing contact", lineNum))
				} else {
					result.Created++ // count as "processed"
					// Handle tags for this overwritten contact
					if tagsStr != "" {
						tagNames := splitTags(tagsStr)
						tagIDs, _ := uc.resolveTagIDs(ctx, orgID, tagNames)
						_ = uc.contactRepo.ReplaceContactTags(ctx, existing[0].ID, tagIDs)
					}
				}
				continue
			}
		}

		contacts = append(contacts, c)

		// Handle tags after bulk insert
		_ = tagsStr
	}

	if len(contacts) > 0 {
		created, err := uc.contactRepo.BulkCreate(ctx, contacts)
		if err != nil {
			return nil, domain.ErrInternal
		}
		result.Created += int(created)
		result.Skipped += len(contacts) - int(created)
	}

	// Handle tags for imported contacts (resolve tag names → IDs)
	for i, row := range rows[1:] {
		tagsStr := getCol(row, colMap, "tags")
		if tagsStr == "" {
			continue
		}
		email := getCol(row, colMap, "email")
		if email == "" {
			continue
		}

		// Find the contact we just inserted. Email, not Q, and the distinction is
		// load-bearing here: this row's tags are REPLACED with no exactness check
		// after the lookup, so a fuzzy match that returned a near-miss (Q treats the
		// last word as a prefix, so "bob@example.com" also matches
		// "bob@example.com.au") would wipe an unrelated contact's tags.
		contactList, _, _ := uc.contactRepo.List(ctx, orgID, domain.ContactFilter{Email: email, Limit: 1})
		if len(contactList) == 0 {
			continue
		}

		tagNames := splitTags(tagsStr)
		tagIDs, err := uc.resolveTagIDs(ctx, orgID, tagNames)
		if err != nil {
			result.ErrorDetails = append(result.ErrorDetails, fmt.Sprintf("row %d: failed to resolve tags", i+2))
			continue
		}
		_ = uc.contactRepo.ReplaceContactTags(ctx, contactList[0].ID, tagIDs)
	}

	return result, nil
}

// ============================================================
// Helpers
// ============================================================

func (uc *contactUseCase) resolveTagIDs(ctx context.Context, orgID uuid.UUID, names []string) ([]uuid.UUID, error) {
	existing, err := uc.contactRepo.FindTagsByNames(ctx, orgID, names)
	if err != nil {
		return nil, err
	}

	existingMap := make(map[string]uuid.UUID)
	for _, t := range existing {
		existingMap[strings.ToLower(t.Name)] = t.ID
	}

	var newTags []domain.Tag
	for _, name := range names {
		if _, ok := existingMap[strings.ToLower(name)]; !ok {
			newTags = append(newTags, domain.Tag{OrgID: orgID, Name: name})
		}
	}

	if len(newTags) > 0 {
		if err := uc.contactRepo.CreateTags(ctx, newTags); err != nil {
			return nil, err
		}
		for _, t := range newTags {
			existingMap[strings.ToLower(t.Name)] = t.ID
		}
	}

	var ids []uuid.UUID
	for _, name := range names {
		if id, ok := existingMap[strings.ToLower(name)]; ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func parseCSV(r io.Reader) ([][]string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // allow variable fields
	return reader.ReadAll()
}

func parseXLSX(r io.Reader) ([][]string, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("no sheets found")
	}

	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func mapColumns(header []string) map[string]int {
	m := make(map[string]int)
	aliases := map[string][]string{
		"first_name":   {"first_name", "firstname", "first name", "name"},
		"last_name":    {"last_name", "lastname", "last name", "surname"},
		"email":        {"email", "e-mail", "email address"},
		"phone":        {"phone", "telephone", "phone number", "mobile"},
		"company_name": {"company_name", "company", "organization", "org"},
		"tags":         {"tags", "tag", "labels"},
	}

	for _, h := range header {
		normalized := strings.ToLower(strings.TrimSpace(h))
		for field, options := range aliases {
			for _, alias := range options {
				if normalized == alias {
					m[field] = indexOf(header, h)
					break
				}
			}
		}
	}
	return m
}

func indexOf(slice []string, item string) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return -1
}

func getCol(row []string, colMap map[string]int, field string) string {
	idx, ok := colMap[field]
	if !ok || idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func splitTags(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
