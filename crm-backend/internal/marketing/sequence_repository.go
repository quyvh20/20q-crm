package marketing

import (
	"context"
	"errors"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Sequence-enrollment persistence (M8). Every query is explicitly org-scoped except the
// callerless feeder claim (ListActiveEnrollments), which spans orgs and carries org_id
// per row so the feeder scopes each follow-up call.

func (r *Repository) CreateEnrollment(ctx context.Context, e *SequenceEnrollment) error {
	return r.db.WithContext(ctx).Create(e).Error
}

func (r *Repository) GetEnrollment(ctx context.Context, orgID, id uuid.UUID) (*SequenceEnrollment, error) {
	var e SequenceEnrollment
	err := r.db.WithContext(ctx).Where("org_id = ? AND id = ?", orgID, id).First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *Repository) ListEnrollments(ctx context.Context, orgID uuid.UUID) ([]SequenceEnrollment, error) {
	var rows []SequenceEnrollment
	if err := r.db.WithContext(ctx).Where("org_id = ?", orgID).Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ActiveEnrollmentForSegment returns an existing ACTIVE enrollment for (sequence,
// segment), so a duplicate create 409s cleanly (backed by the partial-unique index).
func (r *Repository) ActiveEnrollmentForSegment(ctx context.Context, orgID, seqWFID, segmentID uuid.UUID) (*SequenceEnrollment, error) {
	var e SequenceEnrollment
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND sequence_workflow_id = ? AND segment_id = ? AND status = ?", orgID, seqWFID, segmentID, SeqEnrollActive).
		First(&e).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListActiveEnrollments returns up to limit active enrollment jobs for the feeder,
// oldest-touched first. Callerless + multi-org (each row carries org_id).
func (r *Repository) ListActiveEnrollments(ctx context.Context, limit int) ([]SequenceEnrollment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []SequenceEnrollment
	err := r.db.WithContext(ctx).
		Where("status = ?", SeqEnrollActive).
		Order("updated_at").Limit(limit).Find(&rows).Error
	return rows, err
}

// ResolveSegmentPage runs the compiled M5 audience SELECT and returns the next page of
// (contact_id, email_normalized) strictly AFTER afterContactID, ordered by contact_id
// (a stable keyset). afterContactID = uuid.Nil starts from the beginning — the nil UUID
// sorts below every real id, and every real contact_id is non-null, so `> nil` yields
// the whole set. The inner SELECT is the trusted M5 compiler output (bind args only,
// catalog-resolved identifiers spliced); the cursor + limit are bound.
func (r *Repository) ResolveSegmentPage(ctx context.Context, aud domain.AudienceQuery, afterContactID uuid.UUID, limit int) ([]RecipientRow, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `SELECT aud.contact_id, aud.email_normalized FROM (` + aud.SelectSQL + `) aud
		WHERE aud.contact_id > ? ORDER BY aud.contact_id LIMIT ?`
	args := append(append([]any{}, aud.Args...), afterContactID, limit)
	var rows []RecipientRow
	if err := r.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// AdvanceEnrollmentCursor moves the keyset cursor FORWARD (monotonic guard) and adds to
// the enrolled count. The count is bumped in its OWN unguarded statement, decoupled from
// the cursor's monotonic guard: enrolledDelta already counts only NEW enrollments
// (idempotent re-feeds contribute 0 via CreateRun's dup-key path), so under concurrent
// multi-instance feeding the deltas of two instances that split the same page must both
// land — folding the count into the guarded UPDATE would silently drop the laggard's
// delta (its `feeder_cursor < ?` fails once the winner advanced) and undercount. The
// cursor UPDATE stays guarded so a laggard can never rewind it; both touch only an
// ACTIVE row (a cancel/complete racing in wins).
func (r *Repository) AdvanceEnrollmentCursor(ctx context.Context, id, cursor uuid.UUID, enrolledDelta int) error {
	if enrolledDelta > 0 {
		if err := r.db.WithContext(ctx).Exec(`
			UPDATE marketing_sequence_enrollments
			SET enrolled_count = enrolled_count + ?, last_error = '', updated_at = NOW()
			WHERE id = ? AND status = 'active'`, enrolledDelta, id).Error; err != nil {
			return err
		}
	}
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_sequence_enrollments
		SET feeder_cursor = ?, updated_at = NOW()
		WHERE id = ? AND status = 'active' AND (feeder_cursor IS NULL OR feeder_cursor < ?)`,
		cursor, id, cursor).Error
}

// CompleteEnrollment marks an active enrollment drained (segment fully fed).
func (r *Repository) CompleteEnrollment(ctx context.Context, id uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_sequence_enrollments SET status = 'completed', updated_at = NOW()
		WHERE id = ? AND status = 'active'`, id).Error
}

// BlockEnrollment stops an active enrollment that can't proceed (deleted sequence
// workflow), recording why.
func (r *Repository) BlockEnrollment(ctx context.Context, id uuid.UUID, reason string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_sequence_enrollments SET status = 'blocked', last_error = ?, updated_at = NOW()
		WHERE id = ? AND status = 'active'`, reason, id).Error
}

// NoteEnrollmentError records a transient readiness problem (inactive sequence, DB blip)
// WITHOUT leaving 'active' — the feeder retries next tick and resumes on its own.
func (r *Repository) NoteEnrollmentError(ctx context.Context, id uuid.UUID, reason string) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE marketing_sequence_enrollments SET last_error = ?, updated_at = NOW()
		WHERE id = ? AND status = 'active'`, reason, id).Error
}

// CancelEnrollment stops feeding a segment (admin action). Runs already enrolled keep
// running to completion — cancel stops only NEW enrollments.
func (r *Repository) CancelEnrollment(ctx context.Context, orgID, id uuid.UUID) (bool, error) {
	res := r.db.WithContext(ctx).Exec(`
		UPDATE marketing_sequence_enrollments SET status = 'canceled', updated_at = NOW()
		WHERE org_id = ? AND id = ? AND status IN ('active','blocked')`, orgID, id)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
