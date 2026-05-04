import React, { useMemo } from 'react';
import { ACTION_LABELS, ACTION_ICONS, type ActionSpec } from '../types';
import { useBuilderStore } from '../store';
import { TemplateInput } from './inputs';

export const ActionConfigPanel: React.FC = () => {
  const { selectedNodeId, actions, updateAction } = useBuilderStore();
  const action = actions.find((a) => a.id === selectedNodeId);

  if (!action) return null;

  const setParam = (key: string, value: unknown) => {
    updateAction(action.id, { params: { [key]: value } });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-emerald-400 to-teal-500 flex items-center justify-center text-sm">
          {ACTION_ICONS[action.type]}
        </div>
        <h3 className="text-lg font-semibold text-white">{ACTION_LABELS[action.type]}</h3>
      </div>

      {/* Type-specific param editors */}
      {action.type === 'send_email' && <EmailParams action={action} setParam={setParam} />}
      {action.type === 'create_task' && <TaskParams action={action} setParam={setParam} />}
      {action.type === 'assign_user' && <AssignParams action={action} setParam={setParam} />}
      {action.type === 'send_webhook' && <WebhookParams action={action} setParam={setParam} />}
      {action.type === 'delay' && <DelayParams action={action} setParam={setParam} />}

      <TemplateHelp />
    </div>
  );
};

// --- Param editors per action type ---

interface ParamProps {
  action: ActionSpec;
  setParam: (key: string, value: unknown) => void;
}

const EmailParams: React.FC<ParamProps> = ({ action, setParam }) => (
  <div className="space-y-3">
    <TemplateInput label="To" value={String(action.params.to || '')} onChange={(v) => setParam('to', v)} placeholder="Click {x} to insert contact email" />
    <TemplateInput label="From Name" value={String(action.params.from_name || '')} onChange={(v) => setParam('from_name', v)} placeholder="Your Company" />
    <TemplateInput label="Subject" value={String(action.params.subject || '')} onChange={(v) => setParam('subject', v)} placeholder="Click {x} to insert variables" />
    <TemplateInput
      label="Body HTML"
      value={String(action.params.body_html || '')}
      onChange={(v) => setParam('body_html', v)}
      placeholder="Write your email body — click {x} to insert variables"
      multiline
      rows={6}
      mono
    />
  </div>
);

const TaskParams: React.FC<ParamProps> = ({ action, setParam }) => (
  <div className="space-y-3">
    <TemplateInput label="Title" value={String(action.params.title || '')} onChange={(v) => setParam('title', v)} placeholder="Follow up with {{contact.first_name}}" />
    <div>
      <label className="block text-sm text-gray-400 mb-1">Priority</label>
      <select
        value={String(action.params.priority || 'medium')}
        onChange={(e) => setParam('priority', e.target.value)}
        className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
      >
        <option value="low">Low</option>
        <option value="medium">Medium</option>
        <option value="high">High</option>
      </select>
    </div>
    <Field label="Due in Days" value={action.params.due_in_days} onChange={(v) => setParam('due_in_days', parseInt(String(v)) || 0)} type="number" placeholder="3" />
    <TemplateInput label="Assignee Field" value={String(action.params.assignee_field || '')} onChange={(v) => setParam('assignee_field', v)} placeholder="contact.owner_id" />
  </div>
);

const AssignParams: React.FC<ParamProps> = ({ action, setParam }) => (
  <div className="space-y-3">
    <div>
      <label className="block text-sm text-gray-400 mb-1">Entity</label>
      <select
        value={String(action.params.entity || 'contact')}
        onChange={(e) => setParam('entity', e.target.value)}
        className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
      >
        <option value="contact">Contact</option>
        <option value="deal">Deal</option>
      </select>
    </div>
    <div>
      <label className="block text-sm text-gray-400 mb-1">Strategy</label>
      <select
        value={String(action.params.strategy || 'round_robin')}
        onChange={(e) => setParam('strategy', e.target.value)}
        className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
      >
        <option value="specific">Specific User</option>
        <option value="round_robin">Round Robin</option>
        <option value="least_loaded">Least Loaded</option>
      </select>
    </div>
    {action.params.strategy === 'specific' && (
      <Field label="User ID" value={action.params.user_id} onChange={(v) => setParam('user_id', v)} placeholder="UUID" />
    )}
  </div>
);

