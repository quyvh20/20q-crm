import React, { useMemo } from 'react';
import type { TriggerSpec } from '../types';
import { useBuilderStore } from '../store';
import { FieldPicker, type FieldMeta } from './FieldPicker';
import { SmartValueInput } from './SmartValueInput';
import type { SchemaField, SchemaEntity, WorkflowSchema } from '../api';

// ============================================================
// Sentence Trigger Builder — Schema-Driven
// "When a [Contact ▾] [is created ▾]"
// Supports built-in entities AND custom objects from schema.
// ============================================================

// --- Built-in entity icons ---
const ENTITY_ICONS: Record<string, string> = {
  contact: '👤',
  deal: '📊',
  webhook: '🔗',
};

// --- Event definitions per entity key ---
interface EventDef {
  key: string;
  label: string;
  /** How we derive trigger type: either a literal string or a function of the entity slug */
  triggerType: string;
}

/** Built-in events for specific entity types */
const BUILTIN_EVENTS: Record<string, EventDef[]> = {
  contact: [
    { key: 'created', label: 'is created', triggerType: 'contact_created' },
    { key: 'updated', label: 'is updated', triggerType: 'contact_updated' },
    { key: 'no_activity', label: 'has no activity', triggerType: 'no_activity_days' },
  ],
  deal: [
    { key: 'stage_changed', label: 'changes stage', triggerType: 'deal_stage_changed' },
    { key: 'no_activity', label: 'has no activity', triggerType: 'no_activity_days' },
  ],
  webhook: [
    { key: 'receives_data', label: 'receives data', triggerType: 'webhook_inbound' },
  ],
};

/** Generate standard events for a custom object entity */
function makeCustomObjectEvents(slug: string): EventDef[] {
  return [
    { key: 'created', label: 'is created', triggerType: `${slug}_created` },
    { key: 'updated', label: 'is updated', triggerType: `${slug}_updated` },
    { key: 'field_changes', label: 'field changes', triggerType: `${slug}_updated` },
  ];
}

/** Get events for any entity key */
function getEventsForEntity(entityKey: string, customObjectSlugs: string[]): EventDef[] {
  if (BUILTIN_EVENTS[entityKey]) return BUILTIN_EVENTS[entityKey];
  // Custom object: generate events dynamically
  if (customObjectSlugs.includes(entityKey)) return makeCustomObjectEvents(entityKey);
  return [];
}

// --- Build entity list from schema ---
interface EntityOption {
  key: string;
  label: string;
  icon: string;
}

// Entities that exist in schema.entities for template variables but are NOT
// triggerable objects. These are filtered out of the entity dropdown.
const NON_TRIGGERABLE_ENTITIES = new Set(['trigger']);

function buildEntityList(schema: WorkflowSchema | null): EntityOption[] {
  const entities: EntityOption[] = [];

  // Built-in entities from schema (filter out non-triggerable ones)
  if (schema) {
    for (const ent of schema.entities) {
      if (NON_TRIGGERABLE_ENTITIES.has(ent.key)) continue;
      entities.push({
        key: ent.key,
        label: ent.label,
        icon: ENTITY_ICONS[ent.key] || ent.icon || '📦',
      });
    }
    // Custom objects from schema
    for (const obj of (schema.custom_objects || [])) {
      entities.push({
        key: obj.key,
        label: obj.label,
        icon: obj.icon || '📦',
      });
    }
  } else {
    // Fallback if schema not loaded yet
    entities.push(
      { key: 'contact', label: 'Contact', icon: '👤' },
      { key: 'deal', label: 'Deal', icon: '📊' },
    );
  }

  // Webhook is always available (not a schema entity)
  entities.push({ key: 'webhook', label: 'Webhook', icon: '🔗' });

  return entities;
}

// --- Derive entity+event from an existing TriggerSpec ---
function parseTriggerToSentence(trigger: TriggerSpec, customObjectSlugs: string[]): { entity: string; event: string } {
  // Built-in types
  switch (trigger.type) {
    case 'contact_created':
      return { entity: 'contact', event: 'created' };
    case 'contact_updated':
      return { entity: 'contact', event: 'updated' };
    case 'deal_stage_changed':
      return { entity: 'deal', event: 'stage_changed' };
    case 'no_activity_days': {
      const e = (trigger.params?.entity as string) || 'contact';
      return { entity: e, event: 'no_activity' };
    }
    case 'webhook_inbound':
      return { entity: 'webhook', event: 'receives_data' };
  }

  // Dynamic: custom object triggers like "subscription_created"
  for (const slug of customObjectSlugs) {
    if (trigger.type === `${slug}_created`) return { entity: slug, event: 'created' };
    if (trigger.type === `${slug}_updated`) {
      return trigger.params?.watch_field
        ? { entity: slug, event: 'field_changes' }
        : { entity: slug, event: 'updated' };
    }
  }

  return { entity: 'contact', event: 'created' };
}

