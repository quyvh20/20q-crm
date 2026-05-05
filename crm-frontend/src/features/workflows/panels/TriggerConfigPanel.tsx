import React, { useMemo } from 'react';
import type { TriggerType, TriggerSpec } from '../types';
import { useBuilderStore } from '../store';
import { FieldPicker, type FieldMeta } from './FieldPicker';
import { SmartValueInput } from './SmartValueInput';
import type { SchemaField } from '../api';

// ============================================================
// Sentence Trigger Builder
// "When a [Contact ▾] [is created ▾]"
// ============================================================

// --- Entity definitions ---
type EntityKey = 'contact' | 'deal' | 'webhook';

interface EntityDef {
  key: EntityKey;
  label: string;
  icon: string;
}

const ENTITIES: EntityDef[] = [
  { key: 'contact', label: 'Contact', icon: '👤' },
  { key: 'deal', label: 'Deal', icon: '📊' },
  { key: 'webhook', label: 'Webhook', icon: '🔗' },
];

// --- Event definitions per entity ---
interface EventDef {
  key: string;
  label: string;
  triggerType: TriggerType;
}

const EVENTS_BY_ENTITY: Record<EntityKey, EventDef[]> = {
  contact: [
    { key: 'created', label: 'is created', triggerType: 'contact_created' },
    { key: 'updated', label: 'is updated', triggerType: 'contact_updated' },
    { key: 'field_changes', label: 'field changes', triggerType: 'contact_updated' },
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

// --- Derive entity+event from an existing TriggerSpec (for loading saved workflows) ---
function parseTriggerToSentence(trigger: TriggerSpec): { entity: EntityKey; event: string } {
  switch (trigger.type) {
    case 'contact_created':
      return { entity: 'contact', event: 'created' };
    case 'contact_updated':
      return trigger.params?.watch_field
        ? { entity: 'contact', event: 'field_changes' }
        : { entity: 'contact', event: 'updated' };
    case 'deal_stage_changed':
      return { entity: 'deal', event: 'stage_changed' };
    case 'no_activity_days': {
      const e = (trigger.params?.entity as string) || 'contact';
      return { entity: e as EntityKey, event: 'no_activity' };
    }
    case 'webhook_inbound':
      return { entity: 'webhook', event: 'receives_data' };
    default:
      return { entity: 'contact', event: 'created' };
  }
}

// --- Shared select styling ---
const selectClass =
  'bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white font-medium focus:border-indigo-500 focus:outline-none cursor-pointer appearance-none transition-colors hover:border-gray-600';

// ============================================================
// Main Component
// ============================================================

export const TriggerConfigPanel: React.FC = () => {
  const { trigger, setTrigger, schema, schemaLoading } = useBuilderStore();

  // Derive current entity + event from trigger state
  const { entity, event } = useMemo(() => {
    if (!trigger) return { entity: '' as EntityKey, event: '' };
    return parseTriggerToSentence(trigger);
  }, [trigger]);

  const events = entity ? EVENTS_BY_ENTITY[entity] || [] : [];

  // --- Build the TriggerSpec from sentence selections ---
  const handleEntityChange = (newEntity: EntityKey) => {
    // Auto-select first event for the new entity
    const firstEvent = EVENTS_BY_ENTITY[newEntity]?.[0];
    if (!firstEvent) return;
    setTrigger(buildTriggerSpec(newEntity, firstEvent.key, {}));
  };

  const handleEventChange = (newEvent: string) => {
    setTrigger(buildTriggerSpec(entity, newEvent, {}));
  };

  // --- Stage handlers ---
  const handleFromStageChange = (val: string) => {
    setTrigger({
      ...trigger!,
      params: { ...trigger?.params, from_stage: val },
    });
  };

  const handleToStageChange = (val: string) => {
    setTrigger({
      ...trigger!,
      params: { ...trigger?.params, to_stage: val },
    });
  };

  // --- No activity handlers ---
  const handleDaysChange = (val: string) => {
    const days = parseInt(val) || 7;
    setTrigger({
      ...trigger!,
      params: { ...trigger?.params, days },
    });
  };

  // --- Webhook source handler ---
  const handleSourceChange = (val: string) => {
    setTrigger({
      ...trigger!,
      params: { ...trigger?.params, source: val },
    });
  };

  // --- Field changes handlers ---
  const handleWatchFieldChange = (path: string, meta: FieldMeta) => {
    setTrigger({
      type: 'contact_updated',
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
    setTrigger({
      ...trigger,
      params: { ...trigger.params, watch_value: val },
    });
  };

  // Resolve the field meta for watch_field (for SmartValueInput)
  const watchFieldMeta = useMemo((): SchemaField | null => {
    if (!trigger?.params?.watch_field || !schema) return null;
    const watchPath = trigger.params.watch_field as string;
    // Check if we have cached metadata from FieldPicker
    if (trigger.params._fieldMeta) {
      const m = trigger.params._fieldMeta as FieldMeta;
      return {
        path: watchPath,
        label: m.label,
        type: m.type,
        picker_type: m.picker_type,
        options: m.options,
      };
    }
    // Fallback: look up in schema
    for (const ent of [...schema.entities, ...(schema.custom_objects || [])]) {
      const field = ent.fields.find((f) => f.path === watchPath);
      if (field) return field;
    }
    return null;
  }, [trigger?.params?.watch_field, trigger?.params?._fieldMeta, schema]);

  const hasWatchValue = trigger?.params?.watch_value !== undefined;

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Trigger</h3>

      {/* Sentence builder */}
      <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30 space-y-3">
        {/* Row 1: When a [Entity] [Event] */}
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm text-gray-400 font-medium whitespace-nowrap">When a</span>

          {/* Entity dropdown */}
          <div className="relative">
            <select
              value={entity || ''}
              onChange={(e) => handleEntityChange(e.target.value as EntityKey)}
              className={selectClass}
              style={{ paddingRight: '2rem' }}
            >
              <option value="" disabled>pick…</option>
              {ENTITIES.map((e) => (
                <option key={e.key} value={e.key}>{e.icon} {e.label}</option>
              ))}
            </select>
            <ChevronDown />
          </div>

          {/* Event dropdown (appears after entity is picked) */}
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

        {/* Row 2: Contextual params based on event */}

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
                  entities={['contact']}
                  placeholder="Select field to watch…"
                />
              </div>
            </div>

            {/* Optional: to exact value */}
            {trigger?.params?.watch_field && (
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
            {buildSentencePreview(trigger, schema)}
          </p>
        </div>
      )}
    </div>
  );
};

// ============================================================
// Helpers
// ============================================================

function buildTriggerSpec(entity: EntityKey, event: string, existingParams: Record<string, unknown>): TriggerSpec {
  const eventDef = EVENTS_BY_ENTITY[entity]?.find((e) => e.key === event);
  if (!eventDef) return { type: 'contact_created' };

  const type = eventDef.triggerType;
  const params: Record<string, unknown> = {};

  switch (`${entity}:${event}`) {
    case 'contact:created':
    case 'contact:updated':
      // No params needed
      break;
    case 'contact:field_changes':
      // Preserve existing watch_field if any
      if (existingParams.watch_field) params.watch_field = existingParams.watch_field;
      break;
    case 'deal:stage_changed':
      params.from_stage = existingParams.from_stage || '*';
      params.to_stage = existingParams.to_stage || '';
      break;
    case 'contact:no_activity':
      params.entity = 'contact';
      params.days = existingParams.days || 7;
      break;
    case 'deal:no_activity':
      params.entity = 'deal';
      params.days = existingParams.days || 7;
      break;
    case 'webhook:receives_data':
      params.source = existingParams.source || 'custom';
      break;
  }

  return { type, params: Object.keys(params).length > 0 ? params : undefined } as TriggerSpec;
}

function buildSentencePreview(trigger: TriggerSpec, schema: ReturnType<typeof useBuilderStore.getState>['schema']): string {
  const { entity, event } = parseTriggerToSentence(trigger);
  const entityDef = ENTITIES.find((e) => e.key === entity);
  const entityLabel = entityDef?.label || entity;

  switch (`${entity}:${event}`) {
    case 'contact:created':
      return `When a Contact is created`;
    case 'contact:updated':
      return `When a Contact is updated (any field)`;
    case 'contact:field_changes': {
      const wf = trigger.params?.watch_field as string;
      if (!wf) return `When a Contact field changes`;
      // Find field label
      let fieldLabel = wf.replace('contact.', '');
      if (schema) {
        for (const ent of schema.entities) {
          const f = ent.fields.find((ff) => ff.path === wf);
          if (f) { fieldLabel = f.label; break; }
        }
      }
      const hasVal = trigger.params?.watch_value !== undefined;
      if (hasVal) {
        return `When a Contact's ${fieldLabel} changes to "${trigger.params!.watch_value}"`;
      }
      return `When a Contact's ${fieldLabel} changes`;
    }
    case 'deal:stage_changed': {
      const from = (trigger.params?.from_stage as string) || '*';
      const to = (trigger.params?.to_stage as string) || '?';
      const fromLabel = from === '*' ? 'any stage' : from;
      return `When a Deal changes stage from ${fromLabel} to ${to}`;
    }
    case 'contact:no_activity':
    case 'deal:no_activity': {
      const days = (trigger.params?.days as number) || 7;
      return `When a ${entityLabel} has no activity for ${days} days`;
    }
    case 'webhook:receives_data': {
      const src = (trigger.params?.source as string) || 'custom';
      return `When a Webhook receives data from ${src}`;
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
