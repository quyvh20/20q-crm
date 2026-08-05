import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import type { ObjectSchema, RecordSort, UniformRecord } from '../../../lib/api';

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
  // Board-able objects (a "stage" relation) resolve their stage labels here.
  getStages: vi.fn().mockResolvedValue([]),
  // R7.1 bulk actions — contacts only, and the ONLY bulk surface in the app.
  bulkContactAction: vi.fn(),
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
  // R8.2: the Export button gates on data.export via useAuth().hasCapability.
  useAuth: () => ({ hasCapability: () => true }),
}));

import {
  getObjectSchema,
  listObjectRecordsUnified,
  getTags,
  bulkContactAction,
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

// Probe that surfaces the current route so navigation can be asserted. The path
// and the query string are separate nodes so the pre-R7 path assertions keep
// working unchanged while URL state can be asserted on its own.
function LocationProbe() {
  const loc = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <div data-testid="loc">{loc.pathname}</div>
      <div data-testid="search">{loc.search}</div>
      {/* Drives a real POP navigation, which is the only way to exercise the
          "the URL moved under the search box" path the way Back does. */}
      <button onClick={() => navigate(-1)}>probe-back</button>
    </>
  );
}

function renderView(slug: string, entries?: string[], initialIndex?: number) {
  return render(
    <MemoryRouter initialEntries={entries ?? [`/${slug}`]} initialIndex={initialIndex}>
      <ObjectListView slug={slug} />
      <LocationProbe />
    </MemoryRouter>,
  );
}

/** Params of the most recent list call FOR THIS OBJECT — relation dropdowns list
 *  their own target object through the same client, so calls must be filtered. */
