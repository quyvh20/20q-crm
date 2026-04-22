import React from 'react';
import { TRIGGER_LABELS, type TriggerType } from '../types';
import { useBuilderStore } from '../store';

const TRIGGER_TYPES: TriggerType[] = [
  'contact_created', 'contact_updated', 'deal_stage_changed', 'no_activity_days', 'webhook_inbound',
];

export const TriggerConfigPanel: React.FC = () => {
  const { trigger, setTrigger } = useBuilderStore();

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Trigger Configuration</h3>
      <div>
        <label className="block text-sm text-gray-400 mb-2">Trigger Type</label>
        <select
          value={trigger?.type || ''}
          onChange={(e) => setTrigger({ type: e.target.value as TriggerType, params: trigger?.params || {} })}
          className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-indigo-500 focus:outline-none"
        >
          <option value="">Select trigger...</option>
          {TRIGGER_TYPES.map((t) => (
            <option key={t} value={t}>{TRIGGER_LABELS[t]}</option>
          ))}
        </select>
      </div>

      {trigger?.type === 'deal_stage_changed' && (
        <>
          <div>
            <label className="block text-sm text-gray-400 mb-2">From Stage</label>
            <input
              type="text"
              value={(trigger.params?.from_stage as string) || ''}
              onChange={(e) => setTrigger({ ...trigger, params: { ...trigger.params, from_stage: e.target.value || '*' } })}
              placeholder="* (any stage)"
              className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-2">To Stage</label>
            <input
              type="text"
              value={(trigger.params?.to_stage as string) || ''}
              onChange={(e) => setTrigger({ ...trigger, params: { ...trigger.params, to_stage: e.target.value } })}
              placeholder="e.g. won"
              className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-indigo-500 focus:outline-none"
            />
          </div>
        </>
      )}

      {trigger?.type === 'no_activity_days' && (
        <>
          <div>
            <label className="block text-sm text-gray-400 mb-2">Days of Inactivity</label>
            <input
              type="number"
              min={1}
              value={(trigger.params?.days as number) || 7}
              onChange={(e) => setTrigger({ ...trigger, params: { ...trigger.params, days: parseInt(e.target.value) || 7 } })}
              className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-indigo-500 focus:outline-none"
            />
          </div>
          <div>
            <label className="block text-sm text-gray-400 mb-2">Entity</label>
            <select
              value={(trigger.params?.entity as string) || 'contact'}
              onChange={(e) => setTrigger({ ...trigger, params: { ...trigger.params, entity: e.target.value } })}
              className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-indigo-500 focus:outline-none"
            >
              <option value="contact">Contact</option>
              <option value="deal">Deal</option>
            </select>
          </div>
        </>
      )}

      {trigger?.type === 'webhook_inbound' && (
        <div>
          <label className="block text-sm text-gray-400 mb-2">Source</label>
          <select
            value={(trigger.params?.source as string) || 'custom'}
            onChange={(e) => setTrigger({ ...trigger, params: { ...trigger.params, source: e.target.value } })}
            className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-indigo-500 focus:outline-none"
          >
            <option value="custom">Custom</option>
            <option value="typeform">Typeform</option>
          </select>
        </div>
      )}
    </div>
  );
};
