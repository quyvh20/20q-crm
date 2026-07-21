import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LeadSource } from '../../../features/integrations/types';

// The card that decides whether a lead is also an opportunity.
//
// Two of its behaviours are surprising unless the UI says so, and both are the kind
// that is invisible when wrong — a pipeline that quietly does not fill up looks the
// same as a misconfigured source. So the copy is under test, not just the controls.

vi.mock('../../../features/integrations/api', () => ({
  listSources: vi.fn(),
  createSource: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  deleteSource: vi.fn(),
  rotateKey: vi.fn(),
  listEvents: vi.fn(),
  sendTestLead: vi.fn(),
  getMapping: vi.fn(),
  saveMapping: vi.fn(),
  listEventLog: vi.fn(),
  retryEvent: vi.fn(),
  RetryRefusedError: class RetryRefusedError extends Error {
    reason: string;
    constructor(message: string, reason: string) {
      super(message);
      this.name = 'RetryRefusedError';
      this.reason = reason;
    }
  },
  listSourceStats: vi.fn(),
}));

vi.mock('../../../lib/api', () => ({
  getStages: vi.fn(),
}));

import { updateSource } from '../../../features/integrations/api';
import { getStages } from '../../../lib/api';
import LeadDealCard from '../LeadDealCard';

const STAGES = [
  { id: 'st-lead', org_id: 'o1', name: 'Lead In', position: 0, color: '#fff', is_won: false, is_lost: false },
  { id: 'st-qual', org_id: 'o1', name: 'Qualified', position: 1, color: '#fff', is_won: false, is_lost: false },
  { id: 'st-won', org_id: 'o1', name: 'Closed Won', position: 2, color: '#fff', is_won: true, is_lost: false },
];

function makeSource(over: Partial<LeadSource> = {}): LeadSource {
  return {
    id: 's1',
    org_id: 'o1',
    kind: 'api',
    name: 'Website form',
    token_prefix: 'crm_lead_ab12cd34',
    target_slug: 'contact',
    match_fields: ['email'],
    field_map: {},
    owner_pool: [],
    batch_enroll_automation: false,
    update_policy: 'fill_blank_only',
    config: {},
    status: 'active',
    consecutive_failures: 0,
    daily_cap: 1000,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  };
}

function renderCard(source: LeadSource) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <LeadDealCard source={source} />
    </QueryClientProvider>,
  );
}

describe('LeadDealCard', () => {
  beforeEach(() => {
    vi.mocked(getStages).mockResolvedValue(STAGES);
    vi.mocked(updateSource).mockResolvedValue(makeSource());
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it('says plainly that only a NEW contact opens a deal', async () => {
    renderCard(makeSource());
    // The gap this wording covers is silent: a returning customer's submission is
    // the highest-intent lead a form gets, and it produces no deal.
    expect(await screen.findByText(/already in your CRM/i)).toBeInTheDocument();
  });

  it('warns that a test lead never opens a deal', async () => {
    renderCard(makeSource({ config: { deal: { enabled: true, stage_id: 'st-lead' } } }));
    expect(await screen.findByText(/never opens a deal/i)).toBeInTheDocument();
  });

  it('does not offer won or lost stages', async () => {
    renderCard(makeSource({ config: { deal: { enabled: true, stage_id: 'st-lead' } } }));
    expect(await screen.findByRole('option', { name: 'Lead In' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Qualified' })).toBeInTheDocument();
    // A deal created into a won stage keeps is_won=false — wrong in the board and
    // wrong in the forecast, in opposite directions.
    expect(screen.queryByRole('option', { name: 'Closed Won' })).not.toBeInTheDocument();
  });

  it('cannot be turned on without a stage', async () => {
    renderCard(makeSource());
    fireEvent.click(screen.getByRole('checkbox'));
    const save = await screen.findByRole('button', { name: /save/i });
    expect(save).toBeDisabled();
    expect(screen.getByText(/choose a stage to turn this on/i)).toBeInTheDocument();
  });

  it('saves the stage and template together', async () => {
    renderCard(makeSource());
    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.change(await screen.findByLabelText(/start new deals in/i), {
      target: { value: 'st-qual' },
    });
    fireEvent.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => {
      expect(updateSource).toHaveBeenCalledWith('s1', {
        deal: {
          enabled: true,
          stage_id: 'st-qual',
          name_template: '{{full_name}} — {{source_name}}',
        },
      });
    });
  });

  it('previews a name the way the server renders it — a missing field is left out, never printed', async () => {
    renderCard(makeSource({ config: { deal: { enabled: true, stage_id: 'st-lead' } } }));
    const input = await screen.findByLabelText(/deal name/i);

    fireEvent.change(input, { target: { value: '{{full_name}} — {{source_name}}' } });
    expect(screen.getByText('Ada Lovelace — Website form')).toBeInTheDocument();

    // A token the sample has no value for must collapse, not survive as {{…}} on a
    // customer's kanban board.
    fireEvent.change(input, { target: { value: '{{nonexistent}} — {{source_name}}' } });
    expect(screen.getByText('Website form')).toBeInTheDocument();
    expect(screen.queryByText(/\{\{nonexistent\}\}/)).not.toBeInTheDocument();
  });

  it('surfaces the server-computed dead-stage badge', async () => {
    renderCard(makeSource({
      config: { deal: { enabled: true, stage_id: 'st-deleted' } },
      deal_stage_missing: true,
    }));
    expect(await screen.findByText(/has been deleted/i)).toBeInTheDocument();
  });

  it('turning it off saves immediately, without needing a stage', async () => {
    renderCard(makeSource({ config: { deal: { enabled: true, stage_id: 'st-lead' } } }));
    fireEvent.click(screen.getByRole('checkbox'));
    await waitFor(() => {
      expect(updateSource).toHaveBeenCalledWith('s1', expect.objectContaining({
        deal: expect.objectContaining({ enabled: false }),
      }));
    });
  });

  it('a failed stage fetch does not blank the card', async () => {
    vi.mocked(getStages).mockRejectedValue(new Error('offline'));
    renderCard(makeSource({ config: { deal: { enabled: true, stage_id: 'st-lead' } } }));
    expect(await screen.findByText(/could not load your pipeline stages/i)).toBeInTheDocument();
    // The setting itself must still be readable and switchable off.
    expect(screen.getByRole('checkbox')).toBeChecked();
  });
});
