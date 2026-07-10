import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { useBuilderStore } from '../../../store';
import { ConfigPanel } from '../ConfigPanel';
import type { WorkflowSchema } from '../../../api';
import type { TriggerSpec, WorkflowStep } from '../../../types';

// A8: param round-trips for the new builder's action forms — replaces the deleted
// legacy panel tests (EmailActionRoundTrip / AssignActionRoundTrip / ActivityParams).
// Editing a field in the config form must persist onto the selected action via the
// shared store.updateAction, so the value survives save.

vi.mock('../../../api', async () => {
  const actual = await vi.importActual<typeof import('../../../api')>('../../../api');
  return { ...actual, getObjectFields: vi.fn().mockResolvedValue([]) };
});
// send_email's form calls useEmailTemplates; stub it so no QueryClient is needed.
vi.mock('../../../queries', async () => {
  const actual = await vi.importActual<typeof import('../../../queries')>('../../../queries');
  return { ...actual, useEmailTemplates: () => ({ data: { templates: [], total: 0 }, isLoading: false }) };
});

const MOCK_SCHEMA: WorkflowSchema = {
  entities: [{ key: 'contact', label: 'Contact', icon: '👤', fields: [{ path: 'contact.email', label: 'Email', type: 'string' }] }],
  custom_objects: [],
  stages: [],
  tags: [],
  users: [{ id: 'u1', name: 'Ada', email: 'ada@acme.com' }],
};
const CONTACT_TRIGGER: TriggerSpec = { type: 'contact_created', params: {} };

function seedWithStep(step: WorkflowStep) {
  const store = useBuilderStore.getState();
  store.reset();
  useBuilderStore.setState({ schema: MOCK_SCHEMA, trigger: CONTACT_TRIGGER });
  useBuilderStore.getState().addStep(step, null, null, 0);
  useBuilderStore.getState().selectNode(step.id);
}

/** The params the form actually persisted, read back off the flattened action view. */
function paramsOf(id: string): Record<string, unknown> {
  return (useBuilderStore.getState().actions.find((a) => a.id === id)?.params ?? {}) as Record<string, unknown>;
}

beforeEach(() => useBuilderStore.getState().reset());

describe('ActionConfig param round-trips (A8)', () => {
  it('send_email: editing the subject persists onto the action', () => {
    seedWithStep({ id: 'e1', type: 'action', action: { id: 'e1', type: 'send_email', params: { to: '', subject: '', body_html: '' } } });
    render(<ConfigPanel />);
    fireEvent.change(screen.getByPlaceholderText('Click {x} to insert variables'), { target: { value: 'Welcome aboard' } });
    expect(paramsOf('e1').subject).toBe('Welcome aboard');
  });

  it('log_activity: picking a type and typing a title both persist', () => {
    seedWithStep({ id: 'l1', type: 'action', action: { id: 'l1', type: 'log_activity', params: { activity_type: 'note', title: '', body: '' } } });
    render(<ConfigPanel />);
    fireEvent.click(screen.getByRole('button', { name: /Call/ }));
    expect(paramsOf('l1').activity_type).toBe('call');
    fireEvent.change(screen.getByPlaceholderText(/Logged a call with/), { target: { value: 'Called Ada' } });
    expect(paramsOf('l1').title).toBe('Called Ada');
  });

  it('assign_user: changing the strategy persists onto the action', () => {
    seedWithStep({ id: 'a1', type: 'action', action: { id: 'a1', type: 'assign_user', params: { entity: 'contact', strategy: 'round_robin' } } });
    render(<ConfigPanel />);
    const strategySelect = screen.getAllByRole('combobox').find((el) => (el as HTMLSelectElement).value === 'round_robin');
    expect(strategySelect).toBeTruthy();
    fireEvent.change(strategySelect!, { target: { value: 'least_loaded' } });
    expect(paramsOf('a1').strategy).toBe('least_loaded');
  });
});
