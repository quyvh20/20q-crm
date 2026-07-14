-- Down: narrow any team-scoped role back to 'own' (the safe direction — never
-- widen a role to 'all' on a rollback), then restore the two-value constraint.
UPDATE roles SET data_scope = 'own' WHERE data_scope = 'team';

ALTER TABLE roles DROP CONSTRAINT IF EXISTS roles_data_scope_check;
ALTER TABLE roles ADD CONSTRAINT roles_data_scope_check CHECK (data_scope IN ('own', 'all'));

ALTER TABLE user_groups DROP COLUMN IF EXISTS lead_user_id;
