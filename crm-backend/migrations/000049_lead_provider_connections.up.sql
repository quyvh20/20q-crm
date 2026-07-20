-- L5.1: provider connector framework (plan lead_integration_plan.md).
--
-- NOTE: prod never runs this file — golang-migrate is dirty at v2 there, so the
-- boot guards in cmd/server/main.go are the only path that executes on prod.
-- This file keeps a FRESH install and the Docker test harness converged with it.
-- Both must agree; the harness applies this file by name, so adding it here
-- without adding it to newIntegrationsTestDB's list leaves every new test
-- running against a schema that lacks these tables.

-- One row per OAuth'd provider account (a Facebook page, a TikTok advertiser).
CREATE TABLE IF NOT EXISTS integration_connections (
	id                     UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	org_id                 UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	provider               VARCHAR(32) NOT NULL,
	external_account_id    VARCHAR(255) NOT NULL,
	external_account_label VARCHAR(255) NOT NULL DEFAULT '',

	-- Envelope-sealed provider credentials; see internal/integrations/envelope.
	-- key_version MIRRORS the version inside the blob for ops queries ("which
	-- rows are still on key 1"). It is never read back to CHOOSE a key: the
	-- version inside the authenticated blob is the authority, so editing this
	-- column cannot cause a decrypt under the wrong key.
	encrypted_credentials  TEXT NOT NULL,
	key_version            INT NOT NULL DEFAULT 0,
	webhook_secret_encrypted TEXT,

	status                 VARCHAR(32) NOT NULL DEFAULT 'connected',
	cursor                 JSONB NOT NULL DEFAULT '{}'::jsonb,
	config                 JSONB NOT NULL DEFAULT '{}'::jsonb,
	last_synced_at         TIMESTAMPTZ,
	last_error             TEXT,
	consecutive_failures   INT NOT NULL DEFAULT 0,
	created_by             UUID,
	created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	-- Soft delete, matching lead_sources: hard-deleting would orphan the event
	-- ledger rows that reference this connection.
	deleted_at             TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_integration_connections_org
	ON integration_connections(org_id) WHERE deleted_at IS NULL;

-- One live connection per (org, provider, account). Partial on deleted_at so a
-- disconnect-then-reconnect by the same workspace is an ordinary insert rather
-- than a constraint violation.
CREATE UNIQUE INDEX IF NOT EXISTS uix_integration_connections_org_account
	ON integration_connections(org_id, provider, external_account_id)
	WHERE deleted_at IS NULL;

-- The exclusive CLAIM. An app-level provider webhook carries only the external
-- account id, so page -> workspace routing has to be unambiguous; this is a
-- product choice we own, not a platform constraint (Meta will happily feed the
-- same page to several CRMs).
--
-- Statuses that hold the claim are the ones that can still legitimately receive
-- deliveries. 'disconnected' and 'revoked' release it, so a page can be moved
-- between workspaces without support intervention.
--
-- deleted_at IS NULL is part of the predicate for a reason worth stating: a
-- soft-deleted WORKSPACE does not cascade to this table (usecase's
-- DeleteWorkspace touches no integrations table and org soft-delete is an UPDATE,
-- so the org_id ON DELETE CASCADE never fires), so without a release path a
-- deleted workspace would hold a customer's page hostage and the error would name
-- a workspace nobody can sign into. The predicate makes releasing the claim a
-- one-row status/delete write — but the WORKSPACE-deletion release hook itself is
-- NOT yet wired: it is scheduled for L6's lifecycle cascade (disable the org's
-- connections + best-effort Provider.Disconnect + destroy the sealed credentials).
-- Until L6 lands, an admin's explicit Disconnect is the only release path, so a
-- deleted-workspace claim is a known gap. (Reachable only once a provider adapter
-- ships in L5.2; the L5.1 registry is empty.)
CREATE UNIQUE INDEX IF NOT EXISTS uix_integration_connections_claim
	ON integration_connections(provider, external_account_id)
	WHERE deleted_at IS NULL AND status IN ('connected', 'degraded', 'error');

-- Server-side, single-use OAuth state. The state parameter itself is an opaque
-- random string: org and user are resolved from THIS row, never decoded out of
-- the parameter, which is the capture-vulnerability class the U4 review killed.
CREATE TABLE IF NOT EXISTS integration_oauth_states (
	id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	state_hash    VARCHAR(64) NOT NULL UNIQUE,
	org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	user_id       UUID NOT NULL,
	provider      VARCHAR(32) NOT NULL,
	return_to     TEXT NOT NULL DEFAULT '',
	-- PKCE verifier, envelope-sealed under this row's id. Short-lived, but it is
	-- still a secret that upgrades a stolen authorization code into a token.
	code_verifier TEXT,
	key_version   INT NOT NULL DEFAULT 0,
	expires_at    TIMESTAMPTZ NOT NULL,
	consumed_at   TIMESTAMPTZ,
	created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_integration_oauth_states_expiry
	ON integration_oauth_states(expires_at) WHERE consumed_at IS NULL;

-- Custody for an exchanged token between the OAuth callback and the admin
-- choosing which account to connect.
--
-- This table exists so the provider token never rides through the browser: the
-- callback stores it here, the frontend receives only the candidate list plus a
-- single-use selection token, and the select-account POST proves the caller is
-- the same org AND the same user before anything is promoted to a connection.
CREATE TABLE IF NOT EXISTS integration_pending_connections (
	id                   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
	org_id               UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	user_id              UUID NOT NULL,
	provider             VARCHAR(32) NOT NULL,
	encrypted_token      TEXT NOT NULL,
	key_version          INT NOT NULL DEFAULT 0,
	candidate_accounts   JSONB NOT NULL DEFAULT '[]'::jsonb,
	selection_token_hash VARCHAR(64) NOT NULL UNIQUE,
	expires_at           TIMESTAMPTZ NOT NULL,
	consumed_at          TIMESTAMPTZ,
	created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_integration_pending_expiry
	ON integration_pending_connections(expires_at) WHERE consumed_at IS NULL;

-- Which connection a provider-backed source belongs to (a Facebook form belongs
-- to a page). No FK, matching the stance integration_events.connection_id
-- already takes: the boot-guard loop logs a failed DDL and boots anyway, so an
-- FK whose target table failed to create would be a second failure mode with no
-- upside — the join is always org-scoped in Go regardless.
--
-- This column is deliberately NOT mapped on the LeadSource GORM struct. Two
-- independent traps, both already paid for by owner_pool and consent:
-- UpdateSource is a db.Save from a page-load struct, and FindSourceByTokenHash
-- selects lead_sources.*, so a mapped column whose ALTER failed would 500 every
-- capture request in every org rather than breaking one kind's route.
ALTER TABLE lead_sources ADD COLUMN IF NOT EXISTS connection_id UUID;

CREATE INDEX IF NOT EXISTS idx_lead_sources_connection
	ON lead_sources(connection_id) WHERE connection_id IS NOT NULL AND deleted_at IS NULL;

-- One facebook_form source per (connection, provider form id). The enable-form path
-- is idempotent in Go, but that check-then-insert is a TOCTOU under concurrent
-- enables (two tabs, two admins, a retried request); this expression index is the
-- backstop that turns the race into a caught conflict the handler resolves to the
-- existing source. Partial on deleted_at so disable-then-re-enable is an ordinary
-- insert, matching the connection claim index.
CREATE UNIQUE INDEX IF NOT EXISTS uix_lead_sources_conn_form
	ON lead_sources (connection_id, (config->'facebook'->>'form_id'))
	WHERE kind = 'facebook_form' AND deleted_at IS NULL;

-- RLS on, matching lead_sources/integration_events (migration 000043) and every
-- other table this app adds. Defence in depth — the app enforces org scoping in
-- SQL and connects as the table owner (which bypasses RLS with no policies) — but
-- these three are the most sensitive tables the platform has (sealed provider
-- credentials, PKCE verifiers, exchanged tokens), so they must not be the ones
-- that silently opt out.
ALTER TABLE integration_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_oauth_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_pending_connections ENABLE ROW LEVEL SECURITY;
