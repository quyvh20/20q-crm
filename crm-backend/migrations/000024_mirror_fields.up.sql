-- Mirror fields: a read-only field that displays a value pulled from an
-- already-linked record (Salesforce-style cross-object field). Example: a Deal's
-- "Contact Email" mirrors the email of the record its `contact` relation points at.
--
--   via_field    — the relation field on THIS object to follow (e.g. "contact")
--   source_field — the field on the related object to read (e.g. "email")
--
-- Mirror fields store no value of their own; the value is resolved at display time
-- by following via_field to the linked record and reading source_field.

ALTER TABLE object_fields ADD COLUMN IF NOT EXISTS via_field VARCHAR(100);
ALTER TABLE object_fields ADD COLUMN IF NOT EXISTS source_field VARCHAR(100);
