package usecase

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
)

type contactUseCase struct {
	contactRepo domain.ContactRepository
}

func NewContactUseCase(repo domain.ContactRepository) domain.ContactUseCase {
	return &contactUseCase{contactRepo: repo}
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
		return nil, domain.ErrInternal
	}

	// Handle tags
	if len(input.TagIDs) > 0 {
		if err := uc.contactRepo.ReplaceContactTags(ctx, contact.ID, input.TagIDs); err != nil {
			return nil, domain.ErrInternal
		}
	}

	// Re-fetch with preloads
	return uc.contactRepo.GetByID(ctx, orgID, contact.ID)
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
	if input.OwnerUserID != nil {
		contact.OwnerUserID = input.OwnerUserID
	}
	if input.CustomFields != nil {
		contact.CustomFields = *input.CustomFields
	}

	if err := uc.contactRepo.Update(ctx, contact); err != nil {
		return nil, domain.ErrInternal
	}

	// Handle tags
	if input.TagIDs != nil {
		if err := uc.contactRepo.ReplaceContactTags(ctx, contact.ID, *input.TagIDs); err != nil {
			return nil, domain.ErrInternal
		}
	}

	return uc.contactRepo.GetByID(ctx, orgID, contact.ID)
}

// ============================================================
// Delete
// ============================================================

func (uc *contactUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	if err := uc.contactRepo.SoftDelete(ctx, orgID, id); err != nil {
		return domain.ErrContactNotFound
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
	seen := make(map[string]bool)
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

		// Resolve company
		if companyName != "" {
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
			c.CompanyID = &company.ID
		}

		// When overwrite mode: check for existing contact by email and update instead
		if conflictMode == "overwrite" && email != "" {
			existing, _, _ := uc.contactRepo.List(ctx, orgID, domain.ContactFilter{Q: email, Limit: 1})
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

		// Find the contact we just inserted
		contactList, _, _ := uc.contactRepo.List(ctx, orgID, domain.ContactFilter{Q: email, Limit: 1})
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
