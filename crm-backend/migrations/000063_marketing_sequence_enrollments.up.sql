-- M8: drip sequences — the enrollment-job tracking table. A "sequence" is an
-- automation workflow (delay + channel=marketing send steps); one row here says
-- "feed segment X into sequence Y", and the feeder drains it in bounded,
-- cursor-paginated batches at depth 0 (never the 100-capped enroll_records action),
-- idempotent per (sequence, contact). It is a TRACKING row, not a send roster — the
-- enrolled runs live in automation_workflow_runs, and marketing opt-out is enforced
-- LIVE inside each send step, not here. Dev/Docker/fresh-install mirror of the
-- cmd/server/main.go boot guard (prod runs the boot guard — golang-migrate is dead).
CREATE TABLE IF NOT EXISTS marketing_sequence_enrollments (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sequence_workflow_id UUID NOT NULL,
    segment_id           UUID NOT NULL,
    feeder_cursor        UUID,
    status               VARCHAR(20) NOT NULL DEFAULT 'active',
    enrolled_count       INT NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    created_by           UUID,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One ACTIVE enrollment per (sequence, segment); completed/canceled don't block a
-- re-enroll. New empty table → plain UNIQUE (prod's boot guard skips the probe ritual
-- for the same reason).
CREATE UNIQUE INDEX IF NOT EXISTS uix_seq_enroll_active
    ON marketing_sequence_enrollments(org_id, sequence_workflow_id, segment_id) WHERE status = 'active';

-- The feeder polls active enrollments oldest-touched first.
CREATE INDEX IF NOT EXISTS idx_seq_enroll_active
    ON marketing_sequence_enrollments(status, updated_at) WHERE status = 'active';

ALTER TABLE marketing_sequence_enrollments ENABLE ROW LEVEL SECURITY;
