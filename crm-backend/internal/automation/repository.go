package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNilTransaction is returned when a nil transaction is passed to a method that requires one.
var ErrNilTransaction = fmt.Errorf("automation: nil transaction passed to method that requires explicit tx")

// Repository provides data access for the automation engine.
type Repository struct {
	db *gorm.DB
}

// NewRepository creates a new automation repository.
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// migrationLockTimeout bounds how long ONE statement in the schema-migration path will
// wait for a table lock before giving up. See migrateSchema for why it exists and what
// happens when it fires.
//
// 3 seconds: long enough to ride out ordinary request-length transactions on a busy
// engine table, short enough that a boot cannot park the automation tables behind a
// report query or an idle-in-transaction session for minutes.
const migrationLockTimeout = "3s"

// setMigrationLockTimeoutSQL is migrationLockTimeout as SQL. Postgres' SET does not take
// bind parameters, so the value is a literal and the two constants MUST stay in sync —
// migrationLockTimeout is only ever used for logging and documentation.
const setMigrationLockTimeoutSQL = `SET LOCAL lock_timeout = '3s'`

// automationModels is the migration set. Order is the order they are migrated in.
//
// None of these structs declares a relationship to another (no belongs-to/has-many
// fields, no FK constraints between them), so migrating them ONE AT A TIME loses nothing
// that gorm's ReorderModels would otherwise have arranged. That independence is what
// makes migrateSchema's per-model transaction safe.
func automationModels() []any {
	return []any{
		&Workflow{},
		&WorkflowVersion{},
		&WorkflowRun{},
		&RunIdempotencyClaim{},
		&WorkflowActionLog{},
		&WorkflowOrgToken{},
		&AutomationTimer{},
		&EmailTemplate{},
		&AssignCursor{},
	}
}

// migrateSchema runs gorm's AutoMigrate one model at a time, each inside its own
// transaction that first bounds `lock_timeout`.
//
// WHY THIS IS NOT A PLAIN r.db.AutoMigrate(...) ANY MORE
//
// Steady-state boots are not DDL-free. gorm re-emits
// `ALTER TABLE automation_workflows ALTER COLUMN "actions" SET DEFAULT '[]'::jsonb`
// on EVERY boot, on two of the engine's hottest tables — because the postgres driver's
// parseDefaultValueValue (driver/postgres migrator.go) strips the `::jsonb` cast and the
// surrounding quotes off the introspected default before comparing it to the struct tag,
// so a quoted default can never compare equal. The tag cannot be dropped (see
// Workflow.Actions: it is what lets the column outlive the field), and a guarded
// check-then-ALTER of our own does not help — gorm's comparison happens inside
// MigrateColumn and emits gorm's own ALTER regardless of what we do first. MEASURED, not
// assumed: with gorm's SQL logger capturing every statement, a second AutoMigrate over
// an ALREADY-CORRECT schema emitted exactly two `SET DEFAULT '[]'::jsonb` — one per
// table — and a contended one was caught in pg_stat_activity waiting on Lock for
// `ALTER TABLE "automation_workflows" ALTER COLUMN "actions" SET DEFAULT '[]'::jsonb`.
//
// The work is catalog-only (~2 ms, no table rewrite). The LOCK is not: ACCESS EXCLUSIVE
// queues behind any open transaction touching the table, and every reader that arrives
// afterwards queues behind the ALTER. Measured on postgres:16-alpine with one ordinary
// read transaction held open, an unbounded boot made a plain `SELECT count(*)` on
// automation_workflows wait 10.0 s — the whole time the reader held its snapshot. Every
// deploy, restart, rollback, crash-loop and scale event pays that.
//
// MECHANISM, chosen by measurement. `SET LOCAL` inside the SAME transaction that runs
// the DDL is the only option here that is both correct and self-contained:
//
//   - a bare r.db.Exec("SET lock_timeout = …") is the trap: gorm hands each statement
//     whichever pooled connection is free, so the SET very likely lands on a DIFFERENT
//     connection than the one AutoMigrate's DDL runs on, and does nothing.
//   - a DSN `options=-c lock_timeout=…` or an `ALTER ROLE` would work, but both apply to
//     every connection the process makes — they change runtime query behaviour far
//     outside the migration path, and neither is settable from this package.
//   - pinning a raw *sql.Conn and doing a session-level `SET lock_timeout` on it would
//     also be provably correct, but it means hand-building a gorm.DB around that conn as
//     its ConnPool — more moving parts than a transaction, for the same guarantee.
//   - `SET LOCAL` is scoped to the transaction AND the transaction is pinned to one
//     connection, so the timeout provably applies to the DDL. Per-model rather than one
//     big transaction so each table's ACCESS EXCLUSIVE is taken and released on its own
//     instead of every table's lock being held until the last one commits.
//
// VERIFIED, not assumed, by re-running the experiment above against this code: the boot
// ALTER now fails in 3.1 s with `canceling statement due to lock timeout (SQLSTATE
// 55P03)` instead of waiting, and the concurrent plain `SELECT count(*)` waits 1.6 s
// instead of 10.0 s — it is released the moment the ALTER leaves the lock queue.
//
// ON TIMEOUT the ALTER does NOT run, and that is the deliberate trade. A silently
// skipped SET DEFAULT is exactly the state R5 deploy 1 must not ship into, so it is not
// allowed to be silent: the failure is logged at ERROR naming the column and the 23502
// it re-arms, AND returned, so the engine's own "migration failed" Error fires too. Two
// different failures are being defended against and they have two different detectors —
// a DELETED TAG is caught in CI by TestFlatActionsColumnDefault (which reads
// column_default out of information_schema after AutoMigrate), and a TIMED-OUT BOOT is
// caught in production by that log line. Neither substitutes for the other.
//
// A loud, retryable, self-healing miss on a contended boot is strictly safer than the
// alternative: an unbounded ALTER that stalls every read of the automation tables for as
// long as one stray transaction stays open. The next uncontended boot fixes it. And in
// steady state the ALTER is a no-op re-set of a default that is ALREADY correct, so a
// timeout costs nothing at all — measured: after a boot whose ALTER timed out, the
// column default was still '[]'::jsonb. The only boot where a timeout genuinely loses
// something is the FIRST one that applies the tag, which is precisely the boot the
// deploy-0/deploy-1 ordering exists to make someone verify before moving on.
//
// A lock timeout on ONE table does not abandon the other eight: they are independent, a
// busy automation_workflows says nothing about automation_timers, and skipping them
// would mean an unrelated new column never lands while one table stays hot. Every model
// is attempted, the timeouts are joined and returned so AutoMigrate still reports
// failure, and any OTHER migration error still aborts immediately — a genuine schema
// error is not something to push past.
func (r *Repository) migrateSchema() error {
	var timeouts []error
	for _, model := range automationModels() {
		err := r.migrateModelWithLockTimeout(model)
		switch {
		case err == nil:
		case isLockTimeoutErr(err):
			timeouts = append(timeouts, err)
		default:
			return err
		}
	}
	return errors.Join(timeouts...)
}

