package repository

import (
	"gorm.io/gorm"
)

// BackfillPipelines gives every org a default pipeline and stamps every existing
// stage and deal onto it (R9.3). Boot-guarded and idempotent, mirroring
// migrations/000075_pipelines.up.sql.
//
// Returns the number of rows written on this run, so a settled database logs
// nothing and a boot that actually migrated something says so.
func BackfillPipelines(db *gorm.DB) (int64, error) {
	// A cheap no-op if the schema has moved on past this backfill, so a re-run
	// after a hypothetical column drop never queries a column that is gone.
	// (Same guard BackfillObjectLinksFromRecordFKs uses.)
	if !hasColumn(db, "pipeline_stages", "pipeline_id") {
		return 0, nil
	}

	var total int64

	// 1. One default pipeline per org.
	//
	// Driven from organizations, NOT from `SELECT DISTINCT org_id FROM
	// pipeline_stages`. An org can legitimately have ZERO stages —
	// authUseCase.seedDefaultStages discards every Create error (`_ = ...`), so
	// a registration whose stage inserts failed still produced a usable org —
	// and a stage-driven backfill would leave exactly those orgs with no
	// pipeline, which then surfaces as a 500 the first time someone opens their
	// (empty) board.
	//
	// Idempotent through NOT EXISTS; uq_pipelines_org_default is the backstop if
	// two instances boot simultaneously and both pass the NOT EXISTS.
	res := db.Exec(`
		INSERT INTO pipelines (id, org_id, name, position, is_default, created_at, updated_at)
		SELECT uuid_generate_v4(), o.id, 'Sales Pipeline', 0, TRUE, NOW(), NOW()
		FROM organizations o
		WHERE NOT EXISTS (
			SELECT 1 FROM pipelines p WHERE p.org_id = o.id AND p.deleted_at IS NULL
		)
		ON CONFLICT DO NOTHING`)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected

	// 2. Stamp EVERY stage — including soft-deleted ones, deliberately.
	//
	// There is no `AND s.deleted_at IS NULL` here and adding one would corrupt
	// existing orgs. Stage deletion is a GORM soft delete, so the
	// `deals.stage_id … ON DELETE SET NULL` FK never fires and live deals keep
	// pointing at tombstoned stages (applying an industry template soft-deletes
	// the seeded five while their deals stay put). The report label query
	// resolves those names with no deleted_at filter too. A skipped tombstone
	// therefore becomes a stage with a NULL pipeline_id that live rows still
	// reference — a hole that only shows up on prod, and only later.
	res = db.Exec(`
		UPDATE pipeline_stages s SET pipeline_id = p.id
		FROM pipelines p
		WHERE p.org_id = s.org_id
		  AND p.is_default
		  AND p.deleted_at IS NULL
		  AND s.pipeline_id IS NULL`)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected

	// 3. Deals: take the pipeline from the deal's stage where there is one, else
	// the org default. The COALESCE arm is not defensive padding — a deal may
	// legally have stage_id NULL (CreateDealInput.StageID is an unvalidated
	// pointer), so derivation alone has no answer for an unstaged deal and would
	// leave it out of every pipeline-filtered view.
	//
	// The stage lookup is likewise unfiltered by deleted_at, for the reason in
	// step 2: a deal on a tombstoned stage is normal.
	res = db.Exec(`
		UPDATE deals d SET pipeline_id = COALESCE(
			(SELECT s.pipeline_id FROM pipeline_stages s WHERE s.id = d.stage_id),
			(SELECT p.id FROM pipelines p
			  WHERE p.org_id = d.org_id AND p.is_default AND p.deleted_at IS NULL
			  LIMIT 1)
		)
		WHERE d.pipeline_id IS NULL`)
	if res.Error != nil {
		return total, res.Error
	}
	total += res.RowsAffected

	return total, nil
}
