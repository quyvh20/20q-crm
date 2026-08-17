// Brevo-style palette sidebar: a searchable, categorized catalog of Triggers /
// Actions / Rules. Items are draggable onto the canvas (insert slots and the
// trigger card light up as drop targets) and clickable as a shortcut — a step
// click appends at the tail of the main path, a trigger click replaces the
// workflow's trigger. Desktop only; the canvas keeps its edge-"+" insert menu,
// which renders the same shared catalog.

import { useMemo, useState } from 'react';
import { Search, ChevronDown, ChevronRight, GripVertical, PanelLeftClose, Sparkles } from 'lucide-react';
import { useBuilderStore } from '../store';
import {
  ACTION_ITEMS,
  RULE_ITEMS,
  buildTriggerItems,
  CATEGORY_ICONS,
} from './catalog';
import { TRIGGER_ACCENT, ACTION_ACCENT, RULE_ACCENT, type CategoryAccent } from './nodeMeta';
import type { PaletteDrag } from './BuilderContext';
import type { LucideIcon } from 'lucide-react';

type Tab = 'triggers' | 'actions' | 'rules';

const TABS: { key: Tab; label: string; accent: CategoryAccent }[] = [
  { key: 'triggers', label: 'Triggers', accent: TRIGGER_ACCENT },
  { key: 'actions', label: 'Actions', accent: ACTION_ACCENT },
  { key: 'rules', label: 'Rules', accent: RULE_ACCENT },
];

const TAB_HELP: Record<Tab, string> = {
  triggers: 'An automation starts with a trigger. Drag one onto the trigger card, or click to use it.',
  actions: 'Actions run when a record reaches them. Drag one onto a + slot, or click to add it at the end.',
  rules: 'Rules shape the path — add time delays and If / Else splits.',
};

interface Row {
  key: string;
  label: string;
  category: string;
  icon: LucideIcon;
  drag: PaletteDrag;
}

function groupRows(rows: Row[]): { name: string; rows: Row[] }[] {
  const order: string[] = [];
  const byCat: Record<string, Row[]> = {};
  for (const r of rows) {
    if (!byCat[r.category]) {
      byCat[r.category] = [];
      order.push(r.category);
    }
    byCat[r.category].push(r);
  }
  return order.map((name) => ({ name, rows: byCat[name] }));
}

interface Props {
  onPickTrigger: (id: string) => void;
  onPickStep: (type: string) => void;
  onDragChange: (drag: PaletteDrag | null) => void;
  onCollapse: () => void;
  /** Opens the Copilot panel — our AI drafts the whole flow from a description. */
  onAskAI?: () => void;
}

