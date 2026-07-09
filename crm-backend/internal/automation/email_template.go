package automation

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// email_template.go implements the A5 email-templates library: reusable
// subject/body templates an org authors once and references from send_email
// actions (executor_email.go) via template_id. Rendering reuses the single
// interpolation primitive (InterpolateTemplate) so a template's {{merge.tags}}
// resolve exactly like an inline send_email body.
//
// Storage model:
//   - BodyHTML is the canonical send source (interpolated at send time).
//   - BodyJSON is the TipTap document, kept only for lossless re-editing in the
//     builder; it is never sent. A caller that edits the HTML by hand can leave
//     BodyJSON empty.
//   - ObjectSlug is an optional merge scope (e.g. "contact"/"deal") that scopes
//     the editor's variable picker and the test-send sample record; empty means
//     unscoped.
//
// Uniqueness is case-insensitive per org over live rows only (a partial unique
// index on (org_id, lower(name)) WHERE deleted_at IS NULL — see AutoMigrate), so
// a name freed by a soft-delete can be reused.

// EmailTemplate is a reusable, org-scoped email template.
type EmailTemplate struct {
	ID         uuid.UUID      `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	OrgID      uuid.UUID      `gorm:"type:uuid;not null;index" json:"org_id"`
	Name       string         `gorm:"size:200;not null" json:"name"`
	Subject    string         `gorm:"size:500;not null" json:"subject"`
	BodyHTML   string         `gorm:"type:text;not null" json:"body_html"`
	BodyJSON   datatypes.JSON `gorm:"type:jsonb" json:"body_json,omitempty"`
	ObjectSlug string         `gorm:"size:100" json:"object_slug"`
	CreatedBy  uuid.UUID      `gorm:"type:uuid;not null" json:"created_by"`
	UpdatedBy  uuid.UUID      `gorm:"type:uuid;not null" json:"updated_by"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (EmailTemplate) TableName() string { return "automation_email_templates" }

// ErrDuplicateTemplateName is returned by create/update when the org already has a
// live template with the same case-insensitive name (the partial unique index).
var ErrDuplicateTemplateName = errors.New("an email template with this name already exists")

// EmailTemplateRepository is the persistence port for email templates. It reads
// the shared *gorm.DB directly (the automation package must not import
// internal/usecase), mirroring the other automation repositories.
type EmailTemplateRepository struct {
	db *gorm.DB
}

// NewEmailTemplateRepository constructs the repository over the shared DB.
func NewEmailTemplateRepository(db *gorm.DB) *EmailTemplateRepository {
	return &EmailTemplateRepository{db: db}
}

// List returns an org's live templates, newest name first (alphabetical).
func (r *EmailTemplateRepository) List(ctx context.Context, orgID uuid.UUID) ([]EmailTemplate, error) {
	var out []EmailTemplate
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("name ASC").
		Find(&out).Error
	return out, err
}

// Get returns one live template scoped to the org. It returns (nil, nil) when the
// template does not exist, was soft-deleted, or belongs to another org — the
// GORM default scope already excludes soft-deleted rows, so a soft-deleted
// template reads as absent (which the send_email executor treats as a permanent
// failure, and a handler as a 404).
func (r *EmailTemplateRepository) Get(ctx context.Context, orgID, id uuid.UUID) (*EmailTemplate, error) {
	var t EmailTemplate
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// Create inserts a template. A case-insensitive name clash with a live row (the
// partial unique index) is translated to ErrDuplicateTemplateName.
func (r *EmailTemplateRepository) Create(ctx context.Context, t *EmailTemplate) error {
	if err := r.db.WithContext(ctx).Create(t).Error; err != nil {
		// TranslateError isn't enabled on the shared DB, so match the raw pg unique
		// violation too (idx_email_templates_org_name) via the shared helper.
		if isDuplicateKeyError(err) {
			return ErrDuplicateTemplateName
		}
		return err
	}
	return nil
}

// Update applies the mutable fields to a live template scoped to the org. It
// returns (nil, nil) when the template is absent/soft-deleted (handler -> 404) and
// ErrDuplicateTemplateName on a name clash. Only the provided (non-nil) fields are
// written; UpdatedBy is always stamped.
func (r *EmailTemplateRepository) Update(ctx context.Context, orgID, id, updatedBy uuid.UUID, name, subject, bodyHTML, objectSlug *string, bodyJSON datatypes.JSON) (*EmailTemplate, error) {
	existing, err := r.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}

	updates := map[string]any{"updated_by": updatedBy}
	if name != nil {
		updates["name"] = *name
	}
	if subject != nil {
		updates["subject"] = *subject
	}
	if bodyHTML != nil {
		updates["body_html"] = *bodyHTML
	}
	if objectSlug != nil {
		updates["object_slug"] = *objectSlug
	}
	if bodyJSON != nil {
		updates["body_json"] = bodyJSON
	}

	if err := r.db.WithContext(ctx).
		Model(&EmailTemplate{}).
		Where("org_id = ? AND id = ?", orgID, id).
		Updates(updates).Error; err != nil {
		if isDuplicateKeyError(err) {
			return nil, ErrDuplicateTemplateName
		}
		return nil, err
	}

	return r.Get(ctx, orgID, id)
}

// Delete soft-deletes a live template scoped to the org. It reports whether a row
// was affected so the handler can return 404 for an absent/already-deleted id.
func (r *EmailTemplateRepository) Delete(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		Delete(&EmailTemplate{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
