package automation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// run_now_entities.go provides org-scoped entity loaders for the Run Now feature.
//
// Because internal/automation must not depend on internal/delivery/http (and
// vice-versa), these helpers read contact/deal data directly from the shared
// *gorm.DB the Handler already holds (the same pattern WebhookInbound and
// GetWorkflowSchema use). The returned maps mirror the exact keys produced by
// the delivery-layer contactToMap / dealToMap so that the Trigger_Context built
// for a manual run matches the shape natural CRM events produce.

// loadContactForRun loads a contact scoped to org and returns its event-map
// form (the same shape contactToMap produces, including "id"). It returns
// (nil, nil) when the contact does not exist in the org so the caller can
// produce a 404.
func (h *Handler) loadContactForRun(ctx context.Context, orgID, contactID uuid.UUID) (map[string]any, error) {
	type contactRow struct {
		ID           uuid.UUID      `gorm:"column:id"`
		FirstName    string         `gorm:"column:first_name"`
		LastName     string         `gorm:"column:last_name"`
		Email        *string        `gorm:"column:email"`
		Phone        *string        `gorm:"column:phone"`
		CompanyID    *uuid.UUID     `gorm:"column:company_id"`
		OwnerUserID  *uuid.UUID     `gorm:"column:owner_user_id"`
		CustomFields datatypes.JSON `gorm:"column:custom_fields"`
	}

	var row contactRow
	err := h.db.WithContext(ctx).
		Table("contacts").
		Where("org_id = ? AND id = ? AND deleted_at IS NULL", orgID, contactID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	m := map[string]any{
		"id":         row.ID.String(),
		"first_name": row.FirstName,
		"last_name":  row.LastName,
	}
	if row.Email != nil {
		m["email"] = *row.Email
	}
	if row.Phone != nil {
		m["phone"] = *row.Phone
	}
	if row.CompanyID != nil {
		m["company_id"] = row.CompanyID.String()
	}
	if row.OwnerUserID != nil {
		m["owner_user_id"] = row.OwnerUserID.String()
	}

	// Tags as an array of tag-id strings (matching contactToMap's m["tags"]).
	var tagIDs []uuid.UUID
	if err := h.db.WithContext(ctx).
		Table("contact_tags").
		Joins("JOIN tags ON tags.id = contact_tags.tag_id AND tags.deleted_at IS NULL").
		Where("contact_tags.contact_id = ?", contactID).
		Pluck("contact_tags.tag_id", &tagIDs).Error; err == nil && len(tagIDs) > 0 {
		ids := make([]string, len(tagIDs))
		for i, t := range tagIDs {
			ids[i] = t.String()
		}
		m["tags"] = ids
	}

	// Custom fields flattened as "custom_fields.<key>" (matching contactToMap).
	if len(row.CustomFields) > 0 {
		s := string(row.CustomFields)
		if s != "null" && s != "{}" {
			var cf map[string]any
			if err := json.Unmarshal(row.CustomFields, &cf); err == nil {
				for k, v := range cf {
					m["custom_fields."+k] = v
				}
			}
		}
	}

	return m, nil
}

// loadDealForRun loads a deal scoped to org and returns its event-map form (the
// same shape dealToMap produces, including "id" and "stage_id"). It returns
// (nil, nil) when the deal does not exist in the org so the caller can produce
// a 404. The caller uses the returned "stage_id" as new_stage_id in the
// Trigger_Context.
func (h *Handler) loadDealForRun(ctx context.Context, orgID, dealID uuid.UUID) (map[string]any, error) {
	type dealRow struct {
		ID              uuid.UUID  `gorm:"column:id"`
		Title           string     `gorm:"column:title"`
		Value           float64    `gorm:"column:value"`
		Probability     int        `gorm:"column:probability"`
		IsWon           bool       `gorm:"column:is_won"`
		IsLost          bool       `gorm:"column:is_lost"`
		ContactID       *uuid.UUID `gorm:"column:contact_id"`
		CompanyID       *uuid.UUID `gorm:"column:company_id"`
		StageID         *uuid.UUID `gorm:"column:stage_id"`
		OwnerUserID     *uuid.UUID `gorm:"column:owner_user_id"`
		ExpectedCloseAt *time.Time `gorm:"column:expected_close_at"`
		ClosedAt        *time.Time `gorm:"column:closed_at"`
	}

	var row dealRow
	err := h.db.WithContext(ctx).
		Table("deals").
		Where("org_id = ? AND id = ? AND deleted_at IS NULL", orgID, dealID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	m := map[string]any{
		"id":          row.ID.String(),
		"title":       row.Title,
		"value":       row.Value,
		"probability": row.Probability,
		"is_won":      row.IsWon,
		"is_lost":     row.IsLost,
	}
	if row.ContactID != nil {
		m["contact_id"] = row.ContactID.String()
	}
	if row.CompanyID != nil {
		m["company_id"] = row.CompanyID.String()
	}
	if row.StageID != nil {
		m["stage_id"] = row.StageID.String()
	}
	if row.OwnerUserID != nil {
		m["owner_user_id"] = row.OwnerUserID.String()
	}
	if row.ExpectedCloseAt != nil {
		m["expected_close_at"] = row.ExpectedCloseAt.Format(time.RFC3339)
	}
	if row.ClosedAt != nil {
		m["closed_at"] = row.ClosedAt.Format(time.RFC3339)
	}

	return m, nil
}
