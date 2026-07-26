package marketing

import (
	"time"

	"github.com/google/uuid"
)

// Sequence-enrollment lifecycle states.
const (
	SeqEnrollActive    = "active"    // feeding the segment into the sequence
	SeqEnrollCompleted = "completed" // segment fully drained
	SeqEnrollBlocked   = "blocked"   // cannot proceed (sequence workflow deleted)
	SeqEnrollCanceled  = "canceled"  // admin stopped further enrollment
)

// SequenceEnrollment is one "enroll this segment into this drip sequence" job. The
// feeder drains the segment into the sequence workflow in bounded, cursor-paginated
// batches (never the 100-capped enroll_records action). It is a TRACKING row, not a
// send roster — the enrolled runs live in automation_workflow_runs, and the marketing
// opt-out gate runs LIVE inside each send step (M8.1), not here.
type SequenceEnrollment struct {
	ID                 uuid.UUID `gorm:"type:uuid;primaryKey;default:uuid_generate_v4()" json:"id"`
	OrgID              uuid.UUID `gorm:"type:uuid;not null;index" json:"org_id"`
	SequenceWorkflowID uuid.UUID `gorm:"type:uuid;not null" json:"sequence_workflow_id"`
	SegmentID          uuid.UUID `gorm:"type:uuid;not null" json:"segment_id"`
	// FeederCursor is the last contact_id enrolled (keyset). NULL until the first page.
	// Advanced ONLY via the monotonic AdvanceEnrollmentCursor UPDATE — never a GORM Save
	// (which a stale in-memory copy could use to rewind it, the owner_pool trap).
	FeederCursor  *uuid.UUID `gorm:"type:uuid" json:"-"`
	Status        string     `gorm:"type:varchar(20);not null;default:'active'" json:"status"`
	EnrolledCount int        `gorm:"type:int;not null;default:0" json:"enrolled_count"`
	LastError     string     `gorm:"type:text;not null;default:''" json:"last_error,omitempty"`
	CreatedBy     *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	CreatedAt     time.Time  `gorm:"not null;default:now()" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"not null;default:now()" json:"updated_at"`
}

// TableName pins the table name.
func (SequenceEnrollment) TableName() string { return "marketing_sequence_enrollments" }

// RecipientRow is one (contact, email) pair from a resolved segment page — the feeder's
// unit of work.
type RecipientRow struct {
	ContactID       uuid.UUID `gorm:"column:contact_id"`
	EmailNormalized string    `gorm:"column:email_normalized"`
}
