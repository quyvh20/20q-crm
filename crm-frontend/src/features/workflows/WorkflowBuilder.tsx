import React, { useEffect, useCallback, useState, useMemo } from 'react';
import {
  DndContext,
  DragOverlay,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core';
import { useParams, useNavigate } from 'react-router-dom';
import { useBuilderStore, generateActionId, getStepAtPath, isDescendant, type StepPath } from './store';
import type { WorkflowStep } from './types';
import { WorkflowDragContext, type DragContextValue } from './DragContext';
import { TriggerNode } from './nodes/TriggerNode';
import { ConditionNode } from './nodes/ConditionNode';
import { WorkflowStepList } from './nodes/WorkflowStepList';
import { TriggerConfigPanel } from './panels/TriggerConfigPanel';
import { ConditionConfigPanel } from './panels/ConditionConfigPanel';
import { ActionConfigPanel } from './panels/ActionConfigPanel';
import { ActionPalette } from './panels/ActionPalette';
import type { ActionType } from './types';
import { ACTION_LABELS, ACTION_ICONS } from './types';

/**
 * Resolve the full StepPath for a given step ID by searching the tree.
 * Returns the path if found, or null.
 */
function resolvePathById(steps: WorkflowStep[], targetId: string, currentPath: StepPath = []): StepPath | null {
  for (let i = 0; i < steps.length; i++) {
    const step = steps[i];
    const seg = currentPath.length > 0 && currentPath[currentPath.length - 1]?.branch
      ? { index: i, branch: currentPath[currentPath.length - 1]!.branch! } as StepPath[number]
      : { index: i } as StepPath[number];

    // For root level, build a simple path
    const myPath: StepPath = [...currentPath.slice(0, -1), seg];

    if (step.id === targetId) return myPath;
    if (step.yes_steps) {
      const found = resolvePathById(step.yes_steps, targetId, [...myPath, { index: 0, branch: 'yes' }]);
      if (found) return found;
    }
    if (step.no_steps) {
      const found = resolvePathById(step.no_steps, targetId, [...myPath, { index: 0, branch: 'no' }]);
      if (found) return found;
    }
  }
  return null;
}

export const WorkflowBuilder: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const store = useBuilderStore();

  useEffect(() => {
    // Fetch schema once on builder mount (cached in store + deduped)
    store.fetchSchema();

    if (id && id !== 'new') {
      store.loadWorkflow(id);
    } else {
      store.reset();
    }
    return () => store.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: () => ({ x: 0, y: 0 }) })
  );

  const [activeDragType, setActiveDragType] = useState<string | null>(null);
  const [activeDragId, setActiveDragId] = useState<string | null>(null);
  const [activeDragPath, setActiveDragPath] = useState<StepPath | null>(null);

  const handleDragStart = useCallback((event: DragStartEvent) => {
    const data = event.active.data.current;
    if (data?.source === 'palette') {
      setActiveDragType(data.actionType as string);
      setActiveDragId(null);
      setActiveDragPath(null);
    } else if (data?.source === 'canvas') {
      setActiveDragId(event.active.id.toString());
      setActiveDragType(null);
      setActiveDragPath(data.path as StepPath);
    }
  }, []);

  /**
   * Unified drag-end handler supporting:
   * 1. Palette → drop zone (any branch/root)
   * 2. Same-branch reorder (reorderSteps)
   * 3. Cross-branch / cross-tree move (removeStep + addStep)
   *
   * Drop zone data shape:  { parentId, branch, targetIndex }
   * Canvas drag data shape: { source: 'canvas', path: StepPath }
   */
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over) return;

      const activeData = active.data.current;
      const overData = over.data.current;
      const isDropZone = over.id.toString().startsWith('dropzone-');

      // ── 1. Palette → drop zone ─────────────────────────────────────
      if (activeData?.source === 'palette' && isDropZone) {
        const parentId = overData?.parentId ?? null;
        const branch = overData?.branch ?? null;
        const targetIndex = overData?.targetIndex ?? 0;
        const actionType = activeData.actionType as string;

        const id = generateActionId();
        if (actionType === 'condition') {
          store.addStep(
            {
              id,
              type: 'condition',
              condition: {
                op: 'AND',
                rules: [{ field: '', operator: 'eq', value: '' }],
              },
              yes_steps: [],
              no_steps: [],
            },
            parentId,
            branch,
            targetIndex
          );
        } else {
          store.addStep(
            {
              id,
              type: actionType === 'delay' ? 'delay' : 'action',
              action: actionType === 'delay' ? undefined : {
                id,
                type: actionType as any,
                params: getDefaultParams(actionType as any),
              },
              delay: actionType === 'delay' ? { duration_sec: 60 } : undefined,
            },
            parentId,
            branch,
            targetIndex
          );
        }
        return;
      }

      // ── 2 & 3. Canvas step → drop zone (reorder or cross-move) ────
      if (activeData?.source === 'canvas' && isDropZone) {
        const srcPath: StepPath = activeData.path;
        const destParentId: string | null = overData?.parentId ?? null;
        const destBranch: 'yes' | 'no' | null = overData?.branch ?? null;
        const destIndex: number = overData?.targetIndex ?? 0;

        // Derive source's parentId/branch/index from path
        const srcSegment = srcPath[srcPath.length - 1];
        if (!srcSegment) return;
        const srcIndex = srcSegment.index;

        // Determine source container
        let srcParentId: string | null = null;
        let srcBranch: 'yes' | 'no' | null = null;

        if (srcPath.length === 1) {
          // Root level
          srcParentId = null;
          srcBranch = null;
        } else {
          // Inside a branch — the parent is the condition step at srcPath[:-1]
          const parentPath = srcPath.slice(0, -1);
          const parentStep = getStepAtPath(store.steps || [], parentPath);
          srcParentId = parentStep?.id ?? null;
          srcBranch = srcSegment.branch ?? null;
        }

        // Check: same container? → reorder within
        const sameContainer = srcParentId === destParentId && srcBranch === destBranch;

        if (sameContainer) {
          // Adjust destination index: if dragging down within the same list,
          // removing the item shifts indices
          let adjustedDest = destIndex;
          if (srcIndex < destIndex) {
            adjustedDest = destIndex - 1;
          }
          if (srcIndex !== adjustedDest) {
            store.reorderSteps(srcParentId, srcBranch, srcIndex, adjustedDest);
          }
        } else {
          // Cross-branch / cross-tree move
          const draggedStep = store.findStep(active.id.toString());
          if (!draggedStep) return;

          // Cycle guard: if the drop target IS the dragged step or inside
          // its subtree, block the move.
          if (destParentId) {
            if (destParentId === active.id.toString()) {
              return; // Block — can't drop into yourself
            }
            const destParentPath = resolvePathById(store.steps || [], destParentId);
            if (destParentPath && isDescendant(srcPath, destParentPath)) {
              return; // Block — would create a cycle
            }
          }

          // Clone, remove, re-add
          const clone = JSON.parse(JSON.stringify(draggedStep)) as WorkflowStep;
          store.removeStep(active.id.toString());
          // After removal, indices may have shifted. Use getState for fresh data.
          useBuilderStore.getState().addStep(clone, destParentId, destBranch, destIndex);
        }
        return;
      }

      // ── 4. Canvas step → another canvas step (drop directly on a step) ─
      if (activeData?.source === 'canvas' && !isDropZone) {
        // Dropped on another step — find the over step's position and
        // treat it as a reorder to that index in the root list
        const activeStep = store.findStep(active.id.toString());
        const overStep = store.findStep(over.id.toString());
        if (activeStep && overStep) {
          const rootSteps = store.steps || [];
          const fromIdx = rootSteps.findIndex((s) => s.id === active.id);
          const toIdx = rootSteps.findIndex((s) => s.id === over.id);
          if (fromIdx !== -1 && toIdx !== -1 && fromIdx !== toIdx) {
            store.reorderSteps(null, null, fromIdx, toIdx);
          }
        }
      }
    },
    [store]
  );

  const handleDragEnd2 = useCallback(
    (event: DragEndEvent) => {
      setActiveDragType(null);
      setActiveDragId(null);
      setActiveDragPath(null);
      handleDragEnd(event);
    },
    [handleDragEnd]
  );

  // Context value for drop zone validity feedback
  const dragCtx = useMemo<DragContextValue>(() => ({
    activeDragPath,
    activeDragStepId: activeDragId,
  }), [activeDragPath, activeDragId]);

  const handleSave = async () => {
    if (!store.validate()) return; // show validation errors first
    try {
      await store.save();
      const wfId = useBuilderStore.getState().workflowId;
      if (wfId && (!id || id === 'new')) {
        navigate(`/workflows/${wfId}`, { replace: true });
      }
    } catch (e: any) {
      alert(e.message || 'Failed to save workflow');
    }
  };

  const handleCanvasClick = (e: React.MouseEvent) => {
    // Deselect node when clicking on the canvas background
    if (e.target === e.currentTarget) {
      store.selectNode(null);
    }
  };

  // Determine which panel to show
  const renderConfigPanel = () => {
    if (store.selectedNodeId === 'trigger') return <TriggerConfigPanel />;
    if (store.selectedNodeId === 'conditions') return <ConditionConfigPanel />;
    if (store.selectedNodeId) {
      const step = store.findStep(store.selectedNodeId);
      if (step) {
        if (step.type === 'condition') {
          return <ConditionConfigPanel />;
        }
        return <ActionConfigPanel />;
      }
    }
    return <ActionPalette />;
  };

  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={handleDragStart} onDragEnd={handleDragEnd2}>
    <WorkflowDragContext.Provider value={dragCtx}>
      <div className="flex h-[calc(100vh-64px)]">
        {/* Left sidebar — palette + config */}
        <div className="w-80 border-r border-gray-800 bg-gray-900/95 flex flex-col overflow-hidden">
          <div className="p-4 border-b border-gray-800">
            <input
              type="text"
              value={store.name}
              onChange={(e) => store.setName(e.target.value)}
              placeholder="Workflow name..."
              className={`w-full bg-transparent text-lg font-semibold text-white placeholder-gray-600 focus:outline-none border-b-2 pb-1 ${
                store.errors.name || store.errors.steps ? 'border-red-500' : 'border-transparent focus:border-indigo-500'
              }`}
            />
            {store.errors.name && <p className="text-xs text-red-400 mt-1">{store.errors.name[0]}</p>}
            {store.errors.steps && <p className="text-xs text-red-400 mt-1">{store.errors.steps[0]}</p>}
            <textarea
              value={store.description}
              onChange={(e) => store.setDescription(e.target.value)}
              placeholder="Description (optional)..."
              rows={2}
              className="w-full bg-transparent text-sm text-gray-400 placeholder-gray-700 focus:outline-none resize-none mt-2"
            />
          </div>

          {/* Schema error banner */}
          {store.schemaError && (
            <div className="mx-4 mt-3 flex items-center gap-2 p-2.5 rounded-lg bg-red-500/10 border border-red-500/30">
              <span className="text-xs text-red-400 flex-1">⚠ Schema: {store.schemaError}</span>
              <button
                onClick={() => store.invalidateSchema()}
                className="text-xs text-red-300 hover:text-white underline whitespace-nowrap"
              >
                Retry
              </button>
            </div>
          )}

          <div className="flex-1 overflow-y-auto p-4">
            {renderConfigPanel()}
          </div>
          <div className="p-4 border-t border-gray-800 space-y-2">
            <button
              onClick={handleSave}
              disabled={store.saving}
              className="w-full py-2.5 rounded-xl bg-gradient-to-r from-indigo-500 to-purple-500 text-white font-medium text-sm hover:from-indigo-600 hover:to-purple-600 disabled:opacity-50 transition-all"
            >
              {store.saving ? 'Saving...' : store.workflowId ? 'Save Changes' : 'Create Workflow'}
            </button>
            <div className="flex gap-2">
              <button
                onClick={() => navigate('/workflows')}
                className="flex-1 py-2 rounded-xl border border-gray-700 text-gray-400 text-sm hover:text-white hover:border-gray-600 transition-colors"
              >
                ← Back
              </button>
              <button
                onClick={() => store.invalidateSchema()}
                disabled={store.schemaLoading}
                title="Refresh schema (after adding tags, fields, or stages in Settings)"
                className="px-3 py-2 rounded-xl border border-gray-700 text-gray-500 text-sm hover:text-white hover:border-gray-600 transition-colors disabled:opacity-50"
              >
                {store.schemaLoading ? '⟳' : '↻'} Schema
              </button>
            </div>
          </div>
        </div>

        {/* Canvas area */}
        <div className="flex-1 bg-gray-950 overflow-auto" onClick={handleCanvasClick}>
          <div className="min-h-full flex flex-col items-center py-12 px-8" onClick={handleCanvasClick}>
            {/* Trigger */}
            <TriggerNode trigger={store.trigger} />

            {/* Connection line */}
            <div className="flex flex-col items-center">
              <div className="w-px h-6 bg-gray-700" />
            </div>

            {/* Conditions */}
            <ConditionNode conditions={store.conditions} />

            {/* Recursive Steps tree */}
            <div className="w-full flex flex-col items-center">
              <WorkflowStepList steps={store.steps || []} parentId={null} branch={null} />
            </div>

            {(store.steps || []).length === 0 && (
              <div className="text-center py-8 text-gray-600">
                <p className="text-sm">Drag actions/conditions from the sidebar or click + to add steps</p>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Drag overlay — floating preview while dragging */}
      <DragOverlay>
        {activeDragType ? (
          <div className="flex items-center gap-3 p-3 rounded-xl border border-indigo-500 bg-gray-800 shadow-lg shadow-indigo-500/20 opacity-90">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-emerald-400 to-teal-500 flex items-center justify-center text-sm">
              {activeDragType === 'condition' ? '🔀' : ACTION_ICONS[activeDragType as ActionType] || '⚙️'}
            </div>
            <span className="text-sm text-white font-medium">
              {activeDragType === 'condition' ? 'Condition Split' : ACTION_LABELS[activeDragType as ActionType]}
            </span>
          </div>
        ) : activeDragId ? (
          (() => {
            const step = store.findStep(activeDragId);
            if (!step) return null;
            const icon = step.type === 'condition' ? '🔀'
              : step.type === 'delay' ? '⏱️'
              : ACTION_ICONS[step.action?.type as ActionType] || '⚙️';
            const label = step.type === 'condition' ? 'Condition Split'
              : step.type === 'delay' ? 'Delay'
              : ACTION_LABELS[step.action?.type as ActionType] || 'Step';
            return (
              <div className="flex items-center gap-3 p-3 rounded-xl border border-purple-500 bg-gray-800 shadow-lg shadow-purple-500/20 opacity-90">
                <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-purple-400 to-fuchsia-500 flex items-center justify-center text-sm">
                  {icon}
                </div>
                <span className="text-sm text-white font-medium">{label}</span>
              </div>
            );
          })()
        ) : null}
      </DragOverlay>
    </WorkflowDragContext.Provider>
    </DndContext>
  );
};

function getDefaultParams(type: string): Record<string, unknown> {
  switch (type) {
    case 'send_email':
      return { to: '', subject: '', body_html: '' };
    case 'create_task':
      return { title: '', priority: 'medium', due_in_days: 3 };
    case 'assign_user':
      return { entity: 'contact', strategy: 'round_robin' };
    case 'send_webhook':
      return { url: '', method: 'POST', timeout_sec: 10 };
    case 'delay':
      return { duration_sec: 60 };
    default:
      return {};
  }
}

export default WorkflowBuilder;
