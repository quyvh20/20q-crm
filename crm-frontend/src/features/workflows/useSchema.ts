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

/**
 * Return only the operators that make sense for a given field type.
 */
export function getOperatorsForType(type: string) {
  switch (type) {
    case 'number':
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'neq', label: 'Not equals' },
        { value: 'gt', label: 'Greater than' },
        { value: 'gte', label: 'Greater than or equal' },
        { value: 'lt', label: 'Less than' },
        { value: 'lte', label: 'Less than or equal' },
      ];
    case 'boolean':
      return [
        { value: 'eq', label: 'Is' },
        { value: 'neq', label: 'Is not' },
      ];
    case 'array':
      return [
        { value: 'contains', label: 'Contains' },
        { value: 'not_contains', label: 'Does not contain' },
        { value: 'is_empty', label: 'Is empty' },
        { value: 'is_not_empty', label: 'Is not empty' },
      ];
    case 'select':
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'neq', label: 'Not equals' },
        { value: 'in', label: 'Is any of' },
        { value: 'not_in', label: 'Is none of' },
      ];
    case 'date':
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'gt', label: 'After' },
        { value: 'lt', label: 'Before' },
        { value: 'gte', label: 'On or after' },
        { value: 'lte', label: 'On or before' },
      ];
    case 'string':
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'neq', label: 'Not equals' },
        { value: 'contains', label: 'Contains' },
        { value: 'not_contains', label: 'Does not contain' },
        { value: 'starts_with', label: 'Starts with' },
        { value: 'ends_with', label: 'Ends with' },
        { value: 'is_empty', label: 'Is empty' },
        { value: 'is_not_empty', label: 'Is not empty' },
      ];
    default:
      console.warn(`[getOperatorsForType] Unknown field type "${type}" — falling back to string operators`);
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'neq', label: 'Not equals' },
        { value: 'contains', label: 'Contains' },
        { value: 'not_contains', label: 'Does not contain' },
        { value: 'starts_with', label: 'Starts with' },
        { value: 'ends_with', label: 'Ends with' },
        { value: 'is_empty', label: 'Is empty' },
        { value: 'is_not_empty', label: 'Is not empty' },
      ];
  }
}