function lastListParams(slug: string) {
  const calls = vi.mocked(listObjectRecordsUnified).mock.calls.filter((c) => c[0] === slug);
  return calls[calls.length - 1]?.[1];
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  objectAccess = {};
  vi.mocked(getTags).mockResolvedValue([]);
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

// ============================================================
// R7 fixtures
// ============================================================

// A contact schema with the shape the real registry seeds: four native columns
// plus a relation, so filter dropdowns, sortable headers and the column cap are
// all exercised on the same object the bulk actions apply to.
const richContactSchema: ObjectSchema = {
  slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#6366f1',
  is_system: true, searchable: true, has_owner: true, display_field: 'first_name',
  fields: [
    { key: 'first_name', label: 'First Name', type: 'text', is_system: true, required: true },
    { key: 'last_name', label: 'Last Name', type: 'text', is_system: true, required: false },
    { key: 'email', label: 'Email', type: 'text', is_system: true, required: false },
    { key: 'company', label: 'Company', type: 'relation', target_slug: 'company', is_system: true, required: false },
  ],
};

const contactSortMeta: RecordSort = {
  by: 'created_at',
  order: 'desc',
  sortable: ['created_at', 'first_name', 'email'],
};

function contact(id: string, name: string): UniformRecord {
  return record({ id, object: 'contact', display: name, fields: { first_name: name } });
}

/** Serves `contact` from a mutable roster and `company` (the relation target) as empty. */
function serveContacts(roster: () => UniformRecord[], sort: RecordSort | undefined = contactSortMeta) {
  vi.mocked(getObjectSchema).mockResolvedValue(richContactSchema);
  vi.mocked(listObjectRecordsUnified).mockImplementation(async (slug) => {
    if (slug !== 'contact') return { records: [], next_cursor: undefined };
    return { records: roster(), next_cursor: undefined, sort };
  });
}

// ============================================================
// R7.2 — URL state
// ============================================================

describe('ObjectListView keeps its list state in the URL', () => {
  it('restores search, filters, tags, semantic and sort from a deep link', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact', ['/contacts?q=ann&f.company=co-7&tags=t1,t2&ai=1&sort_by=email&sort_order=asc']);

    await screen.findByRole('heading', { name: 'Contacts' });
    await waitFor(() => expect(lastListParams('contact')).toMatchObject({
      q: 'ann',
      filters: { company: 'co-7' },
      tagIds: ['t1', 't2'],
      semantic: true,
      sortBy: 'email',
      sortOrder: 'asc',
    }));
    // …and the search box shows the term it is filtering by, not an empty field
    // that contradicts the results.
    expect(screen.getByLabelText('Search contacts')).toHaveValue('ann');
  });

  it('debounces the search box into the URL and refetches with it', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact');
    await screen.findByRole('heading', { name: 'Contacts' });

    fireEvent.change(screen.getByLabelText('Search contacts'), { target: { value: 'zed' } });

    await waitFor(() => expect(screen.getByTestId('search').textContent).toBe('?q=zed'));
    await waitFor(() => expect(lastListParams('contact')).toMatchObject({ q: 'zed' }));
  });

  it('puts the search term in the URL but never the cursor', async () => {
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (_slug, params) => ({
      records: [record({ id: params?.cursor ? 'd2' : 'd1', object: 'deal', display: params?.cursor ? 'Two' : 'One', fields: {} })],
      next_cursor: params?.cursor ? undefined : 'cursor-1',
    }));

    renderView('deal');

    fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    await screen.findByRole('link', { name: 'Two' });

    // A cursor is opaque and R4 made a stale one fail soft to page 1, so a shared
    // link carrying one would silently show the recipient a different page than
    // the sender saw.
    expect(screen.getByTestId('search').textContent).not.toContain('cursor');
  });

  it('syncs the search box when the URL moves under it (Back)', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact', ['/contacts', '/contacts?q=ann'], 1);

    const box = await screen.findByLabelText('Search contacts');
    expect(box).toHaveValue('ann');

    fireEvent.click(screen.getByText('probe-back'));

    // This is the assertion the WorkflowList pattern buys with a
    // set-state-in-effect mirror; here it comes from one DOM write in an effect
    // that only runs when `q` changed for a reason other than our own commit.
    await waitFor(() => expect(box).toHaveValue(''));
    await waitFor(() => expect(lastListParams('contact')).toMatchObject({ q: '' }));
  });

  it('does not let a pending search commit resurrect itself after a Back', async () => {
    // The two-effect version of this (a debounce effect plus a state mirror) leaves
    // a race: the debounce re-arms with the STALE typed value when `q` changes, and
    // only survives because the mirror's re-render happens to cancel the timer
    // first. Here the timer is cancelled explicitly, on the one path that can
    // strand it — the URL moving while a keystroke is still in flight.
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact', ['/contacts', '/contacts?q=ann'], 1);

    const box = await screen.findByLabelText('Search contacts');
    fireEvent.change(box, { target: { value: 'zed' } });
    fireEvent.click(screen.getByText('probe-back'));

    await waitFor(() => expect(box).toHaveValue(''));
    // Past the debounce window: a stranded timer would have committed ?q=zed here.
    await new Promise((r) => setTimeout(r, 450));
    expect(screen.getByTestId('search').textContent).toBe('');
    expect(box).toHaveValue('');
  });

  it('clears every filter but keeps the sort, which is not a filter', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact', ['/contacts?q=ann&tags=t1&ai=1&sort_by=email&sort_order=asc']);

    fireEvent.click(await screen.findByRole('button', { name: /Clear filters/ }));

    await waitFor(() => {
      const search = screen.getByTestId('search').textContent ?? '';
      expect(search).not.toContain('q=');
      expect(search).not.toContain('tags=');
      expect(search).not.toContain('ai=');
      // Wiping the ordering too would silently re-order the list the user is
      // looking at, which is not what "clear filters" says.
      expect(search).toContain('sort_by=email');
    });
  });

  it('Clear filters cannot APPLY the search it was pressed to get rid of', async () => {
    // The reproduction is narrow and nasty. `q` is absent, so a term typed but
    // not yet committed leaves the URL's `q` and `committedQ` BOTH '' — and the
    // sync effect's guard (`q === committedQ.current`) sends it straight home
    // without cancelling the timer. Clear the (tag) filter inside the debounce
    // window and 300ms later the pending commit lands: the user pressed "clear"
    // and got a newly applied filter.
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact', ['/contacts?tags=t1']);

    const box = await screen.findByLabelText('Search contacts');
    fireEvent.change(box, { target: { value: 'abc' } });
    fireEvent.click(screen.getByRole('button', { name: /Clear filters/ }));

    await waitFor(() => expect(screen.getByTestId('search').textContent).toBe(''));
    // Past the debounce window — this is where the stranded timer used to fire.
    await new Promise((r) => setTimeout(r, 450));

    expect(screen.getByTestId('search').textContent).toBe('');
    // The box must not still be showing a term that is not being applied.
    expect(box).toHaveValue('');
    // …and no request ever went out carrying it.
    const searched = vi
      .mocked(listObjectRecordsUnified)
      .mock.calls.filter((c) => c[0] === 'contact')
      .map((c) => c[1]?.q);
    expect(searched).not.toContain('abc');
  });

  it('leaves a search still being typed alone when an unrelated control patches the query', async () => {
    // The other half of the rule. Cancelling on EVERY patch would be a second
    // bug in the opposite direction: choosing a filter mid-word would silently
    // discard the term while the box went on displaying it.
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact');

    const box = await screen.findByLabelText('Search contacts');
    fireEvent.change(box, { target: { value: 'abc' } });
    fireEvent.click(await screen.findByRole('button', { name: /AI Search/ }));

    await waitFor(() => expect(screen.getByTestId('search').textContent).toContain('q=abc'));
    expect(screen.getByTestId('search').textContent).toContain('ai=1');
    expect(box).toHaveValue('abc');
  });
});

