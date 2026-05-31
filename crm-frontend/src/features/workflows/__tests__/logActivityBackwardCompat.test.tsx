import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { DndContext } from '@dnd-kit/core';
import { useBuilderStore } from '../store';
import { WorkflowStepList } from '../nodes/WorkflowStepList';
import type { ActionSpec, WorkflowStep } from '../types';

/**
 * Backward-compatibility rendering test — Requirements 10.2, 10.3.
 *
 * Approach
 * --------
 * The builder canvas renders its saved step tree through
 * `WorkflowStepList → StepRenderer → ActionNode`. `ActionNode` derives its
 * label/icon with a fallback (`ACTION_LABELS[type] || 'Update Record'`,
 * `ACTION_ICONS[type] || '⚙️'`), so any action type it does not recognize —
 * including the deprecated `update_contact` and a wholly unknown type — renders
 * a generic fallback node instead of crashing.
 *
 * Rendering the full `WorkflowBuilder` is impractical here: it requires a
 * react-router context (`useParams`/`useNavigate`) plus the workflow-schema API.
 * The builder uses `@dnd-kit` (not ReactFlow), and `ActionNode` relies on
 * `useSortable`, so the smallest faithful unit that exercises the real
 * node-rendering path for an action list is `WorkflowStepList` wrapped in a
 * `DndContext` (which also provides the droppable context the inline
 * `AddNodeButton`s use). We seed the builder store with the loaded workflow's
 * steps, mirroring how `WorkflowBuilder` reads `store.steps`.
 */

/** Build an action step as the store holds it after a workflow is loaded. */
function actionStep(id: string, type: string, params: Record<string, unknown>): WorkflowStep {
  // `update_contact` and unknown types are intentionally outside the ActionSpec
  // union — cast to mirror how the store tolerates deprecated/unknown types.
  return { id, type: 'action', action: { id, type: type as ActionSpec['type'], params } };
}

/** Render the step list the way the builder canvas does, seeded from the store. */
function renderCanvas(steps: WorkflowStep[]) {
  const actions = steps.map((s) => s.action).filter((a): a is ActionSpec => !!a);
  useBuilderStore.setState({ steps, actions });
  return render(
    <DndContext>
      <WorkflowStepList steps={steps} parentId={null} branch={null} />
    </DndContext>,
  );
}

beforeEach(() => {
  useBuilderStore.getState().reset();
});

describe('log_activity backward compatibility — builder node rendering', () => {
  it('renders one node per action — known, deprecated, and unknown — without throwing', () => {
    // A saved workflow mixing a known action, the deprecated update_contact
    // action, and an unrecognized action type.
    const steps: WorkflowStep[] = [
      actionStep('a1', 'send_email', { subject: 'Welcome', to: '{{contact.email}}' }),
      actionStep('a2', 'update_contact', {
        updates: [{ field: 'status', op: 'set', value: 'active' }],
      }),
      actionStep('a3', 'totally_unknown_action', { foo: 'bar' }),
    ];

    // Requirement 10.2/10.3: rendering must not throw an unhandled error.
    expect(() => renderCanvas(steps)).not.toThrow();

    // Requirement 10.2: exactly one node per action (each ActionNode renders a
    // "Step N" header), with no action node omitted.
    expect(screen.getAllByText(/^Step \d+$/)).toHaveLength(3);

    // The known action renders its real label.
    expect(screen.getByText('Send Email')).toBeInTheDocument();

    // Requirement 10.3: the deprecated and unknown types each render the generic
    // fallback node ("Update Record") rather than crashing.
    expect(screen.getAllByText('Update Record')).toHaveLength(2);
  });

  it('renders a fallback node (generic label + icon) for an unrecognized action type', () => {
    // Requirement 10.3: an unknown action type renders a fallback node.
    expect(() =>
      renderCanvas([actionStep('u1', 'totally_unknown_action', { foo: 'bar' })]),
    ).not.toThrow();

    // Fallback label and icon come from ActionNode's `|| 'Update Record'` / `|| '⚙️'`.
    expect(screen.getByText('Update Record')).toBeInTheDocument();
    expect(screen.getByText('⚙️')).toBeInTheDocument();
    expect(screen.getByText('Step 1')).toBeInTheDocument();
  });

  it('renders the deprecated update_contact action without throwing', () => {
    // Requirement 10.2: the deprecated update_contact type still renders a node.
    expect(() =>
      renderCanvas([actionStep('d1', 'update_contact', { updates: [] })]),
    ).not.toThrow();

    expect(screen.getByText('Update Record')).toBeInTheDocument();
    expect(screen.getByText('Step 1')).toBeInTheDocument();
  });
});
