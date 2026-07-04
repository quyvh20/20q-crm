-- Migration 000031: User Groups (report-sharing prerequisite)
-- ============================================================
-- Named groups of org members. Built as a general entity (reusable for record
-- sharing later) but first wired to granular report sharing. A group is
-- org-scoped; membership is a simple join table.
--
-- NOTE: golang-migrate is dead on prod, so cmd/server/main.go carries an
-- idempotent boot guard mirroring this file exactly. Keep both in sync.

CREATE TABLE IF NOT EXISTS user_groups (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name        VARCHAR(120) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uix_user_groups_org_name
    ON user_groups(org_id, lower(name)) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS user_group_members (
    group_id   UUID NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_user_group_members_user ON user_group_members(user_id);

ALTER TABLE user_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_group_members ENABLE ROW LEVEL SECURITY;
