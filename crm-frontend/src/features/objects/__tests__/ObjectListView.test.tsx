import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import type { ObjectSchema, UniformRecord } from '../../../lib/api';

// Mock the API layer so the renderer is exercised without a backend. The whole
// point of P3 is that ONE component renders any object from its schema, so these
// tests drive the same <ObjectListView> with a system object (deal) and a custom
// object (project) and assert both render.
vi.mock('../../../lib/api', () => ({
  getObjectSchema: vi.fn(),
  listObjectRecordsUnified: vi.fn(),
  createObjectRecordUnified: vi.fn(),
  updateObjectRecordUnified: vi.fn(),
  deleteObjectRecordUnified: vi.fn(),
  getTags: vi.fn().mockResolvedValue([]),
  // The create form renders an OwnerPicker for objects that have an owner (U6.3),
  // which loads the member list.
  getWorkspaceMembers: vi.fn().mockResolvedValue([]),
}));

// U3.7: the list gates "+ Add"/Import on the caller's OLS create bit. Tests
// flip individual bits through this map; anything unset stays allowed, so the
// pre-existing rendering tests run unchanged.
let objectAccess: Record<string, boolean> = {};
vi.mock('../../../lib/auth', () => ({
  usePermissions: () => ({
    can: () => true,
    canAccess: (slug: string, action: string) => objectAccess[`${slug}.${action}`] ?? true,
    loaded: true,
  }),
}));

import {
  getObjectSchema,
  listObjectRecordsUnified,
} from '../../../lib/api';
import ObjectListView from '../ObjectListView';

const dealSchema: ObjectSchema = {
  slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981',
  is_system: true, searchable: false, has_owner: true, display_field: 'title',
  fields: [
    { key: 'title', label: 'Title', type: 'text', is_system: true, required: true },
    { key: 'value', label: 'Value', type: 'number', is_system: true, required: false },
  ],
};

const contactSchema: ObjectSchema = {
  slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#6366f1',
  is_system: true, searchable: false, has_owner: true, display_field: 'name',
  fields: [
    { key: 'name', label: 'Name', type: 'text', is_system: true, required: true },
  ],
};

const projectSchema: ObjectSchema = {
  slug: 'project', label: 'Project', label_plural: 'Projects', icon: '📁', color: '#6B7280',
  is_system: false, searchable: false, has_owner: true, display_field: 'name',
  fields: [
    { key: 'name', label: 'Name', type: 'text', is_system: false, required: true },
    { key: 'status', label: 'Status', type: 'select', options: ['active', 'done'], is_system: false, required: false },
  ],
};

function record(partial: Partial<UniformRecord>): UniformRecord {
  return {
    id: crypto.randomUUID(), object: 'x', display: '', fields: {},
    created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    ...partial,
  };
}

// Probe that surfaces the current route so navigation can be asserted.
function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.pathname}</div>;
}

function renderView(slug: string) {
  return render(
    <MemoryRouter initialEntries={[`/${slug}`]}>
      <ObjectListView slug={slug} />
      <LocationProbe />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  objectAccess = {};
});

