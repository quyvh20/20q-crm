import React from 'react';
import type { WorkflowStep } from '../types';
import { useBuilderStore, type StepPath } from '../store';
import { WorkflowStepList } from './WorkflowStepList';

interface ConditionSplitNodeProps {
  step: WorkflowStep;
  path: StepPath;
}

export const ConditionSplitNode: React.FC<ConditionSplitNodeProps> = ({ step, path }) => {
  const { selectedNodeId, selectNode, removeStep, errors } = useBuilderStore();
  const isSelected = selectedNodeId === step.id;
  const lastIdx = path[path.length - 1]?.index ?? 0;
  const hasError = !!errors[`step.${step.id}`] || !!errors[`steps.${lastIdx}.condition`];

  const rules = step.condition?.rules ?? [];
  const ruleCount = rules.length;
  const op = step.condition?.op ?? 'AND';

  return (
    <div className="flex flex-col items-center w-full my-4">
      {/* Condition Split Card */}
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
        style={{ minWidth: 280 }}
      >
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-purple-400 to-fuchsia-500 flex items-center justify-center text-lg">
            🔀
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">Condition Split</p>
            <p className="text-sm font-medium text-white truncate">
              {ruleCount > 0
                ? `${ruleCount} rule${ruleCount !== 1 ? 's' : ''} (${op})`
                : 'Configure Conditions'}
            </p>
          </div>
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
        {hasError && (
          <p className="text-xs text-red-400 mt-2">
            {errors[`step.${step.id}`]?.[0] || errors[`steps.${lastIdx}.condition`]?.[0] || 'Invalid conditions'}
          </p>
        )}
      </div>

      {/* Connection line down to branch split */}
      <div className="w-px h-6 bg-gray-700" />

      {/* Branching columns container */}
      <div className="relative flex justify-center w-full max-w-5xl">
        {/* Horizontal connector line */}
        <div className="absolute top-0 left-1/4 right-1/4 h-px bg-gray-700" />

        {/* YES Branch */}
        <div className="flex flex-col items-center w-1/2 px-4 border-r border-gray-800/30">
          {/* Vertical branch line */}
          <div className="w-px h-4 bg-gray-700" />
          <div className="px-3 py-1 rounded-full bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 text-xs font-semibold mb-2">
            Yes / Match
          </div>
          <div className="w-full bg-gray-950/40 p-4 rounded-2xl border border-gray-800/50">
            <WorkflowStepList
              steps={step.yes_steps || []}
              parentId={step.id}
              branch="yes"
              parentPath={path}
            />
          </div>
        </div>

        {/* NO Branch */}
        <div className="flex flex-col items-center w-1/2 px-4">
          {/* Vertical branch line */}
          <div className="w-px h-4 bg-gray-700" />
          <div className="px-3 py-1 rounded-full bg-rose-500/10 border border-rose-500/30 text-rose-400 text-xs font-semibold mb-2">
            No / Else
          </div>
          <div className="w-full bg-gray-950/40 p-4 rounded-2xl border border-gray-800/50">
            <WorkflowStepList
              steps={step.no_steps || []}
              parentId={step.id}
              branch="no"
              parentPath={path}
            />
          </div>
        </div>
      </div>
    </div>
  );
};
