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

  for (const entity of [...schema.entities, ...schema.custom_objects]) {
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
  /** If true, the operator needs TWO value inputs (e.g., between, changed_from_to) */
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
  { value: 'in_last_days', label: 'in last N days' },
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

// --- Change-detection operators (only for Updated / Any) ---

const CHANGE_OPS: OperatorDef[] = [
  { value: 'is_changed', label: 'is changed', noValue: true },
  { value: 'is_set', label: 'is set', noValue: true },
  { value: 'is_cleared', label: 'is cleared', noValue: true },
  { value: 'changed_from_to', label: 'changed from…to', dualValue: true },
];

const CHANGE_OPS_NO_DUAL: OperatorDef[] = [
  { value: 'is_changed', label: 'is changed', noValue: true },
];

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
 * @param firesOn - trigger event context: 'created' | 'updated' | 'deleted' | 'any'
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

  const includeChange = firesOn === 'updated' || firesOn === 'any';

  switch (type) {
    case 'string':
      return includeChange ? [...TEXT_BASE, ...CHANGE_OPS] : TEXT_BASE;
    case 'number':
      return includeChange ? [...NUMBER_BASE, ...CHANGE_OPS] : NUMBER_BASE;
    case 'date':
      return includeChange ? [...DATE_BASE, ...CHANGE_OPS] : DATE_BASE;
    case 'boolean':
      return includeChange ? [...BOOLEAN_BASE, ...CHANGE_OPS_NO_DUAL] : BOOLEAN_BASE;
    case 'select':
      return includeChange ? [...SELECT_BASE, ...CHANGE_OPS] : SELECT_BASE;
    case 'array':
      return includeChange ? [...ARRAY_BASE, ...CHANGE_OPS] : ARRAY_BASE;
    default:
      console.warn(`[getOperatorsForType] Unknown field type "${type}" — falling back to string operators`);
      return includeChange ? [...TEXT_BASE, ...CHANGE_OPS] : TEXT_BASE;
  }
}

/**
 * Check if an operator requires no value input.
 */
export function isNoValueOperator(op: string): boolean {
  const NO_VALUE = new Set([
    'is_empty', 'is_not_empty', 'is_true', 'is_false',
    'is_changed', 'is_set', 'is_cleared',
  ]);
  return NO_VALUE.has(op);
}

/**
 * Check if an operator requires two value inputs.
 */
export function isDualValueOperator(op: string): boolean {
  return op === 'between' || op === 'changed_from_to';
}