describe('ObjectListView renders any object from its schema', () => {
  it('renders a system object (deal)', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ object: 'deal', display: 'Acme renewal', fields: { title: 'Acme renewal', value: 1500 } })],
      next_cursor: undefined,
    });

    renderView('deal');

    expect(await screen.findByRole('heading', { name: 'Deals' })).toBeInTheDocument();
    // "Acme renewal" appears in both the Name cell and the Title field column.
    expect((await screen.findAllByText('Acme renewal')).length).toBeGreaterThan(0);
    // Column headers come from the schema fields.
    expect(screen.getByText('Title')).toBeInTheDocument();
    expect(screen.getByText('Value')).toBeInTheDocument();
  });

  it('renders a custom object (project) through the same component', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ object: 'project', display: 'Apollo', fields: { name: 'Apollo', status: 'active' } })],
      next_cursor: undefined,
    });

    renderView('project');

    expect(await screen.findByRole('heading', { name: 'Projects' })).toBeInTheDocument();
    expect((await screen.findAllByText('Apollo')).length).toBeGreaterThan(0);
    expect(screen.getByText('Status')).toBeInTheDocument();
  });

  it('opens the shared create form from the list', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    renderView('deal');

    fireEvent.click(await screen.findByRole('button', { name: 'Add Deal' }));
    // ObjectForm header + a schema-driven field label appear. (The text shows
    // twice — the Modal's sr-only dialog title and the form's visible header.)
    await waitFor(() => expect(screen.getAllByText('New Deal').length).toBeGreaterThan(0));
    expect(screen.getByText('Create Deal')).toBeInTheDocument();
  });

  it('navigates to the unified record page when a custom-object row is clicked', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ id: 'r9', object: 'project', display: 'Apollo', fields: { name: 'Apollo', status: 'active' } })],
      next_cursor: undefined,
    });

    renderView('project');

    const cell = (await screen.findAllByText('Apollo'))[0];
    fireEvent.click(cell.closest('tr')!);

    await waitFor(() =>
      expect(screen.getByTestId('loc').textContent).toBe('/objects/project/records/r9'),
    );
  });

  it('hides + Add and Import and shows the denied empty-state when create is denied', async () => {
    objectAccess['contact.create'] = false;
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    renderView('contact');

    expect(await screen.findByRole('heading', { name: 'Contacts' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add Contact' })).not.toBeInTheDocument();
    // Import is a contact affordance, so its absence here is the create gate.
    expect(screen.queryByText('Import')).not.toBeInTheDocument();
    // The empty state doesn't tell a create-denied role to click a button it doesn't have.
    expect(await screen.findByText('No contacts to show.')).toBeInTheDocument();
    expect(screen.queryByText(/Click "Add/)).not.toBeInTheDocument();
  });

  it('keeps + Add and Import for a role with create access (contact)', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(contactSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({ records: [], next_cursor: undefined });

    renderView('contact');

    expect(await screen.findByRole('button', { name: 'Add Contact' })).toBeInTheDocument();
    expect(screen.getByText('Import')).toBeInTheDocument();
  });

  it('shows an error state — not the empty state — when the list request fails', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockRejectedValue(
      new Error('the service is temporarily unavailable (reference: 9f3c1a2b)'),
    );

    renderView('deal');

    // The failure is named, with the server's own correlation id.
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load deals.");
    expect(alert).toHaveTextContent('the service is temporarily unavailable (reference: 9f3c1a2b)');

    // …and the empty-state copy, which would tell the user their org has no
    // deals and to go create one, is NOT rendered.
    expect(screen.queryByText('No deals yet.')).not.toBeInTheDocument();
    expect(screen.queryByText(/Click "Add Deal"/)).not.toBeInTheDocument();
    // Nor the "Showing 0 deals" footer, which reads as a confirmed count.
    expect(screen.queryByText(/Showing 0 deals/)).not.toBeInTheDocument();
  });

  it('retries the failed list and renders the records once it succeeds', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    // A flag, not a call counter: mounting fires the list twice (the slug-reset
    // effect hands `filters`/`tagIds` fresh identities, which re-derives
    // fetchFirstPage), so "the Nth call" is not a stable way to say "before the
    // retry".
    let down = true;
    vi.mocked(listObjectRecordsUnified).mockImplementation(async () => {
      if (down) throw new Error('boom');
      return {
        records: [record({ object: 'deal', display: 'Acme renewal', fields: { title: 'Acme renewal' } })],
        next_cursor: undefined,
      };
    });

    renderView('deal');

    const retry = await screen.findByRole('button', { name: /Retry/ });
    down = false;
    fireEvent.click(retry);

    expect((await screen.findAllByText('Acme renewal')).length).toBeGreaterThan(0);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('says so when Load more fails, and keeps the rows already loaded', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    // Keyed on the CURSOR, not on a call index: page one always succeeds, any
    // paged request always fails.
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (_slug, params) => {
      if (params?.cursor) throw new Error('gateway timeout');
      return {
        records: [record({ object: 'deal', display: 'Acme renewal', fields: { title: 'Acme renewal' } })],
        next_cursor: 'cursor-1',
      };
    });

    renderView('deal');

    fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load more.");
    expect(alert).toHaveTextContent('gateway timeout');
    // The page we already had survives the failure…
    expect((await screen.findAllByText('Acme renewal')).length).toBeGreaterThan(0);
    // …and the button re-labels itself instead of silently doing nothing again.
    expect(await screen.findByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });

  it('does not render a record twice when a later page repeats it', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    // OFFSET paging over a non-total ordering hands page 2 a row page 1 already
    // had. Rows are keyed by record id, so an un-deduped concat gives React two
    // rows with the same key — it keeps one, and a click on the survivor can
    // navigate to the OTHER record's detail page.
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (_slug, params) => {
      const apollo = record({ id: 'p1', object: 'project', display: 'Apollo', fields: { name: 'Apollo' } });
      if (params?.cursor) {
        return {
          records: [
            apollo,
            record({ id: 'p2', object: 'project', display: 'Gemini', fields: { name: 'Gemini' } }),
          ],
          next_cursor: undefined,
        };
      }
      return { records: [apollo], next_cursor: 'cursor-1' };
    });

    renderView('project');

    fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));

    // Gemini proves page 2 was applied at all…
    expect(await screen.findByRole('link', { name: 'Gemini' })).toBeInTheDocument();
    // …and Apollo is on the page exactly once, so no two rows share a key.
    expect(screen.getAllByRole('link', { name: 'Apollo' })).toHaveLength(1);
    expect(screen.getAllByRole('row')).toHaveLength(3); // header + 2 records
    // The footer counts real rows, not fetched ones.
    expect(screen.getByText('Showing 2 projects')).toBeInTheDocument();
  });

  it('navigates a deal row to the bespoke /deals/:id page', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ id: 'd7', object: 'deal', display: 'Acme renewal', fields: { title: 'Acme renewal', value: 1500 } })],
      next_cursor: undefined,
    });

    renderView('deal');

    const cell = (await screen.findAllByText('Acme renewal'))[0];
    fireEvent.click(cell.closest('tr')!);

    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/deals/d7'));
  });
});
