import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { useBuilderStore } from '../../../store';
import { ConfigPanel } from '../ConfigPanel';
import type { WorkflowSchema } from '../../../api';
import type { WorkflowStep } from '../../../types';

// TaskTriggerPanel.test.tsx covers the task_status_changed trigger end to end
// through the real panels: picking it from the object dropdown, its to_status/
// from_status pickers, and — the part that would silently break if
// deriveFiresOn/deriveObjectSlug (ConditionConfig.tsx) didn't learn the new
// trigger type — that a condition step under it offers task.* fields, not an
// empty picker.

vi.mock('../../../api', async () => {
  const actual = await vi.importActual<typeof import('../../../api')>('../../../api');
  return {
    ...actual,
    createWorkflow: vi.fn(),
    updateWorkflow: vi.fn(),
    getWorkflow: vi.fn(),
    getWorkflowSchema: vi.fn(),
    getObjectFields: vi.fn().mockResolvedValue([]),
    getWebhookToken: vi.fn(),
  };
});

const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    { key: 'contact', label: 'Contact', icon: '👤', fields: [{ path: 'contact.email', label: 'Email', type: 'string' }] },
    {
      key: 'task',
      label: 'Task',
      icon: '✅',
      fields: [
        { path: 'task.title', label: 'Title', type: 'string' },
        { path: 'task.status', label: 'Status', type: 'select', options: ['open', 'in_progress', 'completed'] },
        { path: 'task.priority', label: 'Priority', type: 'select', options: ['low', 'medium', 'high'] },
        { path: 'task.due_at', label: 'Due Date', type: 'date' },
      ],
    },
  ],
  custom_objects: [],
  stages: [],
  tags: [],
  users: [],
};

beforeEach(() => {
  useBuilderStore.getState().reset();
  useBuilderStore.setState({ schema: MOCK_SCHEMA, schemaLoading: false, schemaError: null });
});

describe('task_status_changed trigger panel', () => {
  it('offers "Task status changed" in the object dropdown', () => {
    useBuilderStore.setState({ selectedNodeId: 'trigger', trigger: null });
    render(<ConfigPanel />);
    expect(screen.getByRole('option', { name: /Task status changed/i })).toBeInTheDocument();
  });

  it('selecting it renders the To Status picker and requires a value', () => {
    useBuilderStore.setState({ selectedNodeId: 'trigger', trigger: { type: 'task_status_changed', params: { to_status: '' } } });
    render(<ConfigPanel />);

    expect(screen.getByText('To Status')).toBeInTheDocument();
    expect(screen.getByText('From Status')).toBeInTheDocument();
    // No generic "Fires on" selector — task_status_changed carries its own semantics.
    expect(screen.queryByText('Fires on')).not.toBeInTheDocument();
  });

  it('setting To Status updates the trigger params', () => {
    useBuilderStore.setState({ selectedNodeId: 'trigger', trigger: { type: 'task_status_changed', params: { to_status: '' } } });
    render(<ConfigPanel />);

    const toStatusSelect = screen.getByText('To Status').parentElement!.querySelector('select')!;
    fireEvent.change(toStatusSelect, { target: { value: 'completed' } });

    expect(useBuilderStore.getState().trigger?.params?.to_status).toBe('completed');
  });

  it('the preview sentence names the status transition', () => {
    useBuilderStore.setState({
      selectedNodeId: 'trigger',
      trigger: { type: 'task_status_changed', params: { to_status: 'completed', from_status: 'in_progress' } },
    });
    render(<ConfigPanel />);
    expect(screen.getByText(/In Progress.*Completed/)).toBeInTheDocument();
  });

  it('a condition step under the trigger offers task fields, not an empty picker', () => {
    // This is the regression this file exists to catch: deriveObjectSlug had no
    // case for task_status_changed and would have returned '', scoping the
    // FieldPicker to nothing.
    const condStep: WorkflowStep = {
      id: 'c1',
      type: 'condition',
      condition: { op: 'AND', rules: [{ field: '', operator: 'eq', value: '' }] },
      yes_steps: [],
      no_steps: [],
    };
    useBuilderStore.setState({
      trigger: { type: 'task_status_changed', params: { to_status: 'completed' } },
      steps: [condStep],
      actions: [],
      selectedNodeId: 'c1',
    });
    render(<ConfigPanel />);

    // FieldPicker is a custom dropdown, not a native input — its placeholder
    // renders as button text, not a `placeholder` attribute, and opens onto a
    // category chooser rather than a flat field list. Search across categories
    // to reach the leaf fields directly.
    fireEvent.click(screen.getByText('Select field…'));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search fields' }), { target: { value: 'stat' } });
    expect(screen.getByText('Status')).toBeInTheDocument();
  });
});
