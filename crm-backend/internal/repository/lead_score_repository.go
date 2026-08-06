package repository

import (
	"bytes"
	"context"
	"errors"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Bounds on one rescore pass. Every one of these exists so a pathological org
// cannot turn a background sweep into an unbounded scan.
const (
	// leadScoreBatchSize is contacts per UPDATE. Larger than the ledger
	// pruner's batches because this is a set-based arithmetic UPDATE — one
	// index scan and an in-place write per row, not a per-row rewrite.
	leadScoreBatchSize = 5000
	// leadScoreMaxRounds caps batches per org per pass, so a million-contact
	// org cannot monopolise the sweep. A pass that hits it says so.
	leadScoreMaxRounds = 40
	// leadScoreOrgsPerPass caps orgs claimed per tick.
	leadScoreOrgsPerPass = 25
	// leadScoreLockKey names the fleet-singleton advisory lock.
	leadScoreLockKey = "lead_score_rescore"
)

type leadScoringRepository struct {
	db *gorm.DB
}

func NewLeadScoringRepository(db *gorm.DB) domain.LeadScoringRepository {
	return &leadScoringRepository{db: db}
}

// ============================================================
// Rule CRUD
// ============================================================

func (r *leadScoringRepository) ListRules(ctx context.Context, orgID uuid.UUID) ([]domain.LeadScoringRule, error) {
	var out []domain.LeadScoringRule
	err := r.db.WithContext(ctx).
		Where("org_id = ?", orgID).
		Order("position ASC, created_at ASC").
		Find(&out).Error
	return out, err
}

func (r *leadScoringRepository) GetRule(ctx context.Context, orgID, id uuid.UUID) (*domain.LeadScoringRule, error) {
	var rule domain.LeadScoringRule
	err := r.db.WithContext(ctx).Where("id = ? AND org_id = ?", id, orgID).First(&rule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (r *leadScoringRepository) CreateRule(ctx context.Context, rule *domain.LeadScoringRule) error {
	if rule.ID == uuid.Nil {
		rule.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Create(rule).Error
}

func (r *leadScoringRepository) UpdateRule(ctx context.Context, rule *domain.LeadScoringRule) error {
	return r.db.WithContext(ctx).Save(rule).Error
}

func (r *leadScoringRepository) DeleteRule(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND org_id = ?", id, orgID).
		Delete(&domain.LeadScoringRule{}).Error
}

func (r *leadScoringRepository) CountRules(ctx context.Context, orgID uuid.UUID) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&domain.LeadScoringRule{}).
		Where("org_id = ?", orgID).Count(&n).Error
	return n, err
}

// ============================================================
// The engine
// ============================================================

// RecomputeOrg rescores every live contact in the org.
//
// Keyset-batched by id rather than OFFSET: the UPDATE changes the very column
// an offset walk would be ordered by in some future variant, and id is stable
// under concurrent inserts. Each batch is its own statement, so a long org does
// not hold one transaction open across the whole rescore.
//
// The WHERE deliberately does NOT skip rows whose score is already correct.
// Detecting that would need the expression evaluated twice (once to compare,
// once to write) or a second pass; writing unconditionally within a bounded
// batch is cheaper and keeps lead_score_at honest as "when we last checked".
func (r *leadScoringRepository) RecomputeOrg(ctx context.Context, orgID uuid.UUID, catalog []domain.ReportField, rules []domain.LeadScoringRule) (int64, []string, error) {
	expr, exprArgs, skipped, err := buildLeadScoreExpr(catalog, rules)
	if err != nil {
		return 0, skipped, err
	}

	// Resume where the last pass stopped. Without this the walk restarts from
	// uuid.Nil every pass, so an org with more contacts than
	// leadScoreBatchSize*leadScoreMaxRounds would rescore the same leading
	// 200k forever and every contact past that boundary would keep its
	// DEFAULT 0 permanently — invisible, since each pass reports success.
	cursor, err := r.resumeCursor(ctx, orgID)
	if err != nil {
		return 0, skipped, err
	}

	var (
		total  int64
		rounds int
	)
	for rounds = 0; rounds < leadScoreMaxRounds; rounds++ {
		// Bind order follows the textual order of the statement: the SET
		// expression's args come first, then the WHERE's. Getting this wrong is
		// silent — every arg shifts by one and the org filter becomes garbage —
		// which is why the expression compiler returns its args in emission
		// order rather than as a map.
		args := make([]any, 0, len(exprArgs)+3)
		args = append(args, exprArgs...)
		args = append(args, orgID, cursor, leadScoreBatchSize)

		var touched []uuid.UUID
		err := r.db.WithContext(ctx).Raw(`
			UPDATE contacts SET lead_score = `+expr+`, lead_score_at = NOW()
			WHERE contacts.id IN (
				SELECT id FROM contacts
				WHERE org_id = ? AND deleted_at IS NULL AND id > ?
				ORDER BY id
				LIMIT ?
			)
			RETURNING contacts.id`, args...).Scan(&touched).Error
		if err != nil {
			return total, skipped, err
		}
		if len(touched) == 0 {
			// Reached the end of the org. Clear the watermark so the next pass
			// starts from the top and picks up contacts created since.
			return total, skipped, r.setResumeCursor(ctx, orgID, uuid.Nil)
		}
		total += int64(len(touched))
		// RETURNING has no defined order, so the next cursor is the MAX id of
		// the batch — not its last element. Taking touched[len-1] would skip
		// every row Postgres happened to return after the true maximum.
		//
		// Compared as BYTES, which is how Postgres orders the uuid type. The
		// hex string form happens to sort identically, but only because the
		// hyphens sit at fixed offsets and hex digits are monotonic in ASCII —
		// a coincidence, not a guarantee, and not one worth resting a cursor on.
		cursor = touched[0]
		for _, id := range touched {
			if bytes.Compare(id[:], cursor[:]) > 0 {
				cursor = id
			}
		}
		if len(touched) < leadScoreBatchSize {
			return total, skipped, r.setResumeCursor(ctx, orgID, uuid.Nil)
		}
	}
	// Hit the round cap with work still to do. Save where we stopped so the NEXT
	// pass continues from here rather than redoing the same leading batch, and
	// report the truncation — the caller treats it as progress, not failure.
	if err := r.setResumeCursor(ctx, orgID, cursor); err != nil {
		return total, skipped, err
	}
	return total, skipped, domain.ErrLeadScoreTruncated
}

// resumeCursor reads where the previous pass stopped (uuid.Nil = start over).
//
// Cast to text and COALESCEd rather than scanned as a uuid: the column is
// nullable, and gorm's Scan cannot put a SQL NULL into a *uuid.UUID — it fails
// with "converting NULL to uint8 is unsupported", which is precisely the
// everyday case (no pass has been truncated yet).
func (r *leadScoringRepository) resumeCursor(ctx context.Context, orgID uuid.UUID) (uuid.UUID, error) {
	var raw string
	if err := r.db.WithContext(ctx).Raw(
		`SELECT COALESCE(lead_score_cursor::text, '') FROM organizations WHERE id = ?`, orgID).
		Scan(&raw).Error; err != nil {
		return uuid.Nil, err
	}
	if raw == "" {
		return uuid.Nil, nil
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		// A corrupt watermark must not wedge the org — start over instead.
		return uuid.Nil, nil
	}
	return parsed, nil
}

// setResumeCursor stores the walk position; uuid.Nil clears it.
func (r *leadScoringRepository) setResumeCursor(ctx context.Context, orgID, cursor uuid.UUID) error {
	if cursor == uuid.Nil {
		return r.db.WithContext(ctx).Exec(
			`UPDATE organizations SET lead_score_cursor = NULL WHERE id = ?`, orgID).Error
	}
	return r.db.WithContext(ctx).Exec(
		`UPDATE organizations SET lead_score_cursor = ? WHERE id = ?`, cursor, orgID).Error
}

// OrgsDueForRescore claims orgs whose scores are stale.
//
// Driven from organizations, NOT from a DISTINCT over lead_scoring_rules, and
// that matters for one specific case: an org that just DELETED its last rule
// still needs one more pass to zero the scores its old rules left behind. A
// rules-driven query would skip exactly that org and leave stale scores
// standing forever.
//
// NULLS FIRST orders never-scored orgs ahead of everyone, so a brand-new org
// (and, after deploy, the entire fleet) converges before anyone is re-swept.
func (r *leadScoringRepository) OrgsDueForRescore(ctx context.Context, staleness time.Duration, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > leadScoreOrgsPerPass {
		limit = leadScoreOrgsPerPass
	}
	secs := int(staleness.Seconds())
	if secs <= 0 {
		secs = 3600
	}
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).Raw(`
		SELECT o.id FROM organizations o
		WHERE o.deleted_at IS NULL
		  AND (o.lead_score_run_at IS NULL OR o.lead_score_run_at < NOW() - make_interval(secs => ?))
		ORDER BY o.lead_score_run_at ASC NULLS FIRST
		LIMIT ?`, secs, limit).Scan(&ids).Error
	return ids, err
}

func (r *leadScoringRepository) MarkOrgRescored(ctx context.Context, orgID uuid.UUID) error {
	return r.db.WithContext(ctx).Exec(
		`UPDATE organizations SET lead_score_run_at = NOW() WHERE id = ?`, orgID).Error
}

// WithLeadScoreLock serializes the sweep across the fleet.
//
// Pinned connection, not the pool: a session-level advisory lock belongs to the
// CONNECTION that took it, so locking and unlocking through a pool can release
// nothing and leak the lock until that backend is recycled — after which no
// replica ever sweeps again. Same shape as WithReconcileLock; do not
// "simplify" it to a db.Exec pair.
func (r *leadScoringRepository) WithLeadScoreLock(ctx context.Context, fn func() error) (bool, error) {
	sqlDB, err := r.db.DB()
	if err != nil {
		return false, err
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	var locked bool
	if err := conn.QueryRowContext(ctx,
		"SELECT pg_try_advisory_lock(hashtext($1))", leadScoreLockKey).Scan(&locked); err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext($1))", leadScoreLockKey)
	}()
	return true, fn()
}
