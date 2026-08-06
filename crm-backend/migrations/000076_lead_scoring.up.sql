-- Lead scoring (R9.4): org-defined rules over contact fields and marketing
-- engagement, summed into a 0-100 contacts.lead_score.
--
-- Prod runs the same statements as boot guards in cmd/server/main.go —
-- golang-migrate is dirty at v2 there and start.sh runs `migrate up || true`, so
-- this file is the local/dev/E2E mirror only. The two must agree statement for
-- statement or CI tests a different schema than production runs.
CREATE TABLE IF NOT EXISTS lead_scoring_rules (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name       VARCHAR(120) NOT NULL,
    kind       VARCHAR(16) NOT NULL DEFAULT 'field',
    points     INT NOT NULL DEFAULT 0,
    condition  JSONB NOT NULL DEFAULT '{}',
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_lead_scoring_rules_org
    ON lead_scoring_rules(org_id) WHERE deleted_at IS NULL;
ALTER TABLE lead_scoring_rules ENABLE ROW LEVEL SECURITY;

-- NOT NULL DEFAULT 0: safe mid-rolling-deploy, and a NOT NULL sort column needs
-- neither the nullable nor the nullAs arm of the keyset machinery.
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS lead_score INT NOT NULL DEFAULT 0;
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS lead_score_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_contacts_org_lead_score
    ON contacts(org_id, lead_score, id) WHERE deleted_at IS NULL;

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS lead_score_run_at TIMESTAMPTZ;

-- Engagement rules join the event ledger on the normalized email — the only
-- durable bridge to a contact, since the campaign roster that carries a real
-- contact_id is hard-deleted 30 days after a campaign finishes.
CREATE INDEX IF NOT EXISTS idx_marketing_email_events_recipient
    ON marketing_email_events(org_id, email_normalized, event_type, occurred_at);
