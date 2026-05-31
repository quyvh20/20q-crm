import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DndContext } from '@dnd-kit/core';
import { ActionPalette } from '../panels/ActionPalette';
import { AddNodeButton } from '../nodes/AddNodeButton';
import { getDefaultParams as addNodeDefaults } from '../nodes/AddNodeButton';
import { getDefaultParams as builderDefaults } from '../WorkflowBuilder';
import { useBuilderStore } from '../store';

// Validates: Requirements 7.1, 7.2, 7.3, 7.4
//
// These tests cover the "Log Activity" palette entries and the default
// parameters produced when a Log Activity action is added to a workflow.
// - 7.1: "Log Activity" is a draggable item in ActionPalette.
// - 7.2: "Log Activity" is a selectable item in AddNodeButton.
// - 7.3: Adding the action creates a `log_activity` node initialized with
//        { activity_type: 'note', title: '', body: '' }.
// - 7.4: getDefaultParams('log_activity') is identical across the two
//        sources (AddNodeButton.tsx and WorkflowBuilder.tsx).

const EXPECTED_DEFAULTS = { activity_type: 'note', title: '', body: '' };

beforeEach(() => {
  useBuilderStore.getState().reset();
});

// dnd-kit's useDraggable/useDroppable require a surrounding DndContext.
function renderInDnd(ui: React.ReactElement) {
  return render(<DndContext>{ui}</DndContext>);
}

// ═══════════════════════════════════════════════════════════════════════
// Requirement 7.1 — ActionPalette lists "Log Activity"
// ═══════════════════════════════════════════════════════════════════════
describe('ActionPalette — Log Activity entry', () => {
  it('lists "Log Activity" as a draggable action item', () => {
    // Validates: Requirements 7.1
    renderInDnd(<ActionPalette />);

    expect(screen.getByText('Log Activity')).toBeInTheDocument();
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Requirement 7.2 / 7.3 — AddNodeButton lists "Log Activity" and adding it
// creates a log_activity node with the expected default params
// ═══════════════════════════════════════════════════════════════════════
describe('AddNodeButton — Log Activity entry and defaults', () => {
  it('lists "Log Activity" as a selectable action item', async () => {
    // Validates: Requirements 7.2
    renderInDnd(<AddNodeButton parentId={null} branch={null} index={0} />);

    // Open the quick-add menu by clicking the "+" affordance.
    await userEvent.click(screen.getByText('+'));

    expect(screen.getByText('Log Activity')).toBeInTheDocument();
  });

  it('adds a log_activity node initialized with the default params', async () => {
    // Validates: Requirements 7.2, 7.3
    renderInDnd(<AddNodeButton parentId={null} branch={null} index={0} />);

    await userEvent.click(screen.getByText('+'));
    await userEvent.click(screen.getByText('Log Activity'));

    const { steps, actions } = useBuilderStore.getState();

    // Exactly one step/action was created.
    expect(steps).toHaveLength(1);
    expect(steps[0].type).toBe('action');
    expect(steps[0].action?.type).toBe('log_activity');

    const created = actions.find((a) => a.type === 'log_activity');
    expect(created).toBeDefined();
    expect(created!.params).toEqual(EXPECTED_DEFAULTS);
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Requirement 7.3 / 7.4 — default params equivalence across both sources
// ═══════════════════════════════════════════════════════════════════════
describe('getDefaultParams — log_activity defaults', () => {
  it('AddNodeButton.getDefaultParams returns the expected defaults', () => {
    // Validates: Requirements 7.3
    expect(addNodeDefaults('log_activity')).toEqual(EXPECTED_DEFAULTS);
  });

  it('WorkflowBuilder.getDefaultParams returns the expected defaults', () => {
    // Validates: Requirements 7.3
    expect(builderDefaults('log_activity')).toEqual(EXPECTED_DEFAULTS);
  });

  it('both sources produce identical defaults for log_activity', () => {
    // Validates: Requirements 7.4
    expect(addNodeDefaults('log_activity')).toEqual(builderDefaults('log_activity'));
  });
});