// --- Shared select styling ---
const selectClass =
  'bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white font-medium focus:border-indigo-500 focus:outline-none cursor-pointer appearance-none transition-colors hover:border-gray-600';

// ============================================================
// Main Component
// ============================================================

export const TriggerConfigPanel: React.FC = () => {
  const { trigger, setTrigger, schema, schemaLoading } = useBuilderStore();

  // Build entity list from schema
  const entityList = useMemo(() => buildEntityList(schema), [schema]);
  const customObjectSlugs = useMemo(
    () => (schema?.custom_objects || []).map((o: SchemaEntity) => o.key),
    [schema],
  );

  // Derive current entity + event from trigger state
  const { entity, event } = useMemo(() => {
    if (!trigger) return { entity: '', event: '' };
    return parseTriggerToSentence(trigger, customObjectSlugs);
  }, [trigger, customObjectSlugs]);

  const events = useMemo(
    () => entity ? getEventsForEntity(entity, customObjectSlugs) : [],
    [entity, customObjectSlugs],
  );

  // --- Build the TriggerSpec from sentence selections ---
  const handleEntityChange = (newEntity: string) => {
    const evts = getEventsForEntity(newEntity, customObjectSlugs);
    const firstEvent = evts[0];
    if (!firstEvent) return;
    setTrigger(buildTriggerSpec(newEntity, firstEvent.key, {}, customObjectSlugs));
  };

  const handleEventChange = (newEvent: string) => {
    setTrigger(buildTriggerSpec(entity, newEvent, {}, customObjectSlugs));
  };

  // --- Stage handlers ---
  const handleFromStageChange = (val: string) => {
    setTrigger({ ...trigger!, params: { ...trigger?.params, from_stage: val } });
  };
  const handleToStageChange = (val: string) => {
    setTrigger({ ...trigger!, params: { ...trigger?.params, to_stage: val } });
  };

  // --- No activity handlers ---
  const handleDaysChange = (val: string) => {
    const days = parseInt(val) || 7;
    setTrigger({ ...trigger!, params: { ...trigger?.params, days } });
  };

  // --- Webhook source handler ---
  const handleSourceChange = (val: string) => {
    setTrigger({ ...trigger!, params: { ...trigger?.params, source: val } });
  };

  // --- Field changes handlers ---
  const handleWatchFieldChange = (path: string, meta: FieldMeta) => {
    // Derive the correct trigger type for the current entity
    const evts = getEventsForEntity(entity, customObjectSlugs);
    const fcEvent = evts.find((e) => e.key === 'field_changes');
    const triggerType = fcEvent?.triggerType || `${entity}_updated`;
    setTrigger({
      type: triggerType,
      params: { watch_field: path, _fieldMeta: meta },
    });
  };

  const handleWatchValueToggle = (enabled: boolean) => {
    if (!trigger) return;
    const params = { ...trigger.params };
    if (enabled) {
      params.watch_value = '';
    } else {
      delete params.watch_value;
    }
    setTrigger({ ...trigger, params });
  };

  const handleWatchValueChange = (val: unknown) => {
    if (!trigger) return;
    setTrigger({ ...trigger, params: { ...trigger.params, watch_value: val } });
  };

  // Resolve the field meta for watch_field (for SmartValueInput)
  const watchFieldMeta = useMemo((): SchemaField | null => {
    if (!trigger?.params?.watch_field || !schema) return null;
    const watchPath = trigger.params.watch_field as string;
    if (trigger.params._fieldMeta) {
      const m = trigger.params._fieldMeta as FieldMeta;
      return { path: watchPath, label: m.label, type: m.type, picker_type: m.picker_type, options: m.options };
    }
    for (const ent of [...schema.entities, ...(schema.custom_objects || [])]) {
      const field = ent.fields.find((f) => f.path === watchPath);
      if (field) return field;
    }
    return null;
  }, [trigger?.params?.watch_field, trigger?.params?._fieldMeta, schema]);

  const hasWatchValue = trigger?.params?.watch_value !== undefined;

  // Determine which entity filter to pass to FieldPicker
  const fieldPickerEntities = useMemo(() => {
    if (!entity || entity === 'webhook') return undefined;
    return [entity];
  }, [entity]);

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Trigger</h3>

      {/* Sentence builder */}
      <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30 space-y-3">
        {/* Row 1: When a [Entity] [Event] */}
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm text-gray-400 font-medium whitespace-nowrap">When a</span>

          {/* Entity dropdown — schema-driven */}
          <div className="relative">
            <select
              value={entity || ''}
              onChange={(e) => handleEntityChange(e.target.value)}
              className={selectClass}
              style={{ paddingRight: '2rem' }}
            >
              <option value="" disabled>pick…</option>
              {entityList.map((e) => (
                <option key={e.key} value={e.key}>{e.icon} {e.label}</option>
              ))}
            </select>
            <ChevronDown />
          </div>

          {/* Event dropdown */}
          {entity && events.length > 0 && (
            <div className="relative">
              <select
                value={event || ''}
                onChange={(e) => handleEventChange(e.target.value)}
                className={selectClass}
                style={{ paddingRight: '2rem' }}
              >
                <option value="" disabled>what happens…</option>
                {events.map((ev) => (
                  <option key={ev.key} value={ev.key}>{ev.label}</option>
                ))}
              </select>
              <ChevronDown />
            </div>
          )}
        </div>

        {/* Row 2: Contextual params */}

        {/* deal_stage_changed: from [stage] to [stage] */}
        {event === 'stage_changed' && (
          <div className="flex items-center gap-2 flex-wrap pl-6">
            <span className="text-sm text-gray-500">from</span>
            {schemaLoading ? (
              <div className="w-28 h-[30px] bg-gray-800 border border-gray-700 rounded-lg animate-pulse" />
            ) : (
              <div className="relative">
                <select
                  value={(trigger?.params?.from_stage as string) || '*'}
                  onChange={(e) => handleFromStageChange(e.target.value)}
                  className={selectClass}
                  style={{ paddingRight: '2rem' }}
                >
                  <option value="*">Any stage</option>
                  {schema?.stages.map((s) => (
                    <option key={s.id} value={s.name}>{s.name}</option>
                  ))}
                </select>
                <ChevronDown />
              </div>
            )}
            <span className="text-sm text-gray-500">to</span>
            {schemaLoading ? (
              <div className="w-28 h-[30px] bg-gray-800 border border-gray-700 rounded-lg animate-pulse" />
            ) : (
              <div className="relative">
                <select
                  value={(trigger?.params?.to_stage as string) || ''}
                  onChange={(e) => handleToStageChange(e.target.value)}
                  className={selectClass}
                  style={{ paddingRight: '2rem' }}
                >
                  <option value="">Select stage…</option>
                  {schema?.stages.map((s) => (
                    <option key={s.id} value={s.name}>{s.name}</option>
                  ))}
                </select>
                <ChevronDown />
              </div>
            )}
          </div>
        )}

        {/* no_activity_days: for [N] days */}
        {event === 'no_activity' && (
          <div className="flex items-center gap-2 pl-6">
            <span className="text-sm text-gray-500">for</span>
            <input
              type="number"
              min={1}
              value={(trigger?.params?.days as number) || 7}
              onChange={(e) => handleDaysChange(e.target.value)}
              className="w-16 bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white text-center focus:border-indigo-500 focus:outline-none"
            />
            <span className="text-sm text-gray-400 font-medium">days</span>
          </div>
        )}

        {/* webhook_inbound: from [source] */}
        {event === 'receives_data' && (
          <div className="flex items-center gap-2 pl-6">
            <span className="text-sm text-gray-500">from</span>
            <div className="relative">
              <select
                value={(trigger?.params?.source as string) || 'custom'}
                onChange={(e) => handleSourceChange(e.target.value)}
                className={selectClass}
                style={{ paddingRight: '2rem' }}
              >
                <option value="custom">Custom</option>
                <option value="typeform">Typeform</option>
              </select>
              <ChevronDown />
            </div>
          </div>
        )}

        {/* field_changes: [FieldPicker] optional: to [SmartValueInput] */}
        {event === 'field_changes' && (
          <div className="space-y-2 pl-6">
            <div className="flex items-center gap-2">
              <span className="text-sm text-gray-500 whitespace-nowrap">field</span>
              <div className="flex-1">
                <FieldPicker
                  value={(trigger?.params?.watch_field as string) || null}
                  onChange={handleWatchFieldChange}
                  entities={fieldPickerEntities}
                  placeholder="Select field to watch…"
                />
              </div>
            </div>

            {/* Optional: to exact value */}
            {!!trigger?.params?.watch_field && (
              <div className="flex items-center gap-2">
                <label className="flex items-center gap-2 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={hasWatchValue}
                    onChange={(e) => handleWatchValueToggle(e.target.checked)}
                    className="accent-indigo-500 w-3.5 h-3.5"
                  />
                  <span className="text-xs text-gray-400">Match exact value</span>
                </label>
              </div>
            )}

            {hasWatchValue && watchFieldMeta && (
              <div className="flex items-center gap-2">
                <span className="text-sm text-gray-500">to</span>
                <div className="flex-1">
                  <SmartValueInput
                    field={watchFieldMeta}
                    operator="eq"
                    value={trigger?.params?.watch_value}
                    onChange={handleWatchValueChange}
                  />
                </div>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Human-readable sentence preview */}
      {trigger && (
        <div className="px-3 py-2 rounded-lg bg-indigo-500/5 border border-indigo-500/10">
          <p className="text-xs text-indigo-300/70">
            <span className="text-indigo-400 font-medium">Preview: </span>
            {buildSentencePreview(trigger, schema, entityList, customObjectSlugs)}
          </p>
        </div>
      )}
    </div>
  );
};

// ============================================================
// Helpers
// ============================================================

function buildTriggerSpec(
  entity: string,
  event: string,
  existingParams: Record<string, unknown>,
  customObjectSlugs: string[],
): TriggerSpec {
  const evts = getEventsForEntity(entity, customObjectSlugs);
  const eventDef = evts.find((e) => e.key === event);
  if (!eventDef) return { type: `${entity}_created` };

  const type = eventDef.triggerType;
  const params: Record<string, unknown> = {};

  // Handle special params per event type
  if (event === 'field_changes' && existingParams.watch_field) {
    params.watch_field = existingParams.watch_field;
  }
  if (event === 'stage_changed') {
    params.from_stage = existingParams.from_stage || '*';
    params.to_stage = existingParams.to_stage || '';
  }
  if (event === 'no_activity') {
    params.entity = entity;
    params.days = existingParams.days || 7;
  }
  if (event === 'receives_data') {
    params.source = existingParams.source || 'custom';
  }

  return { type, params: Object.keys(params).length > 0 ? params : undefined } as TriggerSpec;
}

function buildSentencePreview(
  trigger: TriggerSpec,
  schema: WorkflowSchema | null,
  entityList: EntityOption[],
  customObjectSlugs: string[],
): string {
  const { entity, event } = parseTriggerToSentence(trigger, customObjectSlugs);
  const entityDef = entityList.find((e) => e.key === entity);
  const entityLabel = entityDef?.label || entity;

  // Find field label helper
  const resolveFieldLabel = (watchField: string): string => {
    if (!schema) return watchField;
    for (const ent of [...schema.entities, ...(schema.custom_objects || [])]) {
      const f = ent.fields.find((ff) => ff.path === watchField);
      if (f) return f.label;
    }
    return watchField;
  };

  switch (event) {
    case 'created':
      return `When a ${entityLabel} is created`;
    case 'updated':
      return `When a ${entityLabel} is updated (any field)`;
    case 'field_changes': {
      const wf = trigger.params?.watch_field as string;
      if (!wf) return `When a ${entityLabel} field changes`;
      const fieldLabel = resolveFieldLabel(wf);
      if (trigger.params?.watch_value !== undefined) {
        return `When a ${entityLabel}'s ${fieldLabel} changes to "${String(trigger.params.watch_value)}"`;
      }
      return `When a ${entityLabel}'s ${fieldLabel} changes`;
    }
    case 'stage_changed': {
      const from = (trigger.params?.from_stage as string) || '*';
      const to = (trigger.params?.to_stage as string) || '?';
      return `When a ${entityLabel} changes stage from ${from === '*' ? 'any stage' : from} to ${to}`;
    }
    case 'no_activity': {
      const days = (trigger.params?.days as number) || 7;
      return `When a ${entityLabel} has no activity for ${days} days`;
    }
    case 'receives_data': {
      const src = (trigger.params?.source as string) || 'custom';
      return `When a ${entityLabel} receives data from ${src}`;
    }
    default:
      return `Trigger configured`;
  }
}

// Chevron icon for select dropdowns
const ChevronDown: React.FC = () => (
  <svg
    className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-gray-500"
    fill="none"
    viewBox="0 0 24 24"
    stroke="currentColor"
    strokeWidth={2}
  >
    <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
  </svg>
);
