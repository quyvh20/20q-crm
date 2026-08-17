// React Flow node components for the builder, styled Brevo-like: trigger/action
// nodes are white cards with a category-tinted header strip (solid icon disc +
// title + kebab menu) and a one-line config summary body; rules (delay /
// condition) are compact violet pills; open path tails are "+ Add step" pills
// that read as Exit points when the canvas is read-only. All chrome composes
// design tokens + the three category accents from nodeMeta (no emoji, no
// hardcoded dark colors).
//
// When a dry run is active (A3.5), each node reads its outcome from BuilderContext
// and tints itself: a green ring + "Runs" badge on the taken path, dimmed + "Skipped"
// off it; the condition node shows which branch was taken; the trigger shows whether
// the workflow-level conditions passed.
//
// During a palette drag (Brevo-style drag-to-canvas), the matching drop targets
// light up: insert slots for step items, the trigger node for trigger items.

import { Handle, Position, type NodeProps } from '@xyflow/react';
import { Plus, MoreVertical, LogOut, Settings2, Trash2, CopyPlus, AlertTriangle } from 'lucide-react';
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from '@/components/ui';
import type { BuilderNode } from './graph';
import { NODE_WIDTH } from './graph';
import { useBuilderActions } from './BuilderContext';
import type { TestRunStep } from '../types';
import {
  actionMeta,
  conditionMeta,
  delayMeta,
  triggerMeta,
  triggerTitle,
  triggerDescription,
  stepSubtitle,
  delayLabel,
  ACTION_TITLES,
  TRIGGER_ACCENT,
  ACTION_ACCENT,
  RULE_ACCENT,
} from './nodeMeta';

const cardBase =
  'relative rounded-xl border bg-card shadow-sm transition-all select-none';
const cardSelected = 'border-ring ring-2 ring-ring/40';
const cardIdle = 'border-border hover:border-ring/60';
// Highlight for a valid drop target while a palette item is dragged over the canvas.
const dropTargetClass = 'border-primary ring-2 ring-primary/40';

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
      className={`absolute -top-2 right-3 z-10 rounded-full px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide ${
        runs ? 'bg-emerald-500 text-white' : 'bg-muted text-muted-foreground'
      }`}
    >
      {runs ? 'Runs' : 'Skipped'}
    </span>
  );
}

// Amber "this blocks saving" badge — the canvas points at the exact node with a
// problem instead of leaving the user to hunt through config forms. Appears
// after a failed save and clears live as issues are fixed (NextBuilder
// revalidates on edit while errors exist).
function IssueBadge({ msgs }: { msgs: string[] }) {
  return (
    <span
      title={msgs.join('\n')}
      className="absolute -top-2 left-3 z-10 flex h-4 items-center gap-0.5 rounded-full bg-amber-500 px-1.5 text-[9px] font-bold text-white"
    >
      <AlertTriangle className="h-2.5 w-2.5" />
      {msgs.length}
    </span>
  );
}

function useIssues(stepId: string | undefined): string[] {
  const { issues } = useBuilderActions();
  return (stepId && issues?.byStep[stepId]) || [];
}

// Solid filled icon disc (Brevo-style) used in card headers and rule pills.
function Disc({
  icon: Icon,
  solid,
  size = 'md',
}: {
  icon: React.ComponentType<{ className?: string }>;
  solid: string;
  size?: 'md' | 'sm';
}) {
  const disc = size === 'md' ? 'h-6 w-6' : 'h-5 w-5';
  const glyph = size === 'md' ? 'h-3.5 w-3.5' : 'h-3 w-3';
  return (
    <span className={`flex ${disc} shrink-0 items-center justify-center rounded-full ${solid}`}>
      <Icon className={glyph} />
    </span>
  );
}