// migrateModelWithLockTimeout migrates one model under a bounded lock_timeout.
func (r *Repository) migrateModelWithLockTimeout(model any) error {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// SET LOCAL, not SET: scoped to this transaction, on this transaction's pinned
		// connection — the same one tx.AutoMigrate's DDL runs on.
		if err := tx.Exec(setMigrationLockTimeoutSQL).Error; err != nil {
			return fmt.Errorf("set lock_timeout: %w", err)
		}
		return tx.AutoMigrate(model)
	})
	if err == nil {
		return nil
	}
	if isLockTimeoutErr(err) {
		slog.Error("automation: SCHEMA MIGRATION GAVE UP ON A TABLE LOCK — the migration for this model "+
			"did NOT run, so its schema may be incomplete. This includes the `actions` column DEFAULT "+
			"'[]'::jsonb that the flat-Actions teardown depends on; without it, a build with the Actions "+
			"field removed rejects every workflow write with SQLSTATE 23502. Find the long-running "+
			"transaction on the automation tables and re-deploy.",
			"model", fmt.Sprintf("%T", model), "lock_timeout", migrationLockTimeout, "error", err)
		return err
	}
	slog.Error("automation: schema migration failed", "model", fmt.Sprintf("%T", model), "error", err)
	return err
}

// isLockTimeoutErr reports whether err is Postgres' lock_not_available (SQLSTATE 55P03),
// i.e. a statement that abandoned its lock wait rather than queueing behind it. Matched
// on the rendered error so this does not have to reach for the pgx error types through
// gorm's driver.
func isLockTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "55P03") || strings.Contains(msg, "lock timeout")
}

// AutoMigrate creates/updates tables and indexes for the automation engine.
func (r *Repository) AutoMigrate() error {
	// migrateSchema returns EITHER a hard migration error (returned immediately, as
	// before) OR joined lock timeouts. A timeout is transient and leaves the previous
	// boot's schema in place, so it must not suppress everything below it — least of
	// all the teardown-gate line, which is the one diagnostic per boot the whole R5
	// decision rests on and which would otherwise be missing from exactly the boots
	// that logged an error. The error is still reported, at the end.
	schemaErr := r.migrateSchema()
	if schemaErr != nil && !isLockTimeoutErr(schemaErr) {
		return schemaErr
	}

	// Composite indexes per spec
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wf_runs_status_retry ON automation_workflow_runs (status, next_retry_at)`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wf_action_logs_run_action ON automation_workflow_action_logs (run_id, action_idx)`)
	r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_wf_versions_wf_ver ON automation_workflow_versions (workflow_id, version)`)
	r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_wf_runs_wf_idemp ON automation_workflow_runs (workflow_id, idempotency_key)`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wf_runs_waiting_wake ON automation_workflow_runs (wake_at) WHERE status = 'waiting'`)
	// A4 automation_timers: unique occurrence per workflow (idempotent arm/re-arm/
	// reconcile) + a partial index over the scanner's hot path (due pending timers).
	r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_wf_timers_wf_dedupe ON automation_timers (workflow_id, dedupe_key)`)
	r.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wf_timers_due ON automation_timers (fire_at) WHERE status = 'pending'`)
	// A5 email templates: case-insensitive name unique per org over LIVE rows only,
	// so a name freed by a soft-delete can be reused (GORM can't express a partial/
	// functional unique index, hence raw SQL like the timers indexes above).
	r.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_email_templates_org_name ON automation_email_templates (org_id, lower(name)) WHERE deleted_at IS NULL`)

	// Run data migration from actions -> steps. Logged rather than fatal, for the same
	// reason as the claims backfill below — but it must not be SILENT either: this is
	// the backfill the flat-Actions teardown gate is waiting on, and a swallowed error
	// here is exactly how the gate would sit un-converged for months with nobody
	// noticing. The gate line logged immediately after reports the resulting counts.
	if err := r.MigrateFlatActionsToSteps(context.Background()); err != nil {
		slog.Error("automation: flat actions → steps backfill failed", "error", err)
	}

	// Report the R5 teardown gate to the logs. Pure diagnostic: it reads the gate's
	// counts and says whether the deprecated Actions field can be removed yet. It
	// cannot fail boot — CountFlatActionsGate's error is logged and swallowed inside.
	//
	// slog.Default() is load-bearing and the coupling is invisible from here: this line
	// only reaches Railway because cmd/server/main.go calls slog.SetDefault(autoLogger)
	// a few statements before autoEngine.Start() (which is what calls AutoMigrate), so
	// the package default IS the engine's JSON handler on stdout. Delete or move that
	// SetDefault and the single line the whole teardown decision rests on quietly
	// reverts to slog's built-in text handler on STDERR — still emitted, no error, just
	// not where anyone greps. Anything that reorders main.go's logger setup must keep
	// SetDefault ahead of the engine start.
	r.LogFlatActionsGate(context.Background(), slog.Default())

	// Backfill action_path from action_idx for legacy action logs (idempotent)
	r.db.Exec(`UPDATE automation_workflow_action_logs SET action_path = action_idx::text WHERE action_path = '' OR action_path IS NULL`)

	// Give every pre-existing sequence enrollment its durable claim. Logged rather than
	// fatal: a boot must not fail on a data backfill, and the write path is correct with
	// or without it — this only decides whether contacts enrolled BEFORE the claims table
	// existed are protected from the re-mail defect (they are the ones at risk today).
	if n, err := r.BackfillSequenceEnrollmentClaims(context.Background()); err != nil {
		slog.Error("automation: backfilling sequence enrollment claims failed", "error", err)
	} else if n > 0 {
		slog.Info("automation: backfilled durable sequence enrollment claims", "count", n)
	}

	// Non-nil only when a boot ALTER abandoned its lock wait; already logged loudly, per
	// model, by migrateModelWithLockTimeout.
	return schemaErr
}

// --- Workflow CRUD ---

