import React, { useMemo } from 'react';
import type { TriggerSpec } from '../types';
import { useBuilderStore } from '../store';
import type { WorkflowSchema } from '../api';

interface TriggerNodeProps {
  trigger: TriggerSpec | null;
}

/** Resolve object slug from trigger type */
function getObjectSlug(type: string): string {
  if (type === 'deal_stage_changed') return 'deal';
  if (type === 'no_activity_days') return 'contact';
  if (type === 'webhook_inbound') return 'webhook';
  for (const suffix of ['_created', '_updated', '_deleted', '_any']) {
    if (type.endsWith(suffix)) return type.slice(0, -suffix.length);
  }
  return '';
}

/** Resolve fires-on label */
function getFiresOnLabel(type: string): string {
  if (type === 'deal_stage_changed') return 'updated';
  if (type === 'no_activity_days') return 'idle';
  if (type === 'webhook_inbound') return 'receives data';
  if (type.endsWith('_created')) return 'created';
  if (type.endsWith('_updated')) return 'updated';
  if (type.endsWith('_deleted')) return 'deleted';
  if (type.endsWith('_any')) return 'any change';
  return '';
}

/** Resolve entity label from schema */
function resolveEntityLabel(slug: string, schema: WorkflowSchema | null): string {
  if (!schema) return slug.charAt(0).toUpperCase() + slug.slice(1);
  for (const ent of [...schema.entities, ...(schema.custom_objects || [])]) {
    if (ent.key === slug) return ent.label;
  }
  return slug.charAt(0).toUpperCase() + slug.slice(1);
}

/** Resolve entity icon from schema */
function resolveEntityIcon(slug: string, schema: WorkflowSchema | null): string {
  const ICONS: Record<string, string> = { contact: '👤', deal: '📊', webhook: '🔗' };
  if (ICONS[slug]) return ICONS[slug];
  if (!schema) return '📦';
  for (const obj of (schema.custom_objects || [])) {
    if (obj.key === slug) return obj.icon || '📦';
  }
  for (const ent of schema.entities) {
    if (ent.key === slug) return ent.icon || '📦';
  }
  return '📦';
}

function triggerSentence(trigger: TriggerSpec, schema: WorkflowSchema | null): string {
  const slug = getObjectSlug(trigger.type);
  const label = resolveEntityLabel(slug, schema);
  const event = getFiresOnLabel(trigger.type);
  if (!slug) return 'Trigger';
  return `${label} — ${event}`;
}

export const TriggerNode: React.FC<TriggerNodeProps> = ({ trigger }) => {
  const { selectedNodeId, selectNode, errors, schema } = useBuilderStore();
  const isSelected = selectedNodeId === 'trigger';
  const hasError = !!errors.trigger;

  const label = useMemo(() => {
    if (!trigger) return null;
    return triggerSentence(trigger, schema);
  }, [trigger, schema]);

  const icon = trigger ? resolveEntityIcon(getObjectSlug(trigger.type), schema) : '⚡';

  return (
    <div
      onClick={() => selectNode('trigger')}
      className={`
        relative p-4 rounded-xl cursor-pointer transition-all duration-200
        border-2 ${hasError ? 'border-red-500' : isSelected ? 'border-indigo-500' : 'border-gray-700'}
        ${isSelected ? 'bg-indigo-500/10 shadow-lg shadow-indigo-500/20' : 'bg-gray-800/80 hover:bg-gray-800'}
      `}
      style={{ minWidth: 280 }}
    >
      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-amber-400 to-orange-500 flex items-center justify-center text-lg">
          {icon}
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">Source</p>
          <p className="text-sm font-medium text-white truncate">
            {label || 'Select a source…'}
          </p>
        </div>
      </div>
      {hasError && (
        <p className="text-xs text-red-400 mt-2">{errors.trigger?.[0]}</p>
      )}
    </div>
  );
};