// Kebab menu in a node's header. Stops propagation so opening it never selects
// the node or starts a canvas drag; `nodrag` keeps React Flow's drag away.
// Duplicate is offered for action/delay steps only — a condition must stay
// terminal in its sibling list, so a copy after it would break the no-merge rule.
function NodeKebab({ stepId, canDuplicate }: { stepId?: string; canDuplicate?: boolean }) {
  const { onSelect, onDelete, onDuplicate, readOnly } = useBuilderActions();
  if (readOnly) return null;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label="Step options"
          onClick={(e) => e.stopPropagation()}
          className="nodrag ml-auto flex h-6 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-foreground/10 hover:text-foreground"
        >
          <MoreVertical className="h-3.5 w-3.5" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
        <DropdownMenuItem onSelect={() => onSelect(stepId ?? 'trigger')}>
          <Settings2 />
          Configure
        </DropdownMenuItem>
        {stepId && canDuplicate && onDuplicate && (
          <DropdownMenuItem onSelect={() => onDuplicate(stepId)}>
            <CopyPlus />
            Duplicate
          </DropdownMenuItem>
        )}
        {stepId && onDelete && (
          <DropdownMenuItem destructive onSelect={() => onDelete(stepId)}>
            <Trash2 />
            Delete
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function TriggerNode({ data, selected }: NodeProps<BuilderNode>) {
  const meta = triggerMeta(data.trigger?.type);
  const { dryRun, dragging, onDropTrigger, readOnly, issues } = useBuilderActions();
  const droppable = !readOnly && dragging?.kind === 'trigger';
  const triggerIssues = issues?.trigger ?? [];
  return (
    <div
      className={`${cardBase} ${droppable ? dropTargetClass : selected ? cardSelected : cardIdle}`}
      style={{ width: NODE_WIDTH }}
      onDragOver={
        droppable
          ? (e) => {
              e.preventDefault();
              e.dataTransfer.dropEffect = 'copy';
            }
          : undefined
      }
      onDrop={
        droppable
          ? (e) => {
              e.preventDefault();
              onDropTrigger?.();
            }
          : undefined
      }
    >
      {dryRun && (
        <span
          className={`absolute -top-2 right-3 z-10 rounded-full px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide ${
            dryRun.conditionResult
              ? 'bg-emerald-500 text-white'
              : 'bg-amber-500 text-white'
          }`}
        >
          {dryRun.conditionResult ? 'Match' : 'No match'}
        </span>
      )}
      {triggerIssues.length > 0 && <IssueBadge msgs={triggerIssues} />}
      <div className={`flex items-center gap-2 rounded-t-xl px-3 py-2 ${TRIGGER_ACCENT.header}`}>
        <Disc icon={meta.icon} solid={meta.solid} />
        <span className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground">
          {triggerTitle(data.trigger)}
        </span>
        <NodeKebab />
      </div>
      <div className="px-3 py-2.5 text-xs leading-relaxed text-muted-foreground">
        <span className="line-clamp-2">{triggerDescription(data.trigger)}</span>
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
  const issueMsgs = useIssues(step?.id);
  return (
    <div
      className={`${cardBase} ${selected ? cardSelected : cardIdle} ${dryCardClass(dry)}`}
      style={{ width: NODE_WIDTH }}
    >
      {dry && <DryBadge dry={dry} />}
      {issueMsgs.length > 0 && <IssueBadge msgs={issueMsgs} />}
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground/40 !border-0" />
      <div className={`flex items-center gap-2 rounded-t-xl px-3 py-2 ${ACTION_ACCENT.header}`}>
        <Disc icon={meta.icon} solid={meta.solid} />
        <span className="min-w-0 flex-1 truncate text-xs font-semibold text-foreground">
          {ACTION_TITLES[type as keyof typeof ACTION_TITLES] ?? 'Action'}
        </span>
        <NodeKebab stepId={step?.id} canDuplicate />
      </div>
      <div className="truncate px-3 py-2.5 text-xs text-muted-foreground">
        {step ? stepSubtitle(step) : ''}
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground/40 !border-0" />
    </div>
  );
}

// Shared shell for rule nodes (delay / condition): a compact centered violet
// pill inside a full-NODE_WIDTH wrapper, so dagre's centering and the edge
// handle positions stay stable. The wrapper is pointer-transparent — only the
// pill itself is interactive, so the invisible margins beside it can't steal
// clicks or start a drag-reorder the user meant as a canvas pan.
function RulePill({
  selected,
  dry,
  children,
}: {
  selected?: boolean;
  dry?: TestRunStep;
  children: React.ReactNode;
}) {
  return (
    <div style={{ width: NODE_WIDTH }} className="pointer-events-none flex justify-center">
      <Handle type="target" position={Position.Top} className="!bg-muted-foreground/40 !border-0" />
      <div
        className={`pointer-events-auto relative flex items-center gap-2 rounded-full border bg-violet-500/10 py-1.5 pl-2 pr-2.5 shadow-sm transition-all select-none ${
          selected ? cardSelected : 'border-violet-500/20 hover:border-ring/60'
        } ${dryCardClass(dry)}`}
      >
        {dry && <DryBadge dry={dry} />}
        {children}
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-muted-foreground/40 !border-0" />
    </div>
  );
}

export function DelayNode({ data, selected }: NodeProps<BuilderNode>) {
  const step = data.step;
  const dry = useDry(step?.id);
  const issueMsgs = useIssues(step?.id);
  return (
    <RulePill selected={selected} dry={dry}>
      {issueMsgs.length > 0 && <IssueBadge msgs={issueMsgs} />}
      <Disc icon={delayMeta.icon} solid={delayMeta.solid} size="sm" />
      <span className={`max-w-[190px] truncate text-xs font-semibold ${RULE_ACCENT.accent}`}>
        {delayLabel(step?.delay)}
      </span>
      <NodeKebab stepId={step?.id} canDuplicate />
    </RulePill>
  );
}

export function ConditionNode({ data, selected }: NodeProps<BuilderNode>) {
  const step = data.step;
  const dry = useDry(step?.id);
  const issueMsgs = useIssues(step?.id);
  const takenBranch = dry?.status === 'run' && dry.branch ? dry.branch : null;
  return (
    <RulePill selected={selected} dry={dry}>
      {issueMsgs.length > 0 && <IssueBadge msgs={issueMsgs} />}
      <Disc icon={conditionMeta.icon} solid={conditionMeta.solid} size="sm" />
      <span className={`truncate text-xs font-semibold ${RULE_ACCENT.accent}`}>If / Else</span>
      <span className="max-w-[130px] truncate text-[11px] text-muted-foreground">
        {step ? stepSubtitle(step) : ''}
      </span>
      {takenBranch && (
        <span className="rounded bg-emerald-500/15 px-1 text-[10px] font-semibold uppercase text-emerald-600 dark:text-emerald-400">
          → {takenBranch}
        </span>
      )}
      <NodeKebab stepId={step?.id} />
    </RulePill>
  );
}

export function EndNode({ data }: NodeProps<BuilderNode>) {
  const { onInsert, readOnly, dragging, onDropStep } = useBuilderActions();
  if (readOnly) {
    // Read-only tails render as Brevo-style Exit pills.
    return (
      <div style={{ width: NODE_WIDTH }} className="pointer-events-none flex justify-center">
        <Handle type="target" position={Position.Top} className="!bg-transparent !border-0" />
        <span className="flex items-center gap-1.5 rounded-full border border-border bg-muted px-3 py-1 text-xs font-medium text-muted-foreground">
          <LogOut className="h-3 w-3" />
          Exit
          {data.branchLabel && (
            <span className="rounded bg-background px-1 text-[10px] uppercase">{data.branchLabel}</span>
          )}
        </span>
      </div>
    );
  }
  const droppable = dragging?.kind === 'step' && !!data.insert;
  return (
    <div style={{ width: NODE_WIDTH }} className="pointer-events-none flex justify-center">
      <Handle type="target" position={Position.Top} className="!bg-transparent !border-0" />
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          if (data.insert) onInsert(data.insert, { x: e.clientX, y: e.clientY });
        }}
        onDragOver={
          droppable
            ? (e) => {
                e.preventDefault();
                e.dataTransfer.dropEffect = 'copy';
              }
            : undefined
        }
        onDrop={
          droppable
            ? (e) => {
                e.preventDefault();
                if (data.insert) onDropStep?.(data.insert);
              }
            : undefined
        }
        className={`group pointer-events-auto flex items-center gap-1.5 rounded-full border bg-background px-3 py-1.5 text-xs font-medium transition-colors ${
          droppable
            ? 'animate-pulse border-solid border-primary bg-primary/10 text-primary'
            : 'border-dashed border-border text-muted-foreground hover:border-ring hover:text-foreground'
        }`}
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
