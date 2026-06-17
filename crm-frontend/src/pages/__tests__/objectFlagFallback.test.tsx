import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import type { ReactElement } from 'react';

/**
 * objects.unified_read flag-fallback tests (plan P3, risk R1).
 *
 * P3 puts Contacts and Deals behind the `objects.unified_read` flag and promises
 * that flipping it never changes data — only WHICH component renders, with the
 * legacy pages as the fallback. These tests pin that branch on both pages:
 * flag OFF → the legacy page; flag ON → the shared <ObjectListView>. The shared
 * renderer and the legacy children are stubbed so the test exercises only the
 * page-level flag branch without a backend.
 */

const flagState = vi.hoisted(() => ({ unified: false }));
vi.mock('../../lib/flags', () => ({
  isUnifiedObjectReadEnabled: () => flagState.unified,
}));

// Stub the shared renderer with an identifiable marker carrying the slug.
vi.mock('../../features/objects', async () => {
  const React = await import('react');
  return {
    ObjectListView: ({ slug }: { slug: string }) =>
      React.createElement('div', null, `unified:${slug}`),
  };
});

// Stub the legacy contact children so ContactsPageInner mounts without their fetches.
vi.mock('../../components/contacts/ContactList', async () => {
  const React = await import('react');
  return { default: () => React.createElement('div', null, 'legacy-contact-list') };
});
vi.mock('../../components/contacts/ContactForm', () => ({ default: () => null }));
vi.mock('../../components/contacts/ImportModal', () => ({ default: () => null }));

// Stub the legacy deal children so DealsBoard mounts cleanly.
vi.mock('../../components/deals/KanbanColumn', () => ({ default: () => null }));
vi.mock('../../components/deals/DealCard', () => ({ default: () => null }));
vi.mock('../../components/deals/DealFormModal', () => ({ default: () => null }));
vi.mock('../../components/deals/ForecastChart', () => ({ default: () => null }));

// Stub the API so the legacy trees' on-mount queries resolve to empties.
vi.mock('../../lib/api', () => ({
  getDeals: vi.fn(async () => ({ deals: [] })),
  getStages: vi.fn(async () => []),
  changeDealStage: vi.fn(),
  seedDefaultStages: vi.fn(),
  getCompanies: vi.fn(async () => []),
  getTags: vi.fn(async () => []),
}));

import ContactsPage from '../ContactsPage';
import DealsPage from '../DealsPage';

function renderPage(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  flagState.unified = false;
});

describe('objects.unified_read flag fallback (P3 R1)', () => {
  it('Contacts: flag OFF renders the legacy page, not the unified renderer', () => {
    flagState.unified = false;
    renderPage(<ContactsPage />);
    expect(screen.getByText('Add Contact')).toBeInTheDocument();
    expect(screen.queryByText('unified:contact')).not.toBeInTheDocument();
  });

  it('Contacts: flag ON renders the shared ObjectListView', () => {
    flagState.unified = true;
    renderPage(<ContactsPage />);
    expect(screen.getByText('unified:contact')).toBeInTheDocument();
    expect(screen.queryByText('Add Contact')).not.toBeInTheDocument();
  });

  it('Deals: flag OFF renders the legacy board, not the unified renderer', async () => {
    flagState.unified = false;
    renderPage(<DealsPage />);
    expect(await screen.findByRole('heading', { name: 'Deals' })).toBeInTheDocument();
    expect(screen.queryByText('unified:deal')).not.toBeInTheDocument();
  });

  it('Deals: flag ON renders the shared ObjectListView', () => {
    flagState.unified = true;
    renderPage(<DealsPage />);
    expect(screen.getByText('unified:deal')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Deals' })).not.toBeInTheDocument();
  });
});
