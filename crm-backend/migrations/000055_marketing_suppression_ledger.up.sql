-- M1 Email marketing foundation: suppression & consent ledger (plan email_marketing_plan.md).
-- Mirrored by an idempotent boot guard in cmd/server/main.go — prod runs the guard
-- (golang-migrate is dead at v2 there); a fresh install and the Docker harness run
-- this file. Both must agree. Enums are VARCHAR + app-level validation (the repo
-- uses no PG ENUM type — CREATE TYPE has no IF NOT EXISTS, which the boot-guard
-- regime needs). Every non-zero-default column carries a DDL DEFAULT because GORM
-- omits zero-values on insert. Both tables are org-scoped and RLS-enabled below.

-- One do-not-mail entry, keyed on a NORMALIZED email (never contact_id — several
-- contacts can share one normalized address). Exempt from RedactForRecord, so an
-- opt-out survives contact deletion / GDPR erasure / CSV re-import.
CREATE TABLE IF NOT EXISTS marketing_suppressions (
	id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	org_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	email_normalized  VARCHAR(320) NOT NULL,
	reason            VARCHAR(32) NOT NULL,
	scope             VARCHAR(16) NOT NULL DEFAULT 'marketing',
	topic_id          UUID,
	source            VARCHAR(64) NOT NULL DEFAULT '',
	soft_bounce_count INT NOT NULL DEFAULT 0,
	created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_marketing_suppressions_org_email
	ON marketing_suppressions(org_id, email_normalized);

-- Dedupe key. COALESCE(topic_id, zero) is load-bearing: Postgres treats NULL as
-- DISTINCT in a unique index, so a plain UNIQUE(org_id, email, reason, topic_id)
-- would let every topic-less unsubscribe/bounce/complaint accumulate duplicates.
-- Prod builds this through the probe-and-refuse ritual; a fresh install is empty,
-- so a direct create is safe here.
CREATE UNIQUE INDEX IF NOT EXISTS uix_marketing_suppressions_dedupe
	ON marketing_suppressions(org_id, email_normalized, reason, COALESCE(topic_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- Per-email consent/lifecycle. The provenance columns are deliberately nullable:
-- the GDPR-erasure collapse nulls them while keeping email_normalized +
-- marketing_status so an opt-out keeps being honored after erasure.
CREATE TABLE IF NOT EXISTS contact_marketing_state (
	id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	email_normalized VARCHAR(320) NOT NULL,
	contact_id       UUID REFERENCES contacts(id) ON DELETE SET NULL,
	marketing_status VARCHAR(16) NOT NULL DEFAULT 'pending',
	consent_basis    VARCHAR(40),
	consent_source   VARCHAR(64),
	consent_at       TIMESTAMPTZ,
	consent_ip       VARCHAR(64),
	region           VARCHAR(16),
	casl_expires_at  TIMESTAMPTZ,
	double_opt_in_at TIMESTAMPTZ,
	created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE (org_id, email_normalized)
);

-- RLS on, matching every other org-scoped table this app adds. Never FORCE (owner
-- bypasses zero-policy RLS; FORCE with no policy locks the app out).
ALTER TABLE marketing_suppressions ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_marketing_state ENABLE ROW LEVEL SECURITY;