const WebhookParams: React.FC<ParamProps> = ({ action, setParam }) => (
  <div className="space-y-3">
    <TemplateInput label="URL" value={String(action.params.url || '')} onChange={(v) => setParam('url', v)} placeholder="https://example.com/webhook" />
    <div>
      <label className="block text-sm text-gray-400 mb-1">Method</label>
      <select
        value={String(action.params.method || 'POST')}
        onChange={(e) => setParam('method', e.target.value)}
        className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
      >
        <option value="POST">POST</option>
        <option value="PUT">PUT</option>
      </select>
    </div>
    <TemplateInput
      label="Body Template"
      value={String(action.params.body_template || '')}
      onChange={(v) => setParam('body_template', v)}
      placeholder='{"email": "{{contact.email}}"}'
      multiline
      rows={4}
      mono
    />
    <Field label="Timeout (sec)" value={action.params.timeout_sec} onChange={(v) => setParam('timeout_sec', parseInt(String(v)) || 10)} type="number" placeholder="10" />
  </div>
);

const DelayParams: React.FC<ParamProps> = ({ action, setParam }) => (
  <div className="space-y-3">
    <Field
      label="Duration (seconds)"
      value={action.params.duration_sec}
      onChange={(v) => setParam('duration_sec', parseInt(String(v)) || 60)}
      type="number"
      placeholder="60"
    />
    <p className="text-xs text-gray-500">Max: 86400 seconds (24 hours)</p>
  </div>
);

// --- Shared form field (kept for non-template fields like numbers) ---

interface FieldProps {
  label: string;
  value: unknown;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
}

const Field: React.FC<FieldProps> = ({ label, value, onChange, placeholder, type = 'text' }) => (
  <div>
    <label className="block text-sm text-gray-400 mb-1">{label}</label>
    <input
      type={type}
      value={String(value || '')}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
    />
  </div>
);

// --- Template variables reference ---

const TemplateHelp: React.FC = () => {
  const schema = useBuilderStore((s) => s.schema);
  const schemaLoading = useBuilderStore((s) => s.schemaLoading);
  const schemaError = useBuilderStore((s) => s.schemaError);
  const invalidateSchema = useBuilderStore((s) => s.invalidateSchema);

  // Build template variables from schema — no hardcoded fallback
  const variables = useMemo(() => {
    if (!schema) return [];
    const allEntities = [...schema.entities, ...(schema.custom_objects || [])];
    return allEntities.flatMap((e) =>
      e.fields.map((f) => ({ path: f.path, label: `${e.label}: ${f.label}` }))
    );
  }, [schema]);

  return (
    <div className="mt-4 pt-4 border-t border-gray-700">
      <p className="text-xs text-gray-500 mb-2">Available Template Variables</p>
      {schemaLoading ? (
        <div className="flex flex-wrap gap-1">
          {[...Array(8)].map((_, i) => (
            <div
              key={i}
              className="h-5 rounded bg-gray-800 animate-pulse"
              style={{ width: `${60 + Math.random() * 50}px` }}
            />
          ))}
        </div>
      ) : schemaError ? (
        <div className="flex items-center gap-2 p-2 rounded-lg bg-red-500/10 border border-red-500/30">
          <span className="text-xs text-red-400 flex-1">Failed to load variables</span>
          <button
            onClick={invalidateSchema}
            className="text-xs text-red-300 hover:text-white underline"
          >
            Retry
          </button>
        </div>
      ) : variables.length === 0 ? (
        <p className="text-xs text-gray-600 italic">No template variables available</p>
      ) : (
        <div className="flex flex-wrap gap-1">
          {variables.map((v) => (
            <button
              key={v.path}
              onClick={() => {
                navigator.clipboard.writeText(`{{${v.path}}}`);
              }}
              title={`Copy {{${v.path}}}`}
              className="px-2 py-0.5 rounded bg-gray-800 text-xs text-gray-400 hover:text-white hover:bg-gray-700 transition-colors font-mono"
            >
              {v.path}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};
