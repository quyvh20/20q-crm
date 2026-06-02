import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { EntityPicker } from './EntityPicker';
import { getContacts, getDeals } from '../../lib/api';
import type { Contact, Deal, CursorMeta } from '../../lib/api';

/**
 * EntityPicker tests — Requirements 9.1–9.6.
 *
 * The picker debounces its free-text query (300ms) and then calls
 * `getContacts`/`getDeals` per `kind`. We mock the API module and let the real
 * debounce timer run, polling with `waitFor`/`findBy*` until results render.
 * The api module is mocked at the same specifier the component imports
 * (`../../lib/api`), so no real network calls occur.
 */

vi.mock('../../lib/api', () => ({
  getContacts: vi.fn(),
  getDeals: vi.fn(),
}));

const mockGetContacts = vi.mocked(getContacts);
const mockGetDeals = vi.mocked(getDeals);

const META: CursorMeta = { has_more: false, total: 0 };

function makeContact(over: Partial<Contact> = {}): Contact {
  return {
    id: 'c1',
    org_id: 'org1',
    first_name: 'Ada',
    last_name: 'Lovelace',
    email: 'ada@example.com',
    phone: '555-0001',
    custom_fields: {},
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    ...over,
  };
}

function makeDeal(over: Partial<Deal> = {}): Deal {
  return {
    id: 'd1',
    org_id: 'org1',
    title: 'Big Deal',
    value: 1000,
    probability: 50,
    is_won: false,
    is_lost: false,
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    stage: {
      id: 's1',
      org_id: 'org1',
      name: 'Proposal',
      position: 1,
      color: '#fff',
      is_won: false,
      is_lost: false,
    },
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  // Safe defaults so a function a test doesn't explicitly stub never rejects.
  mockGetContacts.mockResolvedValue({ contacts: [], meta: META });
  mockGetDeals.mockResolvedValue({ deals: [], meta: META });
});

// ── Req 9.1: query input present ───────────────────────────────────────
describe('EntityPicker — query input', () => {
  it('renders a search query input for contacts (Req 9.1)', () => {
    render(<EntityPicker kind="contact" onSelect={vi.fn()} />);
    expect(screen.getByLabelText('Search contacts')).toBeInTheDocument();
  });

  it('renders a search query input for deals (Req 9.1)', () => {
    render(<EntityPicker kind="deal" onSelect={vi.fn()} />);
    expect(screen.getByLabelText('Search deals')).toBeInTheDocument();
  });
});

// ── Req 9.2: typing renders mocked search results ──────────────────────
describe('EntityPicker — search results', () => {
  it('renders matching results returned by the search after typing (Req 9.2)', async () => {
    const user = userEvent.setup();
    mockGetContacts.mockResolvedValue({
      contacts: [
        makeContact({ id: 'c1', first_name: 'Ada', last_name: 'Lovelace', email: 'ada@example.com' }),
        makeContact({ id: 'c2', first_name: 'Alan', last_name: 'Turing', email: 'alan@example.com' }),
      ],
      meta: META,
    });

    render(<EntityPicker kind="contact" onSelect={vi.fn()} />);
    await user.type(screen.getByLabelText('Search contacts'), 'a');

    await waitFor(() => expect(screen.getByText('Ada Lovelace')).toBeInTheDocument(), { timeout: 2000 });
    expect(screen.getByText('Alan Turing')).toBeInTheDocument();
    expect(screen.getAllByRole('option')).toHaveLength(2);
    // Query is forwarded to the search endpoint (debounced + trimmed) with a limit.
    // Wait for the typed-query call specifically — a mount preload (no q) fires first.
    await waitFor(() => expect(mockGetContacts).toHaveBeenCalledWith({ q: 'a', limit: 10 }), { timeout: 2000 });
  });
});

// ── Req 9.3: compatible mode for the workflow's trigger ────────────────
describe('EntityPicker — compatible entity mode', () => {
  it('searches contacts (and never deals) when kind is contact (Req 9.3)', async () => {
    const user = userEvent.setup();
    mockGetContacts.mockResolvedValue({ contacts: [makeContact()], meta: META });

    render(<EntityPicker kind="contact" onSelect={vi.fn()} />);
    await user.type(screen.getByLabelText('Search contacts'), 'ada');

    await waitFor(() => expect(mockGetContacts).toHaveBeenCalled(), { timeout: 2000 });
    expect(mockGetDeals).not.toHaveBeenCalled();
  });

  it('searches deals (and never contacts) when kind is deal (Req 9.3)', async () => {
    const user = userEvent.setup();
    mockGetDeals.mockResolvedValue({ deals: [makeDeal({ title: 'Acme Expansion' })], meta: META });

    render(<EntityPicker kind="deal" onSelect={vi.fn()} />);
    await user.type(screen.getByLabelText('Search deals'), 'acme');

    await waitFor(() => expect(screen.getByText('Acme Expansion')).toBeInTheDocument(), { timeout: 2000 });
    // Wait for the typed-query call (a mount preload with no q fires first).
    await waitFor(() => expect(mockGetDeals).toHaveBeenCalledWith({ q: 'acme', limit: 10 }), { timeout: 2000 });
    expect(mockGetContacts).not.toHaveBeenCalled();
  });
});

