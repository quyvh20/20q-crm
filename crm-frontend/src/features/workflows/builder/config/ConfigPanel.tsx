// The new builder's right config panel. Routes by the store's selectedNodeId to
// the token-styled trigger / condition / action forms, wrapped in a shared shell
// with a header (icon + title) and a delete affordance for steps. Replaces the
// A3.2 stub in NextBuilder.
//
// When a dry run is active (A3.5) it shows, above the form, the selected node's
// outcome — run/skip (+ why), the branch a condition took, and an action's resolved
// (interpolated) params.
//
// Note: the new canvas has no workflow-level "conditions" node (conditions are
// authored as If/Else condition steps). Any legacy global `conditions` on a
// loaded workflow round-trips untouched on save — it's just not editable here.

import { useEffect, useState, type ReactNode } from 'react';
import { Trash2, MousePointerClick, FlaskConical, type LucideIcon } from 'lucide-react';
import { useBuilderStore } from '../../store';
import type { DryRunState } from '../BuilderContext';
import type { TestRunStep, WorkflowStep } from '../../types';
import {
  actionMeta,
  conditionMeta,
  splitMeta,
  delayMeta,
  triggerMeta,
  triggerLabel,
  ACTION_TITLES,
  type NodeMeta,
} from '../nodeMeta';
import { TriggerConfig } from './TriggerConfig';
import { ConditionConfig } from './ConditionConfig';
import { ActionConfig } from './ActionConfig';

interface HeaderSpec {
  eyebrow: string;
  title: string;
  meta: NodeMeta;
}

