// TEMPORARY visual-verification harness for the A3 builder canvas + config panel.
// Renders the WorkflowCanvas and the ConfigPanel with mock data (no backend/auth)
// so the layout, nodes, edges, insert menu, and token-styled config forms can be
// verified in the preview. Delete after verification.

import { useState, useEffect } from 'react';
import { ReactFlowProvider } from '@xyflow/react';
import { BuilderContext } from '../BuilderContext';
import type { InsertContext } from '../graph';
import { WorkflowCanvas } from '../WorkflowCanvas';
import { InsertMenu } from '../InsertMenu';
import { BuilderSidePanel } from '../config/BuilderSidePanel';
import type { TriggerSpec, WorkflowStep } from '../../types';
import type { WorkflowSchema } from '../../api';
import { useBuilderStore } from '../../store';

const trigger: TriggerSpec = { type: 'deal_stage_changed', params: { to_stage: 'stage_won' } };

// Mock schema so the config forms (field pickers, stage/user dropdowns, variable
// picker) render against realistic data without a backend.
const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [
        { path: 'contact.first_name', label: 'First Name', type: 'string' },
        { path: 'contact.email', label: 'Email', type: 'string' },
        { path: 'contact.lifecycle', label: 'Lifecycle', type: 'select', options: ['lead', 'customer'] },
        { path: 'contact.tags', label: 'Tags', type: 'array', picker_type: 'tag' },
      ],
    },
    {
      key: 'deal',
      label: 'Deal',
      icon: '💰',
      fields: [
        { path: 'deal.value', label: 'Value', type: 'number' },
        { path: 'deal.stage_id', label: 'Stage', type: 'string', picker_type: 'stage' },
        { path: 'deal.owner_user_id', label: 'Owner', type: 'string', picker_type: 'user' },
        { path: 'deal.expected_close_at', label: 'Expected Close', type: 'date' },
        { path: 'deal.closed_at', label: 'Closed At', type: 'date' },
      ],
    },
  ],
  custom_objects: [],
  stages: [
    { id: 'stage_new', name: 'New', order: 1, color: '#6366f1' },
    { id: 'stage_won', name: 'Won', order: 2, color: '#10b981' },
  ],
  tags: [{ id: 't1', name: 'vip', color: '#f59e0b' }],
  users: [{ id: 'u1', name: 'Ada Lovelace', email: 'ada@acme.com' }],
};

const sampleSteps: WorkflowStep[] = [
  { id: 's1', type: 'action', action: { id: 's1', type: 'send_email', params: { subject: 'Welcome aboard', to: '{{contact.email}}' } } },
  { id: 'd1', type: 'delay', delay: { duration_sec: 2 * 86400 } },
  {
    id: 'c1',
    type: 'condition',
    condition: { op: 'AND', rules: [{ field: 'deal.value', operator: 'gte', value: 1000 }] },
    yes_steps: [
      { id: 'y1', type: 'action', action: { id: 'y1', type: 'assign_user', params: { strategy: 'round_robin', pool: ['u1'] } } },
    ],
    no_steps: [
      { id: 'n1', type: 'action', action: { id: 'n1', type: 'create_task', params: { title: 'Follow up' } } },
    ],
  },
  { id: 's2', type: 'action', action: { id: 's2', type: 'log_activity', params: { activity_type: 'note' } } },
];

export function BuilderDemo() {
  const [steps, setSteps] = useState(sampleSteps);
  const selectedId = useBuilderStore((s) => s.selectedNodeId);
  const [insertState, setInsertState] = useState<{ slot: InsertContext; anchor: { x: number; y: number } } | null>(null);

  // Seed the store so InsertMenu/ConfigPanel work against a shared tree, and
  // reflect store tree edits back into local state for the canvas. Build the tree
  // via addStep (not a raw steps setState) so the flattened `actions` view stays
  // populated — ActionConfig resolves the selected action from `actions`.
  useEffect(() => {
    useBuilderStore.getState().reset();
    useBuilderStore.setState({ trigger, schema: MOCK_SCHEMA, schemaLoading: false, schemaError: null });
    for (const step of sampleSteps) useBuilderStore.getState().addStep(step, null, null);
    useBuilderStore.getState().selectNode('s1');
    const unsub = useBuilderStore.subscribe((s) => setSteps(s.steps));
    return unsub;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const select = (id: string) => useBuilderStore.getState().selectNode(id);

  return (
    <div className="flex h-screen flex-col bg-background">
      <div className="border-b border-border p-3 text-sm font-semibold text-foreground">Builder canvas + config demo</div>
      <div className="flex flex-1 overflow-hidden">
        <div className="relative flex-1">
          <ReactFlowProvider>
            <BuilderContext.Provider
              value={{
                onSelect: select,
                onInsert: (slot, anchor) => setInsertState({ slot, anchor: anchor ?? { x: 400, y: 300 } }),
                selectedId,
                readOnly: false,
              }}
            >
              <WorkflowCanvas
                trigger={trigger}
                steps={steps}
                selectedId={selectedId}
                onSelect={select}
                onReorder={(p, b, f, t) => useBuilderStore.getState().reorderSteps(p, b, f, t)}
              />
            </BuilderContext.Provider>
          </ReactFlowProvider>
        </div>
        <aside className="w-[380px] shrink-0 overflow-hidden border-l border-border bg-card">
          {/* A7.4: mirror NextBuilder's ?ai= handoff so the copilot auto-draft chain is verifiable here. */}
          <BuilderSidePanel aiPrompt={new URLSearchParams(window.location.search).get('ai')} />
        </aside>
      </div>
      {insertState && (
        <InsertMenu slot={insertState.slot} anchor={insertState.anchor} onClose={() => setInsertState(null)} />
      )}
    </div>
  );
}
