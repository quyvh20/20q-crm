import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { useBuilderStore } from '../../store';
import { ActionConfigPanel } from '../ActionConfigPanel';
import type { WorkflowSchema } from '../../api';
import type { ActionSpec } from '../../types';

// ── Mock API layer ───────────────────────────────────────────────────
vi.mock('../../api', async () => {
  const actual = await vi.importActual<typeof import('../../api')>('../../api');
  return {
    ...actual,
    createWorkflow: vi.fn(),
    updateWorkflow: vi.fn(),
    getWorkflow: vi.fn(),
    getWorkflowSchema: vi.fn(),
  };
});

// ── Fixtures ─────────────────────────────────────────────────────────
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

/** Email action as it would exist after a user inserts {{contact.email}} via the {x} button */
const SAVED_EMAIL_ACTION: ActionSpec = {
  id: 'action_email_1',
  type: 'send_email',
  params: {
    to: '{{contact.email}}',
    from_name: 'Acme Corp',
    subject: 'Welcome, {{contact.first_name}}!',
    body_html: '<p>Hi {{contact.first_name}},</p>',
  },
};

/** Email action with empty defaults — as created by getDefaultParams() */
const NEW_EMAIL_ACTION: ActionSpec = {
  id: 'action_email_new',
  type: 'send_email',
  params: {
    to: '',
    subject: '',
    body_html: '',
  },
};

