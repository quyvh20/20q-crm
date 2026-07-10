import React, { useMemo, useState, useEffect } from 'react';
import { User, Target, Phone, Users, FileText, Mail, Plus, X, Bell } from 'lucide-react';
import type { ActionSpec } from '../../types';
import { useBuilderStore } from '../../store';
import { TemplateInput } from './inputs';
import { useEmailTemplates, useWorkflowsList } from '../../queries';
import { FieldPicker, type FieldMeta } from './FieldPicker';
import { SmartValueInput } from './SmartValueInput';
import type { SchemaField, WorkflowSchema, SchemaEntity } from '../../api';
import { findFieldInSchema } from '../../useSchema';
import { browserTimeZone, allTimeZones } from '../../cron';
import {
  type OffsetDirection,
  offsetToDirection,
  directionToOffset,
  describeWaitUntil,
  resolvableObjectsForTrigger,
  triggerOwnerObject,
  triggerPrimaryObject,
  DEFAULT_AT_TIME,
} from '../../dateField';

export const ActionConfig: React.FC = () => {
  const { selectedNodeId, actions, updateAction } = useBuilderStore();
  const action = actions.find((a) => a.id === selectedNodeId);

  if (!action) return null;

  const setParam = (key: string, value: unknown) => {
    updateAction(action.id, { params: { [key]: value } });
  };

  return (
    <div className="space-y-4">
      {/* Header (icon + title + delete) is provided by ConfigPanel's shell. */}
      {/* Type-specific param editors */}
      {action.type === 'send_email' && <EmailParams action={action} setParam={setParam} />}
      {action.type === 'create_task' && <TaskParams action={action} setParam={setParam} />}
      {action.type === 'assign_user' && <AssignParams action={action} setParam={setParam} />}
      {action.type === 'send_webhook' && <WebhookParams action={action} setParam={setParam} />}
      {action.type === 'delay' && <DelayParams action={action} setParam={setParam} />}
      {action.type === 'update_record' && <UpdateRecordParams action={action} setParam={setParam} />}
      {action.type === 'create_record' && <CreateRecordParams action={action} setParam={setParam} />}
      {action.type === 'find_records' && <FindRecordsParams action={action} setParam={setParam} />}
      {action.type === 'enroll_records' && <EnrollRecordsParams action={action} setParam={setParam} />}
      {action.type === 'ai_generate' && <AIGenerateParams action={action} setParam={setParam} />}
      {action.type === 'log_activity' && <ActivityParams action={action} setParam={setParam} />}
      {action.type === 'notify_user' && <NotifyParams action={action} setParam={setParam} />}
      {/* backward compat for saved workflows */}
      {(action.type as string) === 'update_contact' && <UpdateRecordParams action={action} setParam={setParam} />}

      <TemplateHelp />
    </div>
  );
};

// --- Param editors per action type ---

interface ParamProps {
  action: ActionSpec;
  setParam: (key: string, value: unknown) => void;
}

const EmailParams: React.FC<ParamProps> = ({ action, setParam }) => {
  // A5: an optional library template supplies subject/body. Inline fields still
  // work and override the template per-action (matching the executor).
  const { data: tmplData } = useEmailTemplates();
  const templates = tmplData?.templates ?? [];
  const templateId = String(action.params.template_id || '');
  const usingTemplate = templateId !== '';
  const templateMissing = usingTemplate && !templates.some((t) => t.id === templateId);

  return (
    <div className="space-y-3">
      {/* Template picker */}
      <div>
        <div className="mb-1 flex items-center justify-between">
          <label className="block text-sm text-muted-foreground">Template</label>
          <a
            href="/workflows/email-templates"
            target="_blank"
            rel="noreferrer"
            className="text-xs text-primary hover:underline"
          >
            Manage templates
          </a>
        </div>
        <select
          value={templateId}
          onChange={(e) => setParam('template_id', e.target.value)}
          className="w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Write inline</option>
          {templateMissing && <option value={templateId}>(template not found)</option>}
          {templates.map((t) => (
            <option key={t.id} value={t.id}>{t.name}</option>
          ))}
        </select>
        {usingTemplate && (
          <p className="mt-1 text-xs text-muted-foreground">
            Subject &amp; body come from this template. Fill the fields below only to override them for this action.
          </p>
        )}
      </div>

      <TemplateInput label="To" value={String(action.params.to || '')} onChange={(v) => setParam('to', v)} placeholder="Click {x} to insert contact email" fieldFilter="email" />
      <TemplateInput label="CC" value={String(action.params.cc || '')} onChange={(v) => setParam('cc', v)} placeholder="Separate multiple addresses with commas" fieldFilter="email" />
      <TemplateInput label="From Name" value={String(action.params.from_name || '')} onChange={(v) => setParam('from_name', v)} placeholder="Your Company" />
      <TemplateInput label={usingTemplate ? 'Subject (override)' : 'Subject'} value={String(action.params.subject || '')} onChange={(v) => setParam('subject', v)} placeholder={usingTemplate ? 'Leave blank to use the template subject' : 'Click {x} to insert variables'} />
      <TemplateInput
        label={usingTemplate ? 'Body HTML (override)' : 'Body HTML'}
        value={String(action.params.body_html || '')}
        onChange={(v) => setParam('body_html', v)}
        placeholder={usingTemplate ? 'Leave blank to use the template body' : 'Write your email body — click {x} to insert variables'}
        multiline
        rows={6}
        mono
      />
    </div>
  );
};

// --- Log Activity params ---

const ACTIVITY_TYPES = [
  { value: 'call',    label: 'Call',    Icon: Phone },
  { value: 'meeting', label: 'Meeting', Icon: Users },
  { value: 'note',    label: 'Note',    Icon: FileText },
  { value: 'email',   label: 'Email',   Icon: Mail },
] as const;

const ActivityParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const raw = String(action.params.activity_type || '');
  // Unknown/absent values display Note as selected (Req 5.5)
  const selected = ACTIVITY_TYPES.some((t) => t.value === raw) ? raw : 'note';

  return (
    <div className="space-y-3">
      {/* Type picker — segmented buttons (Req 5.1, 5.2, 5.3, 5.4) */}
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Type</label>
        <div className="flex rounded-lg overflow-hidden border border-border">
          {ACTIVITY_TYPES.map((t) => (
            <button
              key={t.value}
              type="button"
              onClick={() => setParam('activity_type', t.value)}
              className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors ${
                selected === t.value
                  ? 'bg-primary/10 text-primary'
                  : 'bg-background text-muted-foreground hover:text-foreground'
              }`}
            >
              <t.Icon className="h-3.5 w-3.5" /> <span>{t.label}</span>
            </button>
          ))}
        </div>
      </div>

      {/* Title + Body — verbatim template storage via TemplateInput (Req 6) */}
      <TemplateInput
        label="Title"
        value={String(action.params.title || '')}
        onChange={(v) => setParam('title', v)}
        placeholder="Logged a call with {{contact.first_name}}"
      />
      <TemplateInput
        label="Body"
        value={String(action.params.body || '')}
        onChange={(v) => setParam('body', v)}
        placeholder="Notes — click {x} to insert variables"
        multiline
        rows={4}
      />
    </div>
  );
};

// --- Notify User params (A6) ---

const NotifyParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const schema = useBuilderStore((s) => s.schema);
  const trigger = useBuilderStore((s) => s.trigger);
  const updateAction = useBuilderStore((s) => s.updateAction);
  const users = schema?.users || [];

  // "Record owner" is only meaningful when the trigger's primary record has an
  // owner (contact/deal). For schedule/company/custom triggers there's no owner to
  // resolve, so the form offers only "Specific user".
  const ownerObject = triggerOwnerObject(trigger);
  const recipient = String(action.params.recipient || 'owner_field');
  const isOwner = recipient !== 'specific';

  const selectOwner = () => {
    updateAction(action.id, {
      params: { recipient: 'owner_field', owner_field: ownerObject ? `${ownerObject}.owner_user_id` : '' },
    });
  };
  const selectSpecific = () => {
    updateAction(action.id, { params: { recipient: 'specific' } });
  };

  const userId = String(action.params.user_id || '');

  return (
    <div className="space-y-3">
      <TemplateInput label="Title" value={String(action.params.title || '')} onChange={(v) => setParam('title', v)} placeholder="Deal {{deal.title}} needs attention" />
      <TemplateInput
        label="Message"
        value={String(action.params.body || '')}
        onChange={(v) => setParam('body', v)}
        placeholder="Details — click {x} to insert variables"
        multiline
        rows={3}
      />

      {/* Recipient — segmented: Record Owner vs Specific User */}
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Notify</label>
        <div className="flex rounded-lg overflow-hidden border border-border mb-2">
          <button
            type="button"
            onClick={selectOwner}
            disabled={!ownerObject}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
              isOwner
                ? 'bg-primary/10 text-primary border-r border-primary/40'
                : 'bg-background text-muted-foreground hover:text-foreground border-r border-border'
            }`}
          >
            <Bell className="h-3.5 w-3.5" /> <span>Record Owner</span>
          </button>
          <button
            type="button"
            onClick={selectSpecific}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors ${
              !isOwner
                ? 'bg-primary/10 text-primary'
                : 'bg-background text-muted-foreground hover:text-foreground'
            }`}
          >
            <Target className="h-3.5 w-3.5" /> <span>Specific User</span>
          </button>
        </div>

        {isOwner ? (
          ownerObject ? (
            <p className="text-xs text-muted-foreground italic">
              Notifies the {ownerObject}'s current owner.
            </p>
          ) : (
            <p className="text-xs text-amber-600 dark:text-amber-400">
              ⚠ This trigger has no record owner — choose a specific user.
            </p>
          )
        ) : (
          <select
            value={users.some((u) => u.id === userId) ? userId : ''}
            onChange={(e) => setParam('user_id', e.target.value)}
            className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
          >
            <option value="">Select a user…</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>{u.name} ({u.email})</option>
            ))}
          </select>
        )}
      </div>

      <TemplateInput
        label="Link (optional)"
        value={String(action.params.link || '')}
        onChange={(v) => setParam('link', v)}
        placeholder="/deals/{{deal.id}} — defaults to the record"
      />
    </div>
  );
};

const TaskParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const schema = useBuilderStore((s) => s.schema);
  const users = schema?.users || [];

  // Determine assignee mode from current value. The owner column is
  // owner_user_id (A2 fix): contact.owner_id never resolved in the event payload,
  // so "Contact Owner" silently produced no assignee. Accept the legacy value so
  // saved actions still show as "Contact Owner"; re-saving migrates them.
  const assigneeValue = String(action.params.assignee_field || '');
  const isContactOwner =
    assigneeValue === '' ||
    assigneeValue === 'contact.owner_user_id' ||
    assigneeValue === 'contact.owner_id';

  return (
    <div className="space-y-3">
      <TemplateInput label="Title" value={String(action.params.title || '')} onChange={(v) => setParam('title', v)} placeholder="Follow up with {{contact.first_name}}" />
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Priority</label>
        <select
          value={String(action.params.priority || 'medium')}
          onChange={(e) => setParam('priority', e.target.value)}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        >
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
        </select>
      </div>
      <Field label="Due in Days" value={action.params.due_in_days} onChange={(v) => setParam('due_in_days', parseInt(String(v)) || 0)} type="number" placeholder="3" />

      {/* Assignee — segmented: Contact Owner vs Specific User */}
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Assign To</label>
        <div className="flex rounded-lg overflow-hidden border border-border mb-2">
          <button
            type="button"
            onClick={() => setParam('assignee_field', 'contact.owner_user_id')}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors ${
              isContactOwner
                ? 'bg-primary/10 text-primary border-r border-primary/40'
                : 'bg-background text-muted-foreground hover:text-foreground border-r border-border'
            }`}
          >
            <User className="h-3.5 w-3.5" /> <span>Contact Owner</span>
          </button>
          <button
            type="button"
            onClick={() => setParam('assignee_field', '__pick_user__')}
            className={`flex-1 flex items-center justify-center gap-1.5 px-3 py-1.5 text-xs font-medium transition-colors ${
              !isContactOwner
                ? 'bg-primary/10 text-primary'
                : 'bg-background text-muted-foreground hover:text-foreground'
            }`}
          >
            <Target className="h-3.5 w-3.5" /> <span>Specific User</span>
          </button>
        </div>

        {isContactOwner ? (
          <p className="text-xs text-muted-foreground italic">Task will be assigned to the contact's current owner.</p>
        ) : (
          <select
            value={users.some((u) => u.id === assigneeValue) ? assigneeValue : ''}
            onChange={(e) => setParam('assignee_field', e.target.value || '__pick_user__')}
            className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
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
        <label className="block text-sm text-muted-foreground mb-1">Entity</label>
        <select
          value={String(action.params.entity || 'contact')}
          onChange={(e) => setParam('entity', e.target.value)}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        >
          <option value="contact">Contact</option>
          <option value="deal">Deal</option>
        </select>
      </div>
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Strategy</label>
        <select
          value={strategy}
          onChange={(e) => handleStrategyChange(e.target.value)}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        >
          <option value="specific">Specific User</option>
          <option value="round_robin">Round Robin</option>
          <option value="least_loaded">Least Loaded</option>
        </select>
      </div>

      {/* Specific → single user dropdown */}
      {strategy === 'specific' && (
        <div>
          <label className="block text-sm text-muted-foreground mb-1">User</label>
          <select
            value={users.some((u) => u.id === String(action.params.user_id || '')) ? String(action.params.user_id) : ''}
            onChange={(e) => setParam('user_id', e.target.value || '')}
            className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
          >
            <option value="">Select a user…</option>
            {users.map((u) => (
              <option key={u.id} value={u.id}>{u.name} ({u.email})</option>
            ))}
          </select>
          <p className="text-xs text-muted-foreground mt-1">Always assign to this user.</p>
        </div>
      )}

      {/* Round Robin → multi-user pool picker */}
      {strategy === 'round_robin' && (
        <div>
          <label className="block text-sm text-muted-foreground mb-1">
            User Pool{' '}
            <span className="text-muted-foreground/70">({pool.length} selected)</span>
          </label>
          <div className="max-h-48 overflow-y-auto rounded-lg border border-border bg-background divide-y divide-border/60">
            {users.length === 0 ? (
              <p className="px-3 py-2 text-xs text-muted-foreground italic">No users available</p>
            ) : (
              users.map((u) => {
                const checked = pool.includes(u.id);
                return (
                  <label
                    key={u.id}
                    className={`flex items-center gap-3 px-3 py-2 cursor-pointer transition-colors ${
                      checked ? 'bg-primary/10' : 'hover:bg-accent hover:text-accent-foreground'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => togglePoolUser(u.id)}
                      className="rounded border-border bg-background text-primary focus:ring-ring focus:ring-offset-0 h-4 w-4"
                    />
                    <div className="flex flex-col min-w-0">
                      <span className="text-sm text-foreground truncate">{u.name}</span>
                      <span className="text-xs text-muted-foreground truncate">{u.email}</span>
                    </div>
                  </label>
                );
              })
            )}
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            Distributes evenly across selected users by existing assignment count.
          </p>
          {pool.length === 0 && (
            <p className="text-xs text-amber-600 dark:text-amber-400 mt-1">⚠ Select at least one user for round robin.</p>
          )}
        </div>
      )}

      {/* Least Loaded → no picker needed */}
      {strategy === 'least_loaded' && (
        <div className="rounded-lg bg-muted/40 border border-border px-3 py-2">
          <p className="text-xs text-muted-foreground">
            Automatically assigns to the team member with the fewest{' '}
            {String(action.params.entity || 'contact')}s in your org.
          </p>
        </div>
      )}
    </div>
  );
};

// --- Update Record params ---

// Data contract: params.updates = [{ field, op, value }, ...]
// Each FieldUpdate row lets user pick a field (from trigger entity), an operation, and a value.

const UPDATE_OPERATIONS = [
  { value: 'set',       label: 'Set',       description: 'Set field to a specific value' },
  { value: 'add',       label: 'Add',       description: 'Add items to an array field (tags)' },
  { value: 'remove',    label: 'Remove',    description: 'Remove items from an array field (tags)' },
  { value: 'increment', label: 'Increment', description: 'Increase a number field by a value' },
  { value: 'decrement', label: 'Decrement', description: 'Decrease a number field by a value' },
  { value: 'clear',     label: 'Clear',     description: 'Remove the field value entirely' },
] as const;

type UpdateOperation = typeof UPDATE_OPERATIONS[number]['value'];

interface FieldUpdateEntry {
  field: string;
  op: UpdateOperation;
  value?: unknown;
}

/** Which operations are valid for each field type */
function getOperationsForFieldType(fieldType: string, pickerType?: string): UpdateOperation[] {
  // Deal stage: managed field — moving to a stage is the only meaningful op.
  // The backend routes this through changeDealStage (activity log + won/lost flags).
  if (pickerType === 'stage') return ['set'];
  // Tags / array: all array ops
  if (pickerType === 'tag') return ['set', 'add', 'remove', 'clear'];
  if (fieldType === 'array') return ['set', 'add', 'remove', 'clear'];
  // Numbers: set, add (→set), increment, decrement, clear
  if (fieldType === 'number') return ['set', 'add', 'increment', 'decrement', 'clear'];
  // Scalars (string, boolean, select, date, user):
  // set, add (→set fallback), clear. 'remove' excluded for scalars.
  return ['set', 'add', 'clear'];
}

/** Resolve which entity to show fields for based on the workflow's trigger type */
function resolveEntityFromTrigger(triggerType?: string): string[] {
  if (!triggerType) return ['contact'];
  if (triggerType.startsWith('contact')) return ['contact'];
  if (triggerType.startsWith('deal')) return ['deal'];
  // Custom objects: e.g. "ticket_created" → slug = "ticket"
  const suffixes = ['_created', '_updated', '_deleted', '_any'];
  for (const suffix of suffixes) {
    if (triggerType.endsWith(suffix)) {
      const slug = triggerType.slice(0, -suffix.length);
      if (slug) return [slug];
    }
  }
  return ['contact'];
}

const UpdateRecordParams: React.FC<ParamProps> = ({ action }) => {
  const schema = useBuilderStore((s) => s.schema);
  const updateAction = useBuilderStore((s) => s.updateAction);
  const trigger = useBuilderStore((s) => s.trigger);

  const entities = useMemo(() => resolveEntityFromTrigger(trigger?.type), [trigger?.type]);
  // Resolve entity label/icon from schema (supports custom objects)
  const entityKey = entities[0];
  const entityMeta = useMemo(() => {
    if (!schema) return { label: entityKey, icon: '📝' };
    const all = [...schema.entities, ...(schema.custom_objects || [])];
    const found = all.find(e => e.key === entityKey);
    if (found) return { label: found.label || found.key, icon: (found as any).icon || (entityKey === 'deal' ? '💼' : entityKey === 'contact' ? '👤' : '📦') };
    return { label: entityKey, icon: entityKey === 'deal' ? '💼' : entityKey === 'contact' ? '👤' : '📦' };
  }, [schema, entityKey]);

  // Read updates array from params (or migrate from legacy flat format)
  const updates: FieldUpdateEntry[] = useMemo(() => {
    if (Array.isArray(action.params.updates)) {
      return action.params.updates as FieldUpdateEntry[];
    }
    // Legacy migration: flat { field, operation, value } → updates[0]
    if (action.params.field) {
      return [{
        field: String(action.params.field),
        op: (String(action.params.operation || action.params.op || 'set')) as UpdateOperation,
        value: action.params.value,
      }];
    }
    return [];
  }, [action.params]);

  const setUpdates = (newUpdates: FieldUpdateEntry[]) => {
    // Emit the full updates array, clearing legacy keys
    updateAction(action.id, {
      params: {
        updates: newUpdates,
        // Clear legacy flat keys
        field: undefined,
        operation: undefined,
        op: undefined,
        value: undefined,
      },
    });
  };

  const patchUpdate = (idx: number, patch: Partial<FieldUpdateEntry>) => {
    const next = [...updates];
    next[idx] = { ...next[idx], ...patch };
    setUpdates(next);
  };

  const addUpdate = () => {
    setUpdates([...updates, { field: '', op: 'set' }]);
  };

  const removeUpdate = (idx: number) => {
    setUpdates(updates.filter((_, i) => i !== idx));
  };

  return (
    <div className="space-y-3">
      {/* Entity indicator */}
      <div className="flex items-center gap-2 px-2.5 py-1.5 rounded-lg bg-muted/40 border border-border/60">
        <span className="text-sm">{entityMeta.icon}</span>
        <span className="text-xs text-muted-foreground">
          Updates the <span className="font-medium text-foreground">{entityMeta.label}</span> from the trigger source
        </span>
      </div>

      {updates.map((upd, idx) => (
        <UpdateRow
          key={idx}
          entry={upd}
          index={idx}
          schema={schema}
          entities={entities}
          totalCount={updates.length}
          onPatch={(patch) => patchUpdate(idx, patch)}
          onRemove={() => removeUpdate(idx)}
        />
      ))}

      {/* Add update button */}
      <button
        type="button"
        onClick={addUpdate}
        className="w-full flex items-center justify-center gap-1.5 px-3 py-2 rounded-lg border border-dashed border-border text-xs text-muted-foreground hover:border-primary/40 hover:text-primary transition-all"
      >
        <Plus className="h-3.5 w-3.5" />
        <span>{updates.length === 0 ? 'Add a field update' : 'Add another field update'}</span>
      </button>

      {/* Empty state hint */}
      {updates.length === 0 && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-muted/40 border border-border/60">
          <span className="text-sm">💡</span>
          <span className="text-xs text-muted-foreground">
            Add one or more field updates. Each can set, add, remove, increment, decrement, or clear a {entityMeta.label} field.
          </span>
        </div>
      )}
    </div>
  );
};

/** Single field-update row within the UpdateRecordParams component */
const UpdateRow: React.FC<{
  entry: FieldUpdateEntry;
  index: number;
  schema: WorkflowSchema | null;
  entities: string[];
  totalCount: number;
  onPatch: (patch: Partial<FieldUpdateEntry>) => void;
  onRemove: () => void;
}> = ({ entry, index, schema, entities, totalCount, onPatch, onRemove }) => {
  const selectedField: SchemaField | null = useMemo(
    () => findFieldInSchema(schema, entry.field),
    [schema, entry.field],
  );

  const validOps = useMemo(
    () => selectedField
      ? getOperationsForFieldType(selectedField.type, selectedField.picker_type)
      : (['set', 'clear'] as UpdateOperation[]),
    [selectedField],
  );

  const handleFieldChange = (path: string, _meta: FieldMeta) => {
    const field = findFieldInSchema(schema, path);
    const newValidOps = field
      ? getOperationsForFieldType(field.type, field.picker_type)
      : ['set', 'clear'] as UpdateOperation[];

    const patch: Partial<FieldUpdateEntry> = { field: path, value: undefined };
    if (!newValidOps.includes(entry.op)) {
      patch.op = newValidOps[0] as UpdateOperation;
    }
    onPatch(patch);
  };

  const handleOpChange = (op: string) => {
    const patch: Partial<FieldUpdateEntry> = { op: op as UpdateOperation };
    if (op === 'clear') patch.value = undefined;
    onPatch(patch);
  };

  const needsValue = entry.op !== 'clear';
  const isNumericOp = entry.op === 'increment' || entry.op === 'decrement';

  return (
    <div className="relative border border-border/60 rounded-xl p-3 space-y-2.5 bg-muted/40">
      {/* Row header */}
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wider text-muted-foreground/70 font-medium">
          Update {index + 1}
        </span>
        {totalCount > 1 && (
          <button
            type="button"
            onClick={onRemove}
            title="Remove this update"
            className="text-muted-foreground/70 hover:text-destructive transition-colors p-0.5"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      {/* Field picker — entity determined by trigger type */}
      <FieldPicker
        value={entry.field || null}
        onChange={handleFieldChange}
        entities={entities}
        placeholder={`Select a ${entities[0]} field…`}
      />

      {/* Operation picker */}
      {entry.field && (
        <div className="grid grid-cols-3 gap-1">
          {UPDATE_OPERATIONS.filter((op) => validOps.includes(op.value)).map((op) => (
            <button
              key={op.value}
              type="button"
              onClick={() => handleOpChange(op.value)}
              title={op.description}
              className={`
                flex items-center gap-1 px-2 py-1 rounded-lg text-[11px] font-medium transition-all
                ${entry.op === op.value
                  ? 'bg-primary/10 text-primary border border-primary/40'
                  : 'bg-background text-muted-foreground border border-border/60 hover:text-foreground hover:border-border'
                }
              `}
            >
              <span>{op.label}</span>
            </button>
          ))}
        </div>
      )}

      {/* Value input */}
      {entry.field && needsValue && (
        <div>
          <label className="block text-[11px] text-muted-foreground mb-0.5">
            {isNumericOp ? `${entry.op === 'increment' ? 'Increase' : 'Decrease'} by` : 'Value'}
          </label>
          {isNumericOp ? (
            <input
              type="number"
              min={1}
              value={String(entry.value ?? 1)}
              onChange={(e) => {
                const v = parseInt(e.target.value);
                onPatch({ value: isNaN(v) ? 1 : v });
              }}
              className="w-full bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
            />
          ) : selectedField ? (
            <SmartValueInput
              field={selectedField}
              operator={entry.op === 'add' || entry.op === 'remove' ? 'contains' : 'eq'}
              value={entry.value}
              onChange={(v) => onPatch({ value: v })}
            />
          ) : (
            <input
              type="text"
              value={String(entry.value || '')}
              onChange={(e) => onPatch({ value: e.target.value })}
              placeholder="Enter value…"
              className="w-full bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
            />
          )}
        </div>
      )}

      {/* Clear warning */}
      {entry.field && entry.op === 'clear' && (
        <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-amber-500/10 border border-amber-500/30">
          <span className="text-[11px]">⚠️</span>
          <span className="text-[11px] text-amber-600 dark:text-amber-400">
            Clears <span className="font-medium text-amber-700 dark:text-amber-300">{selectedField?.label || entry.field}</span>
          </span>
        </div>
      )}
    </div>
  );
};

// --- Create Record params (A6) ---

interface CreateFieldEntry {
  field: string;
  value?: unknown;
}

const CreateRecordParams: React.FC<ParamProps> = ({ action }) => {
  const schema = useBuilderStore((s) => s.schema);
  const updateAction = useBuilderStore((s) => s.updateAction);
  const trigger = useBuilderStore((s) => s.trigger);

  // Every object the caller can create a record of — system entities + custom.
  const objects = useMemo(() => {
    if (!schema) return [] as { key: string; label: string }[];
    return [...schema.entities, ...(schema.custom_objects || [])].map((e) => ({ key: e.key, label: e.label || e.key }));
  }, [schema]);

  const object = String(action.params.object || '');
  const objectLabel = objects.find((o) => o.key === object)?.label || object;
  const fields: CreateFieldEntry[] = Array.isArray(action.params.fields) ? (action.params.fields as CreateFieldEntry[]) : [];

  // Nudge: creating a record of the same object the workflow triggers on can loop
  // (the new record fires its own _created event). Non-blocking — a condition can
  // guard it, and cross-object loops aren't caught here anyway.
  const selfLoop = !!object && object === triggerPrimaryObject(trigger);

  const setFields = (next: CreateFieldEntry[]) => updateAction(action.id, { params: { fields: next } });
  const handleObjectChange = (slug: string) => {
    // Fields are object-scoped, so switching object clears the prior rows.
    updateAction(action.id, { params: { object: slug, fields: [] } });
  };
  const patchField = (idx: number, patch: Partial<CreateFieldEntry>) => {
    const next = [...fields];
    next[idx] = { ...next[idx], ...patch };
    setFields(next);
  };
  const addField = () => setFields([...fields, { field: '', value: '' }]);
  const removeField = (idx: number) => setFields(fields.filter((_, i) => i !== idx));

  return (
    <div className="space-y-3">
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Object</label>
        <select
          value={objects.some((o) => o.key === object) ? object : ''}
          onChange={(e) => handleObjectChange(e.target.value)}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        >
          <option value="">Select an object…</option>
          {objects.map((o) => (
            <option key={o.key} value={o.key}>{o.label}</option>
          ))}
        </select>
      </div>

      {selfLoop && (
        <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-amber-500/10 border border-amber-500/30">
          <span className="text-[11px]">⚠️</span>
          <span className="text-[11px] text-amber-600 dark:text-amber-400">
            Creating a {objectLabel} from a {objectLabel} trigger can loop — guard it with a condition.
          </span>
        </div>
      )}

      {object &&
        fields.map((f, idx) => (
          <CreateFieldRow
            key={idx}
            entry={f}
            index={idx}
            schema={schema}
            object={object}
            totalCount={fields.length}
            onPatch={(patch) => patchField(idx, patch)}
            onRemove={() => removeField(idx)}
          />
        ))}

      {object && (
        <button
          type="button"
          onClick={addField}
          className="w-full flex items-center justify-center gap-1.5 px-3 py-2 rounded-lg border border-dashed border-border text-xs text-muted-foreground hover:border-primary/40 hover:text-primary transition-all"
        >
          <Plus className="h-3.5 w-3.5" />
          <span>{fields.length === 0 ? 'Add a field' : 'Add another field'}</span>
        </button>
      )}

      <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-muted/40 border border-border/60">
        <span className="text-sm">💡</span>
        <span className="text-xs text-muted-foreground">
          {object
            ? `Set one or more field values for the new ${objectLabel}. Values can use {{variables}} from the trigger.`
            : 'Choose an object to create, then set its field values.'}
        </span>
      </div>
    </div>
  );
};

/** Single field-value row within CreateRecordParams (set-only — no operations). */
const CreateFieldRow: React.FC<{
  entry: CreateFieldEntry;
  index: number;
  schema: WorkflowSchema | null;
  object: string;
  totalCount: number;
  onPatch: (patch: Partial<CreateFieldEntry>) => void;
  onRemove: () => void;
}> = ({ entry, index, schema, object, totalCount, onPatch, onRemove }) => {
  const selectedField: SchemaField | null = useMemo(
    () => findFieldInSchema(schema, entry.field),
    [schema, entry.field],
  );

  return (
    <div className="relative border border-border/60 rounded-xl p-3 space-y-2.5 bg-muted/40">
      <div className="flex items-center justify-between">
        <span className="text-[10px] uppercase tracking-wider text-muted-foreground/70 font-medium">Field {index + 1}</span>
        {totalCount > 1 && (
          <button
            type="button"
            onClick={onRemove}
            title="Remove this field"
            className="text-muted-foreground/70 hover:text-destructive transition-colors p-0.5"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      <FieldPicker
        value={entry.field || null}
        onChange={(path) => onPatch({ field: path, value: undefined })}
        entities={[object]}
        placeholder="Select a field…"
      />

      {entry.field && (
        <div>
          <label className="block text-[11px] text-muted-foreground mb-0.5">Value</label>
          {selectedField ? (
            <SmartValueInput
              field={selectedField}
              operator="eq"
              value={entry.value}
              onChange={(v) => onPatch({ value: v })}
            />
          ) : (
            <input
              type="text"
              value={String(entry.value || '')}
              onChange={(e) => onPatch({ value: e.target.value })}
              placeholder="Enter value…"
              className="w-full bg-background border border-border rounded-lg px-3 py-1.5 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
            />
          )}
        </div>
      )}
    </div>
  );
};

// --- Find / Enroll Records params (A6) ---

/** Object dropdown + optional filter rows, shared by Find and Enroll. Reuses
 *  CreateFieldRow (a FieldPicker + value input scoped to the object) for filters. */
const ObjectFilterSection: React.FC<{ action: ActionSpec; verb: string }> = ({ action, verb }) => {
  const schema = useBuilderStore((s) => s.schema);
  const updateAction = useBuilderStore((s) => s.updateAction);

  const objects = useMemo(() => {
    if (!schema) return [] as { key: string; label: string }[];
    return [...schema.entities, ...(schema.custom_objects || [])].map((e) => ({ key: e.key, label: e.label || e.key }));
  }, [schema]);

  const object = String(action.params.object || '');
  const objectLabel = objects.find((o) => o.key === object)?.label || object;
  const filters: CreateFieldEntry[] = Array.isArray(action.params.filters) ? (action.params.filters as CreateFieldEntry[]) : [];

  const handleObjectChange = (slug: string) => updateAction(action.id, { params: { object: slug, filters: [] } });
  const setFilters = (next: CreateFieldEntry[]) => updateAction(action.id, { params: { filters: next } });
  const patchFilter = (idx: number, patch: Partial<CreateFieldEntry>) => {
    const next = [...filters];
    next[idx] = { ...next[idx], ...patch };
    setFilters(next);
  };
  const addFilter = () => setFilters([...filters, { field: '', value: '' }]);
  const removeFilter = (idx: number) => setFilters(filters.filter((_, i) => i !== idx));

  return (
    <>
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Object</label>
        <select
          value={objects.some((o) => o.key === object) ? object : ''}
          onChange={(e) => handleObjectChange(e.target.value)}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        >
          <option value="">Select an object…</option>
          {objects.map((o) => (
            <option key={o.key} value={o.key}>{o.label}</option>
          ))}
        </select>
      </div>

      {object && (
        <>
          <div className="text-[10px] uppercase tracking-wider text-muted-foreground/70 font-medium">Filters (optional)</div>
          {filters.map((f, idx) => (
            <CreateFieldRow
              key={idx}
              entry={f}
              index={idx}
              schema={schema}
              object={object}
              totalCount={filters.length}
              onPatch={(patch) => patchFilter(idx, patch)}
              onRemove={() => removeFilter(idx)}
            />
          ))}
          <button
            type="button"
            onClick={addFilter}
            className="w-full flex items-center justify-center gap-1.5 px-3 py-2 rounded-lg border border-dashed border-border text-xs text-muted-foreground hover:border-primary/40 hover:text-primary transition-all"
          >
            <Plus className="h-3.5 w-3.5" />
            <span>{filters.length === 0 ? 'Add a filter' : 'Add another filter'}</span>
          </button>
          <p className="text-xs text-muted-foreground">
            {filters.length === 0
              ? `No filters — ${verb} every ${objectLabel} (up to the limit).`
              : `${verb[0].toUpperCase()}${verb.slice(1)} ${objectLabel} records matching all filters.`}
          </p>
        </>
      )}
    </>
  );
};

const FindRecordsParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const limit = Number(action.params.limit) || 100;
  return (
    <div className="space-y-3">
      <ObjectFilterSection action={action} verb="find" />
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Limit</label>
        <input
          type="number"
          min={1}
          max={100}
          value={limit}
          onChange={(e) => setParam('limit', Math.max(1, Math.min(100, parseInt(e.target.value) || 100)))}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
        />
        <p className="text-xs text-muted-foreground mt-1">
          Up to 100. The match count is available to later steps as <code className="text-[11px]">{'{{actions.' + action.id + '.count}}'}</code>.
        </p>
      </div>
    </div>
  );
};

const EnrollRecordsParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const { data } = useWorkflowsList({});
  const currentId = useBuilderStore((s) => s.workflowId);
  const workflows = (data?.workflows || []).filter((w) => w.id !== currentId);
  const workflowId = String(action.params.workflow_id || '');
  const missing = workflowId !== '' && !workflows.some((w) => w.id === workflowId);

  return (
    <div className="space-y-3">
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Enroll into workflow</label>
        <select
          value={workflows.some((w) => w.id === workflowId) ? workflowId : ''}
          onChange={(e) => setParam('workflow_id', e.target.value)}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
        >
          <option value="">Select a workflow…</option>
          {missing && <option value={workflowId}>(workflow not found)</option>}
          {workflows.map((w) => (
            <option key={w.id} value={w.id}>{w.name}{w.is_active ? '' : ' (inactive)'}</option>
          ))}
        </select>
        <p className="text-xs text-muted-foreground mt-1">Each matching record starts one run of this workflow.</p>
      </div>

      <ObjectFilterSection action={action} verb="enroll" />

      <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-muted/40 border border-border/60">
        <span className="text-sm">💡</span>
        <span className="text-xs text-muted-foreground">
          The target must be active. Enrollment chains are capped (max depth 2) so workflows can't enroll each other endlessly.
        </span>
      </div>
    </div>
  );
};

// --- Generate with AI params (A7) ---

const AIGenerateParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const maxTokens = Number(action.params.max_tokens) || 512;
  return (
    <div className="space-y-3">
      <TemplateInput
        label="Prompt"
        value={String(action.params.prompt || '')}
        onChange={(v) => setParam('prompt', v)}
        placeholder="Draft a friendly follow-up email to {{contact.first_name}} about {{deal.title}}…"
        multiline
        rows={5}
      />
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Max length (tokens)</label>
        <input
          type="number"
          min={1}
          max={1024}
          value={maxTokens}
          onChange={(e) => setParam('max_tokens', Math.max(1, Math.min(1024, parseInt(e.target.value) || 512)))}
          className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
        />
      </div>
      <div className="flex items-start gap-2 px-3 py-2 rounded-lg bg-muted/40 border border-border/60">
        <span className="text-sm">✨</span>
        <span className="text-xs text-muted-foreground">
          The generated text is available to later steps as <code className="text-[11px]">{'{{actions.' + action.id + '.text}}'}</code>. Runs against your organization's AI budget.
        </span>
      </div>
    </div>
  );
};

const WebhookParams: React.FC<ParamProps> = ({ action, setParam }) => (
  <div className="space-y-3">
    <TemplateInput label="URL" value={String(action.params.url || '')} onChange={(v) => setParam('url', v)} placeholder="https://example.com/webhook" />
    <div>
      <label className="block text-sm text-muted-foreground mb-1">Method</label>
      <select
        value={String(action.params.method || 'POST')}
        onChange={(e) => setParam('method', e.target.value)}
        className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
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

interface DateFieldGroup {
  key: string;
  label: string;
  fields: SchemaField[];
}

// Date-type fields grouped by object — the candidates a wait-until delay can wait
// on. Scoped to `resolvable` (objects present in the trigger's eval context) when
// provided, so a user can't pick a field the backend will silently fail to resolve
// (e.g. a deal field on a contact-triggered workflow) — which would skip the wait.
function dateFieldsByObject(schema: WorkflowSchema | null, resolvable?: Set<string>): DateFieldGroup[] {
  if (!schema) return [];
  const entities = [...schema.entities.filter((e: SchemaEntity) => e.key !== 'trigger'), ...(schema.custom_objects || [])];
  const groups: DateFieldGroup[] = [];
  for (const e of entities) {
    if (resolvable && !resolvable.has(e.key)) continue;
    const dateFields = e.fields.filter((f) => f.type === 'date');
    if (dateFields.length) groups.push({ key: e.key, label: e.label, fields: dateFields });
  }
  return groups;
}

// DelayParams (A4.4): a mode toggle switches between a fixed duration and a
// wait-until deadline resolved from a record date field.
const DelayParams: React.FC<ParamProps> = ({ action, setParam }) => {
  const { schema, updateAction, trigger } = useBuilderStore();
  const setParams = (obj: Record<string, unknown>) => updateAction(action.id, { params: obj });

  const isUntil = Boolean(action.params.until_field);
  // Only offer date fields the run's eval context can actually resolve for this trigger.
  const resolvable = useMemo(() => resolvableObjectsForTrigger(trigger), [trigger]);
  const groups = useMemo(() => dateFieldsByObject(schema, resolvable), [schema, resolvable]);
  const hasDateFields = groups.length > 0;

  const setMode = (until: boolean) => {
    if (until === isUntil) return;
    if (until) {
      const first = groups[0]?.fields[0]?.path || '';
      setParams({ until_field: first, offset_days: 0, at_time: DEFAULT_AT_TIME, timezone: browserTimeZone(), duration_sec: 0 });
    } else {
      setParams({ until_field: '', duration_sec: Number(action.params.duration_sec) || 60 });
    }
  };

  const modeBtn = (active: boolean) =>
    `flex-1 px-3 py-1.5 rounded-lg text-xs font-medium transition-all duration-150 ${
      active ? 'bg-primary text-primary-foreground shadow-md shadow-primary/25' : 'bg-background text-muted-foreground hover:bg-accent hover:text-accent-foreground'
    }`;

  return (
    <div className="space-y-3">
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Wait type</label>
        <div className="flex gap-1">
          <button type="button" onClick={() => setMode(false)} className={modeBtn(!isUntil)}>For a duration</button>
          <button
            type="button"
            onClick={() => setMode(true)}
            disabled={!hasDateFields && !isUntil}
            className={`${modeBtn(isUntil)} ${!hasDateFields && !isUntil ? 'opacity-50 cursor-not-allowed' : ''}`}
          >
            Until a date
          </button>
        </div>
        {!hasDateFields && !isUntil && (
          <p className="text-[11px] text-muted-foreground/70 mt-1">No date fields available to wait until.</p>
        )}
      </div>

      {isUntil ? (
        <WaitUntilFields action={action} setParam={setParam} groups={groups} />
      ) : (
        <FixedDelayFields action={action} setParam={setParam} />
      )}
    </div>
  );
};

const FixedDelayFields: React.FC<ParamProps> = ({ action, setParam }) => {
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
      <label className="block text-sm text-muted-foreground mb-1">Wait Duration</label>
      <div className="flex gap-2">
        <input
          type="number"
          min={1}
          value={inputValue}
          onChange={(e) => handleValueChange(e.target.value)}
          onBlur={handleBlur}
          className={`flex-1 bg-background border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none ${
            isOverMax ? 'border-destructive focus:border-destructive' : 'border-border focus:border-ring focus:ring-1 focus:ring-ring'
          }`}
        />
        <select
          value={decomposed.unit}
          onChange={(e) => handleUnitChange(e.target.value)}
          className={`w-28 bg-background border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none ${
            isOverMax ? 'border-destructive focus:border-destructive' : 'border-border focus:border-ring focus:ring-1 focus:ring-ring'
          }`}
        >
          {DELAY_UNITS.map((u) => (
            <option key={u.value} value={u.value}>{u.label}</option>
          ))}
        </select>
      </div>

      {/* Over-max error */}
      {isOverMax && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-destructive/10 border border-destructive/40">
          <span className="text-xs text-destructive">⚠ Duration exceeds the maximum of 30 days (2,592,000 seconds). Reduce it to save.</span>
        </div>
      )}

      {/* Friendly preview */}
      {!isOverMax && (
        <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-muted/40 border border-border/60">
          <span className="text-sm">⏱️</span>
          <span className="text-xs text-muted-foreground">
            Workflow will pause for <span className="text-primary font-medium">{summary}</span>
          </span>
        </div>
      )}

      <p className="text-xs text-muted-foreground">Max: 30 days (2,592,000 seconds)</p>
    </div>
  );
};

const WaitUntilFields: React.FC<ParamProps & { groups: DateFieldGroup[] }> = ({ action, setParam, groups }) => {
  const p = action.params;
  const untilField = String(p.until_field || '');
  const offsetDays = Number(p.offset_days) || 0;
  const atTime = String(p.at_time || DEFAULT_AT_TIME);
  const timezone = String(p.timezone || browserTimeZone());
  const { direction, days } = offsetToDirection(offsetDays);

  const fieldLabel = useMemo(() => {
    for (const g of groups) {
      const f = g.fields.find((x) => x.path === untilField);
      if (f) return f.label;
    }
    return undefined;
  }, [groups, untilField]);

  const zones = useMemo(() => {
    const list = allTimeZones();
    return list.includes(timezone) ? list : [timezone, ...list];
  }, [timezone]);

  const onDirection = (dir: OffsetDirection) => {
    const mag = dir === 'on' ? 0 : Math.max(1, days);
    setParam('offset_days', directionToOffset(dir, mag));
  };
  const onDays = (val: string) => {
    const n = Math.max(0, Math.floor(Number(val) || 0));
    setParam('offset_days', directionToOffset(direction === 'on' ? 'before' : direction, n));
  };

  const inputCls =
    'bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring';

  return (
    <div className="space-y-3">
      {/* Date field */}
      <div>
        <label className="block text-sm text-muted-foreground mb-1">Date field</label>
        <select value={untilField} onChange={(e) => setParam('until_field', e.target.value)} className={`${inputCls} w-full`}>
          <option value="" disabled>Select date field…</option>
          {groups.map((g) => (
            <optgroup key={g.key} label={g.label}>
              {g.fields.map((f) => (
                <option key={f.path} value={f.path}>{f.label}</option>
              ))}
            </optgroup>
          ))}
        </select>
      </div>

      {/* Offset */}
      <div>
        <label className="block text-sm text-muted-foreground mb-1">When</label>
        <div className="flex items-center gap-2">
          <input
            type="number"
            min={0}
            value={direction === 'on' ? '' : days}
            onChange={(e) => onDays(e.target.value)}
            disabled={direction === 'on'}
            placeholder="0"
            aria-label="Offset days"
            className={`${inputCls} w-16 disabled:opacity-60 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none`}
          />
          <span className="text-xs text-muted-foreground">day(s)</span>
          <select
            value={direction}
            onChange={(e) => onDirection(e.target.value as OffsetDirection)}
            aria-label="Direction"
            className={`${inputCls} flex-1`}
          >
            <option value="before">before</option>
            <option value="on">on</option>
            <option value="after">after</option>
          </select>
        </div>
      </div>

      {/* At time + timezone */}
      <div className="flex gap-2">
        <div className="flex-1">
          <label className="block text-sm text-muted-foreground mb-1">At</label>
          <input type="time" value={atTime} onChange={(e) => setParam('at_time', e.target.value)} aria-label="Time of day" className={`${inputCls} w-full`} />
        </div>
        <div className="flex-1 min-w-0">
          <label className="block text-sm text-muted-foreground mb-1">Timezone</label>
          <select value={timezone} onChange={(e) => setParam('timezone', e.target.value)} aria-label="Timezone" className={`${inputCls} w-full`}>
            {zones.map((z) => (
              <option key={z} value={z}>{z}</option>
            ))}
          </select>
        </div>
      </div>

      {/* Preview */}
      <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-muted/40 border border-border/60">
        <span className="text-sm">⏱️</span>
        <span className="text-xs text-muted-foreground">
          {untilField
            ? describeWaitUntil({ field: untilField, offset_days: offsetDays, at_time: atTime, timezone }, fieldLabel)
            : 'Pick a date field to wait until.'}
        </span>
      </div>
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
    <label className="block text-sm text-muted-foreground mb-1">{label}</label>
    <input
      type={type}
      value={String(value || '')}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-full bg-background border border-border rounded-lg px-3 py-2 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
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
    <div className="mt-4 pt-4 border-t border-border">
      <p className="text-xs text-muted-foreground mb-2">Available Template Variables</p>
      {schemaLoading ? (
        <div className="flex flex-wrap gap-1">
          {[...Array(8)].map((_, i) => (
            <div
              key={i}
              className="h-5 rounded bg-muted animate-pulse"
              style={{ width: `${60 + Math.random() * 50}px` }}
            />
          ))}
        </div>
      ) : schemaError ? (
        <div className="flex items-center gap-2 p-2 rounded-lg bg-destructive/10 border border-destructive/40">
          <span className="text-xs text-destructive flex-1">Failed to load variables</span>
          <button
            onClick={invalidateSchema}
            className="text-xs text-destructive hover:text-foreground underline"
          >
            Retry
          </button>
        </div>
      ) : variables.length === 0 ? (
        <p className="text-xs text-muted-foreground/70 italic">No template variables available</p>
      ) : (
        <div className="flex flex-wrap gap-1">
          {variables.map((v) => (
            <button
              key={v.path}
              onClick={() => {
                navigator.clipboard.writeText(`{{${v.path}}}`);
              }}
              title={`Copy {{${v.path}}}`}
              className="px-2 py-0.5 rounded bg-muted text-xs text-muted-foreground hover:text-accent-foreground hover:bg-accent transition-colors font-mono"
            >
              {v.path}
            </button>
          ))}
        </div>
      )}
    </div>
  );
};