// CreateWorkflow creates a new workflow and its initial version snapshot.
func (r *Repository) CreateWorkflow(ctx context.Context, wf *Workflow) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if wf.ID == uuid.Nil {
			wf.ID = uuid.New()
		}
		wf.Version = 1
		if err := tx.Create(wf).Error; err != nil {
			return err
		}
		// Create version snapshot
		ver := WorkflowVersion{
			ID:         uuid.New(),
			WorkflowID: wf.ID,
			Version:    wf.Version,
			Trigger:    wf.Trigger,
			Conditions: wf.Conditions,
			Actions:    wf.Actions,
			Steps:      wf.Steps,
			CreatedAt:  time.Now(),
		}
		return tx.Create(&ver).Error
	})
}

// UpdateWorkflow updates a workflow and creates a new version snapshot.
func (r *Repository) UpdateWorkflow(ctx context.Context, wf *Workflow) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		wf.Version++
		if err := tx.Save(wf).Error; err != nil {
			return err
		}
		ver := WorkflowVersion{
			ID:         uuid.New(),
			WorkflowID: wf.ID,
			Version:    wf.Version,
			Trigger:    wf.Trigger,
			Conditions: wf.Conditions,
			Actions:    wf.Actions,
			Steps:      wf.Steps,
			CreatedAt:  time.Now(),
		}
		return tx.Create(&ver).Error
	})
}

// GetWorkflowByID retrieves a workflow by ID within an org.
func (r *Repository) GetWorkflowByID(ctx context.Context, orgID, id uuid.UUID) (*Workflow, error) {
	var wf Workflow
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND id = ?", orgID, id).
		First(&wf).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &wf, nil
}

// ListWorkflows lists workflows for an org with optional filtering and pagination.
// WorkflowWithLastRun holds a workflow plus its latest run info from the JOIN LATERAL query.
type WorkflowWithLastRun struct {
	Workflow
	LastRunStatus *string `gorm:"column:last_run_status"`
	LastRunAt     *string `gorm:"column:last_run_at"`
}

func (r *Repository) ListWorkflows(ctx context.Context, orgID uuid.UUID, activeOnly bool, q string, page, size int) ([]WorkflowWithLastRun, int64, error) {
	// Normalize the search term once; an empty term means "no text filter".
	//
	// NOTE (scale): the `%term%` ILIKE below is a leading-wildcard match, so Postgres
	// cannot use a plain B-tree index and falls back to a sequential scan. That is fine
	// for the expected per-org workflow counts (tens to low hundreds). If an org ever
	// reaches ~10k+ workflows and this shows up in latency, add a trigram GIN index
	// (CREATE EXTENSION pg_trgm; CREATE INDEX ... USING gin (name gin_trgm_ops, ...))
	// or move name/description to a tsvector full-text column. Premature for v1.
	q = strings.TrimSpace(q)
	var like string
	if q != "" {
		like = "%" + q + "%"
	}

	// Count first (simple query)
	countQuery := r.db.WithContext(ctx).Model(&Workflow{}).Where("org_id = ?", orgID)
	if activeOnly {
		countQuery = countQuery.Where("is_active = ?", true)
	}
	if like != "" {
		countQuery = countQuery.Where("(name ILIKE ? OR description ILIKE ?)", like, like)
	}
	var total int64
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	// Main query with LEFT JOIN LATERAL for latest run
	activeFilter := ""
	if activeOnly {
		activeFilter = "AND w.is_active = true"
	}

	// Build args in lockstep with the placeholders below; the optional text
	// filter is interpolated as raw SQL but its values stay parameterized.
	searchFilter := ""
	args := []interface{}{orgID}
	if like != "" {
		searchFilter = "AND (w.name ILIKE ? OR w.description ILIKE ?)"
		args = append(args, like, like)
	}
	args = append(args, offset, size)

	query := `
		SELECT w.*,
			lr.status AS last_run_status,
			lr.created_at AS last_run_at
		FROM automation_workflows w
		LEFT JOIN LATERAL (
			SELECT wr.status, wr.created_at
			FROM automation_workflow_runs wr
			WHERE wr.workflow_id = w.id
			ORDER BY wr.created_at DESC
			LIMIT 1
		) lr ON true
		WHERE w.org_id = ? AND w.deleted_at IS NULL ` + activeFilter + ` ` + searchFilter + `
		ORDER BY w.created_at DESC
		OFFSET ? LIMIT ?`

	var results []WorkflowWithLastRun
	err := r.db.WithContext(ctx).Raw(query, args...).Scan(&results).Error
	return results, total, err
}

// SoftDeleteWorkflow soft-deletes a workflow and deactivates it.
func (r *Repository) SoftDeleteWorkflow(ctx context.Context, orgID, id uuid.UUID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Deactivate first
		if err := tx.Model(&Workflow{}).
			Where("org_id = ? AND id = ?", orgID, id).
			Update("is_active", false).Error; err != nil {
			return err
		}
		return tx.Where("org_id = ? AND id = ?", orgID, id).
			Delete(&Workflow{}).Error
	})
}

// ToggleWorkflow flips the is_active flag.
func (r *Repository) ToggleWorkflow(ctx context.Context, orgID, id uuid.UUID) (*Workflow, error) {
	var wf Workflow
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("org_id = ? AND id = ?", orgID, id).First(&wf).Error; err != nil {
			return err
		}
		wf.IsActive = !wf.IsActive
		return tx.Save(&wf).Error
	})
	if err != nil {
		return nil, err
	}
	return &wf, nil
}

// GetWorkflowVersion retrieves a specific version snapshot.
func (r *Repository) GetWorkflowVersion(ctx context.Context, workflowID uuid.UUID, version int) (*WorkflowVersion, error) {
	var ver WorkflowVersion
	err := r.db.WithContext(ctx).
		Where("workflow_id = ? AND version = ?", workflowID, version).
		First(&ver).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ver, nil
}

// GetActiveWorkflowsByTrigger returns active workflows matching a trigger type for an org.
func (r *Repository) GetActiveWorkflowsByTrigger(ctx context.Context, orgID uuid.UUID, triggerType string) ([]Workflow, error) {
	var workflows []Workflow
	err := r.db.WithContext(ctx).
		Where("org_id = ? AND is_active = ? AND trigger->>'type' = ?", orgID, true, triggerType).
		Find(&workflows).Error
	return workflows, err
}

// --- WorkflowRun CRUD ---