// ============================================================
// R7.1 — bulk actions
// ============================================================

describe('ObjectListView bulk actions (contacts only)', () => {
  it('deletes the selected contacts through the dialog primitive, and the rows leave', async () => {
    let roster = [contact('c1', 'Ann'), contact('c2', 'Bob')];
    serveContacts(() => roster);
    vi.mocked(bulkContactAction).mockImplementation(async () => {
      roster = roster.filter((r) => r.id !== 'c1');
      return { affected: 1, message: '1 contact(s) deleted' };
    });

    renderView('contact');

    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Ann' }));
    expect(await screen.findByText('1 selected')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Delete$/ }));

    // window.confirm cannot be styled, cannot name what is about to be destroyed,
    // and blocks the tab; the shared dialog does all three.
    const confirmButton = await screen.findByRole('button', { name: 'Delete 1 contact' });
    expect(screen.getByText(/consent provenance are erased permanently/)).toBeInTheDocument();
    fireEvent.click(confirmButton);

    await waitFor(() => expect(bulkContactAction).toHaveBeenCalledWith('delete', ['c1']));
    // The durable feedback is the row disappearing — no toast is involved.
    await waitFor(() => expect(screen.queryByRole('link', { name: 'Ann' })).not.toBeInTheDocument());
    expect(screen.getByRole('link', { name: 'Bob' })).toBeInTheDocument();
  });

  it('cancelling the dialog deletes nothing', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact');
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Ann' }));
    fireEvent.click(screen.getByRole('button', { name: /Delete$/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));

    await waitFor(() => expect(screen.queryByRole('button', { name: 'Delete 1 contact' })).not.toBeInTheDocument());
    expect(bulkContactAction).not.toHaveBeenCalled();
  });

  it('drops the selection when the query changes, so a stale row cannot be deleted', async () => {
    // Ann matches the unfiltered list; the filtered one returns only Bob. A
    // selection that survived the filter change would let "Delete" name a record
    // that is no longer on screen — data loss with no visible cause.
    serveContacts(() => (lastListParams('contact')?.q ? [contact('c2', 'Bob')] : [contact('c1', 'Ann')]));

    renderView('contact');
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Ann' }));
    expect(await screen.findByText('1 selected')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search contacts'), { target: { value: 'bob' } });

    await waitFor(() => expect(screen.getByRole('link', { name: 'Bob' })).toBeInTheDocument());
    await waitFor(() => expect(screen.queryByText('1 selected')).not.toBeInTheDocument());
    expect(screen.queryByRole('button', { name: /Delete$/ })).not.toBeInTheDocument();
  });

  it('can never name a row that is no longer on screen, even when nothing cleared the selection', async () => {
    // The clearing rule above covers every control that changes the query. This
    // covers everything else: `fetchFirstPage` also runs after a create or a CSV
    // import, and that collapses a multi-page list back to page 1 without any
    // filter having changed. A raw id set would keep the page-2 row selected and
    // hand it to the next Delete — an off-screen record, deleted with no visible
    // cause. The effective selection is the raw set INTERSECTED with `records`,
    // so the row drops out on its own.
    let cursor: string | undefined = 'page-2';
    vi.mocked(getObjectSchema).mockResolvedValue(richContactSchema);
    vi.mocked(listObjectRecordsUnified).mockImplementation(async (slug, params) => {
      if (slug !== 'contact') return { records: [], next_cursor: undefined };
      if (params?.cursor) return { records: [contact('c2', 'Bob')], next_cursor: undefined, sort: contactSortMeta };
      return { records: [contact('c1', 'Ann')], next_cursor: cursor, sort: contactSortMeta };
    });

    renderView('contact');

    fireEvent.click(await screen.findByRole('button', { name: 'Load more' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Bob' }));
    expect(await screen.findByText('1 selected')).toBeInTheDocument();

    // A create refetches page 1 — and page 2 is no longer loaded.
    cursor = undefined;
    fireEvent.click(screen.getByRole('button', { name: 'Add Contact' }));
    fireEvent.click(await screen.findByText('Create Contact'));

    await waitFor(() => expect(screen.queryByRole('link', { name: 'Bob' })).not.toBeInTheDocument());
    expect(screen.queryByText('1 selected')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Delete$/ })).not.toBeInTheDocument();
  });

  it('gates delete and tagging on their OWN permission bits, as the server does', async () => {
    // The bulk endpoint multiplexes two verbs and checks Delete for one and Edit
    // for the other, and "edit but not delete" ships as a default role — so one
    // combined gate would be wrong in both directions.
    objectAccess['contact.delete'] = false;
    vi.mocked(getTags).mockResolvedValue([{ id: 't1', name: 'VIP', color: '#111111' }] as never);
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact');
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Ann' }));

    expect(await screen.findByLabelText('Assign a tag to the selected contacts')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Delete$/ })).not.toBeInTheDocument();
  });

  it('offers no selection column at all on a non-contact object', async () => {
    // RecordService has no bulk surface (explicitly cut), so a deal list must not
    // grow checkboxes that lead nowhere.
    vi.mocked(getObjectSchema).mockResolvedValue(dealSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ id: 'd1', object: 'deal', display: 'Acme', fields: {} })],
      next_cursor: undefined,
    });

    renderView('deal');

    await screen.findByRole('link', { name: 'Acme' });
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0);
  });

  it('assigns a tag, keeps the selection, and says so durably', async () => {
    vi.mocked(getTags).mockResolvedValue([{ id: 't1', name: 'VIP', color: '#111111' }] as never);
    serveContacts(() => [contact('c1', 'Ann'), contact('c2', 'Bob')]);
    vi.mocked(bulkContactAction).mockResolvedValue({ affected: 2, message: 'ok' });

    renderView('contact');

    fireEvent.click(await screen.findByRole('checkbox', { name: /^Select all/ }));
    expect(await screen.findByText('2 selected')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Assign a tag to the selected contacts'), { target: { value: 't1' } });

    await waitFor(() => expect(bulkContactAction).toHaveBeenCalledWith('assign_tag', ['c1', 'c2'], 't1'));
    // Tags are not a column here, so nothing on screen would otherwise change —
    // the one case where a durable line is the honest feedback.
    expect(await screen.findByRole('status')).toHaveTextContent('Tagged 2 contacts as VIP.');
    expect(screen.getByText('2 selected')).toBeInTheDocument();
  });

  it('surfaces a failed bulk action instead of pretending it worked', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);
    vi.mocked(bulkContactAction).mockRejectedValue(new Error('forbidden (reference: 4b1)'));

    renderView('contact');
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Ann' }));
    fireEvent.click(screen.getByRole('button', { name: /Delete$/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete 1 contact' }));

    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Bulk action failed.');
    expect(alert).toHaveTextContent('forbidden (reference: 4b1)');
    // The row is still there, which is the truth: nothing was deleted.
    expect(screen.getByRole('link', { name: 'Ann' })).toBeInTheDocument();
  });
});

