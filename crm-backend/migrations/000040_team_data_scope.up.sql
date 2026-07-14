-- Migration 000040: team data scope (U6.1)
-- ============================================================
-- roles.data_scope gains 'team' — own records plus records owned by anyone who
-- shares a group with the user. The missing middle: before this, a manager who
-- should see their reports' pipeline had to be handed the entire workspace.
--
-- The CHECK constraint must be DROPPED and re-ADDED. The boot guard that created
-- it is wrapped in `IF NOT EXISTS (SELECT 1 FROM pg_constraint ...)`, so simply
-- re-running an ADD would find the constraint present and skip — leaving
-- IN ('own','all') in force and making every team-scope write fail at the DB
-- layer, in production only.
--
-- KEEP IN SYNC with the boot guard in cmd/server/main.go.

ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_data_scope_check;
ALTER TABLE roles ADD CONSTRAINT roles_data_scope_check CHECK (data_scope IN ('own', 'team', 'all'));

-- A team's lead is display/ownership metadata on the group; teams ARE user_groups
-- (there is no second entity), so a group with members is already a team.
ALTER TABLE user_groups ADD COLUMN IF NOT EXISTS lead_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