// CreateRunWithDurableClaim inserts a run whose idempotency key is a PERMANENT claim: the
// key is also recorded in automation_run_idempotency_claims, a table no pruner touches, so
// the dedupe outlives the run row that PruneCompletedRuns reclaims at 90 days. Returns
// false when the key was already claimed — however long ago, and whether or not the run
// that claimed it still exists. That is the whole fix for the sequence re-mail defect; see
// RunIdempotencyClaim.
//
// Both writes share ONE transaction, so there is no window in which a run exists without
// its claim. A crash in such a window would re-open the re-mail hole for that contact 90
// days later, which is precisely the failure mode being closed.
//
// The run is inserted with ON CONFLICT DO NOTHING rather than by catching a duplicate-key
// error the way CreateRun does: a failed statement poisons the surrounding transaction, so
// the COMMIT would roll back the claim — losing the exact row this method exists to write.
// RowsAffected == 0 on the run insert therefore means a live run already holds this key but
// had no claim (a run created before this shipped, or before the backfill reached it); the
// claim is still committed, healing that row in passing. The conflict is left untargeted
// there, matching CreateRun's tolerance of a missing idx_wf_runs_wf_idemp.
//
// Concurrency needs no extra handling: two instances enrolling the same contact both reach
// the claim insert, the second blocks on the unique index until the first commits, and then
// reads RowsAffected == 0. Same mechanism that guarded the run index before, one table over.
func (r *Repository) CreateRunWithDurableClaim(ctx context.Context, run *WorkflowRun) (bool, error) {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	var inserted bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		claim := &RunIdempotencyClaim{
			ID:             uuid.New(),
			OrgID:          run.OrgID,
			WorkflowID:     run.WorkflowID,
			IdempotencyKey: run.IdempotencyKey,
		}
		// Targeted at the claim's own unique index. An untargeted DO NOTHING would also
		// swallow a primary-key collision and report it as "already claimed", which would
		// drop a contact out of the drip forever with no error anywhere.
		res := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "workflow_id"}, {Name: "idempotency_key"}},
			DoNothing: true,
		}).Create(claim)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			inserted = false // already claimed: this contact enrolled once, however long ago
			return nil
		}
		runRes := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(run)
		if runRes.Error != nil {
			return runRes.Error
		}
		inserted = runRes.RowsAffected > 0
		return nil
	})
	return inserted, err
}

// BackfillSequenceEnrollmentClaims writes a durable claim for every EXISTING sequence-
// enrolled run that predates the claims table. Without it the fix would protect only
// contacts enrolled from now on: everyone already mid-drip, or finished within the current
// 90-day window, still loses their dedupe when the pruner reaches their run and still gets
// re-mailed. The live population is exactly the population at risk.
//
// The discriminator is the run's own marketing tag, not the key's "seq:" prefix — the key
// format belongs to package marketing, while the tag is stamped here (contactEnrollContext),
// so this stays true if that format ever changes. Idempotent via ON CONFLICT, hence safe on
// every boot; DISTINCT ON + ORDER BY keeps it deterministic (earliest claim wins) and holds
// up even on a database whose own idx_wf_runs_wf_idemp went missing. Returns rows written.
func (r *Repository) BackfillSequenceEnrollmentClaims(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Exec(`
		INSERT INTO automation_run_idempotency_claims (id, org_id, workflow_id, idempotency_key, created_at)
		SELECT DISTINCT ON (r.workflow_id, r.idempotency_key)
			gen_random_uuid(), r.org_id, r.workflow_id, r.idempotency_key, COALESCE(r.created_at, NOW())
		FROM automation_workflow_runs r
		WHERE r.idempotency_key <> ''
			AND r.trigger_context ->> ? IS NOT NULL
		ORDER BY r.workflow_id, r.idempotency_key, r.created_at
		ON CONFLICT (workflow_id, idempotency_key) DO NOTHING`, marketingEnrollmentKey)
	return res.RowsAffected, res.Error
}

// CreateRun inserts a new workflow run. Returns false if idempotency key already exists.
// The dedupe lasts only as long as the run row — PruneCompletedRuns reclaims it at 90 days.
// A caller whose key must dedupe beyond that needs CreateRunWithDurableClaim instead.
func (r *Repository) CreateRun(ctx context.Context, run *WorkflowRun) (bool, error) {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	err := r.db.WithContext(ctx).Create(run).Error
	if err != nil {
		// Check for unique constraint violation on idempotency_key
		if errors.Is(err, gorm.ErrDuplicatedKey) || isDuplicateKeyError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LockAndGetRun locks a run row with SELECT FOR UPDATE SKIP LOCKED.
func (r *Repository) LockAndGetRun(ctx context.Context, tx *gorm.DB, runID uuid.UUID) (*WorkflowRun, error) {
	var run WorkflowRun
	err := tx.WithContext(ctx).
		Raw("SELECT * FROM automation_workflow_runs WHERE id = ? AND status IN (?, ?) FOR UPDATE SKIP LOCKED", runID, StatusPending, StatusRunning).
		Scan(&run).Error
	if err != nil {
		return nil, err
	}
	if run.ID == uuid.Nil {
		return nil, nil
	}
	return &run, nil
}

// UpdateRunTx updates a workflow run within a caller-provided transaction.
// tx must not be nil — use UpdateRunStandalone for self-contained writes.
func (r *Repository) UpdateRunTx(ctx context.Context, tx *gorm.DB, run *WorkflowRun) error {
	if tx == nil {
		return ErrNilTransaction
	}
	return tx.WithContext(ctx).Save(run).Error
}

// UpdateRunStandalone updates a workflow run in its own transaction.
// Use for terminal/idempotent writes where no action log atomicity is needed
// (e.g. failRun, skipRun, crash recovery reset).
func (r *Repository) UpdateRunStandalone(ctx context.Context, run *WorkflowRun) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Save(run).Error
	})
}

// ResetRunForRetry atomically transitions a FAILED run back to PENDING for a manual
// retry (P21), then reports whether it actually reset anything. It clears ONLY the retry
// bookkeeping (retry_count, next_retry_at, last_error) and the terminal marker
// (finished_at) — CompletedActions and CurrentActionIdx are deliberately left untouched so
// processRun resumes at the step that failed instead of re-running completed work.
//
// The run row is taken with SELECT ... FOR UPDATE and its status is re-checked under that
// lock before the write, so the reset cannot race a worker or crash recovery transitioning
// the same run concurrently: a second retry, or a crash-recovery requeue that fired between
// the handler's read and this call, blocks on the lock and then observes the committed
// state. If the locked row is not (or no longer) failed — already retried, completed,
// skipped, or in flight — this is a no-op returning false, which the caller maps to a 400.
//
// A manual retry deliberately BYPASSES the exponential backoff (30s/2m/10m) that paces
// automatic retries: the user has chosen to retry now, so next_retry_at is cleared and the
// run is eligible immediately (the worker channel picks it up; if that push is dropped,
// the sweeper's stranded-run clause re-dispatches it). Resetting retry_count to 0 also
// restores a full automatic-retry budget for the step that resumes — so a run that keeps
// failing can be retried by the user any number of times without getting backoff-throttled.
// A map update (not Save) writes the zero values as 0/NULL rather than skipping them, and
// never clobbers CompletedActions/CurrentActionIdx.
func (r *Repository) ResetRunForRetry(ctx context.Context, runID uuid.UUID) (bool, error) {
	var reset bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run WorkflowRun
		// Plain FOR UPDATE (not SKIP LOCKED): we must wait for any concurrent holder and
		// then read the committed status, rather than skip a locked row.
		if err := tx.Raw("SELECT * FROM automation_workflow_runs WHERE id = ? FOR UPDATE", runID).
			Scan(&run).Error; err != nil {
			return err
		}
		if run.ID == uuid.Nil || run.Status != StatusFailed {
			return nil // not found or not failed → leave reset == false
		}
		res := tx.Model(&WorkflowRun{}).
			Where("id = ?", runID).
			Updates(map[string]any{
				"status":        StatusPending,
				"retry_count":   0,
				"last_error":    "",
				"next_retry_at": nil,
				"finished_at":   nil,
			})
		if res.Error != nil {
			return res.Error
		}
		reset = res.RowsAffected > 0
		return nil
	})
	return reset, err
}

