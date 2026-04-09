-- Migration 000003: Contact Enhancements
-- Add owner_user_id, full-text search index, unique email per org

ALTER TABLE contacts ADD COLUMN owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX idx_contacts_owner ON contacts(owner_user_id);

-- Full-text search index for contact name + email
CREATE INDEX idx_contacts_fulltext ON contacts USING GIN (
    to_tsvector('simple', first_name || ' ' || last_name || ' ' || COALESCE(email, ''))
);

-- Unique email per org (partial index: only where email is not null and not deleted)
CREATE UNIQUE INDEX idx_contacts_org_email ON contacts(org_id, email)
    WHERE email IS NOT NULL AND deleted_at IS NULL;