// ── Req 9.4 / 9.5: explicit selection vs no selection ──────────────────
describe('EntityPicker — selection semantics', () => {
  it('marks the entity and calls onSelect only on an explicit click (Req 9.4)', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    mockGetContacts.mockResolvedValue({
      contacts: [makeContact({ id: 'c1', first_name: 'Ada', last_name: 'Lovelace', email: 'ada@example.com' })],
      meta: META,
    });

    render(<EntityPicker kind="contact" onSelect={onSelect} />);
    await user.type(screen.getByLabelText('Search contacts'), 'ada');

    const option = await screen.findByRole('option', {}, { timeout: 2000 });
    // Not selected until the user clicks.
    expect(option).toHaveAttribute('aria-selected', 'false');

    await user.click(option);

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onSelect).toHaveBeenCalledWith({ id: 'c1', label: 'Ada Lovelace', sublabel: 'ada@example.com' });
    // The clicked candidate is now marked as the chosen entity.
    expect(option).toHaveAttribute('aria-selected', 'true');
  });

  it('never calls onSelect when the user searches but does not click a result (Req 9.5)', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    mockGetContacts.mockResolvedValue({ contacts: [makeContact()], meta: META });

    render(<EntityPicker kind="contact" onSelect={onSelect} />);
    await user.type(screen.getByLabelText('Search contacts'), 'ada');

    // Results render, but with no explicit click the parent's confirm stays disabled.
    await screen.findByRole('option', {}, { timeout: 2000 });
    expect(onSelect).not.toHaveBeenCalled();
  });
});

// ── Req 9.6: invalidated selection re-disables confirm (kind switch) ───
describe('EntityPicker — selection invalidation', () => {
  it('clears the prior selection and results when the entity kind switches (Req 9.6)', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    mockGetContacts.mockResolvedValue({
      contacts: [makeContact({ id: 'c1', first_name: 'Ada', last_name: 'Lovelace', email: 'ada@example.com' })],
      meta: META,
    });

    const { rerender } = render(<EntityPicker kind="contact" onSelect={onSelect} />);

    // Establish an explicit contact selection.
    await user.type(screen.getByLabelText('Search contacts'), 'ada');
    const option = await screen.findByRole('option', {}, { timeout: 2000 });
    await user.click(option);
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('option')).toHaveAttribute('aria-selected', 'true');

    // Switching the compatible kind must reset the picker so a stale selection
    // cannot persist (the parent re-disables confirm until a fresh selection).
    rerender(<EntityPicker kind="deal" onSelect={onSelect} />);

    const dealInput = screen.getByLabelText('Search deals') as HTMLInputElement;
    expect(dealInput.value).toBe('');
    // Stale contact selection/results are cleared on the kind switch...
    expect(screen.queryByText('Ada Lovelace')).not.toBeInTheDocument();
    // ...and the deal kind loads its own default list (empty in this test's mock).
    await screen.findByText(/No deals found/i);
    expect(screen.queryByRole('option')).not.toBeInTheDocument();
  });
});

// ── Picklist behavior: preloaded list + case-insensitive search ────────
describe('EntityPicker — initial list and case', () => {
  it('loads a default list on open, before any typing', async () => {
    mockGetContacts.mockResolvedValue({
      contacts: [makeContact({ id: 'c1', first_name: 'Ada', last_name: 'Lovelace' })],
      meta: META,
    });

    render(<EntityPicker kind="contact" onSelect={vi.fn()} />);

    // The list appears with nothing typed (searchable picklist), fetched with no q.
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument();
    expect(mockGetContacts).toHaveBeenCalledWith({ q: undefined, limit: 10 });
  });

  it('forwards the query verbatim regardless of case (server search is case-insensitive)', async () => {
    const user = userEvent.setup();
    mockGetContacts.mockResolvedValue({ contacts: [makeContact()], meta: META });

    render(<EntityPicker kind="contact" onSelect={vi.fn()} />);
    await user.type(screen.getByLabelText('Search contacts'), 'ADA');

    // The picker does not alter case; the backend (tsvector 'simple' / LOWER(title)
    // LIKE LOWER(q)) matches case-insensitively, so uppercase input still resolves.
    await waitFor(() => expect(mockGetContacts).toHaveBeenCalledWith({ q: 'ADA', limit: 10 }));
  });
});