// ListRunsByWorkflow returns paginated runs for a workflow.
func (r *Repository) ListRunsByWorkflow(ctx context.Context, workflowID uuid.UUID, page, size int) ([]WorkflowRun, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	offset := (page - 1) * size

	var total int64
	r.db.WithContext(ctx).Model(&WorkflowRun{}).
		Where("workflow_id = ?", workflowID).
		Count(&total)

	var runs []WorkflowRun
	err := r.db.WithContext(ctx).
		Where("workflow_id = ?", workflowID).
		Order("created_at DESC").
		Offset(offset).Limit(size).
		Find(&runs).Error
	return runs, total, err
}

// GetRunByID retrieves a run by ID.
func (r *Repository) GetRunByID(ctx context.Context, runID uuid.UUID) (*WorkflowRun, error) {
	var run WorkflowRun
	err := r.db.WithContext(ctx).
		Where("id = ?", runID).
		First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

// GetPendingRuns returns runs ready for processing (pending with no future retry).
func (r *Repository) GetPendingRuns(ctx context.Context, limit int) ([]WorkflowRun, error) {
	var runs []WorkflowRun
	err := r.db.WithContext(ctx).
		Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", StatusPending, time.Now()).
		Order("created_at ASC").
		Limit(limit).
		Find(&runs).Error
	return runs, err
}

// GetRunningRuns returns runs with status='running' (for crash recovery).
func (r *Repository) GetRunningRuns(ctx context.Context) ([]WorkflowRun, error) {
	var runs []WorkflowRun
	err := r.db.WithContext(ctx).
		Where("status = ?", StatusRunning).
		Find(&runs).Error
	return runs, err
}

// strandedPendingSweepGrace is how old a pending run with a NULL next_retry_at
// must be before SweepRetries re-dispatches it. A run whose creation-time jobs-
// channel push was dropped (channel full in EnrollRun / triggerEventInternal /
// RunWorkflowNow / RetryRun) is left in exactly that state, and without the
// stranded clause it stays invisible to the sweep until the next engine restart
// (RequeueInFlight, itself capped at 500 runs). The grace keeps the sweep from
// re-pushing runs that are merely queued in the channel backlog; a duplicate
// push is harmless anyway (LockAndGetRun's FOR UPDATE SKIP LOCKED), so this is
// a load valve, not a correctness gate.
const strandedPendingSweepGrace = time.Minute

// SweepRetries finds pending runs whose retry time has arrived, plus stranded
// pending runs: created at least strandedPendingSweepGrace ago with
// next_retry_at still NULL, meaning their creation-time channel push was
// dropped and no retry has been scheduled since.
func (r *Repository) SweepRetries(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	now := time.Now()
	err := r.db.WithContext(ctx).
		Model(&WorkflowRun{}).
		Select("id").
		Where("status = ? AND ((next_retry_at IS NOT NULL AND next_retry_at <= ?) OR (next_retry_at IS NULL AND created_at <= ?))",
			StatusPending, now, now.Add(-strandedPendingSweepGrace)).
		Find(&ids).Error
	return ids, err
}

// WakeDueWaitingRuns atomically flips waiting runs whose wake_at has arrived
// back to pending and returns their ids. The UPDATE...RETURNING is a single
// statement, so concurrent sweepers (multi-instance) each claim a disjoint set.
// next_retry_at is set to now() so that even if the caller's channel push is
// dropped, the regular SweepRetries pass re-picks the run on the next tick.
func (r *Repository) WakeDueWaitingRuns(ctx context.Context) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).
		Raw(`UPDATE automation_workflow_runs
		     SET status = ?, next_retry_at = NOW(), wake_at = NULL, updated_at = NOW()
		     WHERE status = ? AND wake_at <= NOW()
		     RETURNING id`, StatusPending, StatusWaiting).
		Scan(&ids).Error
	return ids, err
}

// --- WorkflowActionLog ---

// CreateActionLogTx inserts an action log entry within a caller-provided transaction.
// tx must not be nil — use CreateActionLogStandalone for self-contained writes.
func (r *Repository) CreateActionLogTx(ctx context.Context, tx *gorm.DB, log *WorkflowActionLog) error {
	if tx == nil {
		return ErrNilTransaction
	}
	if log.ID == uuid.Nil {
		log.ID = uuid.New()
	}
	return tx.WithContext(ctx).Create(log).Error
}

// CreateActionLogStandalone inserts an action log entry in its own transaction.
// Use for pre-execution informational logs (status=running) where
// loss on crash is acceptable.
func (r *Repository) CreateActionLogStandalone(ctx context.Context, log *WorkflowActionLog) error {
	if log.ID == uuid.Nil {
		log.ID = uuid.New()
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return tx.Create(log).Error
	})
}

// UpdateActionLogTx updates an existing action log entry within a caller-provided transaction.
// tx must not be nil.
func (r *Repository) UpdateActionLogTx(ctx context.Context, tx *gorm.DB, log *WorkflowActionLog) error {
	if tx == nil {
		return ErrNilTransaction
	}
	return tx.WithContext(ctx).Save(log).Error
}

// GetActionLogsByRunID returns all action logs for a run.
func (r *Repository) GetActionLogsByRunID(ctx context.Context, runID uuid.UUID) ([]WorkflowActionLog, error) {
	var logs []WorkflowActionLog
	err := r.db.WithContext(ctx).
		Where("run_id = ?", runID).
		Order("action_idx ASC, created_at ASC").
		Find(&logs).Error
	return logs, err
}

