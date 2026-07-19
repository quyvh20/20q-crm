-- L2 batch capture: per-source control over whether batch deliveries trigger
-- workflows.
--
-- Fresh installs only; prod gets this through the boot guard in cmd/server/main.go
-- (golang-migrate is dirty at v2 there). The two are twins and must agree.

-- Positive polarity, defaulting FALSE, so the absent value is the SAFE value: a
-- batch of 100 recovered leads does not enrol 100 contacts into every
-- contact_created workflow unless an admin has said so explicitly.
--
-- A per-source column rather than a wire flag, deliberately. A caller-settable
-- suppression flag is the same sabotage vector the test-lead flow had to answer:
-- a leaked key could file 100 REAL leads that land correctly owned and attributed,
-- look ordinary in the ledger, and page nobody — silent lead loss the victim's own
-- UI conceals. A column is auditable and visible on the source page.
ALTER TABLE lead_sources
    ADD COLUMN IF NOT EXISTS batch_enroll_automation BOOLEAN NOT NULL DEFAULT FALSE;
