import React, { useMemo } from 'react';
import type { TriggerSpec } from '../types';
import { useBuilderStore } from '../store';
import type { WorkflowSchema } from '../api';
import type { FiresOn } from '../useSchema';

// ============================================================
// Source Panel — Step 1
// Object dropdown + Fires-on selector
// ============================================================

const FIRES_ON_OPTIONS: { value: FiresOn; label: string }[] = [
  { value: 'created', label: 'Created' },
  { value: 'updated', label: 'Updated' },
  { value: 'deleted', label: 'Deleted' },
  { value: 'any', label: 'Any' },
];

// Entities from schema.entities that are NOT triggerable (e.g., template variable sources)
const NON_TRIGGERABLE_ENTITIES = new Set(['trigger']);

interface EntityOption {
  key: string;
  label: string;
  icon: string;
}

function buildEntityList(schema: WorkflowSchema | null): EntityOption[] {
  const entities: EntityOption[] = [];

  if (schema) {
    for (const ent of schema.entities) {
      if (NON_TRIGGERABLE_ENTITIES.has(ent.key)) continue;
      entities.push({ key: ent.key, label: ent.label, icon: ent.icon || '📦' });
    }
    for (const obj of (schema.custom_objects || [])) {
      entities.push({ key: obj.key, label: obj.label, icon: obj.icon || '📦' });
    }
  } else {
    entities.push(
      { key: 'contact', label: 'Contact', icon: '👤' },
      { key: 'deal', label: 'Deal', icon: '💰' },
    );
  }

  entities.push({ key: 'webhook', label: 'Webhook', icon: '🔗' });
  return entities;
}

// --- Parse existing trigger into object + firesOn ---
function parseTrigger(trigger: TriggerSpec): { object: string; firesOn: FiresOn } {
  const t = trigger.type;

  // Built-in special types
  if (t === 'deal_stage_changed') return { object: 'deal', firesOn: 'updated' };
  if (t === 'no_activity_days') {
    const ent = (trigger.params?.entity as string) || 'contact';
    return { object: ent, firesOn: 'any' };
  }
  if (t === 'webhook_inbound') return { object: 'webhook', firesOn: 'any' };

  // Dynamic pattern: {slug}_{event}
  for (const suffix of ['_created', '_updated', '_deleted', '_any'] as const) {
    if (t.endsWith(suffix)) {
      const slug = t.slice(0, -suffix.length);
      const firesOn = suffix.slice(1) as FiresOn;
      return { object: slug, firesOn };
    }
  }

  return { object: '', firesOn: 'created' };
}

// --- Build TriggerSpec from object + firesOn ---
function buildTriggerSpec(object: string, firesOn: FiresOn): TriggerSpec {
  if (object === 'webhook') {
    return { type: 'webhook_inbound', params: { source: 'custom' } };
  }
  return { type: `${object}_${firesOn}` };
}

// --- Select styling ---
const selectClass =
  'bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white font-medium focus:border-indigo-500 focus:outline-none cursor-pointer appearance-none transition-colors hover:border-gray-600';

// ============================================================
// Main Component
// ============================================================

export const TriggerConfigPanel: React.FC = () => {
  const { trigger, setTrigger, schema, errors } = useBuilderStore();

  const entityList = useMemo(() => buildEntityList(schema), [schema]);

  const { object, firesOn } = useMemo(() => {
    if (!trigger) return { object: '', firesOn: 'created' as FiresOn };
    return parseTrigger(trigger);
  }, [trigger]);

  const entityLabel = useMemo(() => {
    const e = entityList.find((e) => e.key === object);
    return e?.label || object || '…';
  }, [object, entityList]);

  const handleObjectChange = (newObject: string) => {
    const currentFiresOn = firesOn || 'created';
    setTrigger(buildTriggerSpec(newObject, currentFiresOn));
  };

  const handleFiresOnChange = (newFiresOn: FiresOn) => {
    if (!object) return;
    setTrigger(buildTriggerSpec(object, newFiresOn));
  };

  // For webhook, firesOn is not relevant
  const showFiresOn = object && object !== 'webhook';

  // Fires-on label for preview
  const firesOnLabel = FIRES_ON_OPTIONS.find((o) => o.value === firesOn)?.label?.toLowerCase() || firesOn;

  // Inline errors
  const objectError = errors['trigger.object']?.[0];
  const firesOnError = errors['trigger.firesOn']?.[0];

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Source</h3>
      <p className="text-xs text-gray-400 -mt-2">Choose which object triggers this workflow.</p>

      <div className="p-3 rounded-xl border border-gray-700/50 bg-gray-800/30 space-y-3">
        {/* Object dropdown */}
        <div className="space-y-1.5">
          <label className="text-xs text-gray-500 font-medium uppercase tracking-wider">Object</label>
          <div className="relative">
            <select
              value={object || ''}
              onChange={(e) => handleObjectChange(e.target.value)}
              className={`${selectClass} w-full ${objectError ? '!border-red-500' : ''}`}
              style={{ paddingRight: '2rem' }}
            >
              <option value="" disabled>Select object…</option>
              {entityList.map((e) => (
                <option key={e.key} value={e.key}>{e.icon} {e.label}</option>
              ))}
            </select>
            <ChevronDown />
          </div>
          {objectError && (
            <p className="text-[11px] text-red-400 mt-0.5">⚠ {objectError}</p>
          )}
        </div>

        {/* Fires on selector */}
        {showFiresOn && (
          <div className="space-y-1.5">
            <label className="text-xs text-gray-500 font-medium uppercase tracking-wider">Fires on</label>
            <div className="flex gap-1">
              {FIRES_ON_OPTIONS.map((opt) => (
                <button
                  key={opt.value}
                  onClick={() => handleFiresOnChange(opt.value)}
                  className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-all duration-150 ${
                    firesOn === opt.value
                      ? 'bg-indigo-500 text-white shadow-md shadow-indigo-500/25'
                      : 'bg-gray-800 text-gray-400 hover:text-white hover:bg-gray-700'
                  }`}
                >
                  {opt.label}
                </button>
              ))}
            </div>
            {firesOnError && (
              <p className="text-[11px] text-red-400 mt-0.5">⚠ {firesOnError}</p>
            )}
          </div>
        )}
      </div>

      {/* Preview sentence */}
      {object && (
        <div className="px-3 py-2 rounded-lg bg-indigo-500/5 border border-indigo-500/10">
          <p className="text-xs text-indigo-300/70">
            <span className="text-indigo-400 font-medium">Preview: </span>
            {object === 'webhook'
              ? `When a Webhook receives data`
              : `When a ${entityLabel} is ${firesOnLabel}`}
          </p>
        </div>
      )}
    </div>
  );
};

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