// --- WorkflowOrgToken ---

// GetOrgToken retrieves a token record by its token string.
func (r *Repository) GetOrgToken(ctx context.Context, token string) (*WorkflowOrgToken, error) {
	var t WorkflowOrgToken
	err := r.db.WithContext(ctx).Where("token = ?", token).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// GetOrgTokenByOrgID retrieves the inbound-webhook token record for an org, or
// (nil, nil) if the org has none yet.
func (r *Repository) GetOrgTokenByOrgID(ctx context.Context, orgID uuid.UUID) (*WorkflowOrgToken, error) {
	var t WorkflowOrgToken
	err := r.db.WithContext(ctx).Where("org_id = ?", orgID).First(&t).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// CreateOrgToken creates a new org token.
func (r *Repository) CreateOrgToken(ctx context.Context, t *WorkflowOrgToken) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// UpdateOrgSecret rotates the signing secret for an org's webhook token, leaving
// the token string (and therefore the inbound URL) unchanged.
func (r *Repository) UpdateOrgSecret(ctx context.Context, orgID uuid.UUID, secret string) error {
	return r.db.WithContext(ctx).Model(&WorkflowOrgToken{}).
		Where("org_id = ?", orgID).
		Update("secret", secret).Error
}

// BeginTx starts a transaction.
func (r *Repository) BeginTx(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Begin()
}

// DB returns the underlying database connection.
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// --- Helpers ---

// GetCompletedActionIndices parses the completed_actions JSON array from a run.
func GetCompletedActionIndices(run *WorkflowRun) map[int]bool {
	result := make(map[int]bool)
	if run.CompletedActions == nil {
		return result
	}
	var indices []int
	if err := json.Unmarshal(run.CompletedActions, &indices); err != nil {
		return result
	}
	for _, idx := range indices {
		result[idx] = true
	}
	return result
}

// SetCompletedActions marshals a slice of completed action indices to JSON.
func SetCompletedActions(indices []int) ([]byte, error) {
	return json.Marshal(indices)
}

// isDuplicateKeyError checks if an error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	// Postgres duplicate key error code: 23505
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		containsString(err.Error(), "duplicate key") ||
		containsString(err.Error(), "23505")
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- Flat actions → steps: the backfill and its teardown gate ---

// flatActionsBackfillStats reports what ONE MigrateFlatActionsToSteps pass did to ONE
// table. Every field is logged; the numbers are what tell you whether the pass is
// converging or spinning.
type flatActionsBackfillStats struct {
	// Converted is how many rows were actually rewritten with a steps tree.
	Converted int
	// Unconvertible is how many rows there is no honest conversion for: `actions` is
	// absent or jsonb 'null', or it is valid JSON that is not an array of actions.
	// Deliberately left untouched — but counted, and surfaced again by the teardown
	// gate, because these are exactly the rows that will never satisfy it and someone
	// has to decide what to do with them by hand.
	//
	// Not every one of these is a problem: since '[]' joined the selection predicate,
	// a row with steps = '[]' and actions = 'null' is selected, counted here, and yet
	// loses nothing when the Actions column is dropped. The gate is the authority on
	// which rows actually block the teardown (CountFlatActionsGate's blocking counts);
	// this number only says how many rows the backfill declined to rewrite.
	Unconvertible int
	// Failed is how many UPDATEs errored. Previously discarded with `_ =`.
	Failed int
}

func (s flatActionsBackfillStats) touched() bool {
	return s.Converted > 0 || s.Unconvertible > 0 || s.Failed > 0
}

// stepsFromFlatActions converts a stored flat `actions` blob into the equivalent steps
// tree. ok is false for a blob with no honest conversion: absent, jsonb 'null', or
// valid JSON that is not an array of actions.
//
// An EMPTY actions array converts to an EMPTY steps array, and that is the whole point
// of this being a function rather than an inline loop. `var steps []StepSpec` left nil
// when the loop body never ran, and json.Marshal of a nil slice is the four bytes
// `null` — so a row with actions = '[]' was rewritten to steps = 'null'::jsonb, which
// still matches the migration's own WHERE clause. Every boot re-selected it, re-wrote
// it, and bumped updated_at, forever, and it could never satisfy the teardown gate.
// `[]` is also simply the truthful answer: a workflow with no actions has no steps.
func stepsFromFlatActions(raw datatypes.JSON) (datatypes.JSON, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var actions []ActionSpec
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, false
	}
	steps := make([]StepSpec, 0, len(actions))
	for i := range actions {
		action := actions[i]
		if action.Type == "delay" {
			steps = append(steps, StepSpec{
				Type:  "delay",
				ID:    action.ID,
				Delay: delayParamsFromMap(action.Params),
			})
			continue
		}
		steps = append(steps, StepSpec{
			Type:   "action",
			ID:     action.ID,
			Action: &action,
		})
	}
	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		return nil, false
	}
	return datatypes.JSON(stepsJSON), true
}

// flatActionsBackfillPredicate selects every row the backfill may still have work for:
// no steps tree at all (SQL NULL or jsonb 'null'), or an EMPTY one. Shared by both
// tables so the two halves cannot drift apart, and stated once so it can be compared
// against the gate's own predicate (CountFlatActionsGate) at a glance.
const flatActionsBackfillPredicate = `steps IS NULL OR steps = 'null'::jsonb OR steps = '[]'::jsonb`

