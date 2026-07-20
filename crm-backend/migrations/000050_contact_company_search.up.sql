-- Migration 000050: Contact search matches the related company's name
--
-- contactRepository.List ORs a company-name subquery into contact search. This is
-- the GIN index that subquery probes.
--
-- NOTE: migrations have not run on prod since v2, so this file is effectively
-- local-dev/documentation only. The statement that actually reaches production is
-- the matching IF NOT EXISTS boot guard in cmd/server/main.go — the two must stay
-- in step, and the expression must stay character-identical to the one in
-- contactRepository.List or the planner silently stops using the index.

-- companies.name is NOT NULL, so no COALESCE here or in the query.
CREATE INDEX IF NOT EXISTS idx_companies_fulltext ON companies USING GIN (
    to_tsvector('simple', name)
);

-- Not new — idx_contacts_fulltext ships in migrations/000003, which has never run
-- on prod (same reason its sibling idx_contacts_org_email had to be promoted to a
-- boot guard). Restated here so a fresh database built from migrations and a prod
-- database repaired by the boot guards end up with the same set of indexes.
CREATE INDEX IF NOT EXISTS idx_contacts_fulltext ON contacts USING GIN (
    to_tsvector('simple', first_name || ' ' || last_name || ' ' || COALESCE(email, ''))
);
