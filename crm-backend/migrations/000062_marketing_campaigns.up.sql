-- M7: bulk send engine — campaigns + the recipient roster (the sole durable
-- authority for send state, dedup, progress, resume, pause). Dev/Docker/fresh-
-- install mirror of the cmd/server/main.go boot guard (prod runs the boot guard —
-- golang-migrate is dead). Every non-zero-default column carries a DDL DEFAULT.
CREATE TABLE IF NOT EXISTS marketing_campaigns (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id              UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name                VARCHAR(200) NOT NULL,
    content_id          UUID REFERENCES marketing_campaign_content(id) ON DELETE SET NULL,
    segment_ids         JSONB NOT NULL DEFAULT '[]',
    exclude_segment_ids JSONB NOT NULL DEFAULT '[]',
    sending_domain_id   UUID REFERENCES org_email_domains(id) ON DELETE SET NULL,
    topic_id            UUID REFERENCES marketing_topics(id) ON DELETE SET NULL,
    status              VARCHAR(16) NOT NULL DEFAULT 'draft',
    send_lane           VARCHAR(16) NOT NULL DEFAULT 'single',
    scheduled_at        TIMESTAMPTZ,
    recipient_lock_mode VARCHAR(24) NOT NULL DEFAULT 'lock_on_schedule',
    snapshot_counts     JSONB NOT NULL DEFAULT '{}',
    feedback_id         VARCHAR(128),
    created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    deleted_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_marketing_campaigns_org
    ON marketing_campaigns(org_id) WHERE deleted_at IS NULL;

-- The recipient roster. Surrogate PK (an imported email may have no contact row),
-- with the REAL cross-segment/cross-import dedupe as UNIQUE(campaign_id,
-- email_normalized) — works whether or not contact_id is set, and NULL-safe (unlike
-- the invalid PK(campaign_id, contact_id) the first draft proposed).
CREATE TABLE IF NOT EXISTS marketing_campaign_recipients (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    campaign_id         UUID NOT NULL REFERENCES marketing_campaigns(id) ON DELETE CASCADE,
    org_id              UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_id          UUID REFERENCES contacts(id) ON DELETE SET NULL,
    email_normalized    VARCHAR(320) NOT NULL,
    variant             VARCHAR(32),
    status              VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempts            INT NOT NULL DEFAULT 0,
    next_attempt_at     TIMESTAMPTZ,
    scheduled_for       TIMESTAMPTZ,
    locked_at           TIMESTAMPTZ,
    provider_message_id VARCHAR(128),
    idempotency_key     VARCHAR(160),
    error               TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at        TIMESTAMPTZ
);

-- Cross-segment / cross-import dedupe (the structural guarantee against double-send
-- across overlapping audiences). NOTE: created NON-concurrently here for fresh
-- installs; prod uses the boot-guard probe ritual (the table is new, so it is empty
-- and the UNIQUE can never fail on existing data).
CREATE UNIQUE INDEX IF NOT EXISTS uix_campaign_recipients_campaign_email
    ON marketing_campaign_recipients(campaign_id, email_normalized);

-- The send-lane claim loop polls pending rows due to send, per campaign.
CREATE INDEX IF NOT EXISTS idx_campaign_recipients_claim
    ON marketing_campaign_recipients(campaign_id, status, scheduled_for);

ALTER TABLE marketing_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE marketing_campaign_recipients ENABLE ROW LEVEL SECURITY;
