-- M2 Email marketing: per-org verified sending domain (plan email_marketing_plan.md).
-- Mirrored by an idempotent boot guard in cmd/server/main.go — prod runs the guard
-- (golang-migrate is dead at v2 there); a fresh install and the Docker harness run
-- this file. Both must agree. Mirrors the Resend domain object plus the DMARC state
-- we check ourselves (Resend manages only SPF + DKIM). Nullable provenance/verify
-- columns; every non-zero-default column carries a DDL DEFAULT (GORM omits zero
-- values on insert). Soft-deletable (removing a domain).
CREATE TABLE IF NOT EXISTS org_email_domains (
	id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	org_id             UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	domain             VARCHAR(255) NOT NULL,
	resend_domain_id   VARCHAR(64) NOT NULL DEFAULT '',
	region             VARCHAR(32) NOT NULL DEFAULT '',
	send_subdomain     VARCHAR(63) NOT NULL DEFAULT 'send',
	tracking_subdomain VARCHAR(63),
	return_path        VARCHAR(320) NOT NULL DEFAULT '',
	status             VARCHAR(24) NOT NULL DEFAULT 'not_started',
	spf_verified       BOOLEAN NOT NULL DEFAULT false,
	dkim_verified      BOOLEAN NOT NULL DEFAULT false,
	dmarc_policy       VARCHAR(16),
	dns_records        JSONB NOT NULL DEFAULT '[]',
	verified_at        TIMESTAMPTZ,
	last_checked_at    TIMESTAMPTZ,
	warmup_daily_cap   INT,
	created_by         UUID REFERENCES users(id) ON DELETE SET NULL,
	created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	deleted_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_org_email_domains_org
	ON org_email_domains(org_id) WHERE deleted_at IS NULL;

-- One live row per DOMAIN STRING across the whole table (GLOBAL, not per-org):
-- Resend domains are team-global, so a domain is owned by exactly one org. This is
-- the race-free backstop for the app-level ownership check. Partial so a removed
-- domain can be re-added. Prod builds it through the probe-and-refuse ritual; a
-- fresh install is empty, so a direct create is safe here.
CREATE UNIQUE INDEX IF NOT EXISTS uix_org_email_domains_domain
	ON org_email_domains(domain) WHERE deleted_at IS NULL;

-- RLS on, matching every org-scoped table this app adds. Never FORCE (owner
-- bypasses zero-policy RLS; FORCE with no policy locks the app out).
ALTER TABLE org_email_domains ENABLE ROW LEVEL SECURITY;
