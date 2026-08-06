package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// pipeline_backfill_db_test.go pins the two properties R9.3's backfill has to
// have, both of which fail only on production data:
//
//  1. It stamps SOFT-DELETED stages too. Stage deletion is a soft delete, so
//     deals.stage_id's ON DELETE SET NULL never fires and live deals keep
//     pointing at tombstones. A `deleted_at IS NULL` filter would leave those
//     tombstones with a NULL pipeline_id — invisible here, broken there.
//  2. It is idempotent. It runs on EVERY boot, so a second pass must write
//     nothing at all.

func setupPipelineBackfillSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE organizations (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		name VARCHAR(255) NOT NULL DEFAULT ''
	)`).Error)
	// Mirrors migrations/000075 + the main.go boot guard.
	require.NoError(t, db.Exec(`CREATE TABLE pipelines (
		id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		name       VARCHAR(100) NOT NULL,
		position   INT NOT NULL DEFAULT 0,
		is_default BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX uq_pipelines_org_default
		ON pipelines(org_id) WHERE is_default AND deleted_at IS NULL`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE pipeline_stages (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		pipeline_id UUID REFERENCES pipelines(id),
		name VARCHAR(100) NOT NULL,
		position INT NOT NULL DEFAULT 0,
		color VARCHAR(20) DEFAULT '#3B82F6',
		is_won BOOLEAN NOT NULL DEFAULT FALSE,
		is_lost BOOLEAN NOT NULL DEFAULT FALSE,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE deals (
		id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id UUID NOT NULL,
		title VARCHAR(255) NOT NULL DEFAULT '',
		stage_id UUID,
		pipeline_id UUID REFERENCES pipelines(id),
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at TIMESTAMPTZ
	)`).Error)
}

func TestBackfillPipelines_DB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping DB-backed test in short mode")
	}
	db, cleanup := startPostgres(t)
	defer cleanup()
	setupPipelineBackfillSchema(t, db)

	org := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Acme')`, org).Error)

	// An org with NO stages at all — reachable because seedDefaultStages
	// discards its Create errors. A stage-driven backfill would skip it entirely.
	emptyOrg := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO organizations (id, name) VALUES (?, 'Empty')`, emptyOrg).Error)

	liveStage, deletedStage := uuid.New(), uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position) VALUES (?, ?, 'Lead In', 0)`, liveStage, org).Error)
	require.NoError(t, db.Exec(`INSERT INTO pipeline_stages (id, org_id, name, position, deleted_at) VALUES (?, ?, 'Retired', 1, NOW())`, deletedStage, org).Error)

	stagedDeal, unstagedDeal, tombstonedDeal := uuid.New(), uuid.New(), uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, stage_id) VALUES (?, ?, 'staged', ?)`, stagedDeal, org, liveStage).Error)
	// stage_id NULL is legal — CreateDealInput.StageID is an unvalidated pointer.
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, stage_id) VALUES (?, ?, 'unstaged', NULL)`, unstagedDeal, org).Error)
	// A live deal still pointing at a SOFT-DELETED stage: the shape the
	// deleted_at filter would break.
	require.NoError(t, db.Exec(`INSERT INTO deals (id, org_id, title, stage_id) VALUES (?, ?, 'on a tombstone', ?)`, tombstonedDeal, org, deletedStage).Error)

	n, err := BackfillPipelines(db)
	require.NoError(t, err)
	assert.Greater(t, n, int64(0), "the first pass must write something")

	// Every org has exactly one default pipeline — including the stage-less one.
	var pipelineCount int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pipelines WHERE is_default AND deleted_at IS NULL`).Scan(&pipelineCount).Error)
	assert.EqualValues(t, 2, pipelineCount, "both orgs get a default pipeline, even the one with no stages")

	// No stage is left off a board — soft-deleted ones included.
	var orphanStages int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM pipeline_stages WHERE pipeline_id IS NULL`).Scan(&orphanStages).Error)
	assert.EqualValues(t, 0, orphanStages, "soft-deleted stages must be stamped too — live deals still reference them")

	var orphanDeals int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM deals WHERE pipeline_id IS NULL`).Scan(&orphanDeals).Error)
	assert.EqualValues(t, 0, orphanDeals, "a deal with no stage still needs a board")

	// The tombstoned-stage deal inherited its stage's pipeline, not a guess.
	var same bool
	require.NoError(t, db.Raw(`
		SELECT d.pipeline_id = s.pipeline_id
		FROM deals d JOIN pipeline_stages s ON s.id = d.stage_id
		WHERE d.id = ?`, tombstonedDeal).Scan(&same).Error)
	assert.True(t, same, "a deal on a soft-deleted stage takes that stage's pipeline")

	// Idempotent: a settled database writes nothing on the next boot.
	n2, err := BackfillPipelines(db)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n2, "a second pass must be a no-op")
}
