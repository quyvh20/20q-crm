import React, { useState, useMemo } from 'react';
import { useDroppable } from '@dnd-kit/core';
import { useBuilderStore, generateActionId, isDescendant, type StepPath } from '../store';
import { useWorkflowDrag } from '../DragContext';

const STEP_TYPES = [
  { type: 'send_email', label: 'Send Email', icon: '✉️' },
  { type: 'create_task', label: 'Create Task', icon: '✅' },
  { type: 'assign_user', label: 'Assign User', icon: '👤' },
  { type: 'send_webhook', label: 'Send Webhook', icon: '🔗' },
  { type: 'delay', label: 'Delay', icon: '⏱️' },
  { type: 'update_record', label: 'Update Record', icon: '📝' },
  { type: 'log_activity', label: 'Log Activity', icon: '📞' },
  { type: 'condition', label: 'Condition Split', icon: '🔀' },
];

interface AddNodeButtonProps {
  parentId: string | null;
  branch: 'yes' | 'no' | null;
  index: number;
}

export function getDefaultParams(type: string): Record<string, unknown> {
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
    case 'log_activity':
      return { activity_type: 'note', title: '', body: '' };
    default:
      return {};
  }
}

export const AddNodeButton: React.FC<AddNodeButtonProps> = ({ parentId, branch, index }) => {
  const [showMenu, setShowMenu] = useState(false);
  const addStep = useBuilderStore((s) => s.addStep);
  const { activeDragPath, activeDragStepId } = useWorkflowDrag();

  const { isOver, setNodeRef } = useDroppable({
    id: `dropzone-${parentId ?? 'root'}-${branch ?? 'main'}-${index}`,
    data: { parentId, branch, targetIndex: index },
  });

  /**
   * Determine if this drop zone would create a cycle.
   * A drop is invalid when the dragged step's path is an ancestor of this
   * drop zone's path — i.e., the user is trying to drop a step into its
   * own subtree.
   *
   * Also invalid: dropping a step on itself (same parentId + same index in
   * same branch).
   */
  const isInvalidDrop = useMemo(() => {
    if (!activeDragPath || !activeDragStepId) return false;
    // If this drop zone is inside a condition branch, check ancestry
    if (parentId) {
      // Build the drop zone's path: the parent condition is at activeDragPath's
      // ancestor. We need to check if dragging srcPath into a zone under parentId
      // would create a cycle. If parentId === activeDragStepId, that's a self-drop
      // into the same step's branch — which is actually valid for conditions.
      // The actual cycle is: srcPath is a strict prefix of destPath.

      // Quick check: if the parent of this drop zone IS the dragged step,
      // and the dragged step is a condition, that's fine (dropping into own branch
      // is moving within, not a cycle). But if the parent is a DESCENDANT
      // of the dragged step, that IS a cycle.
      if (parentId === activeDragStepId) {
        // Dropping into own condition branches — this is OK for conditions
        return false;
      }
      // Check if this drop zone's parent is inside the dragged step's subtree
      // We approximate: get the store's steps and find the parentId's path
      const steps = useBuilderStore.getState().steps || [];
      const parentPath = findPathById(steps, parentId);
      if (parentPath && isDescendant(activeDragPath, parentPath)) {
        return true; // cycle!
      }
    }
    return false;
  }, [activeDragPath, activeDragStepId, parentId]);

  const handleAddStep = (type: string) => {
    const id = generateActionId();
    if (type === 'condition') {
      addStep(
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
        index
      );
    } else {
      addStep(
        {
          id,
          type: type === 'delay' ? 'delay' : 'action',
          action: type === 'delay' ? undefined : {
            id,
            type: type as any,
            params: getDefaultParams(type),
          },
          delay: type === 'delay' ? { duration_sec: 60 } : undefined,
        },
        parentId,
        branch,
        index
      );
    }
    setShowMenu(false);
  };

  // Visual states
  const isDragging = activeDragPath !== null || activeDragStepId !== null;
  const isValidHover = isOver && !isInvalidDrop;
  const isInvalidHover = isOver && isInvalidDrop;

  return (
    <div className="flex flex-col items-center py-1 relative">
      <div className="w-px h-6 bg-gray-700" />
      <div
        ref={setNodeRef}
        onClick={() => setShowMenu(!showMenu)}
        className={`
          flex items-center justify-center w-8 h-8 rounded-full border-2 border-dashed
          transition-all duration-200 cursor-pointer z-10
          ${isInvalidHover
            ? 'border-red-400 bg-red-400/20 scale-110 cursor-not-allowed'
            : isValidHover
            ? 'border-emerald-400 bg-emerald-400/20 scale-125'
            : isDragging
            ? 'border-gray-500 bg-gray-800/50'
            : 'border-gray-600 hover:border-indigo-400 hover:bg-indigo-400/10'}
        `}
      >
        <span className={`text-sm transition-colors ${
          isInvalidHover ? 'text-red-400'
          : isValidHover ? 'text-emerald-400'
          : 'text-gray-500 hover:text-indigo-400'
        }`}>
          {isInvalidHover ? '✕' : '+'}
        </span>
      </div>

      {/* Quick-add dropdown menu */}
      {showMenu && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setShowMenu(false)} />
          <div className="absolute top-16 z-50 w-48 py-1 bg-gray-800 border border-gray-700 rounded-xl shadow-xl shadow-black/40 animate-in fade-in slide-in-from-top-2 duration-150">
            {STEP_TYPES.map((item) => (
              <button
                key={item.type}
                onClick={() => handleAddStep(item.type)}
                className="w-full flex items-center gap-2.5 px-3 py-2 text-sm text-gray-300 hover:bg-gray-700 hover:text-white transition-colors"
              >
                <span className="w-6 h-6 rounded-md bg-gradient-to-br from-emerald-400 to-teal-500 flex items-center justify-center text-xs">
                  {item.icon}
                </span>
                {item.label}
              </button>
            ))}
          </div>
        </>
      )}

      <div className="w-px h-6 bg-gray-700" />
    </div>
  );
};

/**
 * Find the path of a step by its ID in the tree.
 * Simple tree walk — used only during drag validation.
 */
function findPathById(
  steps: { id: string; yes_steps?: any[]; no_steps?: any[] }[],
  targetId: string,
  basePath: StepPath = [],
): StepPath | null {
  for (let i = 0; i < steps.length; i++) {
    const branchSeg = basePath.length > 0 ? basePath[basePath.length - 1]?.branch : undefined;
    const seg: StepPath[number] = branchSeg
      ? { index: i, branch: branchSeg }
      : { index: i };
    const myPath: StepPath = [...basePath.slice(0, -1), seg];

    if (steps[i].id === targetId) return myPath;
    if (steps[i].yes_steps) {
      const found = findPathById(steps[i].yes_steps!, targetId, [...myPath, { index: 0, branch: 'yes' }]);
      if (found) return found;
    }
    if (steps[i].no_steps) {
      const found = findPathById(steps[i].no_steps!, targetId, [...myPath, { index: 0, branch: 'no' }]);
      if (found) return found;
    }
  }
  return null;
}