function Shell({ header, onDelete, preview, children }: { header: HeaderSpec; onDelete?: () => void; preview?: ReactNode; children: ReactNode }) {
  const Icon = header.meta.icon;
  return (
    <div className="flex h-full flex-col">
      <div className="sticky top-0 z-10 flex items-center gap-3 border-b border-border bg-card px-4 py-3">
        <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${header.meta.chip}`}>
          <Icon className={`h-4.5 w-4.5 ${header.meta.accent}`} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{header.eyebrow}</div>
          <div className="truncate text-sm font-semibold text-foreground">{header.title}</div>
        </div>
        {onDelete && (
          <button
            type="button"
            onClick={onDelete}
            title="Delete step"
            aria-label="Delete step"
            className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 className="h-4 w-4" />
          </button>
        )}
      </div>
      <div className="flex-1 p-4">
        {preview}
        {children}
      </div>
    </div>
  );
}

// Percentage-split form: one number — Branch A's share; B gets the rest. The
// assignment itself is a deterministic per-run hash (backend splitTakesA), so
// there is nothing else to configure.
function SplitConfig({ step }: { step: WorkflowStep }) {
  const updateStep = useBuilderStore((s) => s.updateStep);
  const p = step.split?.percent_a ?? 50;

  // A loaded/AI-drafted split may arrive without params; commit the default the
  // panel displays so the store matches what the user sees (else Save blocks on
  // an invisible mismatch).
  useEffect(() => {
    if (!step.split) updateStep(step.id, { split: { percent_a: 50 } });
  }, [step.id, step.split, updateStep]);

  const commit = (n: number) => updateStep(step.id, { split: { percent_a: n } });

  // Local draft for the NUMBER input so clearing it to retype doesn't snap to a
  // clamped junk value mid-edit (the FixedDelayFields idea) — only parseable
  // in-range values commit; blur restores the committed value. The slider is
  // always in range and commits directly. External changes (slider, undo) sync
  // into the draft via the render-adjust pattern rather than an effect.
  const [draft, setDraft] = useState(String(p));
  const [prevP, setPrevP] = useState(p);
  if (prevP !== p) {
    setPrevP(p);
    setDraft(String(p));
  }
  const onDraftChange = (raw: string) => {
    setDraft(raw);
    const n = Number(raw);
    if (raw.trim() !== '' && Number.isInteger(n) && n >= 1 && n <= 99) commit(n);
  };

  return (
    <div className="space-y-4">
      <p className="text-xs leading-relaxed text-muted-foreground">
        Each run is randomly assigned to branch A or B at these odds. The assignment is
        stable for the life of a run — retries and resumes never switch branches.
      </p>
      <div>
        <label htmlFor="split-percent-a" className="mb-1 block text-xs font-medium text-foreground">
          Branch A share
        </label>
        <div className="flex items-center gap-3">
          <input
            type="range"
            min={1}
            max={99}
            value={p}
            onChange={(e) => commit(Number(e.target.value))}
            className="flex-1 accent-violet-500"
            aria-label="Branch A share"
          />
          <div className="flex items-center gap-1">
            <input
              id="split-percent-a"
              type="number"
              min={1}
              max={99}
              value={draft}
              onChange={(e) => onDraftChange(e.target.value)}
              onBlur={() => setDraft(String(p))}
              className="w-16 rounded-lg border border-border bg-background px-2 py-1 text-sm text-foreground focus:border-ring focus:outline-none"
            />
            <span className="text-sm text-muted-foreground">%</span>
          </div>
        </div>
      </div>
      <div>
        <div className="mb-1 flex justify-between text-xs text-muted-foreground">
          <span>A · {p}%</span>
          <span>B · {100 - p}%</span>
        </div>
        <div className="flex h-2 overflow-hidden rounded-full bg-muted">
          <div className="bg-violet-500" style={{ width: `${p}%` }} />
          <div className="bg-violet-500/30" style={{ width: `${100 - p}%` }} />
        </div>
      </div>
    </div>
  );
}

function EmptyState({ icon: Icon, title, hint }: { icon: LucideIcon; title: string; hint: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
      <Icon className="h-6 w-6 text-muted-foreground/60" />
      <div className="text-sm font-medium text-foreground">{title}</div>
      <div className="text-xs text-muted-foreground">{hint}</div>
    </div>
  );
}

function fmtValue(v: unknown): string {
  if (v == null) return '';
  if (typeof v === 'string') return v;
  return JSON.stringify(v);
}

// Dry-run outcome for the selected step, shown above its form.
function StepDryPreview({ dry }: { dry: TestRunStep }) {
  const runs = dry.status === 'run';
  const params = dry.resolved_params ?? {};
  return (
    <div className={`mb-3 rounded-lg border p-2.5 text-xs ${runs ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-border bg-muted/40'}`}>
      <div className="flex items-center gap-2">
        <FlaskConical className="h-3.5 w-3.5 text-primary" />
        <span className="font-medium text-foreground">Dry run: {runs ? 'would run' : 'skipped'}</span>
        {(dry.type === 'condition' || dry.type === 'split') && dry.branch && (
          <span className="rounded bg-emerald-500/15 px-1 text-[10px] font-semibold uppercase text-emerald-600 dark:text-emerald-400">
            → {dry.type === 'split' ? (dry.branch === 'yes' ? 'A' : 'B') : dry.branch}
          </span>
        )}
      </div>
      {!runs && dry.reason && <p className="mt-1 text-muted-foreground">{dry.reason}</p>}
      {runs && dry.type === 'delay' && (
        <p className="mt-1 text-muted-foreground">
          {dry.wait_event
            ? `Waits here for ${dry.wait_event === 'email_clicked' ? 'a link click' : 'an email open'}, then continues — or continues anyway once the timeout runs out.`
            : 'Pauses here, then continues.'}
        </p>
      )}
      {runs && dry.type === 'action' && Object.keys(params).length > 0 && (
        <div className="mt-2 space-y-1">
          <div className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Resolved values</div>
          {Object.entries(params).map(([k, v]) => (
            <div key={k} className="flex gap-2">
              <span className="shrink-0 font-mono text-muted-foreground">{k}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-foreground" title={fmtValue(v)}>{fmtValue(v)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function TriggerDryPreview({ dryRun }: { dryRun: DryRunState }) {
  const ok = dryRun.conditionResult;
  return (
    <div className={`mb-3 rounded-lg border p-2.5 text-xs ${ok ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-amber-500/30 bg-amber-500/10'}`}>
      <div className="flex items-center gap-2">
        <FlaskConical className="h-3.5 w-3.5 text-primary" />
        <span className="font-medium text-foreground">Dry run · {dryRun.sampleLabel}</span>
      </div>
      <p className={`mt-1 ${ok ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`}>
        {ok ? 'Trigger conditions match — the flow would run.' : 'Trigger conditions do not match — nothing would run.'}
      </p>
    </div>
  );
}

export function ConfigPanel({ dryRun }: { dryRun?: DryRunState | null }) {
  const selectedNodeId = useBuilderStore((s) => s.selectedNodeId);
  const trigger = useBuilderStore((s) => s.trigger);
  // Subscribe to `steps` so the panel re-renders when the selected step's config
  // changes (findStep itself is a stable ref).
  useBuilderStore((s) => s.steps);
  const findStep = useBuilderStore((s) => s.findStep);
  const removeStep = useBuilderStore((s) => s.removeStep);
  const selectNode = useBuilderStore((s) => s.selectNode);

  if (!selectedNodeId) {
    return (
      <EmptyState
        icon={MousePointerClick}
        title="Nothing selected"
        hint="Select the trigger or a step to configure it, or click a + on the canvas to add one."
      />
    );
  }

  if (selectedNodeId === 'trigger') {
    return (
      <Shell
        header={{ eyebrow: 'Trigger', title: triggerLabel(trigger ?? undefined), meta: triggerMeta(trigger?.type) }}
        preview={dryRun ? <TriggerDryPreview dryRun={dryRun} /> : undefined}
      >
        <TriggerConfig />
      </Shell>
    );
  }

  if (selectedNodeId === 'end') {
    return (
      <EmptyState
        icon={MousePointerClick}
        title="End of branch"
        hint="Use the + button on the canvas to add a step here."
      />
    );
  }

  const step = findStep(selectedNodeId);
  if (!step) {
    return (
      <EmptyState
        icon={MousePointerClick}
        title="Nothing selected"
        hint="Select the trigger or a step to configure it."
      />
    );
  }

  const handleDelete = () => {
    removeStep(step.id);
    selectNode(null);
  };

  const dryStep = dryRun ? dryRun.byStep[step.id] : undefined;
  const preview = dryStep ? <StepDryPreview dry={dryStep} /> : undefined;

  if (step.type === 'condition') {
    return (
      <Shell header={{ eyebrow: 'Rule', title: 'If / Else', meta: conditionMeta }} onDelete={handleDelete} preview={preview}>
        <ConditionConfig />
      </Shell>
    );
  }

  if (step.type === 'split') {
    return (
      <Shell header={{ eyebrow: 'Rule', title: 'Percentage split', meta: splitMeta }} onDelete={handleDelete} preview={preview}>
        <SplitConfig step={step} />
      </Shell>
    );
  }

  const isDelay = step.type === 'delay';
  const actionType = step.action?.type ?? '';
  const meta = isDelay ? delayMeta : actionMeta(actionType);
  const title = isDelay ? 'Wait' : (ACTION_TITLES[actionType as keyof typeof ACTION_TITLES] ?? 'Action');

  return (
    <Shell header={{ eyebrow: isDelay ? 'Rule' : 'Action', title, meta }} onDelete={handleDelete} preview={preview}>
      <ActionConfig />
    </Shell>
  );
}
