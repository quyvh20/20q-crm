import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { useBuilderStore } from '../../../store';
import { ConfigPanel } from '../ConfigPanel';
import type { DryRunState } from '../../BuilderContext';
import type { WorkflowSchema } from '../../../api';
import type { TriggerSpec, WorkflowStep } from '../../../types';

// The new builder's ConfigPanel routes the current selection to the token-styled
// trigger / condition / action forms and owns the delete affordance. These tests
// exercise that routing + delete against the real store tree ops.

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

// EmailParams (the send_email form, A5) calls useEmailTemplates; stub it so the
// panel renders without a QueryClientProvider — the template library isn't under
// test here, only ConfigPanel's routing/delete/dry-run.
vi.mock('../../../queries', async () => {
  const actual = await vi.importActual<typeof import('../../../queries')>('../../../queries');
  return { ...actual, useEmailTemplates: () => ({ data: { templates: [], total: 0 }, isLoading: false }) };
});

// EmailParams also calls useContentList (M8 marketing-content picker); stub it so the
// panel renders without a QueryClientProvider — marketing content isn't under test here.
vi.mock('../../../../marketing/contentQueries', () => ({
  useContentList: () => ({ data: [], isLoading: false }),
}));

const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [
        { path: 'contact.first_name', label: 'First Name', type: 'string' },
        { path: 'contact.email', label: 'Email', type: 'string' },
      ],
    },
  ],
  custom_objects: [],
  stages: [],
  tags: [],
  users: [],
};

const CONTACT_TRIGGER: TriggerSpec = { type: 'contact_created', params: {} };

function emailStep(id = 'a_email'): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'send_email', params: { to: '', subject: '', body_html: '' } } };
}
function delayStep(id = 'a_delay'): WorkflowStep {
  return { id, type: 'delay', delay: { duration_sec: 60 } };
}
function conditionStep(id = 'a_cond'): WorkflowStep {
  // One (empty) rule so the FieldPicker row renders — mirrors clicking "+ Add
  // Condition" once, the "pick a field, no typing" flow.
  return {
    id,
    type: 'condition',
    condition: { op: 'AND', rules: [{ field: '', operator: 'eq', value: '' }] },
    yes_steps: [],
    no_steps: [],
  };
}

/** Seed a fresh store with a trigger + schema, then add the step through the real
 *  addStep mutation (so the flattened `actions` view stays in sync) and select it. */
function seedWithStep(step: WorkflowStep) {
  const store = useBuilderStore.getState();
  store.reset();
  useBuilderStore.setState({ schema: MOCK_SCHEMA, trigger: CONTACT_TRIGGER });
  useBuilderStore.getState().addStep(step, null, null, 0);
  useBuilderStore.getState().selectNode(step.id);
}

beforeEach(() => {
  useBuilderStore.getState().reset();
  useBuilderStore.setState({ schema: MOCK_SCHEMA, schemaLoading: false, schemaError: null });
});

describe('ConfigPanel routing', () => {
  it('shows the empty state when nothing is selected', () => {
    useBuilderStore.setState({ selectedNodeId: null });
    render(<ConfigPanel />);
    expect(screen.getByText('Nothing selected')).toBeInTheDocument();
  });

  it('shows an end-of-branch hint for an end selection', () => {
    useBuilderStore.setState({ selectedNodeId: 'end' });
    render(<ConfigPanel />);
    expect(screen.getByText('End of branch')).toBeInTheDocument();
  });

  it('routes a trigger selection to the trigger form', () => {
    useBuilderStore.setState({ trigger: CONTACT_TRIGGER, selectedNodeId: 'trigger' });
    render(<ConfigPanel />);
    // Header eyebrow + the trigger form's Source heading.
    expect(screen.getByText('Trigger')).toBeInTheDocument();
    expect(screen.getByText('Source')).toBeInTheDocument();
  });

  it('routes a send_email step to the action form', () => {
    seedWithStep(emailStep());
    render(<ConfigPanel />);
    expect(screen.getByText('Send an email')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Click {x} to insert contact email')).toBeInTheDocument();
  });

  it('routes a delay step to the delay form', () => {
    seedWithStep(delayStep());
    render(<ConfigPanel />);
    expect(screen.getByText('Wait Duration')).toBeInTheDocument();
  });

  it('routes a condition step to the condition form', () => {
    seedWithStep(conditionStep());
    render(<ConfigPanel />);
    // With a source object set, the condition form renders its Conditions UI,
    // including the FieldPicker trigger (label shown as button text, not typed).
    expect(screen.getByText('Conditions')).toBeInTheDocument();
    expect(screen.getByText('Select field…')).toBeInTheDocument();
  });
});

// A deal-triggered run carries contact.* and company.*: engine.go's
// buildEvalContext loads the deal's contact (then its company) before any
// condition is evaluated. The panel used to scope its picker to the trigger's own
// object alone, so those fields were unreachable from the UI while the engine
// resolved them perfectly well. Scoped to the listbox because the panel prints the
// source object's label in its own chrome too.
const DEAL_AND_CONTACT_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [{ path: 'contact.email', label: 'Email', type: 'string' }],
    },
    {
      key: 'deal',
      label: 'Deal',
      icon: '💼',
      fields: [{ path: 'deal.amount', label: 'Amount', type: 'number' }],
    },
  ],
  custom_objects: [],
  stages: [],
  tags: [],
  users: [],
};

