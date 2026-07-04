package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ============================================================
// Report comments (P9 sharing, Phase C)
// ============================================================
//
// A comment thread on a saved report. The comment ACCESS TIER completes the
// share level ladder: view / comment / edit / manage. Authorization is resolved
// by the report usecase (ResolveAccess), so this feature never re-implements
// level resolution — it only owns the thread:
//
//	read   → any level (you can see the report → you can read its comments)
//	post   → level >= comment
//	delete → author, or level == manage

// ReportComment is one message in a report's thread. AuthorID is nullable
// (ON DELETE SET NULL) so a removed user's comments survive; deletes are soft
// (DeletedAt) so the thread's history isn't rewritten out from under viewers.
type ReportComment struct {
	ID        uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID     uuid.UUID      `gorm:"type:uuid;not null" json:"org_id"`
	ReportID  uuid.UUID      `gorm:"type:uuid;not null" json:"report_id"`
	AuthorID  *uuid.UUID     `gorm:"type:uuid" json:"author_id,omitempty"`
	Body      string         `gorm:"not null" json:"body"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (ReportComment) TableName() string { return "report_comments" }

// ReportCommentView is one comment rendered for the thread UI with the author's
// display name resolved and CanDelete computed for the current caller (author or
// a manager). CanDelete is set by the usecase, not the repository.
type ReportCommentView struct {
	ID         uuid.UUID  `json:"id"`
	AuthorID   *uuid.UUID `json:"author_id,omitempty"`
	AuthorName string     `json:"author_name"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"created_at"`
	CanDelete  bool       `json:"can_delete"`
}

// AddReportCommentInput posts one comment to a report's thread.
type AddReportCommentInput struct {
	Body string `json:"body" binding:"required,min=1,max=4000"`
}

// ============================================================
// Ports
// ============================================================

// ReportCommentRepository persists a report's comment thread.
type ReportCommentRepository interface {
	Create(ctx context.Context, c *ReportComment) error
	// ListByReport returns non-deleted comments oldest-first with author names
	// resolved. CanDelete is left false — the usecase sets it per caller.
	ListByReport(ctx context.Context, orgID, reportID uuid.UUID) ([]ReportCommentView, error)
	// GetByID returns the comment (for author/soft-delete checks) or nil.
	GetByID(ctx context.Context, orgID, id uuid.UUID) (*ReportComment, error)
	// SoftDelete soft-deletes one comment (scoped to the org). Returns rows affected.
	SoftDelete(ctx context.Context, orgID, id uuid.UUID) (int64, error)
}

// ReportCommentUseCase serves a report's comment thread. Listing needs any
// level; posting needs 'comment'; deleting needs to be the author or 'manage'.
type ReportCommentUseCase interface {
	List(ctx context.Context, orgID, userID, reportID uuid.UUID) ([]ReportCommentView, error)
	Add(ctx context.Context, orgID, userID, reportID uuid.UUID, in AddReportCommentInput) (*ReportComment, error)
	Delete(ctx context.Context, orgID, userID, reportID, commentID uuid.UUID) error
}
