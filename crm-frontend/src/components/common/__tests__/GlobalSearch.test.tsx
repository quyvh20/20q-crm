import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
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

// The palette navigates, so it needs a router. The probe prints the CURRENT
// location: a client-side navigation moves it, a full page reload would not
// (MemoryRouter has no page to reload) — which is exactly the defect R7.5 fixed.
function Probe() {
  return <span data-testid="location">{useLocation().pathname}</span>;
}

function renderPalette() {
  render(
    <MemoryRouter initialEntries={['/deals']}>
      <GlobalSearch />
      <Probe />
    </MemoryRouter>,
  );
}

// userEvent, not fireEvent: opening must move focus ONTO the trigger the way a
// real click does, or there is nothing for the dialog to restore focus to and
// the restore assertion below would pass vacuously.
async function open() {
  renderPalette();
  await userEvent.click(screen.getByLabelText('Open search'));
}

async function search(term: string) {
  fireEvent.change(screen.getByPlaceholderText('Search across every object…'), { target: { value: term } });
}

describe('GlobalSearch', () => {
  it('renders grouped cross-object results with a semantic score badge', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    await open();

    await search('acme');

    await waitFor(() => expect(screen.getByText('Tickets')).toBeInTheDocument());
    expect(screen.getByText('Contacts')).toBeInTheDocument();
    expect(screen.getByText('Acme ticket')).toBeInTheDocument();
    expect(screen.getByText('Jane Doe')).toBeInTheDocument();
    // Semantic hit shows a similarity %, fulltext/contact hit (no score) does not.
    expect(screen.getByText('90%')).toBeInTheDocument();

    // Every result links to its URL-addressable record page (deals would use
    // the bespoke /deals/:id page instead).
    const ticketLink = screen.getByText('Acme ticket').closest('a');
    expect(ticketLink).toHaveAttribute('href', '/objects/ticket/records/t1');
    const contactLink = screen.getByText('Jane Doe').closest('a');
    expect(contactLink).toHaveAttribute('href', '/objects/contact/records/c1');
  });

  it('shows an empty state when nothing matches', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue({ query: 'zzz', groups: [] });
    await open();

    await search('zzz');

    await waitFor(() => expect(screen.getByText('No results for "zzz"')).toBeInTheDocument());
  });
});

describe('GlobalSearch is a real dialog (R7.5)', () => {
  it('opens as a labelled modal dialog and lands focus in the box without autoFocus', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    await open();

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('Search');

    const input = screen.getByPlaceholderText('Search across every object…');
    // Doctrine: never autoFocus inside a Modal. The box is simply the first
    // tabbable node in the panel, so Radix's focus scope puts focus there.
    expect(input).not.toHaveAttribute('autofocus');
    await waitFor(() => expect(input).toHaveFocus());
  });

  it('closes on Escape and gives focus back to the trigger it was opened from', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    await open();

    const trigger = screen.getByLabelText('Open search');
    await screen.findByRole('dialog');
    // The trigger must SURVIVE the palette being open — the old overlay replaced
    // it, leaving focus restore with nothing to restore to.
    expect(trigger).toBeInTheDocument();
    expect(trigger).toHaveAttribute('aria-expanded', 'true');

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('opens on Ctrl+K', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    renderPalette();

    fireEvent.keyDown(document, { key: 'k', ctrlKey: true });

    expect(await screen.findByRole('dialog')).toBeInTheDocument();
  });
});

describe('GlobalSearch navigates client-side (R7.5)', () => {
  it('routes to a record without a page load when a result is clicked', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    await open();
    await search('acme');
    await screen.findByText('Acme ticket');

    fireEvent.click(screen.getByText('Acme ticket'));

    // The router moved: this was a <Link>, not the raw <a href> that used to
    // throw away the whole SPA (170 kB gzip of eager shell) on every result.
    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/objects/ticket/records/t1'));
    // ...and the palette got out of the way.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('routes to a settings section without a page load', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue({ query: 'profile', groups: [] });
    await open();
    await search('profile');

    // Rendered bare (no AuthProvider) only the always-visible personal sections
    // are offered, which is enough to prove the settings hits are Links too.
    const link = await screen.findByRole('link', { name: /Profile/ });
    expect(link).toHaveAttribute('href', '/settings/profile');

    fireEvent.click(link);

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/settings/profile'));
  });
});

describe('GlobalSearch keyboard list navigation (R7.5)', () => {
  it('walks the results with ArrowDown / ArrowUp and cycles back to the box', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    await open();
    await search('acme');
    await screen.findByText('Acme ticket');

    const input = screen.getByPlaceholderText('Search across every object…');
    const dialog = screen.getByRole('dialog');
    const links = within(dialog).getAllByRole('link');
    expect(links).toHaveLength(2);

    await waitFor(() => expect(input).toHaveFocus());

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    expect(links[0]).toHaveFocus();

    fireEvent.keyDown(links[0], { key: 'ArrowDown' });
    expect(links[1]).toHaveFocus();

    fireEvent.keyDown(links[1], { key: 'ArrowUp' });
    expect(links[0]).toHaveFocus();

    // The box is part of the ring, not a dead end above it.
    fireEvent.keyDown(links[0], { key: 'ArrowUp' });
    expect(input).toHaveFocus();

    // ...and wrapping the other way reaches the last result.
    fireEvent.keyDown(input, { key: 'ArrowUp' });
    expect(links[1]).toHaveFocus();
  });

  it('opens the top result on Enter in the box', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue(result);
    await open();
    await search('acme');
    await screen.findByText('Acme ticket');

    const input = screen.getByPlaceholderText('Search across every object…');
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/objects/ticket/records/t1'));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('does nothing on Enter when there is nothing to open', async () => {
    (globalSearch as ReturnType<typeof vi.fn>).mockResolvedValue({ query: 'zzz', groups: [] });
    await open();
    await search('zzz');
    await screen.findByText('No results for "zzz"');

    fireEvent.keyDown(screen.getByPlaceholderText('Search across every object…'), { key: 'Enter' });

    expect(screen.getByTestId('location')).toHaveTextContent('/deals');
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});