// MigrateFlatActionsToSteps automatically converts older flat actions workflows to recursive steps tree.
//
// DEPRECATED: Legacy actions→steps conversion.
// Deadline: 2026-09-01. After this date, remove this fallback and require Steps.
// Before removing, read the FLAT_ACTIONS_TEARDOWN_GATE log line (LogFlatActionsGate),
// which reports the gate's counts on every boot.
//
// SELECTION: steps SQL NULL, jsonb 'null', OR the empty array '[]'.
//
// '[]' is in the predicate deliberately, and it is the fix for a gate that could be
// permanently BLOCKED with no automated remedy. A workflow with REAL flat actions and
// steps = '[]' is creatable through the live API today — handlers.go's hasSteps('[]')
// is false, so nothing derives actions from steps and validation falls through to
// validateActions, which passes — and the old predicate (`steps IS NULL OR steps =
// 'null'`) could never reach such a row. The gate, meanwhile, counts steps = '[]' as
// unsatisfied. Disjoint sets: the backfill could not fix what the gate blocked on, so
// the teardown would sit BLOCKED on every boot forever and the 2026-09-01 deadline
// would pass with nobody able to green-light it.
//
// Runs on every boot, so it must CONVERGE: a row it has already handled must not be
// rewritten again. Two rules enforce that, and BOTH are live controls —
//
//  1. never write a value that still leaves the row un-converged
//     (see stepsFromFlatActions on the '[]' → 'null' defect). This is what stops an
//     empty-actions row from being re-selected and re-written on every boot forever.
//  2. never issue the UPDATE at all when the value would not change.
//
// Rule 2 was previously documented here as unreachable. It is NOT, on two counts.
// Measured: under a mutation that reverts rule 1's empty-slice fix, the updated_at
// assertions in flat_actions_gate_test.go still hold — and they hold BECAUSE rule 2
// fires. It is the live anti-churn control the moment rule 1 regresses. And since '[]'
// joined the predicate above it is unconditionally live in steady state: an
// actions = '[]' row is selected on every boot, converts to exactly the '[]' already
// stored, and rule 2 is the only reason no UPDATE is issued and no updated_at moves.
// Delete neither rule on the strength of the other.
//
// Soft-deleted workflows are skipped, because GORM scopes the Find below to
// `deleted_at IS NULL`. That is pre-existing behaviour and it matches the gate, which
// only counts live workflows. Their VERSION rows are still converted — that table has
// no soft delete — so a deleted workflow can never wedge the gate.
func (r *Repository) MigrateFlatActionsToSteps(ctx context.Context) error {
	var errs []error

	// 1. Migrate workflows table
	var workflows []Workflow
	if err := r.db.WithContext(ctx).Where(flatActionsBackfillPredicate).Find(&workflows).Error; err != nil {
		return fmt.Errorf("scan workflows needing steps: %w", err)
	}
	var wfStats flatActionsBackfillStats
	for i := range workflows {
		wf := &workflows[i]
		steps, ok := stepsFromFlatActions(wf.Actions)
		if !ok {
			wfStats.Unconvertible++
			continue
		}
		if string(steps) == string(wf.Steps) {
			continue // rule 2 — a LIVE control, see the convergence note on this function
		}
		if err := r.db.WithContext(ctx).Model(&Workflow{}).
			Where("id = ?", wf.ID).Update("steps", steps).Error; err != nil {
			wfStats.Failed++
			errs = append(errs, fmt.Errorf("workflow %s: %w", wf.ID, err))
			continue
		}
		wfStats.Converted++
	}
	if wfStats.touched() {
		slog.Info("automation: flat actions → steps backfill (workflows)",
			"converted", wfStats.Converted, "unconvertible", wfStats.Unconvertible,
			"failed", wfStats.Failed)
	}

	// 2. Migrate workflow versions table
	var versions []WorkflowVersion
	if err := r.db.WithContext(ctx).Where(flatActionsBackfillPredicate).Find(&versions).Error; err != nil {
		return errors.Join(append(errs, fmt.Errorf("scan workflow versions needing steps: %w", err))...)
	}
	var verStats flatActionsBackfillStats
	for i := range versions {
		ver := &versions[i]
		steps, ok := stepsFromFlatActions(ver.Actions)
		if !ok {
			verStats.Unconvertible++
			continue
		}
		if string(steps) == string(ver.Steps) {
			continue // rule 2 — a LIVE control, see the convergence note on this function
		}
		if err := r.db.WithContext(ctx).Model(&WorkflowVersion{}).
			Where("id = ?", ver.ID).Update("steps", steps).Error; err != nil {
			verStats.Failed++
			errs = append(errs, fmt.Errorf("workflow version %s: %w", ver.ID, err))
			continue
		}
		verStats.Converted++
	}
	if verStats.touched() {
		slog.Info("automation: flat actions → steps backfill (versions)",
			"converted", verStats.Converted, "unconvertible", verStats.Unconvertible,
			"failed", verStats.Failed)
	}

	return errors.Join(errs...)
}

// FlatActionsGateLogPrefix is the stable, greppable prefix of the teardown-gate log
// line. Grep Railway for it; every boot emits exactly one, cleared or not.
const FlatActionsGateLogPrefix = "automation: FLAT_ACTIONS_TEARDOWN_GATE"

// FlatActionsGateCounts is the flat-Actions teardown gate expressed as numbers.
//
// It answers TWO questions, and keeping them apart is the point:
//
//	"Is it safe to drop the Actions column?"  → the *Blocking counts. Cleared().
//	"Do any rows execute nothing at runtime?" → the *EmptySteps counts. Inert().
//
// They are not the same question. A row with actions = '[]' AND steps = '[]' loses
// NOTHING when the column is dropped — yet it runs the steps interpreter over zero
// steps and reports itself COMPLETED having done nothing. Reporting that row as a
// teardown blocker (which the first cut of this gate did, by never reading `actions` at
// all) would print verdict=BLOCKED on every boot forever with no automated remedy.
// Dropping it from the report entirely would hide a real runtime defect. So both are
// counted, and only one of them decides the verdict.
type FlatActionsGateCounts struct {
	// WorkflowsMissingSteps counts LIVE workflows with no steps tree at all
	// (steps IS NULL, or jsonb 'null'). These still execute off flat actions.
	WorkflowsMissingSteps int64
	// WorkflowsEmptySteps counts LIVE workflows whose steps are an EMPTY array.
	//
	// This is the count a naive `steps IS NOT NULL` gate misses, and it is why the gate
	// is a set of numbers rather than one. processRun takes the steps path whenever
	// `len(steps) > 0 && string(steps) != "null"` (engine.go), and "[]" satisfies both
	// — so an empty-steps row runs the steps interpreter over zero steps and reports
	// itself COMPLETED having executed nothing. It passes a `steps IS NOT NULL` gate
	// while being exactly as broken as a row that has no steps at all.
	WorkflowsEmptySteps int64
	// VersionsMissingSteps counts version snapshots with no steps tree. Runs execute
	// the PINNED VERSION, not the live workflow, so a migrated workflow with an
	// unmigrated version snapshot still runs off flat actions.
	VersionsMissingSteps int64
	// VersionsEmptySteps counts version snapshots whose steps are an empty array.
	VersionsEmptySteps int64

	// WorkflowsBlocking counts LIVE workflows that would LOSE BEHAVIOUR if the Actions
	// column were dropped today: `actions` holds something (not SQL NULL, not jsonb
	// 'null', not '[]') AND steps are missing or empty, so the flat column is the only
	// place that behaviour exists.
	//
	// This — not the four counts above — is the true teardown predicate. Rows whose
	// `actions` is unconvertible (a JSON object rather than an array) count too: the
	// backfill cannot rewrite them, so a human has to look before anything is dropped.
	WorkflowsBlocking int64
	// VersionsBlocking is the same predicate over version snapshots. Runs execute the
	// PINNED VERSION, so a fully-migrated workflow with a blocking version snapshot is
	// still a blocker.
	VersionsBlocking int64
}

