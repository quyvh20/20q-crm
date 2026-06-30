import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import type { RelatedList } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listRecordRelatedLists: vi.fn(),
}));

import { listRecordRelatedLists } from '../../../lib/api';
import RelatedLists from '../RelatedLists';

const dealsGroup: RelatedList = {
  object: 'deal',
  label: 'Deals',
  icon: '💰',
  field_key: 'contact',
  field_label: 'Contact',
  count: 2,
  records: [
    { id: 'd1', object: 'deal', display: 'Big renewal', fields: {}, created_at: '2026-01-02T00:00:00Z', updated_at: '' },
    { id: 'd2', object: 'deal', display: 'Small upsell', fields: {}, created_at: '2026-01-03T00:00:00Z', updated_at: '' },
  ],
};

function renderLists() {
  return render(
    <MemoryRouter>
      <RelatedLists slug="contact" recordId="p1" />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('RelatedLists', () => {
  it('renders each related group with links to the child records', async () => {
    vi.mocked(listRecordRelatedLists).mockResolvedValue([dealsGroup]);

    renderLists();

    await waitFor(() => expect(listRecordRelatedLists).toHaveBeenCalledWith('contact', 'p1'));

    // Group heading + which field relates them.
    expect(await screen.findByText('💰 Deals')).toBeInTheDocument();
    expect(screen.getByText(/via Contact/)).toBeInTheDocument();

    // Each child record is a link to its own record page (deals route to /deals/:id).
    const big = screen.getByText('Big renewal').closest('a');
    expect(big).toHaveAttribute('href', '/deals/d1');
    expect(screen.getByText('Small upsell').closest('a')).toHaveAttribute('href', '/deals/d2');
  });

  it('shows an empty state when there are no related records', async () => {
    vi.mocked(listRecordRelatedLists).mockResolvedValue([]);

    renderLists();

    expect(await screen.findByText('No related records')).toBeInTheDocument();
  });

  it('hides groups that have no records', async () => {
    vi.mocked(listRecordRelatedLists).mockResolvedValue([{ ...dealsGroup, records: [], count: 0 }]);

    renderLists();

    // The group title must not render; the empty state takes over.
    await waitFor(() => expect(listRecordRelatedLists).toHaveBeenCalled());
    expect(screen.queryByText('💰 Deals')).not.toBeInTheDocument();
    expect(await screen.findByText('No related records')).toBeInTheDocument();
  });
});
