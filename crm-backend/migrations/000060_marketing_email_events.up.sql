-- M4: Resend delivery-webhook event ledger (owner-less, org-level) — durable ledger +
-- svix dedupe row + async work queue. Dev/Docker/fresh-install mirror of the
-- cmd/server/main.go boot guard (prod runs the boot guard — golang-migrate is dead).
-- UNIQUE(org_id, svix_id) makes ingestion idempotent. Non-zero-default columns carry
-- DDL defaults (GORM omits zero-values on insert).
CREATE TABLE IF NOT EXISTS marketing_email_events (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    svix_id          VARCHAR(80) NOT NULL,
    event_type       VARCHAR(48) NOT NULL,
    email_normalized VARCHAR(320) NOT NULL DEFAULT '',
    from_domain      VARCHAR(255) NOT NULL DEFAULT '',
    reason           VARCHAR(32) NOT NULL DEFAULT '',
    bounce_type      VARCHAR(24) NOT NULL DEFAULT '',
    channel          VARCHAR(16) NOT NULL DEFAULT '',
    campaign_id      UUID,
    status           VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts         INT NOT NULL DEFAULT 0,
    claimed_at       TIMESTAMPTZ,
    raw_payload      JSONB NOT NULL DEFAULT '{}',
    error            TEXT NOT NULL DEFAULT '',
    occurred_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at     TIMESTAMPTZ,
    UNIQUE (org_id, svix_id)
);

CREATE INDEX IF NOT EXISTS idx_marketing_email_events_pending
    ON marketing_email_events(created_at) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_marketing_email_events_org_created
    ON marketing_email_events(org_id, created_at);

-- Rolling-window soft-bounce accumulation needs a last-seen timestamp on the (M1)
-- suppressions table.
ALTER TABLE marketing_suppressions ADD COLUMN IF NOT EXISTS last_soft_bounce_at TIMESTAMPTZ;

ALTER TABLE marketing_email_events ENABLE ROW LEVEL SECURITY;
