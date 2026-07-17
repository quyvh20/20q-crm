import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { IntegrationEvent, LeadSource } from '../../../features/integrations/types';

// The detail page's job is to answer "what happened to the lead John submitted on
// Tuesday" — including the fields that were deliberately NOT saved. A customer who
// only finds that out from missing data weeks later is the failure being prevented.

vi.mock('../../../features/integrations/api', () => ({
  listSources: vi.fn(),
  createSource: vi.fn(),
  getSource: vi.fn(),
  updateSource: vi.fn(),
  deleteSource: vi.fn(),
  rotateKey: vi.fn(),
  listEvents: vi.fn(),
}));

import { getSource, listEvents, rotateKey } from '../../../features/integrations/api';
import IntegrationSourceDetailSection from '../IntegrationSourceDetailSection';

const SOURCE: LeadSource = {
  id: 's1',
  org_id: 'o1',
  kind: 'api',
  name: 'Website form',
  token_prefix: 'crm_lead_ab12cd34',
  target_slug: 'contact',
  match_fields: ['email'],
  field_map: {},
  update_policy: 'fill_blank_only',
  config: {},
  status: 'active',
  consecutive_failures: 0,
  daily_cap: 0,
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const EVENT: IntegrationEvent = {
  id: 'e1',
  org_id: 'o1',
  source_id: 's1',
  status: 'processed',
  attempts: 1,
  raw_payload: { email: 'ada@example.com', company_size: '50' },
  context: {},
  quarantined_fields: { company_size: '50' },
  result_slug: 'contact',
  result_record_id: 'c9',
  outcome: 'created',
  created_at: new Date().toISOString(),
};

function renderDetail() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/settings/integrations/s1']}>
        <Routes>
          <Route path="/settings/integrations/:id" element={<IntegrationSourceDetailSection />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getSource).mockResolvedValue(SOURCE);
  vi.mocked(listEvents).mockResolvedValue([EVENT]);
});
afterEach(cleanup);

describe('IntegrationSourceDetailSection', () => {
  it('names the tab after the loaded source', async () => {
    // The settings layout deliberately writes NO title for a nested path, because a
    // parent's effect would clobber the child's. If this page forgets, the tab
    // silently keeps the PREVIOUS route's title — no error, just wrong.
    renderDetail();
    await waitFor(() => expect(document.title).toContain('Website form'));
  });

  it('shows what a delivery became, and what was skipped', async () => {
    renderDetail();

    expect(await screen.findByText('created')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /view contact/i })).toHaveAttribute('href', '/contacts/c9');
    // The quarantined key is surfaced in the row, not buried in a payload dump:
    // this is the integrator's only signal that a field never landed.
    expect(screen.getByText('company_size')).toBeInTheDocument();
  });

  it('explains a skipped field in the details modal', async () => {
    renderDetail();
    fireEvent.click(await screen.findByRole('button', { name: /details/i }));

    // Radix renders in a portal — query the screen, never the container.
    expect(await screen.findByText('Recorded but not saved')).toBeInTheDocument();
    expect(screen.getByText(/These aren't contact fields/)).toBeInTheDocument();
  });

  it('shows an empty state before any lead arrives', async () => {
    vi.mocked(listEvents).mockResolvedValue([]);
    renderDetail();
    expect(await screen.findByText('No deliveries yet')).toBeInTheDocument();
  });

  it('warns that rotating breaks the live integration, and reveals the new key once', async () => {
    vi.mocked(rotateKey).mockResolvedValue({ source: SOURCE, plaintext_key: 'crm_lead_NEWKEY' });
    renderDetail();

    fireEvent.click(await screen.findByRole('button', { name: /rotate key/i }));
    // The confirm must say what breaks — rotating silently is how an integration
    // dies at 3am with nobody knowing why.
    expect(await screen.findByText(/stops working immediately/i)).toBeInTheDocument();

    // Two buttons now match: the page's and the dialog's confirm. The dialog is
    // rendered last (a Radix portal appended to body), so its confirm is the last.
    const buttons = screen.getAllByRole('button', { name: /rotate key/i });
    fireEvent.click(buttons[buttons.length - 1]);

    expect(await screen.findByTestId('secret-value')).toHaveTextContent('crm_lead_NEWKEY');
  });
});
