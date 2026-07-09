// React Flow node components for the builder. All nodes share a card shell and
// use design tokens + lucide icons (no emoji, no hardcoded dark colors). Nodes
// are selectable but not draggable — structure is edited via the "+" insert
// buttons on edges, not free-form wiring.
//
// When a dry run is active (A3.5), each node reads its outcome from BuilderContext
// and tints itself: a green ring + "Runs" badge on the taken path, dimmed + "Skipped"
// off it; the condition node shows which branch was taken; the trigger shows whether
// the workflow-level conditions passed.

import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Plus } from 'lucide-react';
import type { BuilderNode } from './graph';
import { NODE_WIDTH } from './graph';
import { useBuilderActions } from './BuilderContext';
import type { TestRunStep } from '../types';
import {
  actionMeta,
  conditionMeta,
  delayMeta,
  triggerMeta,
  triggerLabel,
  stepSubtitle,
  delayLabel,
  ACTION_TITLES,
} from './nodeMeta';

const cardBase =
  'relative rounded-xl border bg-card shadow-sm transition-all select-none';
const cardSelected = 'border-ring ring-2 ring-ring/40';
const cardIdle = 'border-border hover:border-ring/60';

// Dry-run tint applied on top of the base/selected classes.
function dryCardClass(dry: TestRunStep | undefined): string {
  if (!dry) return '';
  return dry.status === 'run' ? 'ring-2 ring-emerald-500/60' : 'opacity-50';
}

function useDry(stepId: string | undefined): TestRunStep | undefined {
  const { dryRun } = useBuilderActions();
  return stepId && dryRun ? dryRun.byStep[stepId] : undefined;
}

function DryBadge({ dry }: { dry: TestRunStep }) {
  const runs = dry.status === 'run';
  return (
    <span
      className={`absolute -top-2 right-3 rounded-full px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide ${
        runs ? 'bg-emerald-500 text-white' : 'bg-muted text-muted-foreground'
      }`}
    >
      {runs ? 'Runs' : 'Skipped'}
    </span>
  );
}

function Chip({ icon: Icon, accent, chip }: { icon: React.ComponentType<{ className?: string }>; accent: string; chip: string }) {
  return (
    <span className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${chip}`}>
      <Icon className={`h-4.5 w-4.5 ${accent}`} />
    </span>
  );
}

export function TriggerNode({ data, selected }: NodeProps<BuilderNode>) {
  const meta = triggerMeta(data.trigger?.type);
  const { dryRun } = useBuilderActions();
  return (
    <div
      className={`${cardBase} ${selected ? cardSelected : cardIdle} px-3 py-2.5`}
      style={{ width: NODE_WIDTH }}
    >
      {dryRun && (
        <span
          className={`absolute -top-2 right-3 rounded-full px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide ${
            dryRun.conditionResult
              ? 'bg-emerald-500 text-white'
              : 'bg-amber-500 text-white'
          }`}
        >
          {dryRun.conditionResult ? 'Match' : 'No match'}
        </span>
      )}
      <div className="flex items-center gap-3">
        <Chip icon={meta.icon} accent={meta.accent} chip={meta.chip} />
        <div className="min-w-0">
          <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Trigger</div>
          <div className="truncate text-sm font-semibold text-foreground">{triggerLabel(data.trigger)}</div>
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground/40 !border-0" />
    </div>
  );
}

export function ActionNode({ data, selected }: NodeProps<BuilderNode>) {
  const step = data.step;
  const type = step?.action?.type ?? '';
  const meta = actionMeta(type);
  const dry = useDry(step?.id);
  return (
    <div
      className={`${cardBase} ${selected ? cardSelected : cardIdle} ${dryCardClass(dry)} px-3 py-2.5`}
      style={{ width: NODE_WIDTH }}
    >
      {dry && <DryBadge dry={dry} />}
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground/40 !border-0" />
      <div className="flex items-center gap-3">
        <Chip icon={meta.icon} accent={meta.accent} chip={meta.chip} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold text-foreground">{ACTION_TITLES[type as keyof typeof ACTION_TITLES] ?? 'Action'}</div>
          <div className="truncate text-xs text-muted-foreground">{step ? stepSubtitle(step) : ''}</div>
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground/40 !border-0" />
    </div>
  );
}

export function DelayNode({ data, selected }: NodeProps<BuilderNode>) {
  const step = data.step;
  const dry = useDry(step?.id);
  return (
    <div
      className={`${cardBase} ${selected ? cardSelected : cardIdle} ${dryCardClass(dry)} px-3 py-2`}
      style={{ width: NODE_WIDTH }}
    >
      {dry && <DryBadge dry={dry} />}
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground/40 !border-0" />
      <div className="flex items-center gap-3">
        <Chip icon={delayMeta.icon} accent={delayMeta.accent} chip={delayMeta.chip} />
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-foreground">
            {delayLabel(step?.delay)}
          </div>
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground/40 !border-0" />
    </div>
  );
}

export function ConditionNode({ data, selected }: NodeProps<BuilderNode>) {
  const step = data.step;
  const dry = useDry(step?.id);
  const takenBranch = dry?.status === 'run' && dry.branch ? dry.branch : null;
  return (
    <div
      className={`${cardBase} ${selected ? cardSelected : cardIdle} ${dryCardClass(dry)} px-3 py-2`}
      style={{ width: NODE_WIDTH }}
    >
      {dry && <DryBadge dry={dry} />}
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground/40 !border-0" />
      <div className="flex items-center gap-3">
        <Chip icon={conditionMeta.icon} accent={conditionMeta.accent} chip={conditionMeta.chip} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm font-semibold text-foreground">If / Else</span>
            {takenBranch && (
              <span className="rounded bg-emerald-500/15 px-1 text-[10px] font-semibold uppercase text-emerald-600 dark:text-emerald-400">
                → {takenBranch}
              </span>
            )}
          </div>
          <div className="truncate text-xs text-muted-foreground">{step ? stepSubtitle(step) : ''}</div>
        </div>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground/40 !border-0" />
    </div>
  );
}

export function EndNode({ data }: NodeProps<BuilderNode>) {
  const { onInsert, readOnly } = useBuilderActions();
  if (readOnly) {
    return (
      <div style={{ width: NODE_WIDTH }} className="flex justify-center">
        <Handle type="target" position={Position.Top} className="!bg-transparent !border-0" />
        <span className="rounded-full border border-dashed border-border px-3 py-1 text-xs text-muted-foreground">End</span>
      </div>
    );
  }
  return (
    <div style={{ width: NODE_WIDTH }} className="flex justify-center">
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0" />
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          if (data.insert) onInsert(data.insert, { x: e.clientX, y: e.clientY });
        }}
        className="group flex items-center gap-1.5 rounded-full border border-dashed border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:border-ring hover:text-foreground"
      >
        <Plus className="h-3.5 w-3.5" />
        Add step
        {data.branchLabel && (
          <span className="ml-1 rounded bg-muted px-1 text-[10px] uppercase">{data.branchLabel}</span>
        )}
      </button>
    </div>
  );
}

export const nodeTypes = {
  trigger: TriggerNode,
  action: ActionNode,
  delay: DelayNode,
  condition: ConditionNode,
  end: EndNode,
};
