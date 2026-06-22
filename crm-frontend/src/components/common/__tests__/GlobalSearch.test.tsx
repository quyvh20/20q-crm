import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import type { SearchResult } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  globalSearch: vi.fn(),
}));

import { globalSearch } from '../../../lib/api';
import GlobalSearch from '../GlobalSearch';

const result: SearchResult = {
  query: 'acme',
  groups: [
    {
      object: 'ticket', label: 'Ticket', label_plural: 'Tickets', icon: '🎫',
      hits: [{ record: { id: 't1', object: 'ticket', display: 'Acme ticket', fields: {}, created_at: '', updated_at: '' }, score: 0.9 }],
    },
    {
      object: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤',
      hits: [{ record: { id: 'c1', object: 'contact', display: 'Jane Doe', fields: {}, created_at: '', updated_at: '' } }],
    },
  ],
};

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function open() {
  render(<GlobalSearch />);
  fireEvent.click(screen.getByLabelText('Open search'));
}

describe('GlobalSearch', () => {
  it('renders grouped cross-object results with a semantic score badge', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    open();

    fireEvent.change(screen.getByPlaceholderText('Search across every object…'), { target: { value: 'acme' } });

    await waitFor(() => expect(screen.getByText('Tickets')).toBeInTheDocument());
    expect(screen.getByText('Contacts')).toBeInTheDocument();
    expect(screen.getByText('Acme ticket')).toBeInTheDocument();
    expect(screen.getByText('Jane Doe')).toBeInTheDocument();
    // Semantic hit shows a similarity %, fulltext/contact hit (no score) does not.
    expect(screen.getByText('90%')).toBeInTheDocument();

    // The ticket result links to the object's area; the deal would link to detail.
    const ticketLink = screen.getByText('Acme ticket').closest('a');
    expect(ticketLink).toHaveAttribute('href', '/objects/ticket');
    const contactLink = screen.getByText('Jane Doe').closest('a');
    expect(contactLink).toHaveAttribute('href', '/contacts');
  });

  it('shows an empty state when nothing matches', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue({ query: 'zzz', groups: [] });
    open();

    fireEvent.change(screen.getByPlaceholderText('Search across every object…'), { target: { value: 'zzz' } });

    await waitFor(() => expect(screen.getByText('No results for "zzz"')).toBeInTheDocument());
  });
});
