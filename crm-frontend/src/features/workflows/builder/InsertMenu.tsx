// Searchable command menu that opens from a "+" insert slot. Picking an item
// builds a default step and inserts it at the slot. Complements the palette
// sidebar (both render from the shared catalog, so the two lists can't drift) —
// it appears where you click, so nothing eats canvas width.

import { useEffect, useMemo, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { useBuilderStore, generateActionId } from '../store';
import { getDefaultParams } from './stepDefaults';
import type { WorkflowStep, ActionSpec } from '../types';
import type { InsertContext } from './graph';
import { ACTION_ITEMS, RULE_ITEMS, findStepById, type PaletteStepItem } from './catalog';
import { actionMeta, conditionMeta, delayMeta } from './nodeMeta';
import { showToast } from '../../../lib/useToast';

function metaFor(type: string) {
  if (type === 'condition') return conditionMeta;
  if (type === 'delay') return delayMeta;
  return actionMeta(type);
}

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

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const match = (it: PaletteStepItem) => !q || it.label.toLowerCase().includes(q) || it.type.includes(q);
    return [
      { name: 'Actions', items: ACTION_ITEMS.filter(match) },
      { name: 'Rules', items: RULE_ITEMS.filter(match) },
    ];
  }, [query]);

  const empty = groups.every((g) => g.items.length === 0);

  const pick = (type: string) => {
    const step = buildStep(type);
    addStep(step, slot.parentId, slot.branch, slot.index);
    // addStep can refuse (depth guard) — selecting the never-inserted id would
    // blank the config panel, so confirm the step landed and surface a refusal.
    const state = useBuilderStore.getState();
    if (findStepById(state.steps, step.id)) {
      selectNode(step.id);
    } else {
      showToast(state.errors.depth?.[0] ?? 'Could not add the step here.', { tone: 'error' });
    }
    onClose();
  };

  // Clamp the menu inside the viewport.
  const left = Math.min(anchor.x, window.innerWidth - 280);
  const top = Math.min(anchor.y, window.innerHeight - 360);

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
          {empty && (
            <div className="px-3 py-4 text-center text-xs text-muted-foreground">No matches</div>
          )}
          {groups.map((g) => {
            if (!g.items.length) return null;
            return (
              <div key={g.name}>
                <div className="px-3 pb-1 pt-2 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{g.name}</div>
                {g.items.map((it) => {
                  const Icon = it.icon;
                  const meta = metaFor(it.type);
                  return (
                    <button
                      key={it.type}
                      type="button"
                      onClick={() => pick(it.type)}
                      className="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm text-foreground hover:bg-accent"
                    >
                      <span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-full ${meta.solid}`}>
                        <Icon className="h-3.5 w-3.5" />
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