// ============================================================
// R7.3 — sort
// ============================================================

describe('ObjectListView sorting', () => {
  it('marks sortable headers with aria-sort from the server response', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact');

    // Ordered by created_at DESC, so that header — and only that header — claims a
    // direction. The others are sortable-but-unsorted.
    await waitFor(() =>
      expect(screen.getByRole('columnheader', { name: 'Created' })).toHaveAttribute('aria-sort', 'descending'),
    );
    expect(screen.getByRole('columnheader', { name: 'Email' })).toHaveAttribute('aria-sort', 'none');
    // Last Name is a real column the server does not offer as a sort key, so it
    // must carry no aria-sort at all — 'none' would promise an ordering control.
    expect(screen.getByRole('columnheader', { name: 'Last Name' })).not.toHaveAttribute('aria-sort');
  });

  it('sorts on header click, writing the ordering to the URL', async () => {
    serveContacts(() => [contact('c1', 'Ann')]);

    renderView('contact');

    fireEvent.click(await screen.findByRole('button', { name: /Email/ }));

    await waitFor(() => expect(screen.getByTestId('search').textContent).toContain('sort_by=email'));
    expect(screen.getByTestId('search').textContent).toContain('sort_order=asc');
    await waitFor(() => expect(lastListParams('contact')).toMatchObject({ sortBy: 'email', sortOrder: 'asc' }));
    // A sort change refetches from page 1: the previous cursor was minted under a
    // different ordering and the backend would refuse it anyway.
    expect(lastListParams('contact')?.cursor).toBeUndefined();
  });

  it('flips direction on the active column and starts a new column ascending', async () => {
    serveContacts(() => [contact('c1', 'Ann')], { by: 'email', order: 'asc', sortable: ['created_at', 'first_name', 'email'] });

    renderView('contact', ['/contacts?sort_by=email&sort_order=asc']);

    fireEvent.click(await screen.findByRole('button', { name: /Email/ }));
    await waitFor(() => expect(screen.getByTestId('search').textContent).toContain('sort_order=desc'));
  });

  it('renders no sort affordance for an object the server says cannot sort', async () => {
    // Company and every custom object: their storage cannot honour an ordering, so
    // the response carries no sort block and the headers stay inert.
    vi.mocked(getObjectSchema).mockResolvedValue(projectSchema);
    vi.mocked(listObjectRecordsUnified).mockResolvedValue({
      records: [record({ id: 'p1', object: 'project', display: 'Apollo', fields: { name: 'Apollo' } })],
      next_cursor: undefined,
    });

    renderView('project');

    await screen.findByRole('link', { name: 'Apollo' });
    // getAll: the display column and the "name" field both render a "Name" header.
    for (const header of screen.getAllByRole('columnheader')) {
      expect(header).not.toHaveAttribute('aria-sort');
    }
    expect(screen.queryByRole('button', { name: /Created/ })).not.toBeInTheDocument();
  });
});
