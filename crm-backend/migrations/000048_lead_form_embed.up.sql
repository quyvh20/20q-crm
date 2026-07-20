-- L4: web-to-lead form embeds (plan lead_integration_plan.md).
--
-- NOTE: prod never runs this file — golang-migrate is dirty at v2 there, so the
-- boot guards in cmd/server/main.go are the only path that executes on prod.
-- This file keeps a FRESH install and the Docker test harness converged with it.

-- The browser origins a form_embed source accepts submissions from.
--
-- Deliberately its own column rather than a key in `config`: the config parser is
-- documented never to fail (missing/junk/null all mean "feature off"), which is
-- right for a naming template and wrong for an allowlist. A security list needs
-- "could not read" to be distinguishable from "empty", because those two must have
-- OPPOSITE outcomes — empty denies every browser origin, unreadable refuses the
-- request outright. No DEFAULT, so NULL (guard never ran) differs from '[]'.
ALTER TABLE lead_sources ADD COLUMN IF NOT EXISTS allowed_origins JSONB;

-- The PRIVATE half of a Cloudflare Turnstile pair. Sent verbatim to siteverify so
-- it cannot be hashed; never returned by any endpoint.
ALTER TABLE lead_sources ADD COLUMN IF NOT EXISTS turnstile_secret TEXT;