// Cleared reports whether the TEARDOWN is safe: no row anywhere would lose behaviour if
// the Actions column were dropped. It deliberately ignores the empty-steps counts — see
// the type comment, and Inert() for the other question.
func (c FlatActionsGateCounts) Cleared() bool {
	return c.WorkflowsBlocking == 0 && c.VersionsBlocking == 0
}

// Inert reports how many rows have an EMPTY steps array. processRun takes the steps path
// whenever `len(steps) > 0 && string(steps) != "null"` (engine.go), and "[]" satisfies
// both — so these rows run the steps interpreter over zero steps and report themselves
// COMPLETED having executed nothing. That is a real defect, and a SEPARATE one from the
// teardown: it is not a reason to keep the Actions column.
func (c FlatActionsGateCounts) Inert() int64 {
	return c.WorkflowsEmptySteps + c.VersionsEmptySteps
}

// CountFlatActionsGate measures the teardown gate.
//
// The workflows half is scoped to `deleted_at IS NULL` — a soft-deleted workflow never
// runs, so it cannot block anything. The versions half is NOT, and deliberately: that
// table has no deleted_at column at all (adding the predicate is a SQL error, not a
// stricter query), and a version snapshot outlives its workflow's soft delete while
// in-flight runs still pin it.
//
// Raw SQL with FILTER rather than a Model().Count() per number: two round trips instead
// of six, and it keeps each table's three numbers consistent with one another by
// construction — they come from a single scan under a single snapshot.
func (r *Repository) CountFlatActionsGate(ctx context.Context) (FlatActionsGateCounts, error) {
	type gateRow struct {
		MissingSteps int64 `gorm:"column:missing_steps"`
		EmptySteps   int64 `gorm:"column:empty_steps"`
		Blocking     int64 `gorm:"column:blocking"`
	}
	// blocking is the only one of the three that reads `actions`, and that is what makes
	// the verdict precise: "steps are missing or empty" AND "the flat column still holds
	// the behaviour". An empty `actions` (SQL NULL, jsonb 'null' or '[]') has nothing to
	// lose, so it is reported in the other two numbers and blocks nothing.
	const gateSelect = `
		SELECT
			count(*) FILTER (WHERE steps IS NULL OR steps::text = 'null') AS missing_steps,
			count(*) FILTER (WHERE steps::text = '[]') AS empty_steps,
			count(*) FILTER (
				WHERE (steps IS NULL OR steps::text = 'null' OR steps::text = '[]')
				  AND actions IS NOT NULL AND actions::text <> 'null' AND actions::text <> '[]'
			) AS blocking
		FROM `

	var out FlatActionsGateCounts
	var wf gateRow
	if err := r.db.WithContext(ctx).
		Raw(gateSelect + `automation_workflows WHERE deleted_at IS NULL`).
		Scan(&wf).Error; err != nil {
		return out, fmt.Errorf("count workflows gate: %w", err)
	}
	var ver gateRow
	if err := r.db.WithContext(ctx).
		Raw(gateSelect + `automation_workflow_versions`).
		Scan(&ver).Error; err != nil {
		return out, fmt.Errorf("count workflow versions gate: %w", err)
	}
	out.WorkflowsMissingSteps = wf.MissingSteps
	out.WorkflowsEmptySteps = wf.EmptySteps
	out.WorkflowsBlocking = wf.Blocking
	out.VersionsMissingSteps = ver.MissingSteps
	out.VersionsEmptySteps = ver.EmptySteps
	out.VersionsBlocking = ver.Blocking
	return out, nil
}

// LogFlatActionsGate emits ONE greppable line reporting the teardown gate.
//
// It exists so that "have all workflows been migrated off flat actions?" is answerable
// from Railway logs by anyone, instead of requiring a production SQL console — which
// is the only reason the R5 teardown has been sitting behind an unmeasured gate.
//
// Never returns an error and never panics the boot: a gate that could break a deploy
// would be worse than the ambiguity it removes, so a failed count is logged at Warn
// with verdict=UNKNOWN and that is the end of it. The prefix is identical in every
// outcome, so one grep finds them all.
//
// TWO independent signals, deliberately readable apart (see FlatActionsGateCounts):
//
//	verdict=CLEAR|BLOCKED|UNKNOWN  — may the Actions field be removed?
//	inert_rows=N                   — how many rows execute NOTHING at runtime?
//
// inert_rows never changes the verdict. It is its own defect and its own fix; a row
// that does nothing is not a reason to keep a column it does not use. It does raise the
// level to Warn, because a workflow silently completing without acting is not something
// to bury at Info — so "CLEAR at Warn" means "tear down when you like, and separately,
// go look at those rows".
func (r *Repository) LogFlatActionsGate(ctx context.Context, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	c, err := r.CountFlatActionsGate(ctx)
	if err != nil {
		log.Warn(FlatActionsGateLogPrefix+" verdict=UNKNOWN — the gate query itself failed; the teardown is NOT cleared by this boot",
			"verdict", "UNKNOWN", "error", err)
		return
	}
	attrs := []any{
		"workflows_blocking", c.WorkflowsBlocking,
		"versions_blocking", c.VersionsBlocking,
		"inert_rows", c.Inert(),
		"workflows_missing_steps", c.WorkflowsMissingSteps,
		"workflows_empty_steps", c.WorkflowsEmptySteps,
		"versions_missing_steps", c.VersionsMissingSteps,
		"versions_empty_steps", c.VersionsEmptySteps,
	}
	switch {
	case !c.Cleared():
		log.Warn(FlatActionsGateLogPrefix+" verdict=BLOCKED — some rows still hold flat actions their steps tree does not (workflows_blocking/versions_blocking); dropping the Actions column would LOSE that behaviour, so do NOT remove the Actions field yet",
			append(attrs, "verdict", "BLOCKED")...)
	case c.Inert() > 0:
		log.Warn(FlatActionsGateLogPrefix+" verdict=CLEAR — the teardown is SAFE: ZERO rows would lose behaviour if the Actions column were dropped. SEPARATELY, inert_rows rows have an EMPTY steps array and so execute NOTHING at runtime; that is its own defect and it does not block the teardown",
			append(attrs, "verdict", "CLEAR")...)
	default:
		log.Info(FlatActionsGateLogPrefix+" verdict=CLEAR — every count is ZERO: no row would lose behaviour if the Actions column were dropped, and no row has an empty steps tree, so the deprecated Actions field is safe to remove",
			append(attrs, "verdict", "CLEAR")...)
	}
}

