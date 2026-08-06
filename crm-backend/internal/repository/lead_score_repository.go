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
func (r *leadScoringRepository) RecomputeOrg(ctx context.Context, orgID uuid.UUID, catalog []domain.ReportField, rules []domain.LeadScoringRule) (int64, error) {
	expr, exprArgs, err := buildLeadScoreExpr(catalog, rules)
	if err != nil {
		return 0, err
	}

	var (
		total  int64
		cursor = uuid.Nil
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
			return total, err
		}
		if len(touched) == 0 {
			return total, nil
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
			return total, nil
		}
	}
	// Hit the round cap with work still to do. Reported, never silent — the
	// next pass resumes from the top and gets further only if the org shrinks,
	// so this needs a human to see it.
	return total, errLeadScoreTruncated
}

// errLeadScoreTruncated signals a pass that stopped at its round cap. The
// usecase logs it with the org id and the row count; it is not a failure of the
// rows that WERE written.
var errLeadScoreTruncated = errors.New("lead scoring: rescore hit its per-org round cap; some contacts were not rescored in this pass")

// IsLeadScoreTruncated reports the round-cap sentinel.
func IsLeadScoreTruncated(err error) bool { return errors.Is(err, errLeadScoreTruncated) }

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
