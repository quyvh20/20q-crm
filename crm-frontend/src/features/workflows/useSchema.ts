import { useState, useEffect } from 'react';
import { getWorkflowSchema, type WorkflowSchema, type SchemaField } from './api';

// Singleton cache so schema is fetched once per session, not per component mount.
let cachedSchema: WorkflowSchema | null = null;
let fetchPromise: Promise<WorkflowSchema> | null = null;

/**
 * Hook that provides the workflow schema for smart pickers.
 * Fetches once and caches — subsequent mounts return instantly.
 */
export function useWorkflowSchema() {
  const [schema, setSchema] = useState<WorkflowSchema | null>(cachedSchema);
  const [loading, setLoading] = useState(!cachedSchema);

  useEffect(() => {
    if (cachedSchema) {
      setSchema(cachedSchema);
      setLoading(false);
      return;
    }

    // Deduplicate concurrent fetches
    if (!fetchPromise) {
      fetchPromise = getWorkflowSchema();
    }

    fetchPromise
      .then((data) => {
        cachedSchema = data;
        setSchema(data);
      })
      .catch((err) => {
        console.error('Failed to load workflow schema:', err);
      })
      .finally(() => {
        setLoading(false);
        fetchPromise = null;
      });
  }, []);

  return { schema, loading };
}

/**
 * Invalidate the cached schema (e.g., after custom field changes in settings).
 */
export function invalidateSchemaCache() {
  cachedSchema = null;
  fetchPromise = null;
}

// --- Schema utility functions ---

/**
 * Find a SchemaField by its path across all entities and custom objects.
 */
export function findFieldInSchema(
  schema: WorkflowSchema | null,
  path: string,
): SchemaField | null {
  if (!schema || !path) return null;

  for (const entity of [...schema.entities, ...(schema.custom_objects || [])]) {
    for (const field of entity.fields) {
      if (field.path === path) return field;
    }
  }
  return null;
}

/**
 * Get all entities + custom objects flattened into one list.
 */
export function getAllEntities(schema: WorkflowSchema | null) {
  if (!schema) return [];
  return [...schema.entities, ...(schema.custom_objects || [])];
}

// ============================================================
// Operator Definitions — Dynamic by field type AND fires-on event
// ============================================================

export type FiresOn = 'created' | 'updated' | 'deleted' | 'any';

export interface OperatorDef {
  value: string;
  label: string;
  /** If true, the operator needs no value input */
  noValue?: boolean;
  /** If true, the operator needs TWO value inputs (e.g., between) */
  dualValue?: boolean;
}

// --- Base operators per field type (available for Created) ---

const TEXT_BASE: OperatorDef[] = [
  { value: 'eq', label: 'equals' },
  { value: 'neq', label: 'not equals' },
  { value: 'contains', label: 'contains' },
  { value: 'not_contains', label: 'does not contain' },
  { value: 'starts_with', label: 'starts with' },
  { value: 'ends_with', label: 'ends with' },
  { value: 'is_empty', label: 'is empty', noValue: true },
  { value: 'is_not_empty', label: 'is not empty', noValue: true },
];

const NUMBER_BASE: OperatorDef[] = [
  { value: 'eq', label: 'equals' },
  { value: 'neq', label: 'not equals' },
  { value: 'gt', label: 'greater than' },
  { value: 'lt', label: 'less than' },
  { value: 'between', label: 'between', dualValue: true },
  { value: 'is_empty', label: 'is empty', noValue: true },
  { value: 'is_not_empty', label: 'is not empty', noValue: true },
];

const DATE_BASE: OperatorDef[] = [
  { value: 'gt', label: 'after' },
  { value: 'lt', label: 'before' },
  { value: 'between', label: 'between', dualValue: true },
  { value: 'last_n_days', label: 'in last N days' },
  { value: 'is_empty', label: 'is empty', noValue: true },
  { value: 'is_not_empty', label: 'is not empty', noValue: true },
];

const BOOLEAN_BASE: OperatorDef[] = [
  { value: 'is_true', label: 'is true', noValue: true },
  { value: 'is_false', label: 'is false', noValue: true },
];

const SELECT_BASE: OperatorDef[] = [
  { value: 'in', label: 'is one of' },
  { value: 'not_in', label: 'is not one of' },
  { value: 'is_empty', label: 'is empty', noValue: true },
  { value: 'is_not_empty', label: 'is not empty', noValue: true },
];

