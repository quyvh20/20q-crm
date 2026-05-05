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
    <TemplateInput label="To" value={String(action.params.to || '')} onChange={(v) => setParam('to', v)} placeholder="Click {x} to insert contact email" fieldFilter="email" />
    <TemplateInput label="CC" value={String(action.params.cc || '')} onChange={(v) => setParam('cc', v)} placeholder="Separate multiple addresses with commas" fieldFilter="email" />
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

const TaskParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const schema = useBuilderStore((s) => s.schema);
  const users = schema?.users || [];

  // Determine assignee mode from current value
  const assigneeValue = String(action.params.assignee_field || '');
  const isContactOwner = assigneeValue === '' || assigneeValue === 'contact.owner_id';

  return (
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

      {/* Assignee — segmented: Contact Owner vs Specific User */}
      <div>
        <label className="block text-sm text-gray-400 mb-1">Assign To</label>
        <div className="flex rounded-lg overflow-hidden border border-gray-700 mb-2">
          <button
            type="button"
            onClick={() => setParam('assignee_field', 'contact.owner_id')}
            className={`flex-1 px-3 py-1.5 text-xs font-medium transition-colors ${
              isContactOwner
                ? 'bg-emerald-500/20 text-emerald-300 border-r border-emerald-500/30'
                : 'bg-gray-800 text-gray-400 hover:text-white border-r border-gray-700'
            }`}
          >
            👤 Contact Owner
          </button>
          <button
            type="button"
            onClick={() => setParam('assignee_field', '__pick_user__')}
            className={`flex-1 px-3 py-1.5 text-xs font-medium transition-colors ${
              !isContactOwner
                ? 'bg-emerald-500/20 text-emerald-300'
                : 'bg-gray-800 text-gray-400 hover:text-white'
            }`}
          >
            🎯 Specific User
          </button>
        </div>

        {isContactOwner ? (
          <p className="text-xs text-gray-500 italic">Task will be assigned to the contact's current owner.</p>
        ) : (
          <select
            value={users.some((u) => u.id === assigneeValue) ? assigneeValue : ''}
            onChange={(e) => setParam('assignee_field', e.target.value || '__pick_user__')}
            className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
          >
            <option value="">Select a user…</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>{u.name} ({u.email})</option>
            ))}
          </select>
        )}
      </div>
    </div>
  );
};

const AssignParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const schema = useBuilderStore((s) => s.schema);
  const updateAction = useBuilderStore((s) => s.updateAction);
  const users = schema?.users || [];

  // pool is stored as string[] of UUIDs
  const pool: string[] = Array.isArray(action.params.pool) ? action.params.pool as string[] : [];

  const togglePoolUser = (userId: string) => {
    const next = pool.includes(userId)
      ? pool.filter((id) => id !== userId)
      : [...pool, userId];
    // Use updateAction directly so we replace the full array (setParam merges shallowly)
    updateAction(action.id, { params: { pool: next } });
  };

  const strategy = String(action.params.strategy || 'round_robin');

  /** Migrate user data across strategy switches so selections aren't lost */
  const handleStrategyChange = (nextStrategy: string) => {
    const prevStrategy = strategy;
    if (nextStrategy === prevStrategy) return;

    const patch: Record<string, unknown> = { strategy: nextStrategy };

    // specific → round_robin: seed pool with the single user_id
    if (prevStrategy === 'specific' && nextStrategy === 'round_robin') {
      const uid = String(action.params.user_id || '');
      if (uid && users.some((u) => u.id === uid)) {
        patch.pool = [uid];
      }
    }

    // round_robin → specific: take first pool member as user_id
    if (prevStrategy === 'round_robin' && nextStrategy === 'specific') {
      if (pool.length > 0) {
        patch.user_id = pool[0];
      }
    }

    updateAction(action.id, { params: patch });
  };

  return (
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
          value={strategy}
          onChange={(e) => handleStrategyChange(e.target.value)}
          className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
        >
          <option value="specific">Specific User</option>
          <option value="round_robin">Round Robin</option>
          <option value="least_loaded">Least Loaded</option>
        </select>
      </div>

      {/* Specific → single user dropdown */}
      {strategy === 'specific' && (
        <div>
          <label className="block text-sm text-gray-400 mb-1">User</label>
          <select
            value={users.some((u) => u.id === String(action.params.user_id || '')) ? String(action.params.user_id) : ''}
            onChange={(e) => setParam('user_id', e.target.value || '')}
            className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
          >
            <option value="">Select a user…</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>{u.name} ({u.email})</option>
            ))}
          </select>
          <p className="text-xs text-gray-500 mt-1">Always assign to this user.</p>
        </div>
      )}

      {/* Round Robin → multi-user pool picker */}
      {strategy === 'round_robin' && (
        <div>
          <label className="block text-sm text-gray-400 mb-1">
            User Pool{' '}
            <span className="text-gray-600">({pool.length} selected)</span>
          </label>
          <div className="max-h-48 overflow-y-auto rounded-lg border border-gray-700 bg-gray-800 divide-y divide-gray-700/50">
            {users.length === 0 ? (
              <p className="px-3 py-2 text-xs text-gray-500 italic">No users available</p>
            ) : (
              users.map((u) => {
                const checked = pool.includes(u.id);
                return (
                  <label
                    key={u.id}
                    className={`flex items-center gap-3 px-3 py-2 cursor-pointer transition-colors ${
                      checked ? 'bg-emerald-500/10' : 'hover:bg-gray-700/50'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => togglePoolUser(u.id)}
                      className="rounded border-gray-600 bg-gray-900 text-emerald-500 focus:ring-emerald-500 focus:ring-offset-0 h-4 w-4"
                    />
                    <div className="flex flex-col min-w-0">
                      <span className="text-sm text-white truncate">{u.name}</span>
                      <span className="text-xs text-gray-500 truncate">{u.email}</span>
                    </div>
                  </label>
                );
              })
            )}
          </div>
          <p className="text-xs text-gray-500 mt-1">
            Distributes evenly across selected users by existing assignment count.
          </p>
          {pool.length === 0 && (
            <p className="text-xs text-amber-400 mt-1">⚠ Select at least one user for round robin.</p>
          )}
        </div>
      )}

      {/* Least Loaded → no picker needed */}
      {strategy === 'least_loaded' && (
        <div className="rounded-lg bg-gray-800/50 border border-gray-700 px-3 py-2">
          <p className="text-xs text-gray-400">
            Automatically assigns to the team member with the fewest{' '}
            {String(action.params.entity || 'contact')}s in your org.
          </p>
        </div>
      )}
    </div>
  );
};

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
      placeholder='{"key": "value"} — click {x} to insert variables'
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