function seedConditionWithTrigger(trigger: TriggerSpec) {
  useBuilderStore.getState().reset();
  useBuilderStore.setState({
    schema: DEAL_AND_CONTACT_SCHEMA,
    schemaLoading: false,
    schemaError: null,
    trigger,
  });
  const step = conditionStep();
  useBuilderStore.getState().addStep(step, null, null, 0);
  useBuilderStore.getState().selectNode(step.id);
  return step;
}

describe('ConditionConfig field scope', () => {
  it('offers the hydrated contact alongside the deal for a deal trigger', () => {
    seedConditionWithTrigger({ type: 'deal_stage_changed', params: {} });
    render(<ConfigPanel />);
    fireEvent.click(screen.getByText('Select field…'));
    const list = within(screen.getByRole('listbox'));
    expect(list.getByText('Deal')).toBeInTheDocument();
    expect(list.getByText('Contact')).toBeInTheDocument();
  });

  it('does NOT offer the deal for a contact trigger — the union is the eval context, not everything', () => {
    seedConditionWithTrigger({ type: 'contact_created', params: {} });
    render(<ConfigPanel />);
    fireEvent.click(screen.getByText('Select field…'));
    const list = within(screen.getByRole('listbox'));
    expect(list.getByText('Contact')).toBeInTheDocument();
    // A contact run hydrates company, never a deal — engine.go has no
    // contact -> deal hop, so offering it would ship a silently-false rule.
    expect(list.queryByText('Deal')).not.toBeInTheDocument();
  });
});

describe('ConfigPanel delete', () => {
  it('removes the selected step and clears the selection', () => {
    seedWithStep(emailStep('to_delete'));
    expect(useBuilderStore.getState().steps).toHaveLength(1);

    render(<ConfigPanel />);
    fireEvent.click(screen.getByRole('button', { name: 'Delete step' }));

    expect(useBuilderStore.getState().steps).toHaveLength(0);
    expect(useBuilderStore.getState().selectedNodeId).toBeNull();
  });

  it('has no delete affordance on the trigger', () => {
    useBuilderStore.setState({ trigger: CONTACT_TRIGGER, selectedNodeId: 'trigger' });
    render(<ConfigPanel />);
    expect(screen.queryByRole('button', { name: 'Delete step' })).not.toBeInTheDocument();
  });
});

describe('ConfigPanel dry-run preview (A3.5)', () => {
  it('shows a would-run preview with resolved values for the selected action', () => {
    seedWithStep(emailStep('a_run'));
    const dryRun: DryRunState = {
      byStep: { a_run: { step_id: 'a_run', type: 'action', status: 'run', action_type: 'send_email', resolved_params: { subject: 'Hi Ada' } } },
      conditionResult: true,
      sampleLabel: 'Ada Lovelace',
    };
    render(<ConfigPanel dryRun={dryRun} />);
    expect(screen.getByText('Dry run: would run')).toBeInTheDocument();
    expect(screen.getByText('Hi Ada')).toBeInTheDocument();
  });

  it('shows skipped + reason for an untaken step', () => {
    seedWithStep(emailStep('a_skip'));
    const dryRun: DryRunState = {
      byStep: { a_skip: { step_id: 'a_skip', type: 'action', status: 'skip', reason: 'branch not taken' } },
      conditionResult: true,
      sampleLabel: 'Ada Lovelace',
    };
    render(<ConfigPanel dryRun={dryRun} />);
    expect(screen.getByText('Dry run: skipped')).toBeInTheDocument();
    expect(screen.getByText('branch not taken')).toBeInTheDocument();
  });

  it('shows the trigger-conditions outcome in the trigger panel', () => {
    useBuilderStore.setState({ trigger: CONTACT_TRIGGER, selectedNodeId: 'trigger' });
    const dryRun: DryRunState = { byStep: {}, conditionResult: false, sampleLabel: 'Ada Lovelace' };
    render(<ConfigPanel dryRun={dryRun} />);
    expect(screen.getByText(/Dry run · Ada Lovelace/)).toBeInTheDocument();
    expect(screen.getByText(/do not match/i)).toBeInTheDocument();
  });
});