const ARRAY_BASE: OperatorDef[] = [
  { value: 'contains', label: 'contains' },
  { value: 'not_contains', label: 'does not contain' },
  { value: 'is_empty', label: 'is empty', noValue: true },
  { value: 'is_not_empty', label: 'is not empty', noValue: true },
];

// Change-detection operators (is_changed / is_set / is_cleared /
// changed_from_to) USED to be appended here for Updated/Any triggers. They were
// removed on 2026-08-18 because nothing could ever have evaluated them: the
// engine's EvalContext carries no before-state. Exactly one of six *_updated
// emitters snapshots the old record (the legacy contact REST handler), the
// uniform RecordService path that serves deals, companies and every custom
// object emits none, and buildEvalContext discards `changed_fields` anyway
// because it is an array and only map values reach the context. They were also
// offered on email_opened / webhook_inbound / schedule triggers, where nothing
// changed at all.
//
// So the choice was a loud 400 at save (what shipped) or a silent permanent No
// branch (what implementing them contact-only would have produced). Neither is
// a feature. Change detection is not missing from the product — it lives at the
// TRIGGER, as watch_field / watch_value ("when the owner field changes", "when
// it changes to X"). If it is wanted at the condition too, the before/after diff
// has to be plumbed through fireLifecycleEvent into EvalContext first; that is
// its own piece of work, not an operator list edit.

// --- Deleted mode: minimal operators ---

const DELETED_TEXT: OperatorDef[] = [
  { value: 'eq', label: 'equals' },
  { value: 'is_empty', label: 'is empty', noValue: true },
];

const DELETED_NUMBER: OperatorDef[] = [
  { value: 'eq', label: 'equals' },
  { value: 'is_empty', label: 'is empty', noValue: true },
];

const DELETED_BOOLEAN: OperatorDef[] = [
  { value: 'is_true', label: 'is true', noValue: true },
  { value: 'is_false', label: 'is false', noValue: true },
];

const DELETED_MINIMAL: OperatorDef[] = [
  { value: 'is_empty', label: 'is empty', noValue: true },
];

/**
 * Return operators that make sense for a given field type AND fires-on event.
 *
 * @param type  - field data type: 'string' | 'number' | 'boolean' | 'array' | 'select' | 'date'
 * @param firesOn - trigger event context: 'created' | 'updated' | 'deleted' | 'any'.
 *   Only 'deleted' changes the answer now; it trims the list to what a deleted
 *   record can still be asked about. Updated/Any used to add change-detection
 *   operators — see the note above CHANGE_OPS' removal.
 */
export function getOperatorsForType(type: string, firesOn: FiresOn = 'created'): OperatorDef[] {
  if (firesOn === 'deleted') {
    switch (type) {
      case 'string': return DELETED_TEXT;
      case 'number': return DELETED_NUMBER;
      case 'boolean': return DELETED_BOOLEAN;
      case 'date': return DELETED_MINIMAL;
      case 'select': return DELETED_MINIMAL;
      case 'array': return DELETED_MINIMAL;
      default: return DELETED_TEXT;
    }
  }

  switch (type) {
    case 'string':
      return TEXT_BASE;
    case 'number':
      return NUMBER_BASE;
    case 'date':
      return DATE_BASE;
    case 'boolean':
      return BOOLEAN_BASE;
    case 'select':
      return SELECT_BASE;
    case 'array':
      return ARRAY_BASE;
    default:
      console.warn(`[getOperatorsForType] Unknown field type "${type}" — falling back to string operators`);
      return TEXT_BASE;
  }
}

/**
 * Check if an operator requires no value input.
 */
export function isNoValueOperator(op: string): boolean {
  const NO_VALUE = new Set(['is_empty', 'is_not_empty', 'is_true', 'is_false']);
  return NO_VALUE.has(op);
}

/**
 * Check if an operator requires two value inputs.
 */
export function isDualValueOperator(op: string): boolean {
  return op === 'between';
}

/**
 * Operators whose operand is a COUNT OF DAYS, not a value of the field's own
 * type. They need their own input, and must be excluded from the ordinary
 * single-value one — on a date field the two used to render side by side and
 * fight over the same `value`, so whichever the user touched last won and the
 * date picker showed an empty box for a stored `7`.
 */
export function isRelativeDaysOperator(op: string): boolean {
  return op === 'last_n_days';
}
