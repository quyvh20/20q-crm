-- L2 owner routing: round-robin lead assignment across a pool of reps.
--
-- Fresh installs only. On prod this file does NOT run (golang-migrate is dirty at
-- v2), so the authoritative copy is the boot guard in cmd/server/main.go — the two
-- are twins and must agree.
--
-- ADD COLUMN with a non-volatile DEFAULT is metadata-only on PG11+: no table
-- rewrite, so none of the lock cost the contacts indexes in the same guard block
-- carry.

-- The ORDERED rotation list. Order is meaningful and admin-visible: reordering
-- changes who is next. An empty array means rotation is off — and via the WHERE
-- predicate on the cursor bump, means zero writes on the capture path for every
-- source that never enables this.
ALTER TABLE lead_sources
    ADD COLUMN IF NOT EXISTS owner_pool JSONB NOT NULL DEFAULT '[]'::jsonb;

-- A MONOTONIC TICKET COUNTER, never an index into the array.
--
-- An index is undefined the moment the pool shrinks, and every implementation then
-- clamps — the clamp is where the skew hides (index 2 saved, pool shrinks to 2,
-- 2%2=0, and the first member takes two turns every cycle forever). A counter is
-- well-defined against any pool size at any moment.
--
-- BIGINT, not INT: a wrapped negative ticket makes `ticket % n` negative and panics
-- on the slice index, and a panic on the capture path is silent lead loss.
ALTER TABLE lead_sources
    ADD COLUMN IF NOT EXISTS owner_cursor BIGINT NOT NULL DEFAULT 0;

-- lead_sources is now written up to twice per lead (cursor bump, then
-- TouchSourceUsed). HOT updates keep the token_hash UNIQUE index — probed on EVERY
-- capture request — free of new entries only while the heap page has room, and the
-- default fillfactor of 100 leaves none.
ALTER TABLE lead_sources SET (fillfactor = 90);

-- Binds the rotation ticket to the DELIVERY rather than to the attempt.
--
-- Ingest deliberately re-runs the pipeline against a prior `failed` row on an
-- Idempotency-Key retry. Without this, a failure-correlated retry pattern takes a
-- second ticket every time: pool [A,B] where every other lead fails gives A ticket 0
-- (fails) → retry ticket 1 (succeeds, B) → A ticket 2 (fails) → retry ticket 3 (B).
-- B receives 100% of created contacts, A none, and the ledger looks green.
--
-- It is also the only place the ledger can answer "which rep did my leads go to",
-- which is the question this feature invites.
ALTER TABLE integration_events
    ADD COLUMN IF NOT EXISTS assigned_owner_id UUID;

-- No GIN index on owner_pool: the offboarding sweep is a rare admin event over
-- dozens of rows per org, and an index would be write cost on every source edit to
-- serve a query that runs when someone quits.
