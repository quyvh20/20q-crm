// Searchable command menu that opens from a "+" insert slot. Picking an item
// builds a default step and inserts it at the slot. Replaces the old palette —
// it appears where you click, so nothing eats canvas width.

import { useEffect, useMemo, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { useBuilderStore, generateActionId } from '../store';
import { getDefaultParams } from '../nodes/AddNodeButton';
import type { WorkflowStep, ActionSpec } from '../types';
import type { InsertContext } from './graph';
import { actionMeta, conditionMeta, delayMeta } from './nodeMeta';

interface Item {
  type: string;
  label: string;
  group: 'Actions' | 'Flow control';
  meta: { icon: React.ComponentType<{ className?: string }>; accent: string; chip: string };
}

const ITEMS: Item[] = [
  { type: 'send_email', label: 'Send Email', group: 'Actions', meta: actionMeta('send_email') },
  { type: 'create_task', label: 'Create Task', group: 'Actions', meta: actionMeta('create_task') },
  { type: 'assign_user', label: 'Assign User', group: 'Actions', meta: actionMeta('assign_user') },
  { type: 'update_record', label: 'Update Record', group: 'Actions', meta: actionMeta('update_record') },
  { type: 'log_activity', label: 'Log Activity', group: 'Actions', meta: actionMeta('log_activity') },
  { type: 'send_webhook', label: 'Send Webhook', group: 'Actions', meta: actionMeta('send_webhook') },
  { type: 'condition', label: 'If / Else', group: 'Flow control', meta: conditionMeta },
  { type: 'delay', label: 'Delay / Wait', group: 'Flow control', meta: delayMeta },
];

export function buildStep(type: string): WorkflowStep {
  const id = generateActionId();
  if (type === 'condition') {
    return { id, type: 'condition', condition: { op: 'AND', rules: [] }, yes_steps: [], no_steps: [] };
  }
  if (type === 'delay') {
    return { id, type: 'delay', delay: { duration_sec: 60 } };
  }
  return {
    id,
    type: 'action',
    action: { id, type: type as ActionSpec['type'], params: getDefaultParams(type) },
  };
}

interface Props {
  slot: InsertContext;
  anchor: { x: number; y: number };
  onClose: () => void;
}

export function InsertMenu({ slot, anchor, onClose }: Props) {
  const addStep = useBuilderStore((s) => s.addStep);
  const selectNode = useBuilderStore((s) => s.selectNode);
  const [query, setQuery] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return ITEMS.filter((it) => !q || it.label.toLowerCase().includes(q) || it.type.includes(q));
  }, [query]);

  const pick = (type: string) => {
    const step = buildStep(type);
    addStep(step, slot.parentId, slot.branch, slot.index);
    selectNode(step.id);
    onClose();
  };

  // Clamp the menu inside the viewport.
  const left = Math.min(anchor.x, window.innerWidth - 280);
  const top = Math.min(anchor.y, window.innerHeight - 360);

  const groups: Item['group'][] = ['Actions', 'Flow control'];

  return (
    <>
      <div className="fixed inset-0 z-40" onClick={onClose} />
      <div
        className="fixed z-50 w-64 overflow-hidden rounded-xl border border-border bg-popover shadow-xl"
        style={{ left, top }}
        role="dialog"
        aria-label="Add a step"
      >
        <div className="flex items-center gap-2 border-b border-border px-3 py-2">
          <Search className="h-4 w-4 text-muted-foreground" />
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Add a step…"
            className="w-full bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
          />
        </div>
        <div className="max-h-72 overflow-y-auto py-1">
          {filtered.length === 0 && (
            <div className="px-3 py-4 text-center text-xs text-muted-foreground">No matches</div>
          )}
          {groups.map((g) => {
            const items = filtered.filter((it) => it.group === g);
            if (!items.length) return null;
            return (
              <div key={g}>
                <div className="px-3 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{g}</div>
                {items.map((it) => {
                  const Icon = it.meta.icon;
                  return (
                    <button
                      key={it.type}
                      type="button"
                      onClick={() => pick(it.type)}
                      className="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm text-foreground hover:bg-accent"
                    >
                      <span className={`flex h-7 w-7 items-center justify-center rounded-md ${it.meta.chip}`}>
                        <Icon className={`h-4 w-4 ${it.meta.accent}`} />
                      </span>
                      {it.label}
                    </button>
                  );
                })}
              </div>
            );
          })}
        </div>
      </div>
    </>
  );
}
