import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { useBuilderStore } from '../../../store';
import { ConfigPanel } from '../ConfigPanel';
import type { WorkflowSchema } from '../../../api';
import type { TriggerSpec, WorkflowStep } from '../../../types';

// The Wait step's mode toggle, driven through the real panel. The property under
// test is that switching AWAY from "For an event" and back does not quietly
// rewrite the user's event, campaign pin or timeout — the panel reads the STEP,
// not the flattened action, which only carries the active mode's keys.

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

vi.mock('../../../queries', async () => {
  const actual = await vi.importActual<typeof import('../../../queries')>('../../../queries');
  return { ...actual, useEmailTemplates: () => ({ data: { templates: [], total: 0 }, isLoading: false }) };
});

vi.mock('../../../../marketing/contentQueries', () => ({
  useContentList: () => ({ data: [], isLoading: false }),
}));

vi.mock('../../../../marketing/campaignsQueries', () => ({
  useCampaigns: () => ({
    data: [
      { id: 'camp-spring', name: 'Spring Launch' },
      { id: 'camp-winter', name: 'Winter Sale' },
    ],
    isLoading: false,
    isError: false,
  }),
}));

const MOCK_SCHEMA: WorkflowSchema = {
  entities: [
    {
      key: 'contact',
      label: 'Contact',
      icon: '👤',
      fields: [
        { path: 'contact.email', label: 'Email', type: 'string' },
        { path: 'contact.renews_at', label: 'Renews At', type: 'date' },
      ],
    },
  ],
  custom_objects: [],
  stages: [],
  tags: [],
  users: [],
};

const CONTACT_TRIGGER: TriggerSpec = { type: 'contact_created', params: {} };

function seedDelay(delay: WorkflowStep['delay']) {
  const store = useBuilderStore.getState();
  store.reset();
  useBuilderStore.setState({ schema: MOCK_SCHEMA, schemaLoading: false, schemaError: null, trigger: CONTACT_TRIGGER });
  useBuilderStore.getState().addStep({ id: 'a_wait', type: 'delay', delay }, null, null, 0);
  useBuilderStore.getState().selectNode('a_wait');
}

const storedDelay = () => useBuilderStore.getState().findStep('a_wait')!.delay!;

beforeEach(() => {
  useBuilderStore.getState().reset();
});

describe('Wait step — mode toggle', () => {
  it('offers all three modes and starts on the configured one', () => {
    seedDelay({ duration_sec: 0, wait_event: 'email_clicked', timeout_sec: 7200, campaign_id: 'camp-spring' });
    render(<ConfigPanel />);

    expect(screen.getByText('For a duration')).toBeInTheDocument();
    expect(screen.getByText('Until a date')).toBeInTheDocument();
    expect(screen.getByText('For an event')).toBeInTheDocument();
    // Event mode's own fields are showing.
    expect(screen.getByLabelText('Campaign to wait on')).toHaveValue('camp-spring');
  });

  it('keeps the event, campaign pin and timeout across a there-and-back toggle', () => {
    seedDelay({ duration_sec: 0, wait_event: 'email_clicked', timeout_sec: 7200, campaign_id: 'camp-spring' });
    render(<ConfigPanel />);

    fireEvent.click(screen.getByText('For a duration'));
    expect(storedDelay().wait_event).toBeUndefined();

    fireEvent.click(screen.getByText('For an event'));

    const d = storedDelay();
    expect(d.wait_event).toBe('email_clicked');
    expect(d.campaign_id).toBe('camp-spring');
    expect(d.timeout_sec).toBe(7200);
  });

  it('keeps the date-wait configuration across a there-and-back toggle too', () => {
    seedDelay({ duration_sec: 0, until_field: 'contact.renews_at', offset_days: -3, at_time: '14:30', timezone: 'Europe/Berlin' });
    render(<ConfigPanel />);

    fireEvent.click(screen.getByText('For a duration'));
    fireEvent.click(screen.getByText('Until a date'));

    const d = storedDelay();
    expect(d.until_field).toBe('contact.renews_at');
    expect(d.offset_days).toBe(-3);
    expect(d.at_time).toBe('14:30');
    expect(d.timezone).toBe('Europe/Berlin');
  });

  it('defaults a fresh switch into event mode, and never leaves two modes set', () => {
    seedDelay({ duration_sec: 900 });
    render(<ConfigPanel />);

    fireEvent.click(screen.getByText('For an event'));

    const d = storedDelay();
    expect(d.wait_event).toBe('email_opened');
    expect(d.timeout_sec).toBe(3 * 86400);
    expect(d.until_field).toBeUndefined();
    // And the flattened payload carries only the event mode's keys.
    const params = useBuilderStore.getState().actions[0].params;
    expect(params).not.toHaveProperty('until_field');
    expect(params.wait_event).toBe('email_opened');
  });

  it('refuses event mode when the trigger has no contact to watch', () => {
    seedDelay({ duration_sec: 900 });
    useBuilderStore.setState({ trigger: { type: 'schedule', params: {} } });
    render(<ConfigPanel />);

    const btn = screen.getByText('For an event');
    expect(btn).toBeDisabled();
    fireEvent.click(btn);
    expect(storedDelay().wait_event).toBeUndefined();
  });
});
