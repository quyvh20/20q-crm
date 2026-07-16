-- Lead integration platform (L1.1): inbound capture sources + the delivery ledger.
--
-- NOTE: on prod this file does NOT run (golang-migrate is dirty at v2 and start.sh
-- swallows the failure). The authoritative copy for prod is the idempotent boot
-- guard block in cmd/server/main.go. Both must agree; this file is what a fresh
-- install gets.

CREATE TABLE IF NOT EXISTS lead_sources (
    id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    kind                 VARCHAR(32) NOT NULL,
    name                 VARCHAR(160) NOT NULL,
    token_hash           VARCHAR(64) UNIQUE,
    token_prefix         VARCHAR(24),
    target_slug          VARCHAR(64) NOT NULL DEFAULT 'contact',
    match_fields         JSONB NOT NULL DEFAULT '["email"]',
    field_map            JSONB NOT NULL DEFAULT '{}',
    update_policy        VARCHAR(24) NOT NULL DEFAULT 'fill_blank_only',
    default_owner_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    config               JSONB NOT NULL DEFAULT '{}',
    status               VARCHAR(16) NOT NULL DEFAULT 'active',
    consecutive_failures INT NOT NULL DEFAULT 0,
    last_used_at         TIMESTAMPTZ,
    daily_cap            INT NOT NULL DEFAULT 0,
    -- Nullable + ON DELETE SET NULL: a source must outlive the admin who made it.
    -- A lead pipe is org infrastructure; killing it when someone leaves is the
    -- credential-dies-with-membership failure this platform exists to avoid.
    created_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ,
    disabled_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_lead_sources_org ON lead_sources(org_id) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS integration_events (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source_id          UUID REFERENCES lead_sources(id) ON DELETE SET NULL,
    -- connection_id has no FK yet: integration_connections arrives in L5. The
    -- column exists now because its dedupe index must — a partial unique index
    -- cannot be retrofitted onto rows that predate it without a backfill.
    connection_id      UUID,
    provider_event_id  TEXT,
    status             VARCHAR(16) NOT NULL,
    claimed_at         TIMESTAMPTZ,
    attempts           INT NOT NULL DEFAULT 0,
    raw_payload        JSONB NOT NULL DEFAULT '{}',
    context            JSONB NOT NULL DEFAULT '{}',
    quarantined_fields JSONB NOT NULL DEFAULT '{}',
    result_slug        VARCHAR(64),
    result_record_id   UUID,
    outcome            VARCHAR(16),
    error              TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at       TIMESTAMPTZ
);

-- TWO dedupe indexes, not one. Postgres treats NULLs as DISTINCT, so a single
-- (source_id, provider_event_id) index never fires for a provider webhook — which
-- resolves a connection but not yet a source — and every retry would create a
-- duplicate contact on the highest-volume channel.
CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_events_source_provider
    ON integration_events(source_id, provider_event_id)
    WHERE source_id IS NOT NULL AND provider_event_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_events_conn_provider
    ON integration_events(connection_id, provider_event_id)
    WHERE connection_id IS NOT NULL AND provider_event_id IS NOT NULL;

-- The async worker's poll (L5) and the daily-cap count.
CREATE INDEX IF NOT EXISTS idx_integration_events_pending
    ON integration_events(created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_integration_events_source_created
    ON integration_events(source_id, created_at);
CREATE INDEX IF NOT EXISTS idx_integration_events_org_created
    ON integration_events(org_id, created_at DESC);

-- The dedupe match index. NON-unique deliberately: the existing unique index is on
-- raw (org_id, email) and is case-SENSITIVE, so case-variant duplicates are legal
-- today and almost certainly exist. A UNIQUE index here would fail to build on any
-- such org -- and since boot guards swallow their errors, it would fail SILENTLY,
-- leaving prod with no index at all while local tests pass.
CREATE INDEX IF NOT EXISTS idx_contacts_org_lower_email
    ON contacts(org_id, LOWER(email)) WHERE deleted_at IS NULL;
