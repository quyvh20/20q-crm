import React, { useState } from 'react';
import type { WorkflowStep, ConditionRule } from '../types';
import { useBuilderStore, type StepPath } from '../store';
import { BranchColumn } from './BranchColumn';

interface ConditionSplitNodeProps {
  step: WorkflowStep;
  path: StepPath;
}

// ── Operator display labels ──────────────────────────────────────────
const OP_LABELS: Record<string, string> = {
  eq: '=',
  neq: '≠',
  gt: '>',
  lt: '<',
  between: 'between',
  contains: 'contains',
  not_contains: '∌',
  starts_with: 'starts with',
  ends_with: 'ends with',
  in: 'in',
  not_in: 'not in',
  is_empty: 'is empty',
  is_not_empty: 'is not empty',
  is_true: 'is true',
  is_false: 'is false',
  is_changed: 'changed',
  is_set: 'is set',
  is_cleared: 'cleared',
  changed_from_to: 'changed →',
  in_last_days: 'in last … days',
};

/** Compact summary of a single condition rule */
function ruleSummary(rule: ConditionRule): string {
  const field = rule.field?.split('.').pop() || '…';
  const op = OP_LABELS[rule.operator || 'eq'] || rule.operator || '?';
  const noValue = ['is_empty', 'is_not_empty', 'is_true', 'is_false', 'is_changed', 'is_set', 'is_cleared'].includes(rule.operator || '');
  if (noValue) return `${field} ${op}`;
  const val = rule.value;
  if (val === null || val === undefined || val === '') return `${field} ${op} …`;
  const display = Array.isArray(val) ? val.join(', ') : String(val);
  return `${field} ${op} "${display}"`;
}

// ── ConditionSplitNode ───────────────────────────────────────────────
export const ConditionSplitNode: React.FC<ConditionSplitNodeProps> = ({ step, path }) => {
  const { selectedNodeId, selectNode, removeStep, errors } = useBuilderStore();
  const isSelected = selectedNodeId === step.id;
  const lastIdx = path[path.length - 1]?.index ?? 0;
  const hasError = !!errors[`step.${step.id}`] || !!errors[`steps.${lastIdx}.condition`];

  const rules = step.condition?.rules ?? [];
  const ruleCount = rules.length;
  const op = step.condition?.op ?? 'AND';

  // Local collapse state — preserved across re-renders
  const [yesCollapsed, setYesCollapsed] = useState(false);
  const [noCollapsed, setNoCollapsed] = useState(false);

  // Build condition summary text
  const summary = ruleCount > 0
    ? rules.slice(0, 2).map((r) => ruleSummary(r)).join(` ${op} `)
      + (ruleCount > 2 ? ` ${op} +${ruleCount - 2} more` : '')
    : null;

  return (
    <div className="flex flex-col items-center w-full my-4">
      {/* ─── Condition Split Card (Header) ─── */}
      <div
        onClick={(e) => {
          e.stopPropagation();
          selectNode(step.id);
        }}
        className={`
          relative p-4 rounded-xl cursor-pointer transition-all duration-200 z-10
          border-2 ${hasError ? 'border-red-500' : isSelected ? 'border-purple-500' : 'border-gray-700'}
          ${isSelected ? 'bg-purple-500/10 shadow-lg shadow-purple-500/20' : 'bg-gray-800/80 hover:bg-gray-800'}
        `}
        style={{ minWidth: 280, maxWidth: 420 }}
      >
        {/* Row 1: Icon + title + edit + delete */}
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-purple-400 to-fuchsia-500 flex items-center justify-center text-lg shrink-0">
            🔀
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">
              Condition Split
            </p>
            <p className="text-sm font-medium text-white truncate">
              {ruleCount > 0
                ? `${ruleCount} rule${ruleCount !== 1 ? 's' : ''} (${op})`
                : 'Configure Conditions'}
            </p>
          </div>

          {/* Edit button — opens the side panel */}
          <button
            onClick={(e) => {
              e.stopPropagation();
              selectNode(step.id);
            }}
            title="Edit conditions"
            className="w-7 h-7 rounded-full flex items-center justify-center text-gray-500 hover:text-purple-400 hover:bg-purple-400/10 transition-colors"
          >
            <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M15.232 5.232l3.536 3.536m-2.036-5.036a2.5 2.5 0 113.536 3.536L6.5 21.036H3v-3.572L16.732 3.732z" />
            </svg>
          </button>

          {/* Delete button */}
          <button
            onClick={(e) => {
              e.stopPropagation();
              removeStep(step.id);
            }}
            className="w-7 h-7 rounded-full flex items-center justify-center text-gray-500 hover:text-red-400 hover:bg-red-400/10 transition-colors"
          >
            ✕
          </button>
        </div>

        {/* Row 2: Condition summary (field + operator + value preview) */}
        {summary && (
          <p className="text-xs text-purple-300/70 mt-2 truncate pl-13" title={summary}>
            {summary}
          </p>
        )}

        {/* Error message */}
        {hasError && (
          <p className="text-xs text-red-400 mt-2">
            {errors[`step.${step.id}`]?.[0] || errors[`steps.${lastIdx}.condition`]?.[0] || 'Invalid conditions'}
          </p>
        )}
      </div>

      {/* Connection line down to branch split */}
      <div className="w-px h-6 bg-gray-700" />

      {/* ─── Branching columns container ─── */}
      <div className="relative flex justify-center w-full max-w-5xl">
        {/* Horizontal connector line */}
        <div className="absolute top-0 left-1/4 right-1/4 h-px bg-gray-700" />

        {/* YES Branch */}
        <BranchColumn
          label="Yes"
          icon="✓"
          colorClass="text-emerald-400"
          bgClass="bg-emerald-500/10"
          borderClass="border border-emerald-500/30"
          steps={step.yes_steps || []}
          parentId={step.id}
          branch="yes"
          parentPath={path}
          collapsed={yesCollapsed}
          onToggle={() => setYesCollapsed((c) => !c)}
        />

        {/* NO Branch */}
        <BranchColumn
          label="No"
          icon="✗"
          colorClass="text-rose-400"
          bgClass="bg-rose-500/10"
          borderClass="border border-rose-500/30"
          steps={step.no_steps || []}
          parentId={step.id}
          branch="no"
          parentPath={path}
          collapsed={noCollapsed}
          onToggle={() => setNoCollapsed((c) => !c)}
        />
      </div>
    </div>
  );
};