// ── Setup ────────────────────────────────────────────────────────────
beforeEach(() => {
  useBuilderStore.getState().reset();
  useBuilderStore.setState({
    schema: MOCK_SCHEMA,
    schemaLoading: false,
    schemaError: null,
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 1: New email actions start empty — placeholder is visible
// ═══════════════════════════════════════════════════════════════════════
describe('TestEmailAction_NewActionShowsPlaceholder', () => {
  it('starts with empty "to" field — no hardcoded {{contact.email}} default', () => {
    useBuilderStore.setState({
      actions: [NEW_EMAIL_ACTION],
      selectedNodeId: NEW_EMAIL_ACTION.id,
    });

    render(<ActionConfigPanel />);

    // The placeholder should be visible (input value is empty)
    const toInput = screen.getByPlaceholderText('Click {x} to insert contact email');
    expect(toInput).toBeInTheDocument();
    expect((toInput as HTMLInputElement).value).toBe('');
  });

  it('shows instruction placeholders on all email fields', () => {
    useBuilderStore.setState({
      actions: [NEW_EMAIL_ACTION],
      selectedNodeId: NEW_EMAIL_ACTION.id,
    });

    render(<ActionConfigPanel />);

    expect(screen.getByPlaceholderText('Click {x} to insert contact email')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Click {x} to insert variables')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Write your email body — click {x} to insert variables')).toBeInTheDocument();
  });
});

// ═══════════════════════════════════════════════════════════════════════
// Test 2: Saved email template values survive round-trip
// ═══════════════════════════════════════════════════════════════════════
describe('TestEmailAction_TemplateRoundTrip', () => {
  it('preserves {{contact.email}} in params.to after store set → read', () => {
    useBuilderStore.setState({
      actions: [SAVED_EMAIL_ACTION],
      selectedNodeId: SAVED_EMAIL_ACTION.id,
    });

    const state = useBuilderStore.getState();
    const savedAction = state.actions.find((a) => a.id === SAVED_EMAIL_ACTION.id);

    expect(savedAction).toBeDefined();
    expect(savedAction!.params.to).toBe('{{contact.email}}');
    expect(savedAction!.params.subject).toBe('Welcome, {{contact.first_name}}!');
    expect(savedAction!.params.body_html).toBe('<p>Hi {{contact.first_name}},</p>');
  });

  it('renders TemplateInput with preserved {{contact.email}} value', () => {
    useBuilderStore.setState({
      actions: [SAVED_EMAIL_ACTION],
      selectedNodeId: SAVED_EMAIL_ACTION.id,
    });

    render(<ActionConfigPanel />);

    const toInput = screen.getByDisplayValue('{{contact.email}}');
    expect(toInput).toBeInTheDocument();
    expect(toInput.tagName).toBe('INPUT');
  });

  it('renders all email template fields with preserved values', () => {
    useBuilderStore.setState({
      actions: [SAVED_EMAIL_ACTION],
      selectedNodeId: SAVED_EMAIL_ACTION.id,
    });

    render(<ActionConfigPanel />);

    expect(screen.getByDisplayValue('{{contact.email}}')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Acme Corp')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Welcome, {{contact.first_name}}!')).toBeInTheDocument();
    expect(screen.getByDisplayValue('<p>Hi {{contact.first_name}},</p>')).toBeInTheDocument();
  });

  it('preserves template after updateAction merges params', () => {
    useBuilderStore.setState({
      actions: [SAVED_EMAIL_ACTION],
      selectedNodeId: SAVED_EMAIL_ACTION.id,
    });

    // Simulate user changing the subject — "to" should stay untouched
    useBuilderStore.getState().updateAction(SAVED_EMAIL_ACTION.id, {
      params: { subject: 'New subject' },
    });

    const updated = useBuilderStore.getState().actions.find((a) => a.id === SAVED_EMAIL_ACTION.id);
    expect(updated!.params.to).toBe('{{contact.email}}');
    expect(updated!.params.subject).toBe('New subject');
    expect(updated!.params.body_html).toBe('<p>Hi {{contact.first_name}},</p>');
  });

  it('builds correct save payload with template values intact', () => {
    useBuilderStore.setState({
      workflowId: 'wf_123',
      name: 'Welcome Flow',
      trigger: { type: 'contact_created', params: {} },
      actions: [SAVED_EMAIL_ACTION],
    });

    const state = useBuilderStore.getState();
    const payload = {
      name: state.name,
      description: state.description,
      trigger: state.trigger,
      conditions: state.conditions,
      actions: state.actions,
    };

    expect(payload.actions[0].params.to).toBe('{{contact.email}}');
    expect(payload.actions[0].params.subject).toBe('Welcome, {{contact.first_name}}!');
    expect(JSON.stringify(payload)).toContain('{{contact.email}}');
  });

  it('simulates full save → load cycle via store', () => {
    // 1. Set up state as if user configured the action via {x} button
    useBuilderStore.setState({
      workflowId: 'wf_456',
      name: 'Onboarding',
      trigger: { type: 'contact_created', params: {} },
      actions: [SAVED_EMAIL_ACTION],
      isDirty: true,
    });

    // 2. Capture the payload that save() would send
    const stateBefore = useBuilderStore.getState();
    const savedPayload = {
      actions: stateBefore.actions.map((a) => ({ ...a, params: { ...a.params } })),
    };

    // 3. Reset store (simulate navigation away)
    useBuilderStore.getState().reset();
    expect(useBuilderStore.getState().actions).toHaveLength(0);

    // 4. Reload from "API response" (simulate loadWorkflow result)
    useBuilderStore.setState({
      workflowId: 'wf_456',
      name: 'Onboarding',
      trigger: { type: 'contact_created', params: {} },
      actions: savedPayload.actions as ActionSpec[],
      isDirty: false,
      selectedNodeId: SAVED_EMAIL_ACTION.id,
    });

    // 5. Verify template values survived the round-trip
    const reloaded = useBuilderStore.getState().actions[0];
    expect(reloaded.params.to).toBe('{{contact.email}}');
    expect(reloaded.params.subject).toBe('Welcome, {{contact.first_name}}!');
    expect(reloaded.params.body_html).toBe('<p>Hi {{contact.first_name}},</p>');

    // 6. Verify UI renders correctly after reload
    render(<ActionConfigPanel />);
    expect(screen.getByDisplayValue('{{contact.email}}')).toBeInTheDocument();
  });
});
