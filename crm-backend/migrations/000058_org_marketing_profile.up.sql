-- M3: per-org marketing sender profile (CAN-SPAM postal address + from identity +
-- the M4 marketing_paused breaker target). Dev/Docker/fresh-install mirror of the
-- cmd/server/main.go boot guard (prod runs the boot guard — golang-migrate is dead).
-- One row per org (org_id PK). marketing_paused carries a DDL DEFAULT because GORM
-- omits zero-values on insert.
CREATE TABLE IF NOT EXISTS org_marketing_profile (
    org_id                  UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    from_name               VARCHAR(160) NOT NULL DEFAULT '',
    reply_to                VARCHAR(320) NOT NULL DEFAULT '',
    physical_postal_address TEXT NOT NULL DEFAULT '',
    marketing_paused        BOOLEAN NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE org_marketing_profile ENABLE ROW LEVEL SECURITY;
