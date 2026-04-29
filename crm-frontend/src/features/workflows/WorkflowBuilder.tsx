import React, { useEffect, useCallback, useState } from 'react';
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
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { useParams, useNavigate } from 'react-router-dom';
import { useBuilderStore, generateActionId } from './store';
import { TriggerNode } from './nodes/TriggerNode';
import { ConditionNode } from './nodes/ConditionNode';
import { ActionNode } from './nodes/ActionNode';
import { AddNodeButton } from './nodes/AddNodeButton';
import { TriggerConfigPanel } from './panels/TriggerConfigPanel';
import { ConditionConfigPanel } from './panels/ConditionConfigPanel';
import { ActionConfigPanel } from './panels/ActionConfigPanel';
import { ActionPalette } from './panels/ActionPalette';
import type { ActionType } from './types';
import { ACTION_LABELS, ACTION_ICONS } from './types';

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
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

  const [activeDragType, setActiveDragType] = useState<ActionType | null>(null);

  const handleDragStart = useCallback((event: DragStartEvent) => {
    const data = event.active.data.current;
    if (data?.source === 'palette') {
      setActiveDragType(data.actionType as ActionType);
    }
  }, []);

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over) return;

      const activeData = active.data.current;
      const overData = over.data.current;

      // Palette → drop zone
      if (activeData?.source === 'palette' && over.id.toString().startsWith('dropzone-')) {
        const targetIndex = overData?.targetIndex ?? store.actions.length;
        const actionType = activeData.actionType as ActionType;
        store.insertAction(
          {
            type: actionType,
            id: generateActionId(),
            params: getDefaultParams(actionType),
          },
          targetIndex
        );
        return;
      }

      // Reorder within canvas
      if (activeData?.source === 'canvas') {
        const fromIdx = activeData.index;
        const overAction = store.actions.findIndex((a) => a.id === over.id);
        if (overAction !== -1 && fromIdx !== overAction) {
          store.reorderActions(fromIdx, overAction);
        }
      }
    },
    [store]
  );

  const handleDragEnd2 = useCallback(
    (event: DragEndEvent) => {
      setActiveDragType(null);
      handleDragEnd(event);
    },
    [handleDragEnd]
  );

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
    if (store.selectedNodeId && store.actions.find((a) => a.id === store.selectedNodeId)) {
      return <ActionConfigPanel />;
    }
    return <ActionPalette />;
  };

  return (
    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={handleDragStart} onDragEnd={handleDragEnd2}>
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
                store.errors.name ? 'border-red-500' : 'border-transparent focus:border-indigo-500'
              }`}
            />
            {store.errors.name && <p className="text-xs text-red-400 mt-1">{store.errors.name[0]}</p>}
            <textarea
              value={store.description}
              onChange={(e) => store.setDescription(e.target.value)}
              placeholder="Description (optional)..."
              rows={2}
              className="w-full bg-transparent text-sm text-gray-400 placeholder-gray-700 focus:outline-none resize-none mt-2"
            />
          </div>
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
            <button
              onClick={() => navigate('/workflows')}
              className="w-full py-2 rounded-xl border border-gray-700 text-gray-400 text-sm hover:text-white hover:border-gray-600 transition-colors"
            >
              ← Back to Workflows
            </button>
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

            {/* Actions with sortable */}
            <SortableContext
              items={store.actions.map((a) => a.id)}
              strategy={verticalListSortingStrategy}
            >
              <AddNodeButton index={0} />
              {store.actions.map((action, idx) => (
                <React.Fragment key={action.id}>
                  <ActionNode action={action} index={idx} />
                  <AddNodeButton index={idx + 1} />
                </React.Fragment>
              ))}
            </SortableContext>

            {store.actions.length === 0 && (
              <div className="text-center py-8 text-gray-600">
                <p className="text-sm">Drag actions from the sidebar or click + to add steps</p>
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
              {ACTION_ICONS[activeDragType]}
            </div>
            <span className="text-sm text-white font-medium">{ACTION_LABELS[activeDragType]}</span>
          </div>
        ) : null}
      </DragOverlay>
    </DndContext>
  );
};

function getDefaultParams(type: ActionType): Record<string, unknown> {
  switch (type) {
    case 'send_email':
      return { to: '{{contact.email}}', subject: '', body_html: '' };
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
