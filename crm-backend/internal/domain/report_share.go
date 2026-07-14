package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ============================================================
// Report shares — granular sharing (P9 sharing)
// ============================================================
//
// A report can be shared with a specific user, a role, or a user group, at an
// access LEVEL. Levels are totally ordered; a caller's effective level on a
// report is the HIGHEST that applies (creator/owner/reports.manage → manage).
// Report data stays per-viewer (OLS/FLS); levels only govern the definition.
//
// The target kinds, the level ladder, and ShareIdentity live in share.go — record
// sharing (U6.2) speaks the same vocabulary. Reports are the shareable that
// supports ShareLevelComment; records are not (IsStorableRecordShareLevel).

type ReportShare struct {
	ID         uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	OrgID      uuid.UUID  `gorm:"type:uuid;not null" json:"org_id"`
	ReportID   uuid.UUID  `gorm:"type:uuid;not null" json:"report_id"`
	TargetType string     `gorm:"size:10;not null" json:"target_type"`
	TargetID   uuid.UUID  `gorm:"type:uuid;not null" json:"target_id"`
	Level      string     `gorm:"size:10;not null;default:'view'" json:"level"`
	CreatedBy  *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (ReportShare) TableName() string { return "report_shares" }

// ReportShareView is one share rendered for the share dialog with the target's
// display name resolved (user full name / role name / group name).
type ReportShareView struct {
	ID         uuid.UUID `json:"id"`
	TargetType string    `json:"target_type"`
	TargetID   uuid.UUID `json:"target_id"`
	TargetName string    `json:"target_name"`
	Level      string    `json:"level"`
	CreatedAt  time.Time `json:"created_at"`
}

// AddReportShareInput grants a report to a target at a level.
type AddReportShareInput struct {
	TargetType string    `json:"target_type" binding:"required"`
	TargetID   uuid.UUID `json:"target_id" binding:"required"`
	Level      string    `json:"level" binding:"required"`
}

// ============================================================
// Ports
// ============================================================

// ReportShareRepository persists report share rows and resolves the caller's
// share identity.
type ReportShareRepository interface {
	Create(ctx context.Context, s *ReportShare) error
	// ListByReport returns a report's shares with target names resolved (dialog).
	ListByReport(ctx context.Context, orgID, reportID uuid.UUID) ([]ReportShareView, error)
	// ListRawByReport returns a report's raw shares (level resolution — no joins).
	ListRawByReport(ctx context.Context, orgID, reportID uuid.UUID) ([]ReportShare, error)
	// Delete removes one share by id (scoped to the report).
	Delete(ctx context.Context, orgID, reportID, shareID uuid.UUID) (int64, error)
	// GetShareIdentity resolves the caller's role id (from org_users) and group
	// ids (from user_group_members) for share matching.
	GetShareIdentity(ctx context.Context, orgID, userID uuid.UUID) (ShareIdentity, error)
}

// ReportShareUseCase manages a report's share list. Listing needs any level;
// adding/removing needs 'manage' (creator/owner/reports.manage).
type ReportShareUseCase interface {
	List(ctx context.Context, orgID, userID, reportID uuid.UUID) ([]ReportShareView, error)
	Add(ctx context.Context, orgID, userID, reportID uuid.UUID, in AddReportShareInput) error
	Remove(ctx context.Context, orgID, userID, reportID, shareID uuid.UUID) error
}
