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
        { value: 'neq', label: 'Not Equals' },
        { value: 'gt', label: 'Greater Than' },
        { value: 'gte', label: 'Greater or Equal' },
        { value: 'lt', label: 'Less Than' },
        { value: 'lte', label: 'Less or Equal' },
      ];
    case 'boolean':
      return [
        { value: 'eq', label: 'Is' },
        { value: 'neq', label: 'Is Not' },
      ];
    case 'array':
      return [
        { value: 'contains', label: 'Contains' },
        { value: 'not_contains', label: 'Not Contains' },
        { value: 'is_empty', label: 'Is Empty' },
        { value: 'is_not_empty', label: 'Is Not Empty' },
      ];
    case 'select':
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'neq', label: 'Not Equals' },
        { value: 'in', label: 'In' },
        { value: 'not_in', label: 'Not In' },
      ];
    case 'date':
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'gt', label: 'After' },
        { value: 'lt', label: 'Before' },
        { value: 'gte', label: 'On or After' },
        { value: 'lte', label: 'On or Before' },
      ];
    case 'string':
    default:
      return [
        { value: 'eq', label: 'Equals' },
        { value: 'neq', label: 'Not Equals' },
        { value: 'contains', label: 'Contains' },
        { value: 'not_contains', label: 'Not Contains' },
        { value: 'starts_with', label: 'Starts With' },
        { value: 'ends_with', label: 'Ends With' },
        { value: 'is_empty', label: 'Is Empty' },
        { value: 'is_not_empty', label: 'Is Not Empty' },
      ];
  }
}
