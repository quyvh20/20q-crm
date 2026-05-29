import React from 'react';
import type { WorkflowStep } from '../types';
import type { StepPath } from '../store';
import { WorkflowStepList } from './WorkflowStepList';

// ── Props ────────────────────────────────────────────────────────────
export interface BranchColumnProps {
  /** Display label, e.g. "Yes" or "No" */
  label: string;
  /** Icon before label, e.g. "✓" or "✗" */
  icon: string;
  /** Tailwind text color, e.g. "text-emerald-400" */
  colorClass: string;
  /** Tailwind background, e.g. "bg-emerald-500/10" */
  bgClass: string;
  /** Tailwind border, e.g. "border border-emerald-500/30" */
  borderClass: string;
  /** Steps inside this branch */
  steps: WorkflowStep[];
  /** Parent condition step ID */
  parentId: string;
  /** Branch key — "yes" or "no" */
  branch: 'yes' | 'no';
  /** Full path to the parent condition step */
  parentPath: StepPath;
  /** Whether the branch content is collapsed */
  collapsed: boolean;
  /** Toggle collapse state */
  onToggle: () => void;
}

/**
 * BranchColumn — renders one branch of a condition split.
 *
 * Contains:
 * 1. Vertical list of steps (recursive StepRenderer per step)
 *    via WorkflowStepList → StepRenderer dispatch
 * 2. "+" button to add step (opens action palette)
 *    via WorkflowStepList → AddNodeButton between each step
 * 3. Drop zone for drag/drop
 *    via WorkflowStepList → SortableContext + AddNodeButton useDroppable
 *
 * Visual wrapper adds:
 * - Branch label pill (icon + name + step count badge)
 * - Collapsible content with animated chevron
 * - "N steps hidden" indicator when collapsed
 */
export const BranchColumn: React.FC<BranchColumnProps> = ({
  label,
  icon,
  colorClass,
  bgClass,
  borderClass,
  steps,
  parentId,
  branch,
  parentPath,
  collapsed,
  onToggle,
}) => {
  const count = steps.length;

  return (
    <div className="flex flex-col items-center flex-1 min-w-[280px] px-4">
      {/* ─── Vertical connector from parent ─── */}
      <div className="w-px h-4 bg-gray-700" />

      {/* ─── Branch label pill with collapse toggle ─── */}
      <button
        onClick={(e) => {
          e.stopPropagation();
          onToggle();
        }}
        className={`
          flex items-center gap-1.5 px-3 py-1 rounded-full text-xs font-semibold mb-2
          transition-all duration-200 cursor-pointer select-none
          ${bgClass} ${borderClass} ${colorClass}
          hover:brightness-125
        `}
      >
        <span>{icon}</span>
        <span>{label}</span>
        {count > 0 && (
          <span className="ml-0.5 px-1.5 py-0 rounded-full bg-black/20 text-[10px] font-mono">
            {count}
          </span>
        )}
        {/* Chevron — rotates when collapsed */}
        <svg
          className={`w-3 h-3 transition-transform duration-200 ${collapsed ? '-rotate-90' : 'rotate-0'}`}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={3}
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
        </svg>
      </button>

      {/* ─── Collapsible step list ─── */}
      <div
        className={`
          w-full overflow-hidden transition-all duration-300 ease-in-out
          ${collapsed ? 'max-h-0 opacity-0' : 'max-h-[2000px] opacity-100'}
        `}
      >
        <div className="w-full bg-gray-950/40 p-4 rounded-2xl border border-gray-800/50">
          {/*
            WorkflowStepList provides:
            ① Recursive StepRenderer for each step (action/condition/delay dispatch)
            ② AddNodeButton between steps ("+" opens action palette)
            ③ SortableContext + useDroppable drop zones for drag/drop
          */}
          <WorkflowStepList
            steps={steps}
            parentId={parentId}
            branch={branch}
            parentPath={parentPath}
          />
        </div>
      </div>

      {/* ─── Collapsed indicator ─── */}
      {collapsed && count > 0 && (
        <p className={`text-[10px] mt-1 ${colorClass} opacity-60`}>
          {count} step{count !== 1 ? 's' : ''} hidden
        </p>
      )}
    </div>
  );
};
