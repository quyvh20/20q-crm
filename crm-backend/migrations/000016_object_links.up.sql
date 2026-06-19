-- Migration 000016: Universal relationships (object_links, P4)
-- ============================================================
-- One polymorphic edge table lets ANY record relate to ANY record — system or
-- custom — mirroring the (record_type, record_id) shape record_shares already
-- uses. System-object FKs (deal→contact, contact→company) STAY as real columns
-- for integrity and speed; object_links is purely additive for everything the
-- rigid FKs can't express (custom↔custom, custom↔company, many-to-many) and for
-- tags on non-contact objects (D4).
--
-- No DB-level FK on from_id/to_id (the endpoints are polymorphic). Referential
-- integrity is app-enforced: RecordService.Delete cascade-soft-deletes every link
-- touching a deleted record (plan R3). Tenant isolation stays app-enforced
-- (WHERE org_id = ?); RLS is enabled to match 000008/000013.

CREATE TABLE IF NOT EXISTS object_links (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    from_slug    VARCHAR(100) NOT NULL,
    from_id      UUID NOT NULL,
    to_slug      VARCHAR(100) NOT NULL,
    to_id        UUID NOT NULL,
    relation_key VARCHAR(100) NOT NULL,    -- e.g. 'account', 'tags'
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);

-- Traversal is fast in BOTH directions (a record's outgoing and incoming edges).
CREATE INDEX IF NOT EXISTS idx_object_links_from
    ON object_links(org_id, from_slug, from_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_object_links_to
    ON object_links(org_id, to_slug, to_id)     WHERE deleted_at IS NULL;

-- One active edge per (from, relation_key, to). Soft-deletes drop out of the
-- index, so re-linking after an unlink is allowed.
CREATE UNIQUE INDEX IF NOT EXISTS uix_object_links_unique
    ON object_links(org_id, from_slug, from_id, relation_key, to_slug, to_id)
    WHERE deleted_at IS NULL;

ALTER TABLE object_links ENABLE ROW LEVEL SECURITY;
