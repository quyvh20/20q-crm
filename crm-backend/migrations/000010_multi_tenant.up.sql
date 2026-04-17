ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255) DEFAULT '';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS type VARCHAR(50) DEFAULT 'company';

CREATE TABLE IF NOT EXISTS org_users (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    role VARCHAR(50) NOT NULL DEFAULT 'viewer',
    status VARCHAR(50) NOT NULL DEFAULT 'active',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, org_id)
);

CREATE INDEX IF NOT EXISTS idx_org_users_org_id ON org_users(org_id);
CREATE INDEX IF NOT EXISTS idx_org_users_user_id ON org_users(user_id);

INSERT INTO org_users (user_id, org_id, role, status, joined_at)
SELECT id, org_id, role::text, 'active', created_at
FROM users
WHERE org_id IS NOT NULL
ON CONFLICT (user_id, org_id) DO NOTHING;

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_contacts_owner ON contacts(owner_user_id);
