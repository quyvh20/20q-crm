-- Records WHO declared a contact's lawful basis for marketing. Until now nothing in
-- the codebase wrote a positive basis at all, so every marketing send suppressed
-- 100% of its recipients with "no_lawful_basis"; the admin bulk-grant fills that
-- gap, and a grant that cannot say who made it is not auditable.
--
-- Nullable on purpose: no DDL DEFAULT is needed, and the GDPR erasure collapse
-- (RedactMarketingStateForEmail) nulls it alongside the other provenance columns.
--
-- Prod runs the same statement as a boot guard in cmd/server/main.go — golang-migrate
-- is dead there, so this file is the local/dev mirror only.
ALTER TABLE contact_marketing_state
    ADD COLUMN IF NOT EXISTS consent_granted_by UUID REFERENCES users(id) ON DELETE SET NULL;
