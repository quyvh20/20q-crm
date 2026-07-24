-- M6 Email marketing: authored campaign content (block document + compiled email-safe
-- HTML). Mirrored by an idempotent boot guard in cmd/server/main.go — prod runs the
-- guard (golang-migrate is dead at v2 there); a fresh install and the Docker harness
-- run this file. Both must agree. body_json = the block/document edit source;
-- body_html_compiled = the mjml-compiled send source (still carrying {{merge}} tokens
-- resolved per recipient at send). A NEW table, never an ALTER of
-- automation_email_templates. Soft-deletable; every non-zero-default column has a DDL
-- DEFAULT (GORM omits zero values on insert).
CREATE TABLE IF NOT EXISTS marketing_campaign_content (
	id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	org_id              UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	name                VARCHAR(160) NOT NULL,
	subject             VARCHAR(998) NOT NULL DEFAULT '',
	preheader           VARCHAR(255) NOT NULL DEFAULT '',
	body_json           JSONB NOT NULL DEFAULT '{"blocks":[]}',
	body_html_compiled  TEXT NOT NULL DEFAULT '',
	plain_text          TEXT NOT NULL DEFAULT '',
	merge_scope         JSONB NOT NULL DEFAULT '["contact","org","campaign"]',
	compiled_size_bytes INT NOT NULL DEFAULT 0,
	compiled_at         TIMESTAMPTZ,
	created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
	created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	deleted_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_marketing_campaign_content_org
	ON marketing_campaign_content(org_id) WHERE deleted_at IS NULL;

-- RLS on, matching every org-scoped table this app adds. Never FORCE.
ALTER TABLE marketing_campaign_content ENABLE ROW LEVEL SECURITY;
