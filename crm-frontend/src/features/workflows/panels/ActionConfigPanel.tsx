import React, { useMemo, useState, useEffect } from 'react';
import { ACTION_LABELS, ACTION_ICONS, type ActionSpec } from '../types';
import { useBuilderStore } from '../store';
import { TemplateInput } from './inputs';
import { FieldPicker, type FieldMeta } from './FieldPicker';
import { SmartValueInput } from './SmartValueInput';
import type { SchemaField } from '../api';
import { findFieldInSchema } from '../useSchema';

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
      {action.type === 'update_contact' && <UpdateContactParams action={action} setParam={setParam} />}

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

// --- Update Contact params ---

const UPDATE_OPERATIONS = [
  { value: 'set',       label: 'Set',       icon: '✏️', description: 'Set field to a specific value' },
  { value: 'add',       label: 'Add',       icon: '➕', description: 'Add items to an array field (tags)' },
  { value: 'remove',    label: 'Remove',    icon: '➖', description: 'Remove items from an array field (tags)' },
  { value: 'increment', label: 'Increment', icon: '⬆️', description: 'Increase a number field by a value' },
  { value: 'decrement', label: 'Decrement', icon: '⬇️', description: 'Decrease a number field by a value' },
  { value: 'clear',     label: 'Clear',     icon: '🗑️', description: 'Remove the field value entirely' },
] as const;

type UpdateOperation = typeof UPDATE_OPERATIONS[number]['value'];

/** Which operations are valid for each field type */
function getOperationsForFieldType(fieldType: string, pickerType?: string): UpdateOperation[] {
  if (pickerType === 'tag') return ['add', 'remove', 'set', 'clear'];
  if (fieldType === 'array') return ['add', 'remove', 'set', 'clear'];
  if (fieldType === 'number') return ['set', 'increment', 'decrement', 'clear'];
  // string, boolean, select, date, user, stage
  return ['set', 'clear'];
}

const UpdateContactParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const schema = useBuilderStore((s) => s.schema);
  const updateAction = useBuilderStore((s) => s.updateAction);

  const fieldPath = String(action.params.field || '');
  const operation = String(action.params.operation || 'set') as UpdateOperation;

  // Resolve the selected field from schema
  const selectedField: SchemaField | null = useMemo(
    () => findFieldInSchema(schema, fieldPath),
    [schema, fieldPath],
  );

  // Get valid operations for the current field
  const validOps = useMemo(
    () => selectedField
      ? getOperationsForFieldType(selectedField.type, selectedField.picker_type)
      : (['set', 'clear'] as UpdateOperation[]),
    [selectedField],
  );

  // When field changes, auto-reset operation if it's no longer valid
  const handleFieldChange = (path: string, _meta: FieldMeta) => {
    const field = findFieldInSchema(schema, path);
    const newValidOps = field
      ? getOperationsForFieldType(field.type, field.picker_type)
      : ['set', 'clear'];

    const patch: Record<string, unknown> = { field: path, value: undefined };

    // If current operation is invalid for the new field, reset to first valid
    if (!newValidOps.includes(operation)) {
      patch.operation = newValidOps[0];
    }

    updateAction(action.id, { params: patch });
  };

  const handleOperationChange = (op: string) => {
    const patch: Record<string, unknown> = { operation: op };
    // Clear value when switching to 'clear'
    if (op === 'clear') {
      patch.value = undefined;
    }
    updateAction(action.id, { params: patch });
  };

  // Determine if the value input needs the SmartValueInput or a simple number input
  const needsValue = operation !== 'clear';
  const isNumericOp = operation === 'increment' || operation === 'decrement';

  return (
    <div className="space-y-3">
      {/* Field picker — contact fields only */}
      <div>
        <label className="block text-sm text-gray-400 mb-1">Field</label>
        <FieldPicker
          value={fieldPath || null}
          onChange={handleFieldChange}
          entities={['contact']}
          placeholder="Select a contact field…"
        />
      </div>

      {/* Operation picker */}
      {fieldPath && (
        <div>
          <label className="block text-sm text-gray-400 mb-1">Operation</label>
          <div className="grid grid-cols-3 gap-1.5">
            {UPDATE_OPERATIONS.filter((op) => validOps.includes(op.value)).map((op) => (
              <button
                key={op.value}
                type="button"
                onClick={() => handleOperationChange(op.value)}
                title={op.description}
                className={`
                  flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg text-xs font-medium transition-all
                  ${operation === op.value
                    ? 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/40 shadow-sm shadow-emerald-500/10'
                    : 'bg-gray-800 text-gray-400 border border-gray-700 hover:text-white hover:border-gray-600'
                  }
                `}
              >
                <span>{op.icon}</span>
                <span>{op.label}</span>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* Value input — adapts to field type */}
      {fieldPath && needsValue && (
        <div>
          <label className="block text-sm text-gray-400 mb-1">
            {isNumericOp ? `${operation === 'increment' ? 'Increase' : 'Decrease'} by` : 'Value'}
          </label>
          {isNumericOp ? (
            <input
              type="number"
              min={1}
              value={String(action.params.value ?? 1)}
              onChange={(e) => {
                const v = parseInt(e.target.value);
                setParam('value', isNaN(v) ? 1 : v);
              }}
              className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
            />
          ) : selectedField ? (
            <SmartValueInput
              field={selectedField}
              operator={operation === 'add' || operation === 'remove' ? 'contains' : 'eq'}
              value={action.params.value}
              onChange={(v) => setParam('value', v)}
            />
          ) : (
            <input
              type="text"
              value={String(action.params.value || '')}
              onChange={(e) => setParam('value', e.target.value)}
              placeholder="Enter value…"
              className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-white focus:border-emerald-500 focus:outline-none"
            />
          )}
        </div>
      )}

      {/* Clear confirmation */}
      {fieldPath && operation === 'clear' && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-amber-500/10 border border-amber-500/30">
          <span className="text-sm">⚠️</span>
          <span className="text-xs text-amber-400">
            This will remove the value of <span className="font-medium text-amber-300">{selectedField?.label || fieldPath}</span> on the contact.
          </span>
        </div>
      )}

      {/* No field selected hint */}
      {!fieldPath && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-gray-800/50 border border-gray-700/50">
          <span className="text-sm">💡</span>
          <span className="text-xs text-gray-500">
            Select a contact field above, then choose how to modify it.
          </span>
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

const DELAY_UNITS = [
  { value: 'seconds', label: 'Seconds', factor: 1 },
  { value: 'minutes', label: 'Minutes', factor: 60 },
  { value: 'hours',   label: 'Hours',   factor: 3600 },
  { value: 'days',    label: 'Days',    factor: 86400 },
] as const;

type DelayUnit = typeof DELAY_UNITS[number]['value'];

/**
 * Decompose total seconds into the best-fitting (value, unit) pair.
 *
 * **Rule: Integer-only decomposition (no fractional units).**
 * Picks the largest unit where `totalSec % factor === 0`:
 *   - 86400  → { 1, 'days' }
 *   - 7200   → { 2, 'hours' }
 *   - 90     → { 90, 'seconds' }  (not 1.5 minutes)
 *   - 7201   → { 7201, 'seconds' } (not "2 hours 1 second")
 *
 * This avoids floating-point display values and ensures the input always
 * shows a clean whole number that round-trips losslessly through seconds.
 */
function decomposeSeconds(totalSec: number): { value: number; unit: DelayUnit } {
  if (totalSec <= 0) return { value: 1, unit: 'minutes' };
  for (let i = DELAY_UNITS.length - 1; i >= 1; i--) {
    const u = DELAY_UNITS[i];
    if (totalSec % u.factor === 0) {
      return { value: totalSec / u.factor, unit: u.value };
    }
  }
  return { value: totalSec, unit: 'seconds' };
}

const MAX_DELAY_SEC = 2592000; // 30 days

const DelayParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const totalSec = Number(action.params.duration_sec) || 60;
  const decomposed = useMemo(() => decomposeSeconds(totalSec), [totalSec]);

  // Local state lets user clear the field to type a new value without it snapping back to 1
  const [inputValue, setInputValue] = useState(String(decomposed.value));

  // Sync local input when the store value changes externally (load, unit switch)
  useEffect(() => {
    setInputValue(String(decomposed.value));
  }, [decomposed.value]);

  const currentFactor = DELAY_UNITS.find((u) => u.value === decomposed.unit)!.factor;

  const handleValueChange = (raw: string) => {
    setInputValue(raw); // always update the visible text immediately
    const parsed = parseInt(raw);
    if (!isNaN(parsed) && parsed > 0) {
      setParam('duration_sec', parsed * currentFactor);
    }
  };

  const handleBlur = () => {
    // If the user left the field empty or invalid, restore the last valid value
    const parsed = parseInt(inputValue);
    if (isNaN(parsed) || parsed <= 0) {
      setInputValue(String(decomposed.value));
    }
  };

  const handleUnitChange = (unit: string) => {
    const factor = DELAY_UNITS.find((u) => u.value === unit)!.factor;
    const currentValue = parseInt(inputValue) || decomposed.value;
    setParam('duration_sec', currentValue * factor);
  };

  const isOverMax = totalSec > MAX_DELAY_SEC;

  // Friendly human-readable summary
  const summary = `${decomposed.value} ${decomposed.value === 1 ? decomposed.unit.replace(/s$/, '') : decomposed.unit}`;

  return (
    <div className="space-y-3">
      <label className="block text-sm text-gray-400 mb-1">Wait Duration</label>
      <div className="flex gap-2">
        <input
          type="number"
          min={1}
          value={inputValue}
          onChange={(e) => handleValueChange(e.target.value)}
          onBlur={handleBlur}
          className={`flex-1 bg-gray-800 border rounded-lg px-3 py-2 text-sm text-white focus:outline-none [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none ${
            isOverMax ? 'border-red-500 focus:border-red-500' : 'border-gray-700 focus:border-emerald-500'
          }`}
        />
        <select
          value={decomposed.unit}
          onChange={(e) => handleUnitChange(e.target.value)}
          className={`w-28 bg-gray-800 border rounded-lg px-3 py-2 text-sm text-white focus:outline-none ${
            isOverMax ? 'border-red-500 focus:border-red-500' : 'border-gray-700 focus:border-emerald-500'
          }`}
        >
          {DELAY_UNITS.map((u) => (
            <option key={u.value} value={u.value}>{u.label}</option>
          ))}
        </select>
      </div>

      {/* Over-max error */}
      {isOverMax && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-red-500/10 border border-red-500/30">
          <span className="text-xs text-red-400">⚠ Duration exceeds the maximum of 30 days (2,592,000 seconds). Reduce it to save.</span>
        </div>
      )}

      {/* Friendly preview */}
      {!isOverMax && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-gray-800/50 border border-gray-700/50">
          <span className="text-sm">⏱️</span>
          <span className="text-xs text-gray-400">
            Workflow will pause for <span className="text-emerald-400 font-medium">{summary}</span>
          </span>
        </div>
      )}

      <p className="text-xs text-gray-500">Max: 30 days (2,592,000 seconds)</p>
    </div>
  );
};

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
