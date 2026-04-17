ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255) DEFAULT '';

UPDATE users SET full_name = TRIM(first_name || ' ' || last_name) WHERE full_name = '' OR full_name IS NULL;
