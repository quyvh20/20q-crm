-- Rollback Migration 000004
DELETE FROM system_templates WHERE slug IN ('real_estate', 'education', 'agency', 'retail');
