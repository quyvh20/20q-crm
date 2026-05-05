import React, { useMemo } from 'react';
import type { TriggerSpec } from '../types';
import { useBuilderStore } from '../store';
import type { WorkflowSchema } from '../api';

interface TriggerNodeProps {
  trigger: TriggerSpec | null;
}

/**
 * Builds a compact, human-readable label for the trigger node on the canvas.
 * Supports built-in entities AND custom objects dynamically.
 * Examples:
 *   "Contact is created"
 *   "Contact's Owner changes"
 *   "Deal changes stage → Qualified"
 *   "Subscription is created"
 *   "No activity for 7 days"
 */
function triggerSentence(trigger: TriggerSpec, schema: WorkflowSchema | null): string {
  // Built-in types
  switch (trigger.type) {
    case 'contact_created':
      return 'Contact is created';
    case 'contact_updated': {
      return formatUpdatedSentence('Contact', trigger, schema);
    }
    case 'deal_stage_changed': {
      const to = (trigger.params?.to_stage as string) || '';
      return to ? `Stage → ${to}` : 'Stage changes';
    }
    case 'no_activity_days': {
      const days = (trigger.params?.days as number) || 7;
      const ent = (trigger.params?.entity as string) || 'contact';
      const entLabel = resolveEntityLabel(ent, schema);
      return `${entLabel} idle ${days}d`;
    }
    case 'webhook_inbound': {
      const src = (trigger.params?.source as string) || 'custom';
      return `Webhook (${src})`;
    }
  }

  // Dynamic: custom object triggers
  if (trigger.type.endsWith('_created')) {
    const slug = trigger.type.replace(/_created$/, '');
    return `${resolveEntityLabel(slug, schema)} is created`;
  }
  if (trigger.type.endsWith('_updated')) {
    const slug = trigger.type.replace(/_updated$/, '');
    return formatUpdatedSentence(resolveEntityLabel(slug, schema), trigger, schema);
  }

  return 'Trigger';
}

/** Shared: format an "updated" trigger sentence with optional field/value watch */
function formatUpdatedSentence(entityLabel: string, trigger: TriggerSpec, schema: WorkflowSchema | null): string {
  const wf = trigger.params?.watch_field as string | undefined;
  if (!wf) return `${entityLabel} is updated`;
  let fieldLabel = wf.split('.').pop() || wf;
  if (schema) {
    for (const ent of [...schema.entities, ...(schema.custom_objects || [])]) {
      const f = ent.fields.find((ff) => ff.path === wf);
      if (f) { fieldLabel = f.label; break; }
    }
  }
  const hasVal = trigger.params?.watch_value !== undefined;
  if (hasVal) return `${fieldLabel} → ${String(trigger.params!.watch_value)}`;
  return `${fieldLabel} changes`;
}

/** Resolve a slug to a display label using schema data */
function resolveEntityLabel(slug: string, schema: WorkflowSchema | null): string {
  if (!schema) {
    // Capitalize first letter as fallback
    return slug.charAt(0).toUpperCase() + slug.slice(1);
  }
  for (const ent of [...schema.entities, ...(schema.custom_objects || [])]) {
    if (ent.key === slug) return ent.label;
  }
  return slug.charAt(0).toUpperCase() + slug.slice(1);
}

// Entity icon for trigger type — now dynamic
function triggerIcon(trigger: TriggerSpec, schema: WorkflowSchema | null): string {
  const ICONS: Record<string, string> = {
    contact: '👤',
    deal: '📊',
    webhook: '🔗',
  };

  switch (trigger.type) {
    case 'contact_created':
    case 'contact_updated':
      return '👤';
    case 'deal_stage_changed':
      return '📊';
    case 'no_activity_days':
      return (trigger.params?.entity as string) === 'deal' ? '📊' : '👤';
    case 'webhook_inbound':
      return '🔗';
  }

  // Dynamic: extract slug from {slug}_created or {slug}_updated
  const slug = trigger.type.replace(/_(created|updated)$/, '');
  if (ICONS[slug]) return ICONS[slug];

  // Look up custom object icon from schema
  if (schema) {
    for (const obj of (schema.custom_objects || [])) {
      if (obj.key === slug) return obj.icon || '📦';
    }
  }

  return '📦'; // Default for custom objects
}

export const TriggerNode: React.FC<TriggerNodeProps> = ({ trigger }) => {
  const { selectedNodeId, selectNode, errors, schema } = useBuilderStore();
  const isSelected = selectedNodeId === 'trigger';
  const hasError = !!errors.trigger;

  const label = useMemo(() => {
    if (!trigger) return null;
    return triggerSentence(trigger, schema);
  }, [trigger, schema]);

  const icon = trigger ? triggerIcon(trigger, schema) : '⚡';

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
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">When</p>
          <p className="text-sm font-medium text-white truncate">
            {label || 'Select a trigger…'}
          </p>
        </div>
      </div>
      {hasError && (
        <p className="text-xs text-red-400 mt-2">{errors.trigger?.[0]}</p>
      )}
    </div>
  );
};