export function Palette({ onPickTrigger, onPickStep, onDragChange, onCollapse, onAskAI }: Props) {
  const schema = useBuilderStore((s) => s.schema);
  const [tab, setTab] = useState<Tab>('triggers');
  const [query, setQuery] = useState('');
  const [closed, setClosed] = useState<Record<string, boolean>>({});

  const active = TABS.find((t) => t.key === tab)!;

  const rows = useMemo<Row[]>(() => {
    if (tab === 'triggers') {
      return buildTriggerItems(schema).map((it) => ({
        key: it.id,
        label: it.label,
        category: it.category,
        icon: it.icon,
        drag: { kind: 'trigger', type: it.id },
      }));
    }
    const items = tab === 'actions' ? ACTION_ITEMS : RULE_ITEMS;
    return items.map((it) => ({
      key: it.type,
      label: it.label,
      category: it.category,
      icon: it.icon,
      drag: { kind: 'step', type: it.type },
    }));
  }, [tab, schema]);

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const filtered = q ? rows.filter((r) => r.label.toLowerCase().includes(q)) : rows;
    return groupRows(filtered);
  }, [rows, query]);

  const pick = (row: Row) => {
    if (row.drag.kind === 'trigger') onPickTrigger(row.drag.type);
    else onPickStep(row.drag.type);
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Search + collapse */}
      <div className="flex items-center gap-2 px-3 pb-1 pt-3">
        <div className="flex min-w-0 flex-1 items-center gap-2 rounded-full border border-border bg-background px-3 py-1.5 transition-colors focus-within:border-ring">
          <Search className="h-4 w-4 shrink-0 text-muted-foreground" />
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search by step name"
            className="w-full bg-transparent text-sm text-foreground outline-none placeholder:text-muted-foreground"
          />
        </div>
        <button
          type="button"
          onClick={onCollapse}
          title="Hide steps panel"
          aria-label="Hide steps panel"
          className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <PanelLeftClose className="h-4 w-4" />
        </button>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-4 border-b border-border px-3">
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setTab(t.key)}
            aria-pressed={tab === t.key}
            className={`flex items-center gap-1.5 border-b-2 px-0.5 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? 'border-primary text-foreground'
                : 'border-transparent text-muted-foreground hover:text-foreground'
            }`}
          >
            <span className={`h-2 w-2 rounded-full ${t.accent.dot}`} />
            {t.label}
          </button>
        ))}
      </div>

      {/* Items */}
      <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-4">
        {/* The thing Brevo doesn't have: describe the flow and Copilot drafts it
            onto the canvas. Purple = the app's established AI accent. */}
        {onAskAI && (
          <button
            type="button"
            onClick={onAskAI}
            className="mt-3 flex w-full items-center gap-2.5 rounded-xl border border-purple-500/20 bg-purple-500/10 px-3 py-2.5 text-left transition-colors hover:border-purple-500/40"
          >
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-purple-500 text-white">
              <Sparkles className="h-4 w-4" />
            </span>
            <span className="min-w-0">
              <span className="block text-sm font-medium text-foreground">Draft with AI</span>
              <span className="block truncate text-xs text-muted-foreground">Describe the flow — Copilot builds it</span>
            </span>
          </button>
        )}
        <p className="py-3 text-xs leading-relaxed text-muted-foreground">{TAB_HELP[tab]}</p>
        {groups.length === 0 && (
          <div className="py-6 text-center text-xs text-muted-foreground">No steps match “{query.trim()}”.</div>
        )}
        {groups.map((g) => {
          const SectionIcon = CATEGORY_ICONS[g.name];
          const isOpen = !closed[g.name];
          return (
            <div key={g.name}>
              <button
                type="button"
                onClick={() => setClosed((c) => ({ ...c, [g.name]: !c[g.name] }))}
                aria-expanded={isOpen}
                className="flex w-full items-center gap-2 px-1 py-2 text-sm font-semibold text-foreground"
              >
                {SectionIcon && <SectionIcon className="h-4 w-4 text-muted-foreground" />}
                {g.name}
                {isOpen ? (
                  <ChevronDown className="h-3.5 w-3.5 text-muted-foreground" />
                ) : (
                  <ChevronRight className="h-3.5 w-3.5 text-muted-foreground" />
                )}
              </button>
              {isOpen && (
                <div className="flex flex-col gap-2 pb-3">
                  {g.rows.map((row) => {
                    const Icon = row.icon;
                    return (
                      <div
                        key={row.key}
                        role="button"
                        tabIndex={0}
                        draggable
                        onDragStart={(e) => {
                          e.dataTransfer.setData('application/x-builder-item', JSON.stringify(row.drag));
                          e.dataTransfer.effectAllowed = 'copy';
                          onDragChange(row.drag);
                        }}
                        onDragEnd={() => onDragChange(null)}
                        onClick={() => pick(row)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            pick(row);
                          }
                        }}
                        className="group flex cursor-grab items-center gap-2.5 rounded-xl border border-border bg-card px-3 py-2.5 shadow-sm transition-colors hover:border-ring/60 active:cursor-grabbing"
                      >
                        <span className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full ${active.accent.solid}`}>
                          <Icon className="h-4 w-4" />
                        </span>
                        <span className="min-w-0 flex-1 truncate text-sm text-foreground">{row.label}</span>
                        <GripVertical className="h-4 w-4 shrink-0 text-muted-foreground/40 transition-colors group-hover:text-muted-foreground/80" />
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
